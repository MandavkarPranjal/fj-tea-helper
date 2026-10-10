package main

import (
	"flag"
	"io"
	"os"
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
	if got := pickTarget(prompt.NonInteractive(io.Discard), one); got.ID() != only.ID() {
		t.Errorf("single target = %q, want %q", got.ID(), only.ID())
	}
	if got := pickTarget(prompt.New(strings.NewReader(""), os.Stderr), one); got.ID() != only.ID() {
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
	got := pickTarget(prompt.NonInteractive(io.Discard), list)
	if got.ID() != "fj/codeberg.org" {
		t.Errorf("fallback = %q, want fj/codeberg.org", got.ID())
	}
}
