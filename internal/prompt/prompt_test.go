package prompt

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// plainStyles returns styles with colour forced off, so View assertions can be
// written against the bare text layout.
func plainStyles() styles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	return newStylesFrom(r)
}

// colorStyles returns styles with a real colour profile, to prove the prompt
// views actually emit styling rather than just looking styled.
func colorStyles() styles {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	return newStylesFrom(r)
}

// newTest returns a Prompter reading from input. A strings.Reader is not a
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

func TestSelectFallsBackToDefaultWithoutTerminal(t *testing.T) {
	p, out, _ := newTest("")
	got := p.Select("Remote URL", []Option{
		{Label: "ssh", Value: "ssh"},
		{Label: "https", Value: "https"},
	}, "https")
	if got != "https" {
		t.Errorf("Select = %q, want https", got)
	}
	if !strings.Contains(out.String(), "no terminal") {
		t.Errorf("the applied default should be reported, got %q", out.String())
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
	if p.YesNo("Set as upstream?", true) != true {
		t.Error("YesNo should honour the default")
	}
}

func TestNoteMentionsTheLabel(t *testing.T) {
	p, out, _ := newTest("")
	p.Line("Remote name for the new remote", "gitea")
	if !strings.Contains(out.String(), "Remote name for the new remote") {
		t.Errorf("output should name the question, got %q", out.String())
	}
}

// The models below are pure Update/View functions, so they can be driven with
// synthetic key messages instead of a real terminal.

// press feeds key messages to a model and stops early if the model quits.
func press[M tea.Model](t *testing.T, m M, keys ...tea.KeyMsg) M {
	t.Helper()
	for _, k := range keys {
		next, cmd := m.Update(k)
		m = next.(M)
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				return m
			}
		}
	}
	return m
}

func enter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func down() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }
func up() tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyUp} }
func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestSelectEnterTakesTheDefault(t *testing.T) {
	m := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh"},
		{Label: "https", Value: "https"},
	}, "ssh", plainStyles())
	final := press(t, m, enter())
	if final.result != "ssh" {
		t.Errorf("result = %q, want ssh", final.result)
	}
}

func TestSelectArrowsMoveAndWrap(t *testing.T) {
	opts := []Option{
		{Label: "ssh", Value: "ssh"},
		{Label: "https", Value: "https"},
	}
	m := newSelectModel("Remote URL", opts, "ssh", plainStyles())

	final := press(t, m, down())
	if final.cursor != 1 {
		t.Errorf("down should move to https, cursor = %d", final.cursor)
	}

	final = press(t, final, down())
	if final.cursor != 0 {
		t.Errorf("down should wrap back to ssh, cursor = %d", final.cursor)
	}

	final = press(t, final, up())
	if final.cursor != 1 {
		t.Errorf("up should wrap to https, cursor = %d", final.cursor)
	}
}

func TestSelectLetterJumpsToOption(t *testing.T) {
	m := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh"},
		{Label: "https", Value: "https"},
	}, "ssh", plainStyles())
	final := press(t, m, keyRune('h'))
	if final.result != "https" {
		t.Errorf("pressing h should choose https, got %q", final.result)
	}
}

func TestSelectEscKeepsTheDefault(t *testing.T) {
	m := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh"},
		{Label: "https", Value: "https"},
	}, "ssh", plainStyles())
	final := press(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if final.result != "ssh" {
		t.Errorf("Esc should keep the highlighted option, got %q", final.result)
	}
}

func TestSelectViewShowsDetailAndErasesWhenDone(t *testing.T) {
	m := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh", Detail: "git@codeberg.org:you/repo.git"},
		{Label: "https", Value: "https", Detail: "https://codeberg.org/you/repo.git"},
	}, "ssh", plainStyles())

	view := m.View()
	for _, want := range []string{"ssh", "https", "git@codeberg.org:you/repo.git"} {
		if !strings.Contains(view, want) {
			t.Errorf("view should show %q, got:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "> ssh") {
		t.Errorf("the default option should be highlighted, got:\n%s", view)
	}

	final := press(t, m, enter())
	if final.View() != "" {
		t.Errorf("an answered prompt should erase itself, got %q", final.View())
	}
}

func TestSelectResultLabelReadsBackTheOption(t *testing.T) {
	m := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh"},
		{Label: "https", Value: "https"},
	}, "ssh", plainStyles())
	final := press(t, m, down(), enter())
	if got := final.resultLabel(); got != "https" {
		t.Errorf("resultLabel = %q, want https", got)
	}
}

func TestSelectWithNoOptionsReturnsDefault(t *testing.T) {
	p, _, _ := newTest("")
	if got := p.Select("Remote URL", nil, "ssh"); got != "ssh" {
		t.Errorf("Select with no options = %q, want ssh", got)
	}
}

func TestLineModelAcceptsDefaultOnEnter(t *testing.T) {
	m := newLineModel("Remote name", "codeberg", plainStyles())
	final := press(t, m, enter())
	if final.result != "codeberg" {
		t.Errorf("result = %q, want codeberg", final.result)
	}
}

// backspaces builds the key sequence that clears a prefilled value.
func backspaces(n int) []tea.KeyMsg {
	keys := make([]tea.KeyMsg, 0, n)
	for range n {
		keys = append(keys, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	return keys
}

func TestLineModelKeepsDefaultWhenCleared(t *testing.T) {
	m := newLineModel("Remote name", "codeberg", plainStyles())
	keys := append(backspaces(len("codeberg")), enter())
	final := press(t, m, keys...)
	if final.result != "codeberg" {
		t.Errorf("an emptied input should fall back to the default, got %q", final.result)
	}
}

func TestLineModelKeepsTypedValue(t *testing.T) {
	m := newLineModel("Remote name", "codeberg", plainStyles())
	keys := backspaces(len("codeberg"))
	for _, r := range "myforge" {
		keys = append(keys, keyRune(r))
	}
	final := press(t, m, append(keys, enter())...)
	if final.result != "myforge" {
		t.Errorf("result = %q, want myforge", final.result)
	}
}

func TestLineModelTypingReplacesTheDefault(t *testing.T) {
	// Without this, typing after the seeded suggestion would append to it and
	// produce "codebergmyforge".
	m := newLineModel("Remote name", "codeberg", plainStyles())
	keys := make([]tea.KeyMsg, 0, len("myforge"))
	for _, r := range "myforge" {
		keys = append(keys, keyRune(r))
	}
	final := press(t, m, append(keys, enter())...)
	if final.result != "myforge" {
		t.Errorf("result = %q, want myforge (typing must replace the default)", final.result)
	}
}

func TestLineViewSeedsTheDefault(t *testing.T) {
	m := newLineModel("Remote name", "codeberg", plainStyles())
	view := m.View()
	if !strings.Contains(view, "codeberg") {
		t.Errorf("view should prefill the default, got:\n%s", view)
	}
	if !strings.Contains(view, "Remote name") {
		t.Errorf("view should show the label, got:\n%s", view)
	}
}

// --- colour ---------------------------------------------------------------

// Colour must not reach a terminal that cannot show it, so that a piped run
// leaves no escape sequences behind in the prompt area.
func TestPromptViewsHaveNoEscapesWithoutColor(t *testing.T) {
	sel := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh", Detail: "git@host:o/n.git"},
		{Label: "https", Value: "https", Detail: "https://host/o/n.git"},
	}, "ssh", plainStyles())
	if s := sel.View(); strings.ContainsRune(s, 0x1b) {
		t.Errorf("select view still contains escapes without colour:\n%q", s)
	}
	line := newLineModel("Remote name", "codeberg", plainStyles())
	if s := line.View(); strings.ContainsRune(s, 0x1b) {
		t.Errorf("line view still contains escapes without colour:\n%q", s)
	}
}

// With a colour profile every part of the prompt should be styled, otherwise
// the styles are dead weight.
func TestPromptViewsEmitStyleWithColor(t *testing.T) {
	sel := newSelectModel("Remote URL", []Option{
		{Label: "ssh", Value: "ssh", Detail: "git@host:o/n.git"},
		{Label: "https", Value: "https", Detail: "https://host/o/n.git"},
	}, "ssh", colorStyles())
	if s := sel.View(); !strings.ContainsRune(s, 0x1b) {
		t.Errorf("select view produced no styling with colour enabled:\n%q", s)
	}
	line := newLineModel("Remote name", "codeberg", colorStyles())
	if s := line.View(); !strings.ContainsRune(s, 0x1b) {
		t.Errorf("line view produced no styling with colour enabled:\n%q", s)
	}
}

// Colour must not disturb the layout: stripping the escapes has to leave the
// same text, with the options still padded into one column.
func TestColorDoesNotBreakSelectAlignment(t *testing.T) {
	opts := []Option{
		{Label: "ssh", Value: "ssh", Detail: "git@host:o/n.git"},
		{Label: "https", Value: "https", Detail: "https://host/o/n.git"},
	}
	colored := newSelectModel("Remote URL", opts, "ssh", colorStyles()).View()
	bare := newSelectModel("Remote URL", opts, "ssh", plainStyles()).View()
	if plain(colored) != bare {
		t.Errorf("colour changed the layout:\n got %q\nwant %q", plain(colored), bare)
	}
	if strings.Count(bare, "\n") != 3 {
		t.Errorf("expected label + 2 options + hint, got:\n%s", bare)
	}
}
