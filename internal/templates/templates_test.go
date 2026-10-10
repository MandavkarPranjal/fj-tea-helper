package templates

import (
	"strings"
	"testing"
)

func TestRenderFillsPlaceholders(t *testing.T) {
	for _, id := range LicenseIDs() {
		l := Licenses[id]
		raw, err := licenseFS.ReadFile("licenses/" + l.File)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		got, err := l.Render(2026, "Acme Inc")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, ph := range []string{
			"<year>", "<copyright holders>", "<owner>",
			"[yyyy]", "[name of copyright owner]", "<name of author>",
		} {
			if strings.Contains(got, ph) {
				t.Errorf("%s: placeholder %q survived rendering", id, ph)
			}
		}
		// Only require the year where the template actually has one
		// (e.g. the Unlicense text has no copyright line).
		if (strings.Contains(string(raw), "<year>") || strings.Contains(string(raw), "[yyyy]")) &&
			!strings.Contains(got, "2026") {
			t.Errorf("%s: year not substituted", id)
		}
	}
}

func TestMITUsesHolder(t *testing.T) {
	l, ok := LicenseByID("MIT")
	if !ok {
		t.Fatal("MIT not found")
	}
	got, err := l.Render(2026, "Acme Inc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Copyright (c) 2026 Acme Inc") {
		t.Errorf("unexpected MIT header:\n%s", firstLines(got, 4))
	}
}

func TestLicenseByIDAliases(t *testing.T) {
	cases := map[string]string{
		"mit":          "MIT",
		"apache2":      "Apache-2.0",
		"gpl3":         "GPL-3.0-only",
		"bsd3":         "BSD-3-Clause",
		"":             "MIT",
		"Apache-2.0":   "Apache-2.0",
		"publicdomain": "Unlicense",
	}
	for in, want := range cases {
		got, ok := LicenseByID(in)
		if !ok {
			t.Errorf("%q not recognised", in)
			continue
		}
		if got.ID != want {
			t.Errorf("%q resolved to %s, want %s", in, got.ID, want)
		}
	}
	if _, ok := LicenseByID("nope"); ok {
		t.Error("expected unknown license to be rejected")
	}
}

func TestGitignore(t *testing.T) {
	if !strings.Contains(Gitignore("go"), "/bin/") {
		t.Error("go gitignore missing")
	}
	if !strings.Contains(Gitignore("node"), "node_modules/") {
		t.Error("node gitignore missing")
	}
	if Gitignore("cobol") != "" {
		t.Error("unknown language should yield no gitignore")
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
