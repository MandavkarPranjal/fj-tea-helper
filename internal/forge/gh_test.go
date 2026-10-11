package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStubGh installs a fake `gh` binary on PATH for the duration of the test.
func writeStubGh(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// stubGhAuth is a `gh auth status --json hosts` that reports one working
// github.com login and one Enterprise login whose token has expired.
const stubGhAuth = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "gh version 2.62.0 (2024-11-14)"; exit 0; fi
case " $* " in
  *" auth status "*)
    cat <<'EOF'
{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat","gitProtocol":"https"}],"ghe.example.com":[{"state":"success","active":false,"host":"ghe.example.com","login":"old"},{"state":"HTTP 401","active":true,"host":"ghe.example.com","login":"broken"}]}}
EOF
    exit 0 ;;
esac
exit 1
`

func TestGhAvailableAndVersion(t *testing.T) {
	writeStubGh(t, stubGhAuth)
	g := ghBackend{}
	if !g.Available(context.Background()) {
		t.Fatal("expected the stub gh to be available")
	}
	if got := g.Version(context.Background()); got != "2.62.0" {
		t.Errorf("Version() = %q, want 2.62.0", got)
	}
}

func TestGhLoginsAndWhoAmI(t *testing.T) {
	writeStubGh(t, stubGhAuth)
	g := ghBackend{}
	ctx := context.Background()

	logins, err := g.Logins(ctx)
	if err != nil {
		t.Fatalf("Logins: %v", err)
	}
	if len(logins) != 2 {
		t.Fatalf("got %d logins, want 2: %+v", len(logins), logins)
	}
	// github.com leads, so it is the entry Enter picks for gh.
	if logins[0].Name != "github.com" || logins[0].User != "octocat" || !logins[0].Default {
		t.Errorf("logins[0] = %+v, want github.com/octocat/default", logins[0])
	}
	if logins[0].URL != "https://github.com" {
		t.Errorf("URL = %q, want https://github.com", logins[0].URL)
	}
	// A host whose active account has a dead token must be reported without a
	// user, which is what marks it unverified and keeps it out of the picker.
	if logins[1].Name != "ghe.example.com" {
		t.Errorf("logins[1] = %+v, want ghe.example.com", logins[1])
	}
	if logins[1].User != "" {
		t.Errorf("expired login resolved to user %q, want none", logins[1].User)
	}

	u, err := g.WhoAmI(ctx, "github.com")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if u != "octocat" {
		t.Errorf("WhoAmI() = %q, want octocat", u)
	}
	if _, err := g.WhoAmI(ctx, "ghe.example.com"); err == nil {
		t.Error("expected an error for a host with no working account")
	}
}

// ghHostOrder exists only because Go randomises map iteration: an unsorted list
// would reshuffle the instance picker between runs.
func TestGhHostOrderIsStableAndPutsGitHubFirst(t *testing.T) {
	hosts := map[string][]ghAccount{
		"zeta.example.com":  nil,
		"github.com":        nil,
		"alpha.example.com": nil,
	}
	want := []string{"github.com", "alpha.example.com", "zeta.example.com"}
	for i := 0; i < 20; i++ {
		got := ghHostOrder(hosts)
		if len(got) != len(want) {
			t.Fatalf("ghHostOrder = %v", got)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("ghHostOrder = %v, want %v", got, want)
			}
		}
	}
}

func TestGhLoginsWithoutAnyWorkingAccount(t *testing.T) {
	writeStubGh(t, `#!/bin/sh
case " $* " in
  *" auth status "*)
    echo '{"hosts":{"github.com":[{"state":"HTTP 401","active":true,"host":"github.com","login":"broken"}]}}'
    exit 0 ;;
esac
exit 1
`)
	_, err := ghBackend{}.Logins(context.Background())
	if err == nil {
		t.Fatal("expected an error when no account works")
	}
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("error = %v, want it to name the login command", err)
	}
}

func TestGhCreateRepoPassesVisibilityAndDescription(t *testing.T) {
	logPath := argsLog(t)
	writeStubGh(t, `#!/bin/sh
echo "$@" >> "$ARGS_LOG"
case " $* " in
  *" repo create "*) echo "https://github.com/octocat/demo"; exit 0 ;;
  *" repo view "*)   echo '{"nameWithOwner":"octocat/demo","url":"https://github.com/octocat/demo","sshUrl":"git@github.com:octocat/demo.git"}'; exit 0 ;;
esac
exit 1
`)

	g := ghBackend{}
	repo, err := g.CreateRepo(context.Background(), CreateRequest{
		Login: "github.com", Owner: "octocat", Account: "octocat",
		Name: "demo", Description: "hello",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.FullName != "octocat/demo" {
		t.Errorf("FullName = %q, want octocat/demo", repo.FullName)
	}
	// gh reports no URLs on create, so they must come from `gh repo view`.
	if repo.WebURL != "https://github.com/octocat/demo" {
		t.Errorf("WebURL = %q", repo.WebURL)
	}
	if repo.CloneURL != "https://github.com/octocat/demo.git" {
		t.Errorf("CloneURL = %q", repo.CloneURL)
	}
	if repo.SSHURL != "git@github.com:octocat/demo.git" {
		t.Errorf("SSHURL = %q", repo.SSHURL)
	}

	args := readFileForTest(t, logPath)
	// Visibility must be explicit: without it gh drops into interactive
	// prompts, which fth must never reach.
	if !strings.Contains(args, "--public") {
		t.Errorf("visibility not sent: %s", args)
	}
	if !strings.Contains(args, "repo create octocat/demo") {
		t.Errorf("owner not passed as part of the name: %s", args)
	}
	if !strings.Contains(args, "--description hello") {
		t.Errorf("missing description: %s", args)
	}
	// Seeding files server-side would create a commit that collides with the
	// one fth pushes from the local scaffold.
	for _, forbidden := range []string{"--add-readme", "--gitignore", "--license", "--clone", "--push"} {
		if strings.Contains(args, forbidden) {
			t.Errorf("%s must not be passed to gh: %s", forbidden, args)
		}
	}
}

func TestGhCreateRepoPrivateAndEnterpriseHost(t *testing.T) {
	logPath := argsLog(t)
	writeStubGh(t, `#!/bin/sh
echo "$GH_HOST|$@" >> "$ARGS_LOG"
case " $* " in
  *" repo create "*|" repo view "*) echo '{"nameWithOwner":"acme/demo","url":"https://ghe.example.com/acme/demo","sshUrl":"git@ghe.example.com:acme/demo.git"}'; exit 0 ;;
esac
exit 1
`)

	repo, err := ghBackend{}.CreateRepo(context.Background(), CreateRequest{
		Login: "ghe.example.com", Owner: "acme", Account: "me", Name: "demo", Private: true,
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	args := readFileForTest(t, logPath)
	// gh picks its host from the environment, so an Enterprise host has to be
	// named there or the command would go to github.com.
	if !strings.Contains(args, "ghe.example.com|repo create") {
		t.Errorf("GH_HOST not set for the Enterprise host: %s", args)
	}
	if !strings.Contains(args, "--private") {
		t.Errorf("expected private: %s", args)
	}
	if repo.SSHURL != "git@ghe.example.com:acme/demo.git" {
		t.Errorf("SSHURL = %q", repo.SSHURL)
	}
}

// A failure on the second `gh repo view` call must not lose the repository that
// was already created; the URLs are derived instead.
func TestGhCreateRepoSurvivesViewFailure(t *testing.T) {
	writeStubGh(t, `#!/bin/sh
case " $* " in
  *" repo create "*) echo "https://github.com/octocat/demo"; exit 0 ;;
esac
exit 1
`)
	repo, err := ghBackend{}.CreateRepo(context.Background(), CreateRequest{
		Login: "github.com", Owner: "octocat", Account: "octocat", Name: "demo",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.CloneURL != "https://github.com/octocat/demo.git" {
		t.Errorf("CloneURL = %q, want it derived from the host", repo.CloneURL)
	}
	if repo.SSHURL != "git@github.com:octocat/demo.git" {
		t.Errorf("SSHURL = %q, want it derived from the host", repo.SSHURL)
	}
}

func TestGhCreateRepoAuthFailureIsActionable(t *testing.T) {
	writeStubGh(t, `#!/bin/sh
echo "To get started with GitHub CLI, please run:  gh auth login" >&2
exit 1
`)
	_, err := ghBackend{}.CreateRepo(context.Background(), CreateRequest{
		Login: "github.com", Owner: "octocat", Name: "demo",
	})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("error = %v, want it to point at the login command", err)
	}
}

// gh treats GH_HOST as a definitive override, so an inherited value must never
// decide the host for a command fth targets explicitly. This is a real
// configuration: Enterprise users set GH_HOST routinely.
func TestGhEnvDiscardsInheritedHost(t *testing.T) {
	t.Setenv("GH_HOST", "ghe.example.com")

	// A github.com target must not inherit the Enterprise host, or the create
	// would silently land on the wrong instance.
	got := envValue(ghEnv("github.com"), "GH_HOST")
	if got != "github.com" {
		t.Errorf("GH_HOST = %q, want github.com", got)
	}
	// An explicit Enterprise target still wins.
	if got := envValue(ghEnv("ghe.example.com"), "GH_HOST"); got != "ghe.example.com" {
		t.Errorf("GH_HOST = %q, want ghe.example.com", got)
	}
	// No login means github.com, which is gh's own default.
	if got := envValue(ghEnv(""), "GH_HOST"); got != "github.com" {
		t.Errorf("GH_HOST = %q, want github.com for an omitted host", got)
	}
}

// Appending to an inherited GH_HOST leaves a duplicate key, and which value the
// child process sees then depends on its getenv implementation.
func TestGhEnvNeverDuplicatesHostKey(t *testing.T) {
	t.Setenv("GH_HOST", "ghe.example.com")
	for _, host := range []string{"", "github.com", "ghe.example.com"} {
		env := ghEnv(host)
		n := 0
		for _, kv := range env {
			if key, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "GH_HOST") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("ghEnv(%q) has %d GH_HOST entries, want exactly 1", host, n)
		}
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

// An omitted login means github.com for gh, and an explicit Enterprise host is
// still named. authHint is handed a bare host string: deciding that a string is
// an account rather than a host is the caller's job, so the owner-as-host case
// is covered by the CreateRepo test below rather than here.
func TestGhAuthHintHost(t *testing.T) {
	if got := authHint(ToolGh, ""); got != "Authenticate with: gh auth login" {
		t.Errorf("hint for an omitted host = %q, want the plain login command", got)
	}
	if got := authHint(ToolGh, "github.com"); got != "Authenticate with: gh auth login" {
		t.Errorf("hint for github.com = %q, want no --hostname", got)
	}
	if got := authHint(ToolGh, "ghe.example.com"); !strings.Contains(got, "--hostname ghe.example.com") {
		t.Errorf("hint = %q, want it to name the Enterprise host", got)
	}
}

func TestGhCreateRepoAuthFailureDoesNotNameTheOwner(t *testing.T) {
	writeStubGh(t, `#!/bin/sh
echo "To get started with GitHub CLI, please run:  gh auth login" >&2
exit 1
`)
	// Owner is set but Login is not, which is the case that used to produce
	// "gh auth login --hostname octocat".
	_, err := ghBackend{}.CreateRepo(context.Background(), CreateRequest{
		Owner: "octocat", Name: "demo",
	})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if strings.Contains(err.Error(), "--hostname octocat") {
		t.Errorf("error suggests the owner as a hostname: %v", err)
	}
}

func TestGhCreateHint(t *testing.T) {
	if got := ghCreateHint(""); got != "" {
		t.Errorf("empty output should produce no hint, got %q", got)
	}
	if got := ghCreateHint("HTTP 422: Validation Failed"); got != "" {
		t.Errorf("unrelated output should produce no hint, got %q", got)
	}
	if got := ghCreateHint("HTTP 422: name already exists on this account"); !strings.Contains(got, "already exists") {
		t.Errorf("hint = %q, want it to mention the clash", got)
	}
	if got := ghCreateHint("name is reserved"); !strings.Contains(got, "rejected") {
		t.Errorf("hint = %q, want it to explain the rejection", got)
	}
}

// gh can hold a live credential, so `gh auth status --show-token` output must be
// scrubbed on exactly the same terms as cafe's.
func TestGhTokenNeverLeaks(t *testing.T) {
	writeStubGh(t, `#!/bin/sh
case " $* " in
  *" repo create "*)
    echo 'HTTP 401: Bad credentials (https://github.com/octocat/demo) token=ghp_0123456789abcdefghij' >&2
    exit 1 ;;
esac
echo '{"hosts":{"github.com":[{"state":"success","active":true,"host":"github.com","login":"octocat","token":"ghp_0123456789abcdefghij"}]}}'
`)
	g := ghBackend{}
	if _, err := g.Logins(context.Background()); err != nil {
		t.Fatalf("Logins: %v", err)
	}
	_, err := g.CreateRepo(context.Background(), CreateRequest{Login: "github.com", Name: "demo"})
	if err == nil {
		t.Fatal("expected the create to fail")
	}
	if strings.Contains(err.Error(), "ghp_") {
		t.Errorf("token leaked into error: %v", err)
	}
}
