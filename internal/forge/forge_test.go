package forge

import (
	"context"
	"encoding/json"
	"testing"
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

func TestSelectHonoursOverride(t *testing.T) {
	fj, _ := Discover(ToolFj)
	tea, _ := Discover(ToolTea)
	cands := []Candidate{
		{Backend: fj, Verified: true, Users: map[string]string{"codeberg.org": "a"}},
		{Backend: tea, Verified: true, Users: map[string]string{"gitea.com": "b"}},
	}
	// No binary on PATH in tests, so only check the name matching logic.
	if _, err := Select(context.Background(), cands, "nope"); err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestResolveLogin(t *testing.T) {
	c := Candidate{
		Logins:  []Login{{Name: "a.com"}, {Name: "b.com", Default: true}},
		Users:   map[string]string{"a.com": "ua", "b.com": "ub"},
		Backend: fjBackend{},
	}
	l, err := ResolveLogin(c, "")
	if err != nil || l.Name != "b.com" {
		t.Errorf("expected default login b.com, got %v (%v)", l.Name, err)
	}
	l, err = ResolveLogin(c, "a.com")
	if err != nil || l.Name != "a.com" {
		t.Errorf("expected a.com, got %v (%v)", l.Name, err)
	}
	if _, err := ResolveLogin(c, "z.com"); err == nil {
		t.Error("expected error for unknown host")
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
