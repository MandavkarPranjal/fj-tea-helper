package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func renderLicenseStub(spdx string, year int, holder string) (string, error) {
	return "LICENSE for " + spdx + " " + holder, nil
}

func gitignoreStub(lang string) string {
	if lang == "" {
		return ""
	}
	return "ignoring " + lang
}

func TestCreateScaffoldsAndPushes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "proj")

	res, err := Create(Options{
		Dir:           target,
		Name:          "proj",
		Description:   "a test",
		LicenseSPDX:   "MIT",
		LicenseHolder: "Me",
		Language:      "go",
		DefaultBranch: "main",
		WriteReadme:   true,
		RemoteName:    "origin",
	}, renderLicenseStub, gitignoreStub)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"README.md", ".gitignore", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(target, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if !res.Initialised || !res.Committed {
		t.Errorf("expected initialised+committed, got %+v", res)
	}
	if res.Branch != "main" {
		t.Errorf("branch = %q", res.Branch)
	}
}

func TestCreateRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "busy")
	os.MkdirAll(target, 0o755)
	os.WriteFile(filepath.Join(target, "keep.txt"), []byte("x"), 0o644)

	if _, err := Create(Options{Dir: target, Name: "p"}, renderLicenseStub, gitignoreStub); err == nil {
		t.Fatal("expected refusal for non-empty directory")
	}
}

func TestPrecheckCatchesExistingRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Precheck(dir, false); err == nil {
		t.Error("expected Precheck to reject an existing git repository")
	}
}

func TestPrecheckAllowsMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := Precheck(dir, false); err != nil {
		t.Errorf("expected missing dir to be allowed, got %v", err)
	}
}

func TestReadmeFlagIsHonoured(t *testing.T) {
	// -readme=false must suppress README.md even though a name is present.
	dir := t.TempDir()
	target := filepath.Join(dir, "p")
	if _, err := Create(Options{Dir: target, Name: "p", WriteReadme: false},
		renderLicenseStub, gitignoreStub); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err == nil {
		t.Error("README.md should not be written when WriteReadme is false")
	}
}

func TestReadmeWrittenByDefault(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "p")
	if _, err := Create(Options{Dir: target, Name: "p", WriteReadme: true},
		renderLicenseStub, gitignoreStub); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Errorf("README.md missing: %v", err)
	}
}
