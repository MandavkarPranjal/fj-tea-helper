package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

// newRepoWithCommit creates a repository containing one commit and returns
// its path.
func newRepoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		if _, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "commit", "--quiet", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInspectDetectsExistingRepo(t *testing.T) {
	dir := newRepoWithCommit(t)
	st, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.IsRepo || !st.HasCommits {
		t.Errorf("expected repo with commits, got %+v", st)
	}
	if st.Branch == "" {
		t.Error("expected a branch name")
	}
}

func TestInspectMissingDir(t *testing.T) {
	st, err := Inspect(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Exists || st.IsRepo {
		t.Errorf("expected nothing to exist, got %+v", st)
	}
}

func TestInspectPlainDirIsNotARepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.IsRepo {
		t.Error("plain directory must not be reported as a repository")
	}
}

func TestInspectDetectsNestedRepo(t *testing.T) {
	// A subdirectory of a repository must be flagged, because adding a
	// remote there would silently modify the outer repository.
	outer := newRepoWithCommit(t)
	inner := filepath.Join(outer, "sub", "deep")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(inner)
	if err != nil {
		t.Fatal(err)
	}
	if !st.NestedInRepo {
		t.Errorf("expected NestedInRepo, got %+v", st)
	}
	if st.IsRepo {
		t.Error("a nested directory is not itself the repo root")
	}
}

func TestInspectListsRemotes(t *testing.T) {
	dir := newRepoWithCommit(t)
	if _, err := git(dir, "remote", "add", "origin", "https://example.com/a.git"); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.RemoteExists("origin") || st.RemoteExists("nope") {
		t.Errorf("unexpected remotes: %v", st.Remotes)
	}
}

func TestAttachDoesNotWriteStarterFiles(t *testing.T) {
	dir := newRepoWithCommit(t)
	res, err := Attach(dir, Options{
		RemoteName:    "codeberg",
		RemoteURL:     "https://codeberg.org/o/n.git",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Attached || !res.RemoteAdded {
		t.Errorf("unexpected result: %+v", res)
	}
	if len(res.FilesCreated) != 0 {
		t.Errorf("Attach must not write files, got %v", res.FilesCreated)
	}
	for _, f := range []string{"README.md", ".gitignore", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("Attach must not create %s", f)
		}
	}
	url, err := git(dir, "remote", "get-url", "codeberg")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://codeberg.org/o/n.git" {
		t.Errorf("remote url = %q", url)
	}
}

func TestSetRemoteNeverRepointsExisting(t *testing.T) {
	// The old code fell back to `remote set-url`, which silently moved an
	// established origin somewhere else.
	dir := newRepoWithCommit(t)
	if _, err := git(dir, "remote", "add", "origin", "https://github.com/o/n.git"); err != nil {
		t.Fatal(err)
	}
	err := setRemote(dir, "origin", "https://codeberg.org/o/n.git")
	if err == nil {
		t.Fatal("expected a refusal to repoint an existing remote")
	}
	url, _ := git(dir, "remote", "get-url", "origin")
	if url != "https://github.com/o/n.git" {
		t.Errorf("origin was moved to %q", url)
	}
}

func TestSetRemoteIsIdempotent(t *testing.T) {
	dir := newRepoWithCommit(t)
	const url = "https://codeberg.org/o/n.git"
	if err := setRemote(dir, "origin", url); err != nil {
		t.Fatal(err)
	}
	if err := setRemote(dir, "origin", url); err != nil {
		t.Errorf("re-adding the same URL should be accepted, got %v", err)
	}
}

func TestPushWithoutUpstreamLeavesTrackingAlone(t *testing.T) {
	// This is the scenario the feature exists for: the repository already
	// tracks origin, and fth pushes the same branch to a second remote.
	// Tracking must stay on origin.
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
	} {
		if _, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "commit", "--quiet", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	origin := t.TempDir()
	if _, err := git(origin, "init", "--bare", "--quiet"); err != nil {
		t.Fatal(err)
	}
	codeberg := t.TempDir()
	if _, err := git(codeberg, "init", "--bare", "--quiet"); err != nil {
		t.Fatal(err)
	}

	if _, err := git(dir, "remote", "add", "origin", origin); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "remote", "add", "codeberg", codeberg); err != nil {
		t.Fatal(err)
	}
	if err := Push(dir, "origin", "main", true); err != nil {
		t.Fatal(err)
	}
	before, err := git(dir, "rev-parse", "--abbrev-ref", "main@{upstream}")
	if err != nil {
		t.Fatal(err)
	}
	if before != "origin/main" {
		t.Fatalf("setup: upstream = %q", before)
	}

	// Now push to the new remote without touching upstream.
	if err := Push(dir, "codeberg", "main", false); err != nil {
		t.Fatal(err)
	}
	after, err := git(dir, "rev-parse", "--abbrev-ref", "main@{upstream}")
	if err != nil {
		t.Fatal(err)
	}
	if after != "origin/main" {
		t.Errorf("upstream moved to %q, want origin/main", after)
	}
	// The push must actually have happened.
	if _, err := git(codeberg, "log", "--oneline", "main"); err != nil {
		t.Errorf("codeberg remote did not receive the branch: %v", err)
	}
}

// seededRemote builds a bare remote whose branch holds only a lone root commit,
// the shape every host creates for a fresh repository. It returns the remote
// path and the seed commit's SHA.
func seededRemote(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	if _, err := git(dir, "init", "--bare", "--quiet"); err != nil {
		t.Fatal(err)
	}
	// Seed it the way a host does: a single README commit on a branch that
	// shares no history with the local repository.
	work := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "host@example.com"},
		{"config", "user.name", "Host"},
	} {
		if _, err := git(work, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "commit", "--quiet", "-m", "Initial commit"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "remote", "add", "origin", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "push", "--quiet", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	sha, err := git(work, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return dir, sha
}

func TestPushReconcilingSeedReplacesPlaceholder(t *testing.T) {
	// The real-world failure: the host seeded an "Initial commit", so a plain
	// push is rejected as non-fast-forward even though nothing real is there.
	remote, seed := seededRemote(t)
	dir := newRepoWithCommit(t)
	if _, err := git(dir, "remote", "add", "try", remote); err != nil {
		t.Fatal(err)
	}

	if err := Push(dir, "try", "main", true); err == nil {
		t.Fatal("plain push should be rejected by the placeholder commit")
	}

	replaced, err := PushReconcilingSeed(dir, "try", "main", true)
	if err != nil {
		t.Fatalf("PushReconcilingSeed: %v", err)
	}
	if !replaced {
		t.Error("expected the placeholder to be reported as replaced")
	}
	// The local history must be what landed, not the README.
	head, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	got, err := git(remote, "rev-parse", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Errorf("remote tip = %s, want local %s (seed was %s)", got, head, seed)
	}
}

func TestPushReconcilingSeedRefusesRealHistory(t *testing.T) {
	// A remote with actual commits must never be overwritten, however many
	// there are. This is the guard that keeps the seed handling from becoming
	// a data-loss bug.
	remote, _ := seededRemote(t)
	// Add a second, unrelated commit on the remote.
	work := t.TempDir()
	if _, err := git(work, "clone", "--quiet", remote, "."); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"config", "user.email", "dev@example.com"},
		{"config", "user.name", "Dev"},
	} {
		if _, err := git(work, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "work.txt"), []byte("real work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "commit", "--quiet", "-m", "real work"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(work, "push", "--quiet", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	realTip, err := git(work, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	dir := newRepoWithCommit(t)
	if _, err := git(dir, "remote", "add", "try", remote); err != nil {
		t.Fatal(err)
	}
	replaced, err := PushReconcilingSeed(dir, "try", "main", false)
	if err == nil {
		t.Fatal("expected an error: real remote history must not be overwritten")
	}
	if replaced {
		t.Error("must not report replacing a seed when the remote holds real history")
	}
	after, err := git(remote, "rev-parse", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if after != realTip {
		t.Errorf("remote tip moved to %s, want %s: real work was overwritten", after, realTip)
	}
}

func TestPushReconcilingSeedLeavesCleanRemoteAlone(t *testing.T) {
	// The overwhelmingly common case: nothing on the remote. The push should
	// just work and not claim it replaced anything.
	remote := t.TempDir()
	if _, err := git(remote, "init", "--bare", "--quiet"); err != nil {
		t.Fatal(err)
	}
	dir := newRepoWithCommit(t)
	if _, err := git(dir, "remote", "add", "try", remote); err != nil {
		t.Fatal(err)
	}
	replaced, err := PushReconcilingSeed(dir, "try", "main", true)
	if err != nil {
		t.Fatalf("PushReconcilingSeed: %v", err)
	}
	if replaced {
		t.Error("nothing should have been replaced on an empty remote")
	}
}

func TestPushReconcilingSeedIgnoresUnrelatedFailures(t *testing.T) {
	// An unreachable remote must surface its own error rather than being
	// mistaken for a fast-forward rejection.
	dir := newRepoWithCommit(t)
	if _, err := git(dir, "remote", "add", "try", t.TempDir()+"/missing.git"); err != nil {
		t.Fatal(err)
	}
	if _, err := PushReconcilingSeed(dir, "try", "main", false); err == nil {
		t.Fatal("expected the push to fail against a missing remote")
	}
}

func TestSuggestRemoteName(t *testing.T) {
	taken := func(names ...string) func(string) bool {
		return func(n string) bool {
			for _, c := range names {
				if c == n {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		host  string
		taken func(string) bool
		want  string
	}{
		{"codeberg.org", nil, "codeberg"},
		{"gitea.com", nil, "gitea"},
		{"codeberg.org", taken("codeberg"), "forge"},
		{"codeberg.org", taken("codeberg", "forge", "upstream"), "mirror"},
		{"codeberg.org", taken("codeberg", "forge", "upstream", "mirror"), "codeberg2"},
		{"", nil, "forge"},
		{"127.0.0.1", nil, "forge"},
	}
	for _, c := range cases {
		if got := SuggestRemoteName(c.host, c.taken); got != c.want {
			t.Errorf("SuggestRemoteName(%q) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestValidRemoteName(t *testing.T) {
	good := []string{"origin", "upstream", "codeberg", "my.remote", "a-b_c"}
	bad := []string{"", "-leading", "has space", "has/slash", "has:colon"}
	for _, n := range good {
		if !ValidRemoteName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range bad {
		if ValidRemoteName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestEffectiveEntryCountIgnoresDotGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	n, err := effectiveEntryCount(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a directory holding only .git should count as empty, got %d", n)
	}
}
