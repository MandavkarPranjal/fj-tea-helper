// Package ui centralises the terminal presentation used by fth.
//
// Colour is applied through lipgloss, which detects the terminal profile once
// at start-up: output is colourised on a capable TTY, and rendered as plain
// text when piped, redirected, or when NO_COLOR is set. That means callers can
// decorate freely without any of them having to ask whether colour is safe.
package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// labelWidth is wide enough for the longest plan label ("remote url"), so
// every value in a block of fields starts in the same column.
const labelWidth = 11

var (
	headingStyle = lipgloss.NewStyle().Bold(true)
	labelStyle   = lipgloss.NewStyle().Faint(true)
	valueStyle   = lipgloss.NewStyle()
	accentStyle  = lipgloss.NewStyle().Faint(true)
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	bulletStyle  = lipgloss.NewStyle().Faint(true)
)

// Heading is a section title such as "Plan" or "Backends".
func Heading(s string) string { return headingStyle.Render(s) }

// Value is ordinary body text.
func Value(s string) string { return valueStyle.Render(s) }

// Dim is secondary detail: URLs, hints, anything the eye should skim.
func Dim(s string) string { return accentStyle.Render(s) }

// Bullet is a secondary list item.
func Bullet(s string) string { return bulletStyle.Render(s) }

// Success marks a completed step.
func Success(s string) string { return successStyle.Render(s) }

// Warn marks a non-fatal problem.
func Warn(s string) string { return warnStyle.Render(s) }

// Error marks a fatal problem.
func Error(s string) string { return errorStyle.Render(s) }

// Label is a field name, e.g. "backend".
func Label(s string) string { return labelStyle.Render(s) }

// Done renders a check mark, used on its own so the colour covers the whole
// line rather than just a coloured glyph.
func Done(s string) string { return successStyle.Render("✓ " + s) }

// Dot renders the middle-dot bullet used for indented detail lines.
func Dot(s string) string { return bulletStyle.Render("· " + s) }

// Field renders an aligned "label   value" pair, padding the label so values
// line up no matter how long the label is.
func Field(label, value string) string {
	pad := labelWidth - len(label)
	if pad < 1 {
		pad = 1
	}
	return "  " + labelStyle.Render(label) + strings.Repeat(" ", pad) + value
}

// Pad right-aligns s in a field of the given width, leaving it untouched if
// it is already at least that wide. It counts bytes, which is adequate for the
// ASCII identifiers and tool names printed by fth.
func Pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// KeyValue renders a label and value without the two-space indent, for lists
// where the label is the subject rather than a field of a plan.
func KeyValue(label, value string) string {
	return labelStyle.Render(label) + " " + value
}

// Errorf formats a message and renders the "error:" prefix in red, leaving the
// underlying message in the reader's default colour so wrapped error text stays
// legible.
func Errorf(format string, a ...any) string {
	return errorStyle.Render("error:") + " " + fmt.Sprintf(format, a...)
}

// Warnf is the equivalent of Errorf for non-fatal problems.
func Warnf(format string, a ...any) string {
	return warnStyle.Render("warning:") + " " + fmt.Sprintf(format, a...)
}
