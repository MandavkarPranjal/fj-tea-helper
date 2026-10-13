package forge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolateCache points the memo at a temp directory for the duration of the test
// and restores the real TTL afterwards.
//
// Both halves matter. XDG_CACHE_HOME keeps a test run from reading or overwriting
// the developer's own memo, and detectTTL is a package global that other tests
// call DisableCache on, so a cache test that forgot to restore it would leave the
// rest of the suite probing nothing.
//
// LOCALAPPDATA is set as well because it is not redundant: os.UserCacheDir reads
// XDG_CACHE_HOME on Unix but %LocalAppData% on Windows, so with only the former
// redirected every one of these tests would have used — and rewritten — the real
// cache on that platform.
func isolateCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LOCALAPPDATA", dir)
	old := detectTTL
	t.Cleanup(func() { detectTTL = old })
	detectTTL = DefaultCacheTTL
	return dir
}

// A memo read back must describe the backend it was measured for, with the live
// backend reattached. Dropping the Backend on the floor would make every method
// on the reconstructed Candidate panic, and the memo cannot carry one because it
// is written to disk as JSON.
func TestCacheRoundTripsACandidate(t *testing.T) {
	isolateCache(t)
	b := stagedBackend{Tool_name: ToolFj, User: "anna"}

	detected := probe(t.Context(), b)
	if !detected.Verified {
		t.Fatalf("probe did not verify: %v", detected.Notes)
	}

	writeCache(cacheFile{}.store(b, detected.detection()))

	got, ok := readCache().lookup(b, DefaultCacheTTL)
	if !ok {
		t.Fatal("lookup missed an entry that was just written")
	}
	if !got.Verified || !got.Installed {
		t.Errorf("round-tripped Verified=%v Installed=%v, want both true", got.Verified, got.Installed)
	}
	if got.Users["fj.example"] != "anna" {
		t.Errorf("Users = %v, want the account name to survive", got.Users)
	}

	c := got.toCandidate(b)
	if c.Backend != Backend(b) {
		t.Error("toCandidate dropped the live backend")
	}
	if !c.isInstalled() {
		t.Error("isInstalled() re-probed and disagreed with the memo")
	}
	if got := c.Status(); got == "" || got == "not installed" {
		t.Errorf("Status() = %q; a memoised candidate must describe itself without a process", got)
	}
}

// The point of the memo: a fresh entry must be served without touching the
// backend at all. A backend that would fail if called is the strongest possible
// assertion — if the probe ran, the lookup path is broken and this test fails.
func TestLookupServesAFreshEntryWithoutProbing(t *testing.T) {
	isolateCache(t)
	b := stagedBackend{Tool_name: ToolTea, User: "anna"}

	file := cacheFile{}.store(b, Detection{
		Installed: true,
		Logins:    []Login{{Name: "tea.example", User: "anna"}},
		Users:     map[string]string{"tea.example": "anna"},
		Verified:  true,
		Version:   "0.15.1",
		At:        time.Now(),
	})

	got, ok := file.lookup(b, DefaultCacheTTL)
	if !ok {
		t.Fatal("fresh entry was not served")
	}
	if got.Version != "0.15.1" || len(got.Logins) != 1 {
		t.Errorf("served %+v, want the stored logins and version", got)
	}
}

// A memo is a claim about the past. An entry older than the TTL has to be
// rejected, or fth would keep offering an instance whose credential was revoked
// an hour ago.
func TestLookupRejectsAStaleEntry(t *testing.T) {
	isolateCache(t)
	b := stagedBackend{Tool_name: ToolGh, User: "anna"}
	aged := Detection{Verified: true, At: time.Now().Add(-2 * DefaultCacheTTL)}
	file := cacheFile{}.store(b, aged)

	if _, ok := file.lookup(b, DefaultCacheTTL); ok {
		t.Error("served an entry twice its age old")
	}
	// Still aged, but the wider window covers it — the check is on age, not on
	// some absolute expiry.
	if _, ok := file.lookup(b, 5*DefaultCacheTTL); !ok {
		t.Error("rejected an entry that a longer TTL still covers")
	}
}

// Zero means -no-cache. It has to bypass even an entry written a second ago,
// since re-reading your own memo is not what that flag promises.
func TestLookupHonoursAZeroTTL(t *testing.T) {
	isolateCache(t)
	b := stagedBackend{Tool_name: ToolFj, User: "anna"}
	// Dated in the future. This is the only case the zero-TTL guard decides on its
	// own: the age check below rejects an ordinary entry for a zero TTL anyway,
	// since any real timestamp is in the past. A future one — clock skew, or a
	// hand-edited file — would otherwise sneak past an age check that compares a
	// negative elapsed time, and -no-cache would silently serve it.
	file := cacheFile{}.store(b, Detection{Verified: true, At: time.Now().Add(time.Hour)})

	if _, ok := file.lookup(b, 0); ok {
		t.Error("a zero TTL still served an entry; -no-cache would not force a probe")
	}
	if _, ok := file.lookup(b, DefaultCacheTTL); !ok {
		t.Error("rejected a future-dated entry under a normal TTL; only a zero TTL may")
	}

	DisableCache()
	if detectTTL != 0 {
		t.Errorf("detectTTL = %v after DisableCache, want 0", detectTTL)
	}
}

// A store written before timestamps existed, or by a hand-edited file, has a
// zero At. Nothing can be said about how old it is, so it must be re-probed
// rather than trusted forever.
func TestLookupRejectsAnEntryWithNoTimestamp(t *testing.T) {
	isolateCache(t)
	b := stagedBackend{Tool_name: ToolFj, User: "anna"}
	file := cacheFile{}.store(b, Detection{Verified: true})

	if _, ok := file.lookup(b, DefaultCacheTTL); ok {
		t.Error("served an entry with a zero At; its age is unknowable")
	}
}

// The key is the only thing standing between a memo for one binary and a run
// driving a different one. -try-bin exists precisely because the program named
// `try` on PATH is often not Trylle at all, so the two must not share an entry.
func TestCacheKeySeparatesBinaryOverrides(t *testing.T) {
	isolateCache(t)
	fromPath := tryBackend{}
	explicit := tryBackend{bin: "/opt/trylle-cli"}

	if cacheKey(fromPath) == cacheKey(explicit) {
		t.Fatalf("PATH-resolved try and an explicit -try-bin share key %q", cacheKey(fromPath))
	}
	if cacheKey(explicit) != cacheKey(tryBackend{bin: "/opt/trylle-cli"}) {
		t.Error("the same -try-bin produced two different keys")
	}

	file := cacheFile{}.store(fromPath, Detection{Verified: true, At: time.Now()})
	if _, ok := file.lookup(explicit, DefaultCacheTTL); ok {
		t.Error("an explicit -try-bin reused the entry measured against PATH")
	}
	if _, ok := file.lookup(fromPath, DefaultCacheTTL); !ok {
		t.Error("the PATH-resolved backend lost its own entry")
	}
}

// Tools with no binary override must not collide with each other either, or a
// warm fj entry would be reported as gh.
func TestCacheKeySeparatesTools(t *testing.T) {
	isolateCache(t)
	seen := map[string]Tool{}
	for _, tool := range AllTools() {
		b, err := Discover(tool)
		if err != nil {
			t.Fatalf("Discover(%s): %v", tool, err)
		}
		k := cacheKey(b)
		if prev, dup := seen[k]; dup {
			t.Errorf("%s and %s share key %q", prev, tool, k)
		}
		seen[k] = tool
	}
}

// Anything unreadable is simply not a cache. Every one of these files used to be
// a valid memo; all of them must degrade to "probe it", never to a crash and
// never to a misread answer.
func TestReadCacheDegradesOnBadInput(t *testing.T) {
	dir := isolateCache(t)
	path := filepath.Join(dir, "fth", "detect.json")

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"empty file", ""},
		{"not json", "this is not json"},
		{"truncated", `{"version":1,"entries":{"fj":{"key":"fj`},
		{"wrong schema version", `{"version":999,"entries":{}}`},
		{"null entries", `{"version":1,"entries":null}`},
		{"wrong types", `{"version":1,"entries":[1,2,3]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			f := readCache()
			if f.Version != cacheVersion {
				t.Errorf("Version = %d, want %d", f.Version, cacheVersion)
			}
			if len(f.Entries) != 0 {
				t.Errorf("kept %d entries from a bad file, want none", len(f.Entries))
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		// The subtests above each leave their malformed file behind, so this one
		// has to remove it or it reads the previous case's bytes and never reaches
		// the error path it is meant to cover.
		if err := os.Remove(path); err != nil {
			t.Fatalf("could not remove the malformed memo: %v", err)
		}
		if got := len(readCache().Entries); got != 0 {
			t.Errorf("read %d entries from a file that does not exist", got)
		}
	})
}

// The memo names the accounts and hosts the user is signed into. Credentials are
// redacted out of everything fth captures, but the list of instances is still the
// user's business, so the file and its directory must not be world-readable.
func TestWriteCacheIsPrivateAndAtomic(t *testing.T) {
	dir := isolateCache(t)
	b := stagedBackend{Tool_name: ToolFj, User: "anna"}

	writeCache(cacheFile{}.store(b, Detection{Verified: true, At: time.Now()}))

	path := filepath.Join(dir, "fth", "detect.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("memo was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("memo mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("cache dir mode = %o, want 700", perm)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("a temp file was left behind; the rename should have consumed it")
	}
}

// With nowhere to cache, fth must still work — it just probes every time.
func TestCacheDegradesWhenThereIsNowhereToStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")
	// Windows consults %LocalAppData% first and ignores the two above, so clearing
	// only those left the assertion failing there for a reason that has nothing to
	// do with the code under test.
	t.Setenv("LOCALAPPDATA", "")
	old := detectTTL
	t.Cleanup(func() { detectTTL = old })

	if p := cachePath(); p != "" {
		t.Errorf("cachePath() = %q with no home, want \"\"", p)
	}
	if len(readCache().Entries) != 0 {
		t.Error("readCache invented entries with no cache dir")
	}
	writeCache(cacheFile{}.store(stagedBackend{Tool_name: ToolFj}, Detection{At: time.Now()})) // must not panic

	out, err := Detect(t.Context(), []Tool{ToolFj})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("got %d candidates, want 1", len(out))
	}
}

// A warm run must be decided entirely from the memo. If it were still probing,
// the candidate would arrive late; if it were skipping the file, the backend
// would have to answer. This is the end-to-end assertion that the memo is on the
// path and not merely written on the side.
func TestDetectIsServedFromTheMemoWhenWarm(t *testing.T) {
	isolateCache(t)
	backends := []Backend{
		stagedBackend{Tool_name: ToolFj, Delay: 400 * time.Millisecond, User: "anna"},
		stagedBackend{Tool_name: ToolTea, Delay: 5 * time.Millisecond, User: "bob"},
	}
	// Warm it with the same keys the real run will use.
	seed := cacheFile{}
	for _, b := range backends {
		seed = seed.store(b, probe(t.Context(), b).detection())
	}
	writeCache(seed)

	start := time.Now()
	results := detectStream(t.Context(), backends)
	for r := range results {
		if users := r.Candidate.Users; len(users) == 0 {
			t.Errorf("%s arrived memoised but with no users: %+v", r.Candidate.Backend.Tool(), r.Candidate)
		}
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("a warm run took %v; the slowest memoised backend alone would be 400ms", elapsed)
	}
}

// Detection looks up from one snapshot while other goroutines accumulate results
// into another. Those two must not share a map: a concurrent write to a map being
// read is a runtime throw, not a lost update, and it only shows up under load —
// which is why this runs the stream the way it really runs.
func TestDetectStreamDoesNotShareTheCacheMap(t *testing.T) {
	isolateCache(t)
	// A wide, slow, mixed workload: every backend probes, so several goroutines
	// store at once while others look up.
	backends := make([]Backend, 12)
	for i := range backends {
		backends[i] = stagedBackend{
			Tool_name: Tool(string(rune('a'+i%5)) + "-backend"),
			Delay:     time.Duration(i%4) * time.Millisecond,
			User:      "anna",
		}
	}
	for range 20 {
		for r := range detectStream(t.Context(), backends) {
			_ = r
		}
	}
}

// A re-probe replaces the entry that is already there without changing how many
// entries exist, so detecting "did anything change" by comparing counts never
// noticed. The symptom is a memo that is refreshed in memory but never on disk:
// its timestamp stays old, so it is stale again the instant it is written, and a
// backend installed since the last run is never recorded at all.
func TestDetectStreamRefreshesAnEntryThatAlreadyExisted(t *testing.T) {
	dir := isolateCache(t)
	b := stagedBackend{Tool_name: ToolFj, User: "anna"}

	// An entry that is present but stale, which is the case a length check misses.
	writeCache(cacheFile{}.store(b, Detection{
		Verified: true,
		At:       time.Now().Add(-2 * DefaultCacheTTL),
	}))
	path := filepath.Join(dir, "fth", "detect.json")

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Well past mtime resolution, so a rewritten file is distinguishable.
	time.Sleep(10 * time.Millisecond)

	for range detectStream(t.Context(), []Backend{b}) {
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().After(before.ModTime()) {
		t.Errorf("the memo was not rewritten (mtime %v, was %v); a stale entry is refreshed in memory but not on disk",
			after.ModTime(), before.ModTime())
	}
	if _, ok := readCache().lookup(b, DefaultCacheTTL); !ok {
		t.Fatal("the refreshed memo is not usable; it will be re-probed every run")
	}
}

// Two fth runs overlap often enough — a terminal multiplexer starting several
// repos at once — and each holds the snapshot it read at startup. Writing that
// snapshot back wholesale would discard what the other run measured.
func TestWriteCacheMergesRatherThanOverwrites(t *testing.T) {
	isolateCache(t)
	fj := stagedBackend{Tool_name: ToolFj, User: "anna"}
	gh := stagedBackend{Tool_name: ToolGh, User: "bob"}

	// Our snapshot, which knows nothing about gh.
	mine := cacheFile{}.store(fj, Detection{Verified: true, Users: map[string]string{"fj.example": "anna"}, At: time.Now()})
	// What another process wrote in the meantime.
	writeCache(cacheFile{}.store(gh, Detection{Verified: true, Users: map[string]string{"gh.example": "bob"}, At: time.Now()}))

	writeCache(mine)

	file := readCache()
	if _, ok := file.lookup(fj, DefaultCacheTTL); !ok {
		t.Error("our own measurement was lost")
	}
	if _, ok := file.lookup(gh, DefaultCacheTTL); !ok {
		t.Error("another process's measurement was discarded by the merge")
	}
}

// Where two runs measured the same backend, the newer measurement must win —
// otherwise the older one is written back and the memo goes backwards in time.
func TestWriteCacheKeepsTheNewerMeasurement(t *testing.T) {
	isolateCache(t)
	b := stagedBackend{Tool_name: ToolFj, User: "anna"}

	old := Detection{Verified: false, Notes: []string{"stale failure"}, At: time.Now().Add(-time.Hour)}
	newer := Detection{Verified: true, Users: map[string]string{"fj.example": "anna"}, At: time.Now()}

	writeCache(cacheFile{}.store(b, old))
	writeCache(cacheFile{}.store(b, newer))

	got, ok := readCache().lookup(b, DefaultCacheTTL)
	if !ok || !got.Verified {
		t.Errorf("lookup returned %+v, want the newer successful measurement", got)
	}
}

// Installing a CLI changes which program PATH resolves, so a memo keyed only by
// tool name would hand back the previous program's logins until it expired.
func TestCacheKeyFollowsPath(t *testing.T) {
	t.Setenv("PATH", "/one")
	first := cacheKey(tryBackend{})
	t.Setenv("PATH", "/two")
	second := cacheKey(tryBackend{})

	if first == second {
		t.Errorf("key %q is the same for two different PATHs; a newly installed try would report the old one's logins", first)
	}
	t.Setenv("PATH", "/one")
	if again := cacheKey(tryBackend{}); again != first {
		t.Errorf("key is not stable for one PATH: %q then %q", first, again)
	}
	// An explicit binary does not depend on PATH at all, so it must not move when
	// PATH does.
	explicit := cacheKey(tryBackend{bin: "/opt/trylle-cli"})
	t.Setenv("PATH", "/two")
	if again := cacheKey(tryBackend{bin: "/opt/trylle-cli"}); again != explicit {
		t.Errorf("an explicit -try-bin key moved with PATH: %q then %q", explicit, again)
	}
}

// clone must produce a map that is genuinely separate. This is the invariant the
// concurrent-map panic rested on, asserted directly: assigning a struct holding a
// map copies the header, not the map, so the obvious `fresh := file` shares
// storage and every store is a write to the map other goroutines are reading.
//
// A race detector run cannot reliably demonstrate that on its own — it needs the
// unlucky interleaving, and a missing race report proves nothing. Asserting the
// aliasing itself does.
func TestCloneDoesNotShareTheEntryMap(t *testing.T) {
	original := cacheFile{}.store(stagedBackend{Tool_name: ToolFj},
		Detection{Verified: true, At: time.Now()})
	before := len(original.Entries)

	c := original.clone()
	c.store(stagedBackend{Tool_name: ToolGh}, Detection{Verified: true, At: time.Now()})

	if got := len(original.Entries); got != before {
		t.Errorf("storing into the clone changed the original: %d entries, want %d", got, before)
	}
	if _, leaked := original.Entries[cacheKey(stagedBackend{Tool_name: ToolGh})]; leaked {
		t.Error("the clone shares the original's map; a concurrent store is a concurrent map write")
	}
	// And the clone still carries what it was made from.
	if got := len(c.Entries); got != 2 {
		t.Errorf("clone has %d entries, want 2", got)
	}
}
