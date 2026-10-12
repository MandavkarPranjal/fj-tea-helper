package forge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Detection is a memo of what a backend looked like a moment ago.
//
// Every field here is something fth asks the network for. Re-running the probe
// costs a TLS handshake per instance — roughly 400ms to git.cafe, 400ms to
// GitHub, 400ms to Trylle — and none of it gets cheaper on a second attempt,
// because each CLI process opens its own connection. Running all the probes
// concurrently already reduced the total from the sum of the calls to the
// slowest one; a memo is what takes it below that, because it removes the calls
// rather than overlapping them.
type Detection struct {
	// Installed reports whether the binary was found on PATH.
	Installed bool
	// Logins is what the CLI reported, in its own order.
	Logins []Login
	// Users maps login name -> account name.
	Users map[string]string
	// Verified reports whether at least one login authenticated.
	Verified bool
	// Notes carries forward any non-fatal problem worth showing again.
	Notes []string
	// Version is the CLI's version string, so the listing does not spawn a
	// second process for it.
	Version string
	// At is when this was measured.
	At time.Time
}

// cacheEntry is one backend's Detection plus the key it was measured under.
//
// The key matters: the same tool probed with a different -cafe-bin or -try-bin
// is a different backend, and reusing the other one's answer would report on a
// binary fth is not going to use.
type cacheEntry struct {
	Key  string    `json:"key"`
	Data Detection `json:"data"`
}

// cacheFile is the on-disk shape: a schema version, and one entry per tool.
type cacheFile struct {
	Version int                   `json:"version"`
	Entries map[string]cacheEntry `json:"entries"`
}

// cacheVersion is bumped whenever the shape changes, so an old file is discarded
// rather than misread. Nothing here is worth migrating.
const cacheVersion = 1

// DefaultCacheTTL is how long a measurement is trusted.
//
// It is short on purpose. The whole point of the memo is to skip a probe that
// costs the better part of a second, and the cost of being wrong is that fth
// offers an instance whose credential has since been revoked and the creation
// fails — which is recoverable, and something that also happens today whenever a
// token expires between the probe and the push.
const DefaultCacheTTL = 10 * time.Minute

// cachePath returns where the memo lives, or "" if there is nowhere to put it
// (no HOME, no cache dir). A missing location is not an error: the memo is an
// optimisation, and fth works exactly as before without one.
func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "fth", "detect.json")
}

// cacheKey identifies the backend a Detection was measured for.
//
// Only the explicit binary overrides can change which program is driven, so
// those are all that belong in the key; everything else about a tool is fixed.
func cacheKey(b Backend) string {
	switch v := b.(type) {
	case cafeBackend:
		return "cafe\x00" + v.bin
	case tryBackend:
		return "try\x00" + v.bin
	default:
		return string(b.Tool()) + "\x00"
	}
}

// readCache loads the memo, treating any problem as an empty one. A corrupt,
// unreadable or outdated file is simply not a cache: it is the state fth is in
// when it has no memo at all, and probing still produces the right answer.
func readCache() cacheFile {
	var f cacheFile
	p := cachePath()
	if p == "" {
		return f
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return f
	}
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != cacheVersion {
		return cacheFile{Version: cacheVersion}
	}
	if f.Entries == nil {
		f.Entries = map[string]cacheEntry{}
	}
	return f
}

// writeCache persists the memo. Every failure is ignored for the same reason
// readCache treats them as empty: a memo that cannot be saved costs time, not
// correctness.
func writeCache(f cacheFile) {
	p := cachePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return
	}
	// 0600: the file names the accounts and hosts the user is signed into. It
	// holds no credentials — those are redacted out of everything fth captures —
	// but the list of instances is still nobody else's business.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	// Rename is atomic, so a run that is interrupted mid-write cannot leave a
	// half-written file that the next run would have to reject.
	_ = os.Rename(tmp, p)
}

// lookup returns a fresh measurement for b, if there is one.
func (c cacheFile) lookup(b Backend, ttl time.Duration) (Detection, bool) {
	if ttl <= 0 {
		return Detection{}, false
	}
	e, ok := c.Entries[cacheKey(b)]
	if !ok {
		return Detection{}, false
	}
	// A zero At means the entry predates the timestamp and is not trustworthy.
	if e.Data.At.IsZero() || time.Since(e.Data.At) > ttl {
		return Detection{}, false
	}
	return e.Data, true
}

// store records a measurement, replacing any previous one for the same backend.
func (c cacheFile) store(b Backend, d Detection) cacheFile {
	if c.Entries == nil {
		c.Entries = map[string]cacheEntry{}
	}
	c.Version = cacheVersion
	c.Entries[cacheKey(b)] = cacheEntry{Key: cacheKey(b), Data: d}
	return c
}

// toCandidate rebuilds a Candidate from a memo, reattaching the live backend
// because the memo stores facts about it, never the value itself.
func (d Detection) toCandidate(b Backend) Candidate {
	users := d.Users
	if users == nil {
		users = map[string]string{}
	}
	return Candidate{
		Backend:   b,
		Logins:    d.Logins,
		Users:     users,
		Verified:  d.Verified,
		Notes:     d.Notes,
		probed:    true,
		installed: d.Installed,
		version:   d.Version,
	}
}

// DisableCache turns the memo off for the rest of the process. Detect then
// probes every backend and rewrites the stored result, which is what makes
// -no-cache the way to recover from a credential that has just been fixed.
func DisableCache() { detectTTL = 0 }
