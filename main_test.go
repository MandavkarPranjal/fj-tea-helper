package main

import (
	"flag"
	"reflect"
	"testing"
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
	fs.Parse(got)
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
	fs.Parse(got)
	if fs.Arg(0) != "-weird-name" {
		t.Errorf("positional = %q, want %q", fs.Arg(0), "-weird-name")
	}
}
