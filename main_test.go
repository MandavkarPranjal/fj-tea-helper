package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/himmel/fj-tea-helper/internal/forge"
	"github.com/himmel/fj-tea-helper/internal/prompt"
)

func newTestFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("name", "", "")
	fs.String("dir", "", "")
	fs.Bool("force", false, "")
	fs.Bool("private", false, "")
	return fs
}

func TestReorderArgs(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		// Value flags after the positional argument are hoisted along with
		// their values.
		{[]string{"proj", "-dir", "/tmp/x"}, []string{"-dir", "/tmp/x", "proj"}},
		{[]string{"-dir", "/tmp/x", "proj"}, []string{"-dir", "/tmp/x", "proj"}},
		// Boolean flags take no value, so the next token must not be eaten.
		{[]string{"proj", "-force", "-dir", "/tmp/x"}, []string{"-force", "-dir", "/tmp/x", "proj"}},
		// --flag=value is self-contained.
		{[]string{"proj", "-dir=/tmp/x"}, []string{"-dir=/tmp/x", "proj"}},
		// Everything after -- is positional, and the terminator is preserved
		// so that a dash-leading name is not mistaken for a flag.
		{[]string{"-force", "--", "-weird-name"}, []string{"-force", "--", "-weird-name"}},
		{[]string{"proj"}, []string{"proj"}},
	}
	for _, c := range cases {
		got := reorderArgs(newTestFlags(), c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("reorderArgs(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestReorderArgsNonBoolFlagConsumesDashValue(t *testing.T) {
	// A value-taking flag written as "-name -weird" legitimately consumes
	// "-weird" as its value, even though it looks like a flag. To pass a name
	// that starts with a dash you must use "--" or owner/name.
	fs := newTestFlags()
	got := reorderArgs(fs, []string{"-name", "-weird", "-dir", "/tmp/x"})
	if err := fs.Parse(got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fs.Arg(0) != "" {
		t.Errorf("expected no positional, got %q", fs.Arg(0))
	}
	if name := fs.Lookup("name").Value.String(); name != "-weird" {
		t.Errorf("name = %q, want %q", name, "-weird")
	}
}

func TestReorderArgsDoubleDashKeepsDashyName(t *testing.T) {
	fs := newTestFlags()
	got := reorderArgs(fs, []string{"--force", "--", "-weird-name"})
	if err := fs.Parse(got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fs.Arg(0) != "-weird-name" {
		t.Errorf("positional = %q, want %q", fs.Arg(0), "-weird-name")
	}
}

func TestNamesLocalDir(t *testing.T) {
	// A bare `fth` and an explicit `fth .` both mean "here".
	for _, args := range [][]string{nil, {"."}, {"./"}, {"  ./.  "}} {
		if !namesLocalDir(args, "") {
			t.Errorf("namesLocalDir(%q) = false, want true", args)
		}
	}

	// Every other positional stays a repository name.
	for _, args := range [][]string{{"demo"}, {"owner/demo"}, {"./demo"}, {"~/demo"}, {".."}, {"a", "b"}} {
		if namesLocalDir(args, "") {
			t.Errorf("namesLocalDir(%q) = true, want false", args)
		}
	}

	// -dir is the user's statement about where the repository goes, so it wins
	// over anything typed — including a name that matches the directory.
	if namesLocalDir(nil, "/tmp/somewhere") {
		t.Error("an explicit -dir must not be overridden by a bare fth")
	}
	if namesLocalDir([]string{"."}, "/tmp/somewhere") {
		t.Error("an explicit -dir must not be overridden by fth .")
	}
}

// Standing in a directory called demo, `fth demo` means that directory rather
// than a new one nested inside it.
func TestNamesLocalDirMatchesCurrentDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(dir)

	if !namesLocalDir([]string{"demo"}, "") {
		t.Error(`fth demo inside ~/demo should mean this directory`)
	}
	// A different name is still a new repository beside it.
	for _, arg := range []string{"other", "demo2", "Demo", "my-demo"} {
		if namesLocalDir([]string{arg}, "") {
			t.Errorf("namesLocalDir(%q) = true, want false in a directory named demo", arg)
		}
	}
	// owner/name names an owner, so it cannot be shorthand for a location.
	if namesLocalDir([]string{"someone/demo"}, "") {
		t.Error(`fth owner/demo should not be read as "here"`)
	}
}

func TestRepoNameForDir(t *testing.T) {
	// An empty directory means the one fth runs from.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	base := filepath.Base(wd)
	if got, err := repoNameForDir(""); err != nil || got != base {
		t.Errorf("repoNameForDir(\"\") = %q, %v; want %q", got, err, base)
	}
	if got, err := repoNameForDir("."); err != nil || got != base {
		t.Errorf("repoNameForDir(\".\") = %q, %v; want %q", got, err, base)
	}

	// A named directory names the repository, which is what makes
	// `fth -dir ~/project/other` publish "other".
	dir := filepath.Join(t.TempDir(), "demo")
	if got, err := repoNameForDir(dir); err != nil || got != "demo" {
		t.Errorf("repoNameForDir(%q) = %q, %v; want demo", dir, got, err)
	}

	// A directory whose name cannot be a repository name says so, and points
	// at the fix, instead of complaining about a name nobody typed.
	root := string(filepath.Separator)
	if _, err := repoNameForDir(root); err == nil {
		t.Errorf("repoNameForDir(%q) succeeded, want an error", root)
	} else if !strings.Contains(err.Error(), "pass a name") {
		t.Errorf("repoNameForDir(%q) error = %v; want it to suggest passing a name", root, err)
	}
	spaced := filepath.Join(t.TempDir(), "my project")
	if _, err := repoNameForDir(spaced); err == nil {
		t.Errorf("repoNameForDir(%q) succeeded, want an error", spaced)
	}
}

// fakeTarget builds a Target with a real backend, since a zero-valued
// Candidate has no Backend and Target.ID asks it for the tool name.
func fakeTarget(tool forge.Tool, host, account string) forge.Target {
	b, err := forge.Discover(tool)
	if err != nil {
		panic(err)
	}
	return forge.Target{
		Candidate: forge.Candidate{Backend: b, Users: map[string]string{host: account}},
		Login:     forge.Login{Name: host, URL: "https://" + host},
		Account:   account,
	}
}

// A target list with one entry needs no question, and without a terminal there
// is nobody to answer one, so both take the default.
func TestPickTargetTakesTheOnlyOption(t *testing.T) {
	only := fakeTarget(forge.ToolTea, "gitea.com", "me")
	one := []forge.Target{only}
	got, err := pickTarget(prompt.NonInteractive(io.Discard), one)
	if err != nil {
		t.Fatalf("pickTarget: %v", err)
	}
	if got.ID() != only.ID() {
		t.Errorf("single target = %q, want %q", got.ID(), only.ID())
	}
	got, err = pickTarget(prompt.New(strings.NewReader(""), os.Stderr), one)
	if err != nil {
		t.Fatalf("pickTarget: %v", err)
	}
	if got.ID() != only.ID() {
		t.Errorf("single target without a terminal = %q, want %q", got.ID(), only.ID())
	}
}

// With no terminal and several instances to choose from, fth must still pick
// one rather than refusing to run.
func TestPickTargetFallsBackToTheFirst(t *testing.T) {
	list := []forge.Target{
		fakeTarget(forge.ToolFj, "codeberg.org", "a"),
		fakeTarget(forge.ToolTea, "gitea.com", "b"),
	}
	got, err := pickTarget(prompt.NonInteractive(io.Discard), list)
	if err != nil {
		t.Fatalf("pickTarget: %v", err)
	}
	if got.ID() != "fj/codeberg.org" {
		t.Errorf("fallback = %q, want fj/codeberg.org", got.ID())
	}
}
