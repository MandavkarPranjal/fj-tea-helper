// Package prompt implements the interactive questions fth asks when a decision
// cannot be expressed as a flag.
//
// Each question is a small Bubble Tea program: an Elm-architecture loop that
// owns the terminal only while it is being answered, then hands it back. Bubble
// Tea renders inline rather than in the alternate screen, so fth's plain
// progress output scrolls normally instead of being wiped by a TUI.
//
// When stdin is piped or redirected the prompter falls back to the documented
// default and says so on stderr, so fth stays usable from scripts and CI
// without hanging.
package prompt

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// styles carries the prompt's colour scheme. It is built from a renderer bound
// to the prompt's own output stream rather than lipgloss's package-level
// default, because fth prints progress to stdout but asks questions on stderr:
// detecting the profile against stdout would strip colour from prompts in
// exactly the case where the terminal is right there.
type styles struct {
	label   lipgloss.Style // question text
	answer  lipgloss.Style // the settled answer
	hint    lipgloss.Style // key hints and the suggested default
	detail  lipgloss.Style // secondary text beside an option
	marker  lipgloss.Style // the cursor
	picked  lipgloss.Style // the highlighted option
	option  lipgloss.Style // an ordinary option
	input   lipgloss.Style // live text input
	success lipgloss.Style // non-fatal notices
}

func newStyles(w io.Writer) styles {
	return newStylesFrom(lipgloss.NewRenderer(w))
}

// newStylesFrom is the injection point the tests use to pin a colour profile.
func newStylesFrom(r *lipgloss.Renderer) styles {
	return styles{
		label:   r.NewStyle().Bold(true),
		answer:  r.NewStyle().Foreground(lipgloss.Color("2")),
		hint:    r.NewStyle().Faint(true),
		detail:  r.NewStyle().Faint(true),
		marker:  r.NewStyle().Foreground(lipgloss.Color("6")),
		picked:  r.NewStyle().Foreground(lipgloss.Color("6")).Bold(true),
		option:  r.NewStyle(),
		input:   r.NewStyle(),
		success: r.NewStyle().Foreground(lipgloss.Color("2")),
	}
}

// Option is one choice in a selection prompt. Detail is shown alongside the
// label and is normally the concrete value fth would use, so the user can see
// what they are picking before they pick it.
type Option struct {
	Label  string
	Value  string
	Detail string
}

// Prompter asks questions, falling back to defaults when it cannot.
type Prompter struct {
	in          io.Reader
	out         io.Writer
	interactive bool
	s           styles
}

// New returns a Prompter bound to in and out. In practice these are os.Stdin
// and os.Stderr: prompts go to stderr so that fth's progress output on stdout
// stays pipeable.
//
// Both streams have to be terminals. Testing stdin alone is not enough: with
// `fth ... < /dev/null` in a terminal shell, stdin is a regular file but
// stderr is still a tty. Bubble Tea would then start up, fail to read, and
// leave stray escape sequences behind on its way to the default answer.
func New(in io.Reader, out io.Writer) *Prompter {
	return &Prompter{in: in, out: out, interactive: isTerminal(in) && isTerminal(out), s: newStyles(out)}
}

// NonInteractive returns a Prompter that never asks anything and always returns
// the default. This is what -yes selects.
func NonInteractive(out io.Writer) *Prompter {
	return &Prompter{in: strings.NewReader(""), out: out, interactive: false, s: newStyles(out)}
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
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

// writef prints a prompt or a result line. Failures are ignored on purpose: a
// prompt is advisory, and a broken stderr must not abort the run.
func (p *Prompter) writef(format string, args ...any) {
	_, _ = fmt.Fprintf(p.out, format, args...)
}

// runPrompt executes a Bubble Tea program against the terminal and returns the
// final model, typed as M.
//
// The type assertion has to be to M itself, not to *M: the models here use
// value receivers, so tea returns a value. Asserting to *M compiles, always
// fails, and would silently throw away every answer the user gave.
func runPrompt[M tea.Model](p *Prompter, m M) (final M, ok bool) {
	prog := tea.NewProgram(m, tea.WithInput(p.in), tea.WithOutput(p.out))
	res, err := prog.Run()
	if err != nil {
		return final, false
	}
	out, ok := res.(M)
	if !ok {
		return final, false
	}
	return out, true
}

// Line asks for a free-form value, returning def when the user presses Enter
// without changing anything or when there is no terminal to ask on.
func (p *Prompter) Line(label, def string) string {
	if !p.interactive {
		p.note(label, def)
		return def
	}
	m, ok := runPrompt(p, newLineModel(label, def, p.s))
	if !ok {
		p.note(label, def)
		return def
	}
	p.writef("%s %s\n", p.s.hint.Render(label+":"), p.s.answer.Render(m.result))
	return m.result
}

// YesNo asks a yes/no question, returning def when the user presses Enter
// without changing anything or when there is no terminal to ask on.
func (p *Prompter) YesNo(label string, def bool) bool {
	choice := "no"
	if def {
		choice = "yes"
	}
	return p.Select(label, []Option{
		{Label: "yes", Value: "yes"},
		{Label: "no", Value: "no"},
	}, choice) == "yes"
}

// Select asks the user to pick one of options, returning the chosen value. The
// option matching def is highlighted on entry, and Enter accepts it.
func (p *Prompter) Select(label string, options []Option, def string) string {
	if len(options) == 0 {
		return def
	}
	if !p.interactive {
		p.note(label, def)
		return def
	}
	m, ok := runPrompt(p, newSelectModel(label, options, def, p.s))
	if !ok {
		p.note(label, def)
		return def
	}
	p.writef("%s %s\n", p.s.hint.Render(label+":"), p.s.answer.Render(m.resultLabel()))
	return m.result
}

// note explains a default that was applied without asking, so a script or CI
// log never leaves it looking like fth silently guessed.
func (p *Prompter) note(label, value string) {
	p.writef("%s %s %s\n",
		p.s.hint.Render("fth:"),
		p.s.hint.Render(fmt.Sprintf("no terminal, using default for %q:", label)),
		p.s.answer.Render(value))
}

// lineModel is the Bubble Tea model behind Line: a single text input seeded
// with the suggested value so Enter accepts it and typing replaces it.
type lineModel struct {
	label  string
	def    string
	input  textinput.Model
	done   bool
	result string
	s      styles
	// typed records whether the user has entered anything yet, so the first
	// keystroke can replace the seeded default instead of appending to it.
	typed bool
}

func newLineModel(label, def string, s styles) lineModel {
	ti := textinput.New()
	ti.Placeholder = def
	ti.SetValue(def)
	ti.CharLimit = 128
	ti.Width = 40
	ti.Prompt = ""
	ti.PromptStyle = s.input
	ti.Focus()
	return lineModel{label: label, def: def, input: ti, s: s}
}

func (m lineModel) Init() tea.Cmd { return textinput.Blink }

func (m lineModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.Type {
		case tea.KeyEnter:
			v := strings.TrimSpace(m.input.Value())
			if v == "" {
				v = m.def
			}
			m.done, m.result = true, v
			return m, tea.Quit
		case tea.KeyCtrlC, tea.KeyEsc:
			// Treat cancellation as "keep the default" rather than aborting
			// the run, so a stray keystroke cannot leave a half-created repo.
			m.done, m.result = true, m.def
			return m, tea.Quit
		case tea.KeyRunes:
			// The suggestion is shown as editable text so Enter can accept it,
			// but that means the first keystroke would otherwise land after it
			// and produce "giteamyforge". Clear it on first type instead, so
			// typing a name replaces the suggestion.
			if !m.typed {
				m.typed = true
				m.input.SetValue("")
				m.input.CursorStart()
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// View hides itself once answered, so Bubble Tea erases the live input as soon
// as the question is settled and the confirmed answer is printed instead.
func (m lineModel) View() string {
	if m.done {
		return ""
	}
	return fmt.Sprintf("%s %s %s\n",
		m.s.label.Render(m.label),
		m.s.hint.Render("["+m.def+"]:"),
		m.input.View())
}

// selectModel is the Bubble Tea model behind Select and YesNo.
type selectModel struct {
	label   string
	options []Option
	cursor  int
	done    bool
	result  string
	s       styles
}

func newSelectModel(label string, options []Option, def string, s styles) selectModel {
	m := selectModel{label: label, options: options, s: s}
	for i, o := range options {
		if o.Value == def {
			m.cursor = i
			break
		}
	}
	return m
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok || m.done {
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		m.cursor = (m.cursor - 1 + len(m.options)) % len(m.options)
		return m, nil
	case tea.KeyDown, tea.KeyTab:
		m.cursor = (m.cursor + 1) % len(m.options)
		return m, nil
	case tea.KeyEnter, tea.KeySpace:
		m.result = m.options[m.cursor].Value
		m.done = true
		return m, tea.Quit
	case tea.KeyCtrlC, tea.KeyEsc:
		m.result = m.options[m.cursor].Value
		m.done = true
		return m, tea.Quit
	}

	// Typing the first letter of an option jumps straight to it, so "s" and
	// "h" pick ssh and https without touching the arrow keys.
	if key.Type == tea.KeyRunes && len(key.Runes) == 1 {
		for _, o := range m.options {
			if o.Value != "" && strings.EqualFold(string([]rune(o.Value)[0]), string(key.Runes[0])) {
				m.result = o.Value
				m.done = true
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	if m.done {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.s.label.Render(m.label) + "\n")
	width := 0
	for _, o := range m.options {
		if n := len(o.Label); n > width {
			width = n
		}
	}
	for i, o := range m.options {
		picked := i == m.cursor
		marker := "  "
		label := m.s.option
		detail := m.s.detail
		if picked {
			// Highlight the whole chosen row, not just the cursor glyph, so it
			// is obvious at a glance which answer Enter will accept.
			marker = m.s.marker.Render("> ")
			label = m.s.picked
			detail = m.s.option
		}
		fmt.Fprintf(&b, "  %s%s", marker, label.Render(fmt.Sprintf("%-*s", width, o.Label)))
		if o.Detail != "" {
			fmt.Fprintf(&b, "  %s", detail.Render(o.Detail))
		}
		b.WriteString("\n")
	}
	b.WriteString(m.s.hint.Render("  (up/down to move, enter to choose)"))
	return b.String()
}

// resultLabel renders the chosen option's label, which is what the user saw in
// the list, so the confirmation reads back exactly what they selected.
func (m selectModel) resultLabel() string {
	for _, o := range m.options {
		if o.Value == m.result {
			return o.Label
		}
	}
	return m.result
}
