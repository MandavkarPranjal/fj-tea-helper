package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStubTry installs a fake CLI at the given name on PATH for the duration of
// the test, ahead of anything already there.
func writeStubTry(t *testing.T, name, script string) string {
	t.Helper()
	bin := writeStubScript(t, name, script)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

// writeStubScript writes an executable into a fresh directory and returns its
// path, leaving PATH alone.
func writeStubScript(t *testing.T, name, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return bin
}

// stubTry is a Trylle CLI reporting one working login on trylle.com.
const stubTry = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "trylle-cli v1.2.0"; exit 0; fi
case " $* " in
  *" auth status "*)
    echo '{"apiEndpoint":"https://api.trylle.com","webUrl":"https://trylle.com","authenticated":true,"viewer":{"login":"octocat"}}'
    exit 0 ;;
  *" whoami "*)
    echo '{"login":"octocat"}'
    exit 0 ;;
  *" repo create "*)
    echo '{"fullName":"octocat/demo","url":"https://trylle.com/octocat/demo","cloneUrl":"https://api.trylle.com/git/octocat/demo.git","sshCloneUrl":"git@trylle.com:octocat/demo.git"}'
    exit 0 ;;
esac
exit 1
`

func TestTryAvailableAndVersion(t *testing.T) {
	writeStubTry(t, "trylle-cli", stubTry)
	tb := tryBackend{}
	if !tb.Available(context.Background()) {
		t.Fatal("expected the stub to be available")
	}
	if got := tb.Version(context.Background()); got != "1.2.0" {
		t.Errorf("Version() = %q, want 1.2.0", got)
	}
}

func TestTryLoginsAndWhoAmI(t *testing.T) {
	writeStubTry(t, "trylle-cli", stubTry)
	tb := tryBackend{}
	ctx := context.Background()

	logins, err := tb.Logins(ctx)
	if err != nil {
		t.Fatalf("Logins: %v", err)
	}
	// Trylle holds one credential pointed at one endpoint, so there is exactly
	// one login and it is the default.
	if len(logins) != 1 {
		t.Fatalf("got %d logins, want 1: %+v", len(logins), logins)
	}
	l := logins[0]
	if l.Name != "trylle.com" || l.User != "octocat" || !l.Default {
		t.Errorf("login = %+v, want trylle.com/octocat/default", l)
	}
	if l.URL != "https://trylle.com" {
		t.Errorf("URL = %q, want https://trylle.com", l.URL)
	}

	u, err := tb.WhoAmI(ctx, "trylle.com")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if u != "octocat" {
		t.Errorf("WhoAmI() = %q, want octocat", u)
	}
}

// A self-hosted instance reports its own web URL, and fth must create on that
// rather than assuming trylle.com.
func TestTryLoginFollowsSelfHostedWebURL(t *testing.T) {
	writeStubTry(t, "trylle-cli", `#!/bin/sh
case " $* " in
  *" auth status "*)
    echo '{"apiEndpoint":"https://api.git.acme.test","webUrl":"https://git.acme.test","authenticated":true,"viewer":{"login":"octocat"}}'
    exit 0 ;;
esac
exit 1
`)
	logins, err := tryBackend{}.Logins(context.Background())
	if err != nil {
		t.Fatalf("Logins: %v", err)
	}
	if logins[0].Name != "git.acme.test" {
		t.Errorf("login = %q, want the reported web host git.acme.test", logins[0].Name)
	}
}

func TestTryLoginsWithoutCredentialIsActionable(t *testing.T) {
	writeStubTry(t, "trylle-cli", `#!/bin/sh
case " $* " in
  *" auth status "*)
    echo '{"apiEndpoint":"https://api.trylle.com","webUrl":"https://trylle.com","authenticated":false}'
    exit 0 ;;
esac
exit 1
`)
	_, err := tryBackend{}.Logins(context.Background())
	if err == nil {
		t.Fatal("expected an error with no credential")
	}
	if !strings.Contains(err.Error(), "trylle-cli auth login") {
		t.Errorf("error = %v, want it to name the login command", err)
	}
}

func TestTryCreateRepoPassesVisibilityAndDescription(t *testing.T) {
	logPath := argsLog(t)
	writeStubTry(t, "trylle-cli", `#!/bin/sh
echo "$@" >> "$ARGS_LOG"
case " $* " in
  *" repo create "*)
    echo '{"fullName":"octocat/demo","url":"https://trylle.com/octocat/demo","cloneUrl":"https://api.trylle.com/git/octocat/demo.git","sshCloneUrl":"git@trylle.com:octocat/demo.git"}'
    exit 0 ;;
esac
exit 1
`)

	repo, err := tryBackend{}.CreateRepo(context.Background(), CreateRequest{
		Login: "trylle.com", Owner: "octocat", Account: "octocat",
		Name: "demo", Description: "hello", DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.FullName != "octocat/demo" {
		t.Errorf("FullName = %q, want octocat/demo", repo.FullName)
	}
	if repo.WebURL != "https://trylle.com/octocat/demo" {
		t.Errorf("WebURL = %q", repo.WebURL)
	}
	// The HTTPS clone URL comes from the API host, not the web host: getting
	// this wrong yields a remote that cannot be pushed to.
	if repo.CloneURL != "https://api.trylle.com/git/octocat/demo.git" {
		t.Errorf("CloneURL = %q", repo.CloneURL)
	}
	if repo.SSHURL != "git@trylle.com:octocat/demo.git" {
		t.Errorf("SSHURL = %q", repo.SSHURL)
	}

	args := readFileForTest(t, logPath)
	// Visibility is always explicit, so the user is never asked to confirm one.
	if !strings.Contains(args, "--public") {
		t.Errorf("visibility not sent: %s", args)
	}
	if !strings.Contains(args, "--owner octocat") {
		t.Errorf("missing owner: %s", args)
	}
	if !strings.Contains(args, "--description hello") {
		t.Errorf("missing description: %s", args)
	}
	if !strings.Contains(args, "--default-branch main") {
		t.Errorf("missing default branch: %s", args)
	}
	// A server-side commit would collide with the one fth pushes locally.
	if strings.Contains(args, "--auto-init") {
		t.Errorf("--auto-init must not be passed: %s", args)
	}
}

func TestTryCreateRepoPrivate(t *testing.T) {
	logPath := argsLog(t)
	writeStubTry(t, "trylle-cli", `#!/bin/sh
echo "$@" >> "$ARGS_LOG"
case " $* " in
  *" repo create "*)
    echo '{"fullName":"acme/demo","cloneUrl":"https://api.trylle.com/git/acme/demo.git","sshCloneUrl":"git@trylle.com:acme/demo.git"}'
    exit 0 ;;
esac
exit 1
`)
	if _, err := (tryBackend{}).CreateRepo(context.Background(), CreateRequest{
		Login: "trylle.com", Owner: "acme", Account: "me", Name: "demo", Private: true,
	}); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if args := readFileForTest(t, logPath); !strings.Contains(args, "--private") {
		t.Errorf("expected private: %s", args)
	}
}

// A failure on the follow-up `repo view` must not lose the repository that was
// already created, so the URLs are filled in from the parts fth knows.
func TestTryCreateRepoSurvivesViewFailure(t *testing.T) {
	writeStubTry(t, "trylle-cli", `#!/bin/sh
case " $* " in
  *" repo create "*) echo '{"fullName":"octocat/demo"}'; exit 0 ;;
esac
exit 1
`)
	repo, err := tryBackend{}.CreateRepo(context.Background(), CreateRequest{
		Login: "trylle.com", Owner: "octocat", Account: "octocat", Name: "demo",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.SSHURL != "git@trylle.com:octocat/demo.git" {
		t.Errorf("SSHURL = %q, want it derived from the web host", repo.SSHURL)
	}
	// Never the web host: that URL does not serve git over HTTPS.
	if repo.CloneURL != "https://api.trylle.com/git/octocat/demo.git" {
		t.Errorf("CloneURL = %q, want the API host", repo.CloneURL)
	}
	if repo.WebURL != "https://trylle.com/octocat/demo" {
		t.Errorf("WebURL = %q", repo.WebURL)
	}
}

func TestTryCreateRepoAuthFailureIsActionable(t *testing.T) {
	writeStubTry(t, "trylle-cli", `#!/bin/sh
echo "error: no CLI key configured; run: try auth login" >&2
exit 1
`)
	_, err := tryBackend{}.CreateRepo(context.Background(), CreateRequest{
		Login: "trylle.com", Owner: "octocat", Name: "demo",
	})
	if err == nil {
		t.Fatal("expected a failure")
	}
	// The CLI's own wording may still say `try`, but fth's hint must name the
	// unambiguous executable: `trylle-cli auth login` does not contain the
	// substring "try auth login".
	if !strings.Contains(err.Error(), "trylle-cli auth login") {
		t.Errorf("error = %v, want it to point at the login command", err)
	}
}

// `try` is not a distinctive name: an unrelated program owns it on plenty of
// machines. Driving that one as if it were a forge client would report its
// failures as the user's problem, so it must be identified and refused.
func TestTryRejectsUnrelatedBinaryNamedTry(t *testing.T) {
	// PATH holds only the impostor: a real trylle-cli elsewhere on the
	// developer's machine must not make this test pass for the wrong reason.
	bin := writeStubScript(t, "try", `#!/bin/sh
echo "try v1.10.1 - ephemeral workspace manager"
`)
	t.Setenv("PATH", filepath.Dir(bin))
	tb := tryBackend{}
	ctx := context.Background()
	if tb.Available(ctx) {
		t.Error("an unrelated `try` must not count as the Trylle CLI")
	}
	// And nothing may be created through it.
	if _, err := tb.CreateRepo(ctx, CreateRequest{Login: "trylle.com", Name: "demo"}); err == nil {
		t.Error("expected a refusal rather than a call into the wrong program")
	}
	// The note has to name the actual obstacle, not just "not installed".
	if note := tb.UnavailableNote(ctx); !strings.Contains(note, "different program") {
		t.Errorf("note = %q, want it to explain the name collision", note)
	}
}

// The same binary found under the unambiguous name must still be accepted.
func TestTryAcceptsUnambiguousBinaryName(t *testing.T) {
	writeStubTry(t, "trylle-cli", stubTry)
	tb := tryBackend{}
	ctx := context.Background()
	if !tb.Available(ctx) {
		t.Fatal("trylle-cli should be accepted without a version probe")
	}
	logins, err := tb.Logins(ctx)
	if err != nil {
		t.Fatalf("Logins: %v", err)
	}
	if logins[0].Name != "trylle.com" {
		t.Errorf("login = %q", logins[0].Name)
	}
}

// trylle-cli is preferred over try even when both are present.
func TestTryPrefersUnambiguousName(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"trylle-cli": stubTry,
		"try":        "#!/bin/sh\necho 'try v1.10.1 - ephemeral workspace manager'\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatalf("write stub: %v", err)
		}
	}
	t.Setenv("PATH", dir)

	tb := tryBackend{}
	if got := tb.exe(context.Background()); got != filepath.Join(dir, "trylle-cli") {
		t.Errorf("exe() = %q, want the trylle-cli on PATH", got)
	}
}

// An explicit -try-bin is trusted as given: the user named that path on
// purpose, so fth does not second-guess it.
func TestTryExplicitBinSkipsDetection(t *testing.T) {
	tb := tryBackend{bin: "/nonexistent/trylle-cli"}
	if !tb.Available(context.Background()) {
		t.Error("an explicit binary should be reported as available")
	}
	if note := tb.UnavailableNote(context.Background()); note != "" {
		t.Errorf("note = %q, want none for an explicit binary", note)
	}
}

func TestTryExecRefusesWhenNoBinaryIsFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := tryBackend{}.exec(context.Background(), "--version")
	if err == nil {
		t.Fatal("expected exec to refuse rather than run an arbitrary `try`")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error = %v, want it to report the CLI as missing", err)
	}
}

// The preview must agree with what creation actually produces, or the user is
// shown a remote that will not work.
func TestTryPreviewURLs(t *testing.T) {
	ssh, https := tryBackend{}.PreviewURLs("trylle.com", "octocat", "demo")
	if ssh != "git@trylle.com:octocat/demo.git" {
		t.Errorf("ssh = %q, want the web host", ssh)
	}
	// Not https://trylle.com/...: HTTPS clone is served from the API host.
	if https != "https://api.trylle.com/git/octocat/demo.git" {
		t.Errorf("https = %q, want the API host", https)
	}
}

func TestTryPreviewURLsForSelfHosted(t *testing.T) {
	ssh, https := tryBackend{}.PreviewURLs("git.acme.test", "octocat", "demo")
	if ssh != "git@git.acme.test:octocat/demo.git" {
		t.Errorf("ssh = %q", ssh)
	}
	if https != "https://api.git.acme.test/git/octocat/demo.git" {
		t.Errorf("https = %q", https)
	}
}

// Backends without a special case keep the host-based derivation.
func TestPreviewForFallsBackToHostDerivation(t *testing.T) {
	ssh, https := PreviewFor(ghBackend{}, "github.com", "octocat", "demo")
	if ssh != "git@github.com:octocat/demo.git" {
		t.Errorf("ssh = %q", ssh)
	}
	if https != "https://github.com/octocat/demo.git" {
		t.Errorf("https = %q", https)
	}
}

func TestTryCreateHint(t *testing.T) {
	if got := tryCreateHint(""); got != "" {
		t.Errorf("empty output should produce no hint, got %q", got)
	}
	if got := tryCreateHint("HTTP 422: Validation Failed"); got != "" {
		t.Errorf("unrelated output should produce no hint, got %q", got)
	}
	if got := tryCreateHint("repository already exists"); !strings.Contains(got, "already exists") {
		t.Errorf("hint = %q, want it to mention the clash", got)
	}
	if got := tryCreateHint("name is reserved"); !strings.Contains(got, "rejected") {
		t.Errorf("hint = %q, want it to explain the rejection", got)
	}
}

// The CLI holds a live credential, so `try auth status` output must be scrubbed
// on the same terms as cafe's and gh's.
//
// The assertion is on the RAW captured string, not on a decoded struct.
// Decoding into tryAuthJSON cannot catch a leak, because that struct has no
// field for apiKey: json.Unmarshal drops the unknown key on the floor, so the
// credential would be gone whether or not the scrubber had run, and the test
// would pass either way. Reading the cleaned output directly is what actually
// proves clean() removed it.
func TestTryTokenNeverLeaks(t *testing.T) {
	writeStubTry(t, "trylle-cli", `#!/bin/sh
case " $* " in
  *" auth status "*)
    echo '{"apiEndpoint":"https://api.trylle.com","authenticated":true,"apiKey":"trylle_sk_0123456789abcdef","viewer":{"login":"octocat"}}'
    exit 0 ;;
esac
exit 1
`)
	tb := tryBackend{}
	ctx := context.Background()

	// The ordinary path must still work with the key present.
	if _, err := tb.Logins(ctx); err != nil {
		t.Fatalf("Logins: %v", err)
	}

	out, err := tb.exec(ctx, "auth", "status")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if strings.Contains(out, "trylle_sk_0123456789abcdef") {
		t.Errorf("credential survived scrubbing: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Errorf("output = %s, want the credential replaced rather than merely dropped", out)
	}
	// And the rest of the payload must be intact: a scrubber that ate the
	// whole response would pass the check above while leaving fth blind.
	if !strings.Contains(out, "octocat") {
		t.Errorf("output = %s, want the captured payload", out)
	}
}

func TestParseToolAcceptsTrylleSpellings(t *testing.T) {
	for _, in := range []string{"try", "trylle", "trylle-cli", "TRY", " trylle "} {
		got, err := ParseTool(in)
		if err != nil {
			t.Fatalf("ParseTool(%q): %v", in, err)
		}
		if got != ToolTry {
			t.Errorf("ParseTool(%q) = %q, want %q", in, got, ToolTry)
		}
	}
	for _, in := range []string{"", "auto"} {
		got, err := ParseTool(in)
		if err != nil {
			t.Fatalf("ParseTool(%q): %v", in, err)
		}
		if string(got) != in {
			t.Errorf("ParseTool(%q) = %q, want it unchanged", in, got)
		}
	}
	for _, in := range []string{"tea", "fj", "cafe", "gh"} {
		if got, err := ParseTool(in); err != nil || string(got) != in {
			t.Errorf("ParseTool(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseTool("gitea"); err == nil {
		t.Error("expected an error for an unknown tool")
	}
}

// Trylle is probed after the backends a Forgejo or Gitea user most likely came
// for, so it never takes the default target away from them.
func TestAllToolsPutsTrylleLast(t *testing.T) {
	all := AllTools()
	if all[len(all)-1] != ToolTry {
		t.Errorf("AllTools() = %v, want try probed last", all)
	}
}

// `trylle-cli`, not `try`: the login hint must name the executable fth would
// actually trust, since a bare `try` is often a different program.
func TestTryAuthHintNamesTheLoginCommand(t *testing.T) {
	if got := authHint(ToolTry, "trylle.com"); !strings.Contains(got, "trylle-cli auth login") {
		t.Errorf("hint = %q", got)
	}
}
