package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// argsLog returns the path a stub should append its argv to.
func argsLog(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "args")
	t.Setenv("ARGS_LOG", p)
	return p
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// writeStubCafe installs a fake `cafe` binary on PATH for the duration of the
// test and returns the directory holding it.
func writeStubCafe(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "cafe")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

const stubCafeStatus = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "cafe 0.7.0"; exit 0; fi
cat <<'EOF'
{"schemaVersion":1,"command":"auth status","data":{"host":"git.cafe","apiUrl":"https://git.cafe/api","username":"mandavkarpranjal","token":"gct_3a32XXXXXX291f","expiresAt":"2026-12-27T08:33:00.706Z"}}
EOF
`

func TestCafeAvailableAndVersion(t *testing.T) {
	writeStubCafe(t, stubCafeStatus)
	c := cafeBackend{}
	if !c.Available(context.Background()) {
		t.Fatal("expected the stub cafe to be available")
	}
	if got := c.Version(context.Background()); got != "0.7.0" {
		t.Errorf("Version() = %q, want %q", got, "0.7.0")
	}
}

func TestCafeLoginsAndWhoAmI(t *testing.T) {
	writeStubCafe(t, stubCafeStatus)
	c := cafeBackend{}
	ctx := context.Background()

	logins, err := c.Logins(ctx)
	if err != nil {
		t.Fatalf("Logins: %v", err)
	}
	if len(logins) != 1 {
		t.Fatalf("got %d logins, want 1", len(logins))
	}
	l := logins[0]
	if l.Name != "git.cafe" || l.User != "mandavkarpranjal" || !l.Default {
		t.Errorf("login = %+v, want git.cafe/mandavkarpranjal/default", l)
	}
	if l.URL != "https://git.cafe" {
		t.Errorf("URL = %q, want https://git.cafe", l.URL)
	}

	// cafe reports the user inline, so WhoAmI is only a confirmation.
	u, err := c.WhoAmI(ctx, "git.cafe")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if u != "mandavkarpranjal" {
		t.Errorf("WhoAmI() = %q, want mandavkarpranjal", u)
	}
	if _, err := c.WhoAmI(ctx, "example.com"); err == nil {
		t.Error("expected an error when asked about a different host")
	}
}

// The access token is the one thing cafe prints that must never escape into an
// error message, a log line, or -verbose output.
func TestCafeTokenNeverLeaks(t *testing.T) {
	// The stub echoes the status payload, token and all. auth status succeeds;
	// repo create fails, so both the success and the failure path are covered.
	writeStubCafe(t, `#!/bin/sh
case " $* " in
  *" repo create "*)
    echo '{"schemaVersion":1,"error":{"message":"boom","token":"gct_3a32XXXXXX291f"}}'
    exit 1 ;;
esac
cat <<'EOF'
{"schemaVersion":1,"command":"auth status","data":{"host":"git.cafe","username":"mandavkarpranjal","token":"gct_3a32XXXXXX291f"}}
EOF
`)
	c := cafeBackend{}
	ctx := context.Background()

	// A login listing must succeed even though the token is in the payload.
	if _, err := c.Logins(ctx); err != nil {
		t.Fatalf("Logins: %v", err)
	}
	if _, err := c.WhoAmI(ctx, "git.cafe"); err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	// And a failure path must not echo it either.
	repo, err := c.CreateRepo(ctx, CreateRequest{Name: "x"})
	if err == nil {
		t.Fatalf("expected failure, got repo %+v", repo)
	}
	if strings.Contains(err.Error(), "gct_") {
		t.Errorf("token leaked into error: %v", err)
	}
	// Nor may it be sitting in the decoded struct.
	var st cafeStatus
	if err := json.Unmarshal([]byte(`{"data":{"username":"u","token":"gct_3a32XXXXXX291f"}}`), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", st), "gct_") {
		t.Errorf("token leaked into decoded struct: %+v", st)
	}
}

func TestCafeCreateRepoUsesPersonalAccountAsDefaultOwner(t *testing.T) {
	logPath := argsLog(t)
	writeStubCafe(t, `#!/bin/sh
echo "$@" >> "$ARGS_LOG"
echo '{"data":{"resource":{"owner":"mandavkarpranjal","name":"demo"}}}'
`)

	c := cafeBackend{}
	repo, err := c.CreateRepo(context.Background(), CreateRequest{
		Login: "git.cafe", Owner: "mandavkarpranjal", Account: "mandavkarpranjal",
		Name: "demo", DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if repo.FullName != "mandavkarpranjal/demo" {
		t.Errorf("FullName = %q, want mandavkarpranjal/demo", repo.FullName)
	}
	// cafe reports no URLs, so they must be derived from the login.
	if repo.SSHURL != "git@git.cafe:mandavkarpranjal/demo.git" {
		t.Errorf("SSHURL = %q", repo.SSHURL)
	}
	if repo.CloneURL != "https://git.cafe/mandavkarpranjal/demo.git" {
		t.Errorf("CloneURL = %q", repo.CloneURL)
	}

	args := readFileForTest(t, logPath)
	// --org must be omitted for the user's own account, the same way tea's
	// --owner is.
	if strings.Contains(args, "--org") {
		t.Errorf("--org passed for the personal account: %s", args)
	}
	if !strings.Contains(args, "--visibility public") {
		t.Errorf("visibility not sent explicitly: %s", args)
	}
	if !strings.Contains(args, "--no-update-check") {
		t.Errorf("update check not disabled: %s", args)
	}
}

func TestCafeCreateRepoPassesOrgAndPrivate(t *testing.T) {
	logPath := argsLog(t)
	writeStubCafe(t, `#!/bin/sh
echo "$@" >> "$ARGS_LOG"
echo '{"data":{"resource":{"owner":"acme","name":"demo"}}}'
`)

	c := cafeBackend{}
	_, err := c.CreateRepo(context.Background(), CreateRequest{
		Login: "git.cafe", Owner: "acme", Account: "mandavkarpranjal",
		Name: "demo", Private: true, Description: "hello",
	})
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	args := readFileForTest(t, logPath)
	if !strings.Contains(args, "--org acme") {
		t.Errorf("missing --org acme: %s", args)
	}
	if !strings.Contains(args, "--visibility private") {
		t.Errorf("expected private: %s", args)
	}
	if !strings.Contains(args, "--description hello") {
		t.Errorf("missing description: %s", args)
	}
	if !strings.Contains(args, "--json") {
		t.Errorf("missing --json: %s", args)
	}
}

func TestCafeCreateHint(t *testing.T) {
	if got := cafeCreateHint(""); got != "" {
		t.Errorf("empty output should produce no hint, got %q", got)
	}
	if got := cafeCreateHint("something unrelated"); got != "" {
		t.Errorf("unrelated output should produce no hint, got %q", got)
	}
	got := cafeCreateHint("Error: repository already exists")
	if !strings.Contains(got, "already exists") {
		t.Errorf("hint = %q, want it to mention the clash", got)
	}
}

func TestCafeUsesExplicitBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "my-cafe")
	if err := os.WriteFile(bin, []byte(stubCafeStatus), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Deliberately leave PATH alone: only the explicit path should be used.
	t.Setenv("PATH", t.TempDir())

	b, err := Discover(ToolCafe, WithCafeBin(bin))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !b.Available(context.Background()) {
		t.Fatal("explicit -cafe-bin path should be considered available")
	}
	if got := b.Version(context.Background()); got != "0.7.0" {
		t.Errorf("Version() = %q, want 0.7.0", got)
	}
}

func TestInstallHintCoversEveryBackend(t *testing.T) {
	for _, tc := range []Tool{ToolTea, ToolFj, ToolCafe, ToolGh} {
		if InstallHint(tc) == "" {
			t.Errorf("InstallHint(%q) is empty", tc)
		}
	}
	if InstallHint("nope") != "" {
		t.Error("unknown tools should have no install hint")
	}
}
