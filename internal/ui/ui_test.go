package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// ascii forces lipgloss to drop colour, standing in for a pipe or NO_COLOR.
func ascii(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

func TestFieldAlignsValuesInOneColumn(t *testing.T) {
	ascii(t)

	labels := []string{"backend", "instance", "repository", "local dir",
		"remote", "remote url", "push", "upstream", "x"}
	for _, l := range labels {
		got := plain(Field(l, "value"))
		// Two leading spaces, then the label, then at least one space of
		// padding before the value.
		if !strings.HasPrefix(got, "  "+l+" ") {
			t.Errorf("Field(%q) = %q, want the value after a padded label", l, got)
		}
		idx := strings.Index(got, "value")
		if idx < 0 {
			t.Fatalf("Field(%q) lost its value: %q", l, got)
		}
		if idx != 2+labelWidth {
			t.Errorf("Field(%q) value starts at column %d, want %d", l, idx, 2+labelWidth)
		}
	}
}

func TestFieldPadsOverlongLabel(t *testing.T) {
	ascii(t)
	long := strings.Repeat("x", labelWidth+10)
	got := plain(Field(long, "v"))
	if got != "  "+long+" v" {
		t.Errorf("Field(overlong) = %q, want a single separating space", got)
	}
}

func TestPad(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"fj", 4, "fj  "},
		{"fj", 2, "fj"},
		{"fj", 0, "fj"},
		{"tea", 4, "tea "},
	}
	for _, c := range cases {
		if got := Pad(c.s, c.width); got != c.want {
			t.Errorf("Pad(%q, %d) = %q, want %q", c.s, c.width, got, c.want)
		}
	}
}

// Colour must vanish when the output cannot show it, so that piping fth into a
// file or a pager yields plain text.
func TestNoEscapesWithoutColor(t *testing.T) {
	ascii(t)

	rendered := []string{
		Heading("Plan"), Label("backend"), Value("tea"), Dim("git@host:x/y.git"),
		Success("yes"), Warn("careful"), Error("boom"), Bullet("b"), Dot("d"),
		Done("created x"), Field("remote", "origin"),
		Errorf("bad thing"), Warnf("odd thing"),
	}
	for _, r := range rendered {
		if strings.ContainsRune(r, 0x1b) {
			t.Errorf("%q still contains escape sequences without color", r)
		}
	}
}

// With colour enabled every decoration must actually emit styling, otherwise
// the styles above are dead weight.
func TestEmitsStyleWithColor(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })

	rendered := []string{
		Heading("Plan"), Label("backend"), Success("yes"), Warn("careful"),
		Error("boom"), Dim("detail"), Done("created"), Dot("file"),
	}
	for _, r := range rendered {
		if !strings.ContainsRune(r, 0x1b) {
			t.Errorf("%q produced no styling with a color profile set", r)
		}
	}
}

func TestDoneAndDotPrefixes(t *testing.T) {
	ascii(t)
	if got := Done("created x"); got != "✓ created x" {
		t.Errorf("Done = %q", got)
	}
	if got := Dot("README.md"); got != "· README.md" {
		t.Errorf("Dot = %q", got)
	}
}
