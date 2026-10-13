package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// Row is one backend's state in a Detect display.
type Row struct {
	// Label is the tool name, fixed for the life of the row.
	Label string
	// Detail is what to show once the row has settled: the version, a reason it
	// is unusable, or nothing.
	Detail string
	// Done reports whether this backend has finished probing. Rows that have not
	// animate, so the display always looks like it is working.
	Done bool
	// Failed marks a backend that settled, but not usably. It gets a warning mark
	// rather than a tick, because a mark that means "fine" on a row that is not
	// fine is worse than no mark at all.
	Failed bool
}

// Detect draws a live list of backends, one row each, settling as results arrive.
//
// It exists because the tools do not finish together. On a machine with all five
// CLIs installed, fj and tea are resolved in under fifty milliseconds while cafe
// spends over a second in a TLS handshake. A spinner holds that information back
// until the slowest one lands; this shows each result the moment it is known, so
// the useful answer is on screen long before the run is ready.
//
// Like Spinner it animates only when its output is a terminal, and it redraws
// in place rather than scrolling, so the list does not push the prompt off
// screen.
//
// The zero value is not usable; call NewDetect.
type Detect struct {
	out     io.Writer
	enabled bool
	style   lipgloss.Style
	warn    lipgloss.Style
	dim     lipgloss.Style

	mu      sync.Mutex
	rows    []Row
	frame   int
	running bool
	done    chan struct{}
	wg      sync.WaitGroup
}

// NewDetect returns a Detect that will draw one row per label. The labels are the
// tools being probed, in the order they should appear; the order does not depend
// on which one answers first.
func NewDetect(out io.Writer, labels []string) *Detect {
	r := lipgloss.NewRenderer(out)
	d := &Detect{
		out:     out,
		enabled: isTerminal(out),
		style:   r.NewStyle(),
		warn:    r.NewStyle().Foreground(lipgloss.Color("3")),
		dim:     r.NewStyle().Faint(true),
	}
	d.rows = make([]Row, len(labels))
	for i, l := range labels {
		d.rows[i] = Row{Label: l}
	}
	return d
}

// Settle marks a row finished. Callers pass the index reported by the detection
// stream, so a late backend updates its own row wherever that row sits.
func (d *Detect) Settle(i int, detail string, failed bool) {
	if d == nil || !d.enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if i < 0 || i >= len(d.rows) {
		return
	}
	d.rows[i] = Row{Label: d.rows[i].Label, Detail: detail, Done: true, Failed: failed}
	d.draw()
}

// Start begins animating the unfinished rows.
func (d *Detect) Start() {
	if d == nil || !d.enabled {
		return
	}
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.done = make(chan struct{})
	done := d.done
	d.draw()
	d.mu.Unlock()

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := newTicker()
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				d.mu.Lock()
				d.frame++
				d.draw()
				d.mu.Unlock()
			}
		}
	}()
}

// Stop halts the animation and erases the block, waiting for the animation
// goroutine first so nothing writes after Stop returns.
func (d *Detect) Stop() {
	if d == nil || !d.enabled {
		return
	}
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	d.running = false
	close(d.done)
	n := len(d.rows)
	d.mu.Unlock()

	d.wg.Wait()
	d.erase(n)
}

// erase removes the block, leaving the cursor where the block started so the
// caller can print its real output from the same position.
//
// draw() ends by stepping the cursor back up to the top of the block, so the
// cursor is already on the block's first row. Each row is then cleared with
// eraseLine, stepping down a line at a time, and the cursor is walked back up by
// one row per line stepped down.
//
// ESC[J would be shorter, and wrong: it clears from the cursor to the bottom of
// the terminal, not to the bottom of the block, so anything printed below the
// display would go with it. The row count is what keeps the blast radius at the
// display.
//
// Walking the cursor up one line per row from the top instead would be off by
// the height of the block: the cursor is at the top, not the bottom, and
// stepping up from there would erase whatever the shell had printed above it.
func (d *Detect) erase(n int) {
	if n <= 0 {
		return
	}
	var b strings.Builder
	b.WriteString(eraseLine)
	for i := 1; i < n; i++ {
		b.WriteString("\n")
		b.WriteString(eraseLine)
	}
	if n > 1 {
		// Stepping back up is what puts the cursor back on the first row, and it
		// is only needed when there was somewhere to step down to.
		b.WriteString(fmt.Sprintf("\x1b[%dA", n-1))
	}
	d.write(b.String())
}

// draw rewrites every row in place.
//
// Each row is written with eraseLine first so no leftover text from a longer
// previous version of that row survives, then the cursor is stepped back up by
// the height of the block so the next draw overwrites the same rows. Writing
// upwards is what keeps the terminal from scrolling and pushing the prompt off
// screen.
func (d *Detect) draw() {
	var b strings.Builder
	width := 0
	for _, r := range d.rows {
		if n := utf8Len(r.Label); n > width {
			width = n
		}
	}
	frame := spinnerDotFrames[d.frame%len(spinnerDotFrames)]
	for _, r := range d.rows {
		b.WriteString(eraseLine)
		b.WriteString(render(r, frame, width, d))
		b.WriteString("\n")
	}
	// Leave the cursor on the first row of the block, so erase knows where the
	// block begins without having to count back up from the bottom.
	b.WriteString(fmt.Sprintf("\x1b[%dA", len(d.rows)))
	d.write(b.String())
}

func render(r Row, frame string, width int, d *Detect) string {
	label := r.Label + strings.Repeat(" ", max(0, width-utf8Len(r.Label)))
	switch {
	case r.Done && r.Failed:
		return d.warn.Render(warnMark) + " " + label + " " + d.dim.Render(r.Detail)
	case r.Done:
		return okMark + " " + label + " " + d.dim.Render(r.Detail)
	default:
		return d.style.Render(frame) + " " + d.style.Render(label) + " " + d.dim.Render("checking…")
	}
}

func (d *Detect) write(s string) { _, _ = fmt.Fprint(d.out, s) }
