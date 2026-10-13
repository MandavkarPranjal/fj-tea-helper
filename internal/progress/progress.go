// Package progress reports on work that has no measurable total.
//
// fth's slowest steps are network calls (probing logins, creating a repository,
// pushing) and git cannot report a meaningful percentage for any of them, so
// there is nothing honest to fill a progress bar with. What there is worth
// showing is "this is still going": an indeterminate spinner that occupies one
// line and is erased when the step finishes.
//
// The frames come from bubbles/spinner, but they are driven by a plain goroutine
// rather than by a second tea.Program. Two Bubble Tea programs sharing one
// terminal fight over cursor position and raw mode, which leaves stray escape
// sequences behind and is especially painful here because the prompts are
// Bubble Tea programs too.
package progress

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

// eraseLine returns the cursor to the start of the line and clears it, so the
// next write lands on a clean row.
const eraseLine = "\r\x1b[2K"

// Spinner shows an animated "working…" line. A Spinner whose output is not a
// terminal is inert: Start and Stop write nothing, so piped logs and CI output
// get exactly the same plain text they would with no progress reporting at all.
//
// The zero value is not usable; call New.
type Spinner struct {
	out     io.Writer
	enabled bool
	style   lipgloss.Style

	mu      sync.Mutex
	done    chan struct{}
	wg      sync.WaitGroup
	label   string
	running bool
}

// New returns a Spinner writing to out, animating only if out is a terminal.
func New(out io.Writer) *Spinner {
	s := &Spinner{out: out, enabled: isTerminal(out)}
	// Bound the style to the same stream, for the same reason the prompts do:
	// detecting against stdout would strip colour from progress written to
	// stderr.
	s.style = lipgloss.NewRenderer(out).NewStyle().Foreground(lipgloss.Color("6"))
	return s
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

// Enabled reports whether this spinner will draw anything. Callers that want to
// avoid pointless work can skip preparing a message when it is false.
func (s *Spinner) Enabled() bool { return s != nil && s.enabled }

// Start begins animating with the given message. Calling Start while already
// running is a no-op, so callers do not have to track state.
func (s *Spinner) Start(message string) {
	if s == nil || !s.enabled {
		return
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.label = message
	s.done = make(chan struct{})
	s.running = true
	done := s.done
	s.mu.Unlock()

	s.draw(0)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(spinner.Dot.FPS)
		defer ticker.Stop()
		frame := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				frame++
				s.draw(frame)
			}
		}
	}()
}

// Stop halts the animation and erases the line, waiting for the animation
// goroutine to finish first. That wait is what guarantees nothing writes to the
// terminal after Stop returns, which is what would otherwise leave a stray frame
// or a half-erased line above the next piece of output.
//
// Stop is safe to call when not running, and safe to call more than once.
func (s *Spinner) Stop() {
	if s == nil || !s.enabled {
		return
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	done := s.done
	s.mu.Unlock()

	close(done)
	s.wg.Wait()
	s.write(eraseLine)
}

func (s *Spinner) draw(frame int) {
	frames := spinner.Dot.Frames
	s.mu.Lock()
	label := s.label
	s.mu.Unlock()
	s.write(eraseLine + s.style.Render(frames[frame%len(frames)]) + " " + label)
}

// write reports progress to the terminal. Write errors are ignored on purpose:
// a broken stderr must not abort a repository creation that is otherwise fine.
func (s *Spinner) write(line string) {
	_, _ = fmt.Fprint(s.out, line)
}

// spinnerDotFrames is the animation Detect reuses. Sharing one set of frames
// keeps the two displays looking like the same program.
var spinnerDotFrames = spinner.Dot.Frames

const (
	okMark   = "✓"
	warnMark = "!"
)

// newTicker returns the animation clock, at the same rate as the Spinner.
func newTicker() *time.Ticker {
	return time.NewTicker(spinner.Dot.FPS)
}

// utf8Len is the display width of a label. Detection labels are ASCII tool
// names, so byte length would do, but a rune count is correct for anything else
// without pulling in a width table.
func utf8Len(s string) int { return len([]rune(s)) }
