package forge

import (
	"crypto/sha256"
	"encoding/hex"
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
// It has to capture everything that decides *which program gets driven*, because
// a key that does not makes the memo answer about a different backend than the
// one about to run. That is the explicit -cafe-bin/-try-bin overrides, and it is
// also PATH: every one of these tools is found on PATH, and `try` in particular
// is only accepted after being run to prove it is the Trylle CLI at all. Install
// a different program under the same name, or reorder PATH, and the memo would
// otherwise serve another executable's logins and version for the whole TTL.
//
// PATH is hashed rather than embedded, so the key stays short and the memo on
// disk does not become a listing of the user's directories.
func cacheKey(b Backend) string {
	switch v := b.(type) {
	case cafeBackend:
		if v.bin != "" {
			return "cafe\x00" + v.bin
		}
	case tryBackend:
		if v.bin != "" {
			return "try\x00" + v.bin
		}
	}
	return string(b.Tool()) + "\x00" + pathFingerprint()
}

// pathFingerprint is a short, stable digest of PATH, or "" when PATH is unset.
func pathFingerprint() string {
	p := os.Getenv("PATH")
	if p == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:8])
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

// clone returns a copy whose Entries map is genuinely separate. Assigning the
// struct alone copies the map header, not the map, so storing into the copy
// would otherwise write to the map other goroutines are reading.
func (c cacheFile) clone() cacheFile {
	out := cacheFile{Version: c.Version}
	if c.Entries != nil {
		out.Entries = make(map[string]cacheEntry, len(c.Entries))
		for k, v := range c.Entries {
			out.Entries[k] = v
		}
	}
	return out
}

// merge returns the union of c and latest, keeping the newer measurement where
// both measured the same backend. Two fth processes overlap often enough, and
// each holds the snapshot it read at startup; writing that snapshot back
// wholesale would discard what the other run measured.
func (c cacheFile) merge(latest cacheFile) cacheFile {
	out := latest.clone()
	if out.Entries == nil {
		out.Entries = map[string]cacheEntry{}
	}
	for k, v := range c.Entries {
		if cur, ok := out.Entries[k]; !ok || !cur.Data.At.After(v.Data.At) {
			out.Entries[k] = v
		}
	}
	out.Version = cacheVersion
	return out
}

// writeCache persists the memo. Every failure is ignored for the same reason
// readCache treats them as empty: a memo that cannot be saved costs time, not
// correctness.
func writeCache(f cacheFile) {
	p := cachePath()
	if p == "" {
		return
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	// MkdirAll does not tighten an existing directory, so enforce private
	// permissions in case it was created shared or writable.
	_ = os.Chmod(dir, 0o700)
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return
	}
	// Merge with whatever another process wrote since we read, so one invocation
	// does not discard another's results. Where both measured the same backend
	// the newer measurement wins.
	merged := f.merge(readCache())
	raw, err := json.Marshal(merged)
	if err != nil {
		return
	}
	// 0600: the file names the accounts and hosts the user is signed into. It
	// holds no credentials — those are redacted out of everything fth captures —
	// but the list of instances is still nobody else's business.
	//
	// A fixed temporary name would let another user plant a symlink and redirect
	// this write, so create a fresh file without following any pre-existing path.
	tmp, err := os.CreateTemp(dir, "detect-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	// Rename is atomic, so a run that is interrupted mid-write cannot leave a
	// half-written file that the next run would have to reject.
	_ = os.Rename(tmpName, p)
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
