package prompt

import (
	"bytes"
	"strings"
	"testing"
)

// newTest returns a Prompter reading from input. A bytes.Buffer is not a
// terminal, so these exercise the non-interactive fallback path.
func newTest(input string) (*Prompter, *bytes.Buffer, bool) {
	out := &bytes.Buffer{}
	return New(strings.NewReader(input), out), out, false
}

func TestLineFallsBackToDefaultWithoutTerminal(t *testing.T) {
	p, out, interactive := newTest("")
	if interactive {
		t.Fatal("a strings.Reader must not be treated as a terminal")
	}
	if got := p.Line("Remote name", "codeberg"); got != "codeberg" {
		t.Errorf("Line = %q, want codeberg", got)
	}
	if !strings.Contains(out.String(), "no terminal") {
		t.Errorf("the applied default should be reported, got %q", out.String())
	}
}

func TestYesNoFallsBackToDefaultWithoutTerminal(t *testing.T) {
	p, _, _ := newTest("")
	if !p.YesNo("Set as upstream?", true) {
		t.Error("expected the default (true) to be used")
	}
	if p.YesNo("Set as upstream?", false) {
		t.Error("expected the default (false) to be used")
	}
}

func TestNonInteractiveNeverAsks(t *testing.T) {
	out := &bytes.Buffer{}
	p := NonInteractive(out)
	if p.Interactive() {
		t.Error("NonInteractive must report itself as non-interactive")
	}
	if got := p.Line("Remote name", "forge"); got != "forge" {
		t.Errorf("Line = %q", got)
	}
}

func TestNoteMentionsTheLabel(t *testing.T) {
	p, out, _ := newTest("")
	p.Line("Remote name for the new remote", "gitea")
	if !strings.Contains(out.String(), "Remote name for the new remote") {
		t.Errorf("output should name the question, got %q", out.String())
	}
}
