// Package prompt implements the interactive questions fth asks when a decision
// cannot be expressed as a flag.
//
// Prompts are only ever shown on an interactive terminal. When stdin is piped
// or redirected the prompter falls back to the documented default and says so
// on stderr, so fth stays usable from scripts and CI without hanging.
package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompter asks questions, falling back to defaults when it cannot.
type Prompter struct {
	in          *bufio.Reader
	out         io.Writer
	interactive bool
}

// New returns a Prompter bound to in and out. In practice these are os.Stdin
// and os.Stderr: prompts go to stderr so that fth's progress output on stdout
// stays pipeable.
func New(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{
		in:          bufio.NewReader(in),
		out:         out,
		interactive: isTerminal(in),
	}
}

// NonInteractive returns a Prompter that never asks anything and always
// returns the default. This is what -yes selects.
func NonInteractive(out io.Writer) *Prompter {
	return &Prompter{in: bufio.NewReader(strings.NewReader("")), out: out, interactive: false}
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Interactive reports whether questions can actually be asked. Callers use it
// to avoid retry loops that would never terminate without a terminal.
func (p *Prompter) Interactive() bool { return p.interactive }

// writef prints a prompt. Failures are ignored on purpose: a prompt is
// advisory, and a broken stderr must not abort the run.
func (p *Prompter) writef(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, format, args...)
}

// Line asks for a free-form value, returning def when the user just presses
// Enter or when there is no terminal to ask on.
func (p *Prompter) Line(label, def string) string {
	if !p.interactive {
		p.note(label, def)
		return def
	}
	p.writef("%s [%s]: ", label, def)
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		p.writef("\n")
		return def
	}
	if v := strings.TrimSpace(line); v != "" {
		return v
	}
	return def
}

// YesNo asks a yes/no question, returning def when the user just presses
// Enter or when there is no terminal to ask on.
func (p *Prompter) YesNo(label string, def bool) bool {
	if !p.interactive {
		suffix := "no"
		if def {
			suffix = "yes"
		}
		p.note(label, suffix)
		return def
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	p.writef("%s %s ", label, hint)
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		p.writef("\n")
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}

// note explains a default that was applied without asking, so a script or CI
// log never leaves it looking like fth silently guessed.
func (p *Prompter) note(label, value string) {
	p.writef("fth: no terminal, using default for %q: %s\n", label, value)
}
