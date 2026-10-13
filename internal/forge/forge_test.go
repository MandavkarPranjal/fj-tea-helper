package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"fj v0.6.0", "0.6.0"},
		{"Version:\t0.15.1\tgolang: 1.26.5", "0.15.1"},
		{"Version: 1.2", "1.2"},
		{"fj 0.6.0", "0.6.0"},
		{"no version here", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseVersion(c.line); got != c.want {
			t.Errorf("parseVersion(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestParseFjRepoHarvestsURLs(t *testing.T) {
	out := "file:///srv/remote.git/Owner/proj.git\nhttps://codeberg.org/Owner/proj\n"
	req := CreateRequest{Login: "codeberg.org", Owner: "Owner", Name: "proj"}
	r, err := parseFjRepo(out, req)
	if err != nil {
		t.Fatal(err)
	}
	if r.CloneURL != "file:///srv/remote.git/Owner/proj.git" {
		t.Errorf("CloneURL = %q", r.CloneURL)
	}
	if r.WebURL != "https://codeberg.org/Owner/proj" {
		t.Errorf("WebURL = %q", r.WebURL)
	}
	// The SSH URL is not in the output, so it is derived from the host.
	if r.SSHURL != "git@codeberg.org:Owner/proj.git" {
		t.Errorf("SSHURL = %q", r.SSHURL)
	}
}

func TestParseFjRepoSSHRemote(t *testing.T) {
	out := "git@codeberg.org:Owner/proj.git\n"
	req := CreateRequest{Login: "codeberg.org", Owner: "Owner", Name: "proj"}
	r, _ := parseFjRepo(out, req)
	if r.SSHURL != "git@codeberg.org:Owner/proj.git" {
		t.Errorf("SSHURL = %q", r.SSHURL)
	}
	// Derived https clone URL should still be available.
	if r.CloneURL != "https://codeberg.org/Owner/proj.git" {
		t.Errorf("CloneURL = %q", r.CloneURL)
	}
}

func TestParseFjRepoPrefersJSON(t *testing.T) {
	out := `{"full_name":"o/n","html_url":"https://h/o/n","ssh_url":"git@h:o/n.git","clone_url":"https://h/o/n.git","owner":{"login":"o"},"name":"n"}`
	req := CreateRequest{Login: "h", Owner: "o", Name: "n"}
	r, _ := parseFjRepo(out, req)
	if r.FullName != "o/n" || r.CloneURL != "https://h/o/n.git" {
		t.Errorf("unexpected repo: %+v", r)
	}
}

func TestParseTeaRepo(t *testing.T) {
	out := `{"full_name":"o/n","html_url":"https://h/o/n","ssh_url":"git@h:o/n.git","clone_url":"https://h/o/n.git","owner":{"login":"o"},"name":"n"}`
	req := CreateRequest{Login: "h", Owner: "o", Name: "n"}
	r, _ := parseTeaRepo(out, req)
	if r.Owner != "o" || r.Name != "n" || r.CloneURL != "https://h/o/n.git" {
		t.Errorf("unexpected repo: %+v", r)
	}
}

func TestFirstUserLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"\n  # MandavkarPranjal  \n  \n  https://pr5.dev  \n", "MandavkarPranjal"},
		{"StubForgejoUser\n", "StubForgejoUser"},
		{"  Follower Count: 0  \n", ""},
		{"", ""},
		// Real `fj whoami` output. The user and host are each wrapped in
		// Unicode isolates (U+2068 FIRST STRONG ISOLATE / U+2069 POP
		// DIRECTIONAL ISOLATE) so the name cannot be spoofed; those must be
		// stripped before the sentence can be parsed. Escaped here so the
		// invisible characters stay visible in review.
		{"currently signed into \u2068MandavkarPranjal\u2069@\u2068codeberg.org\u2069\n", "MandavkarPranjal"},
		{"  currently signed into \u2068some\u2069@\u2068git.example.com\u2069  \n", "some"},
	}
	for _, c := range cases {
		if got := firstUserLine(c.in); got != c.want {
			t.Errorf("firstUserLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRepoFromParts(t *testing.T) {
	r := repoFromParts(CreateRequest{Login: "https://codeberg.org/", Owner: "o", Name: "n"})
	if r.CloneURL != "https://codeberg.org/o/n.git" {
		t.Errorf("CloneURL = %q", r.CloneURL)
	}
	if r.SSHURL != "git@codeberg.org:o/n.git" {
		t.Errorf("SSHURL = %q", r.SSHURL)
	}
}

func TestSanitizeName(t *testing.T) {
	if _, err := SanitizeName("ok-name"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	for _, bad := range []string{"", "  ", "a b", "a/b", "a\\b"} {
		if _, err := SanitizeName(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestIsAuthError(t *testing.T) {
	if !isAuthError("api error: grant does not exist") {
		t.Error("should detect fj grant failure")
	}
	if isAuthError("repository name already exists") {
		t.Error("should not treat a normal error as auth failure")
	}
}

func fakeCandidates() []Candidate {
	fj, _ := Discover(ToolFj)
	tea, _ := Discover(ToolTea)
	return []Candidate{
		{
			Backend:  fj,
			Verified: true,
			Logins: []Login{
				{Name: "git.acme.com"},
				{Name: "codeberg.org", Default: true},
			},
			Users: map[string]string{"git.acme.com": "alice", "codeberg.org": "a"},
		},
		{
			Backend:  tea,
			Verified: true,
			Logins:   []Login{{Name: "gitea.com"}},
			Users:    map[string]string{"gitea.com": "b"},
		},
	}
}

// Every tool/login pair fth could act on, one flat list to choose from.
func TestTargetsFlattensToolsAndLogins(t *testing.T) {
	got := Targets(fakeCandidates())
	want := []string{"fj/codeberg.org", "fj/git.acme.com", "tea/gitea.com"}
	if len(got) != len(want) {
		t.Fatalf("Targets = %d entries, want %d (%v)", len(got), len(want), targetIDs(got))
	}
	for i, id := range want {
		if got[i].ID() != id {
			t.Errorf("Targets[%d] = %q, want %q", i, got[i].ID(), id)
		}
	}
	// The default login leads its tool, so the first entry is the one fth
	// would have picked without asking.
	if got[0].Account != "a" {
		t.Errorf("first target account = %q, want a", got[0].Account)
	}
}

// A login whose credential is broken must not be offered: fj lists hosts whose
// OAuth grant has been revoked, and picking one would fail confusingly later.
func TestTargetsSkipsUnverifiedLogins(t *testing.T) {
	cands := fakeCandidates()
	cands[0].Users = map[string]string{"codeberg.org": "a"} // git.acme.com never verified
	got := Targets(cands)
	if len(got) != 2 {
		t.Fatalf("Targets = %v, want fj/codeberg.org and tea/gitea.com only", targetIDs(got))
	}
	for _, tg := range got {
		if tg.Login.Name == "git.acme.com" {
			t.Error("unverified login should not be offered")
		}
	}
}

func TestFilterNarrowsByToolAndHost(t *testing.T) {
	cands := fakeCandidates()
	all := Targets(cands)

	got, err := Filter(cands, all, "", "")
	if err != nil || len(got) != 3 {
		t.Errorf("no filters = %v (%v), want all three", targetIDs(got), err)
	}
	got, err = Filter(cands, all, "fj", "")
	if err != nil || len(got) != 2 {
		t.Errorf("-tool fj = %v (%v), want the two fj logins", targetIDs(got), err)
	}
	got, err = Filter(cands, all, "fj", "git.acme.com")
	if err != nil || len(got) != 1 || got[0].ID() != "fj/git.acme.com" {
		t.Errorf("-tool fj -host git.acme.com = %v (%v)", targetIDs(got), err)
	}
	// "auto" is the default and must not filter anything out.
	got, err = Filter(cands, all, "auto", "")
	if err != nil || len(got) != 3 {
		t.Errorf("-tool auto = %v (%v), want all three", targetIDs(got), err)
	}
}

// -host may be spelled as the login name, the full URL, or a bare host.
func TestFilterMatchesHostSpellings(t *testing.T) {
	cands := fakeCandidates()
	all := Targets(cands)
	for _, want := range []string{"gitea.com", "https://gitea.com"} {
		got, err := Filter(cands, all, "tea", want)
		if err != nil || len(got) != 1 || got[0].Login.Name != "gitea.com" {
			t.Errorf("-host %q = %v (%v)", want, targetIDs(got), err)
		}
	}
}

// A flag that matches nothing is an error, never a silent fallback to some
// other instance than the one the user asked for.
func TestFilterReportsUnmatchedFlags(t *testing.T) {
	cands := fakeCandidates()
	all := Targets(cands)
	if _, err := Filter(cands, all, "gitlab", ""); err == nil {
		t.Error("expected an error for an unknown tool")
	}
	_, err := Filter(cands, all, "", "nope.example")
	if err == nil || !strings.Contains(err.Error(), "known:") {
		t.Errorf("unknown host error = %v, want one listing the known targets", err)
	}
}

// slowBackend is a Backend whose every network call takes a fixed time, used to
// measure how long probing takes rather than what it returns.
type slowBackend struct {
	Tool_name Tool
	// Delay is applied to Logins and to each WhoAmI.
	Delay time.Duration
	// Dead hosts are logins whose verification always fails.
	Dead map[string]bool
	// WhoAmIDelay overrides the per-login delay, so a test can make logins finish
	// in an order other than the one they were listed in.
	WhoAmIDelay func(login string) time.Duration
}

func (b slowBackend) isDead(login string) bool { return b.Dead[login] }

func (b slowBackend) whoAmIDelay(login string) time.Duration {
	if b.WhoAmIDelay != nil {
		return b.WhoAmIDelay(login)
	}
	return b.Delay
}

func (b slowBackend) Tool() Tool                         { return b.Tool_name }
func (b slowBackend) Available(ctx context.Context) bool { return true }
func (b slowBackend) LoginHint() string                  { return "test auth login" }
func (b slowBackend) Version(ctx context.Context) string { return "1.0.0" }

func (b slowBackend) CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error) {
	return &Repo{}, nil
}

// wait stands in for a network round trip. It honours ctx, the way the real
// CLIs do when their deadline expires, so a test can simulate an unreachable host
// instead of actually hanging.
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (b slowBackend) Logins(ctx context.Context) ([]Login, error) {
	if err := wait(ctx, b.Delay); err != nil {
		return nil, err
	}
	// a.example and b.example are always present, so a fixture that marks them dead
	// does not end up listing the same login twice. Sorted, because the whole point
	// is that notes come back in login order.
	set := map[string]bool{"a.example": true, "b.example": true}
	for n := range b.Dead {
		set[n] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	logins := make([]Login, len(names))
	for i, n := range names {
		logins[i] = Login{Name: n}
	}
	return logins, nil
}

func (b slowBackend) WhoAmI(ctx context.Context, login string) (string, error) {
	if err := wait(ctx, b.whoAmIDelay(login)); err != nil {
		return "", err
	}
	if b.isDead(login) {
		return "", fmt.Errorf("host is unreachable")
	}
	return "user-" + login[0:1], nil
}

// Probing N logins must cost one delay, not N. This is the regression test for
// the sequential version of probe, which made a three-login backend take three
// times as long as it needed to.
func TestProbeVerifiesLoginsConcurrently(t *testing.T) {
	const delay = 60 * time.Millisecond
	start := time.Now()
	c := probe(context.Background(), slowBackend{Tool_name: ToolFj, Delay: delay})
	elapsed := time.Since(start)

	if got, want := len(c.Users), 2; got != want {
		t.Fatalf("Users = %v, want %d verified logins", c.Users, want)
	}
	if !c.Verified {
		t.Error("a backend with a working login must be Verified")
	}
	// Concurrent is two delays: Logins, then the three WhoAmI at once. Serial is
	// four. The threshold sits between them, and deliberately closer to the
	// concurrent figure — a five-delay limit would have let the serial
	// implementation, at four delays, pass this test while proving nothing.
	if limit := 3 * delay; elapsed > limit {
		t.Errorf("probe took %v, want under %v (logins are not concurrent)", elapsed, limit)
	}
}

// Concurrency must not cost determinism: the users map and the notes have to be
// assembled in login order, so two runs against the same hosts agree.
func TestProbeOutputIsOrderedNotCompletionOrdered(t *testing.T) {
	// Three failures, and the one listed first is the slowest to answer. A single
	// failure could not detect this at all — there is only one note, so it looks
	// ordered whether or not it is — and this ordering is only observable because
	// completion order is the reverse of login order.
	b := slowBackend{
		Tool_name: ToolFj,
		Delay:     10 * time.Millisecond,
		Dead: map[string]bool{
			"a.example": true,
			"b.example": true,
			"c.example": true,
		},
		WhoAmIDelay: func(login string) time.Duration {
			// a.example is listed first but answers last, so completion order is the
			// reverse of login order.
			if login == "a.example" {
				return 40 * time.Millisecond
			}
			return 5 * time.Millisecond
		},
	}

	first := probe(context.Background(), b)
	want := []string{"a.example", "b.example", "c.example"}
	if len(first.Notes) != len(want) {
		t.Fatalf("got %d notes, want %d: %q", len(first.Notes), len(want), first.Notes)
	}
	for i, host := range want {
		if !strings.Contains(first.Notes[i], host) {
			t.Errorf("Notes[%d] = %q, want it to name %s", i, first.Notes[i], host)
		}
	}
	// The reason has to survive too, or the user cannot tell which of their hosts
	// is broken.
	for i, note := range first.Notes {
		if !strings.Contains(note, "unreachable") {
			t.Errorf("Notes[%d] = %q, want the reason", i, note)
		}
	}

	// Repeated runs must agree exactly. Comparing only Notes[0] was not enough to
	// catch reordering of the ones after it.
	for range 5 {
		got := probe(context.Background(), b)
		if !slices.Equal(got.Notes, first.Notes) {
			t.Fatalf("notes vary between runs:\n got %q\nwant %q", got.Notes, first.Notes)
		}
		for name, user := range first.Users {
			if got.Users[name] != user {
				t.Errorf("Users[%q] = %q, want %q", name, got.Users[name], user)
			}
		}
	}
}

// A backend that never answers must not hold the run open: the probe has its own
// deadline, so an absent host costs the budget rather than the whole invocation.
func TestProbeHonoursItsDeadline(t *testing.T) {
	// The caller's context is deliberately left alive. The point is probe's *own*
	// deadline, and a short deadline on the caller's context made this pass even if
	// probeTimeout were never applied at all.
	old := probeTimeout
	probeTimeout = 80 * time.Millisecond
	t.Cleanup(func() { probeTimeout = old })

	b := slowBackend{Tool_name: ToolFj, Delay: time.Hour}

	start := time.Now()
	c := probe(context.Background(), b)
	elapsed := time.Since(start)

	// Generous upper bound: the deadline is 80ms, but a loaded machine is allowed
	// to take its time reporting it. A missing deadline means an hour, not a
	// slightly slow return.
	if limit := 5 * time.Second; elapsed > limit {
		t.Errorf("probe took %v, so it ignored its own %v deadline", elapsed, probeTimeout)
	}
	// Nothing verified is correct here: the point is that it returned at all.
	if c.Verified {
		t.Error("a probe that never completed must not claim a verified login")
	}
}

// countingBackend records how many times each method ran.
type countingBackend struct {
	Tool_name Tool
	mu        sync.Mutex
	versions  int
	avail     int
}

func (b *countingBackend) Tool() Tool        { return b.Tool_name }
func (b *countingBackend) LoginHint() string { return "test auth login" }
func (b *countingBackend) Available(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.avail++
	return true
}
func (b *countingBackend) Version(ctx context.Context) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.versions++
	return "1.0.0"
}
func (b *countingBackend) Logins(ctx context.Context) ([]Login, error) {
	return []Login{{Name: "a.example", User: "alice"}}, nil
}
func (b *countingBackend) WhoAmI(ctx context.Context, l string) (string, error) {
	return "alice", nil
}
func (b *countingBackend) CreateRepo(ctx context.Context, r CreateRequest) (*Repo, error) {
	return &Repo{}, nil
}

// Printing the listing must not shell out again. Status used to call
// Available and Version itself, so every fth run paid a second process per
// backend — about 650ms on a machine with all five CLIs — to learn facts Detect
// had already established.
func TestStatusDoesNotReprobe(t *testing.T) {
	b := &countingBackend{Tool_name: ToolFj}
	c := probe(context.Background(), b)
	if got := b.versions; got != 1 {
		t.Fatalf("probe ran Version %d times, want 1", got)
	}
	if got := b.avail; got != 1 {
		t.Fatalf("probe ran Available %d times, want 1", got)
	}

	if got := c.Status(); got != "fj 1.0.0" {
		t.Errorf("Status = %q, want %q", got, "fj 1.0.0")
	}
	for range 3 {
		_ = c.Status()
	}
	// Both, not just Version. Counting only Version would have passed even if
	// Status went on consulting Available for every row it printed, which is the
	// other half of what this test exists to pin down.
	if got := b.versions; got != 1 {
		t.Errorf("Status ran Version %d times in total, want 1", got)
	}
	if got := b.avail; got != 1 {
		t.Errorf("Status ran Available %d times in total, want 1", got)
	}
}

// A Candidate nobody probed must still answer honestly rather than reporting a
// zero-value installed flag as "not installed".
func TestStatusOnUnprobedCandidate(t *testing.T) {
	// A fake, not Discover(ToolFj): that hands back the real fj backend, so the
	// outcome depended on whether fj happened to be installed on the machine
	// running the suite. Here it is installed and verified by construction.
	b := &countingBackend{Tool_name: ToolFj}
	c := Candidate{Backend: b, Verified: true}

	if got := c.Status(); got == "not installed" {
		t.Error("an unprobed Candidate must ask the backend, not assume absence")
	}
	// Asking about availability is the fallback this case allows. The version has
	// no recorded value to fall back on, so it has to come from the backend too —
	// otherwise Status prints a bare tool name with nothing after it.
	if got := c.Status(); got != "fj 1.0.0" {
		t.Errorf("Status = %q, want %q", got, "fj 1.0.0")
	}
}

// run() folds CLI output into the errors it returns, so anything a backend
// prints can reach a terminal or a log. Credentials must never survive that.
func TestRedactSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     string
	}{
		{
			name: "json token",
			in:   `{"data":{"username":"u","token":"gct_3a32XXXXXX291f"}}`,
			want: `{"data":{"username":"u","token":"[redacted]"}}`,
		},
		{
			name: "bare token prefix",
			in:   "authorization failed for gct_abcdef0123456789",
			want: "authorization failed for [redacted]",
		},
		{
			name: "github token",
			in:   "using ghp_0123456789abcdefghij",
			want: "using [redacted]",
		},
		{
			name: "yaml style password",
			in:   "password = hunter2hunter2",
			want: "password = [redacted]",
		},
		{
			name: "unrelated text is untouched",
			in:   "repository owner/name already exists",
			want: "repository owner/name already exists",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactSecrets(tc.in); got != tc.want {
				t.Errorf("redactSecrets(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// Redaction must not corrupt JSON, since the backends parse what they print.
func TestRedactSecretsKeepsJSONValid(t *testing.T) {
	in := `{"data":{"host":"git.cafe","username":"u","token":"gct_3a32XXXXXX291f"}}`
	var v map[string]any
	if err := json.Unmarshal([]byte(redactSecrets(in)), &v); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}
}
