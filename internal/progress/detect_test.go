package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// fakeTerminal reports itself as a terminal so the display animates, and records
// everything written. The escape sequences are the fragile part of this display,
// so they are worth asserting on rather than eyeballing.
type fakeTerminal struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (f *fakeTerminal) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.Write(p)
}

func (f *fakeTerminal) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.buf.String()
}

// A Detect writing anywhere but a terminal must stay silent, exactly like
// Spinner: piped output and CI logs get the plain text they would get with no
// progress reporting at all.
func TestDetectSilentWhenNotATerminal(t *testing.T) {
	var buf bytes.Buffer
	d := NewDetect(&buf, []string{"fj", "tea"})
	d.Start()
	d.Settle(0, "fj 0.6.0", false)
	d.Stop()
	if got := buf.String(); got != "" {
		t.Errorf("wrote %q to a non-terminal, want nothing", got)
	}
}

// The display must never walk the cursor above the top of its own block. That
// would erase whatever the shell had printed above it, which is a real bug rather
// than a cosmetic one.
func TestDetectEraseStaysWithinBlock(t *testing.T) {
	ft := &fakeTerminal{}
	d := newDetectForTest(ft, []string{"fj", "tea", "cafe"})

	d.Settle(0, "fj 0.6.0", false)
	out := ft.String()

	// Every cursor-up in a draw must be exactly the height of the block. If it
	// were ever larger, Stop's erase would have nothing left to land on.
	if strings.Contains(out, "\x1b[4A") {
		t.Errorf("drew a cursor-up past the top of a 3-row block:\n%q", out)
	}
	// The up-sequence a 3-row block legitimately uses.
	if !strings.Contains(out, "\x1b[3A") {
		t.Errorf("expected the block to step back up by its own height:\n%q", out)
	}
}

// Stop must end with the block cleared and the cursor left where the block began,
// so the caller can print from that position.
func TestDetectStopClearsBlock(t *testing.T) {
	ft := &fakeTerminal{}
	d := newDetectForTest(ft, []string{"fj", "tea"})
	d.Start()
	d.Settle(0, "fj 0.6.0", false)
	d.Settle(1, "tea 0.15.1", false)
	d.Stop()

	out := ft.String()
	if !strings.HasSuffix(out, eraseToEnd) {
		t.Errorf("Stop did not finish by clearing from the cursor down:\n%q", tail(out))
	}
	// Stop must wait for the animation goroutine, so nothing may follow the erase.
	if strings.Contains(out[len(out)-len(eraseToEnd):], "\x1b[2K") {
		t.Error("something wrote to the display after Stop")
	}
}

// A backend that failed gets a warning mark, not a tick. A tick on a row that is
// not fine is worse than no mark.
func TestDetectMarksFailures(t *testing.T) {
	ft := &fakeTerminal{}
	d := newDetectForTest(ft, []string{"fj", "cafe"})
	d.Settle(0, "fj 0.6.0", false)
	d.Settle(1, "installed, no working login", true)
	d.Stop()

	out := stripANSI(ft.String())
	if !strings.Contains(out, okMark+" fj") {
		t.Errorf("a working backend should be ticked:\n%s", out)
	}
	if !strings.Contains(out, warnMark+" cafe") {
		t.Errorf("a broken backend should be marked with a warning, not a tick:\n%s", out)
	}
	if strings.Contains(out, okMark+" cafe") {
		t.Error("a broken backend must never be shown as working")
	}
}

// A row must settle in its own place no matter when its result arrives: the
// display is ordered by backend, not by completion.
func TestDetectSettlesRowInPlace(t *testing.T) {
	ft := &fakeTerminal{}
	d := newDetectForTest(ft, []string{"fj", "tea", "cafe"})
	// cafe settles first even though it is last in the list.
	d.Settle(2, "cafe 0.6.0", false)
	d.Settle(0, "fj 0.6.0", false)
	d.Stop()

	out := stripANSI(ft.String())
	lines := strings.Split(out, "\n")
	var fj, cafe int
	for i, l := range lines {
		if strings.Contains(l, "fj ") && strings.Contains(l, "fj 0.6.0") {
			fj = i
		}
		if strings.Contains(l, "cafe") && strings.Contains(l, "cafe 0.6.0") {
			cafe = i
		}
	}
	if fj == 0 && cafe == 0 {
		t.Fatalf("could not find both settled rows:\n%s", out)
	}
	if fj > cafe {
		t.Errorf("fj settled at row %d and cafe at %d; rows must keep the given order", fj, cafe)
	}
}

// Out-of-range and nil Settle calls must be harmless: detection reports indices
// the caller maps, and a defensive display is better than a panic mid-run.
func TestDetectIgnoresBadIndices(t *testing.T) {
	ft := &fakeTerminal{}
	d := newDetectForTest(ft, []string{"fj"})
	d.Settle(5, "x", false)
	d.Settle(-1, "y", false)
	d.Stop()
	var nilD *Detect
	nilD.Start()
	nilD.Settle(0, "z", false)
	nilD.Stop()
}

func tail(s string) string {
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	return s
}

// stripANSI removes escape sequences so tests can assert on the visible text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && (s[j] == '[') {
				j++
				for j < len(s) && !(s[j] >= 0x40 && s[j] <= 0x7e) {
					j++
				}
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// newDetectForTest builds a Detect that believes it owns a terminal.
func newDetectForTest(out *fakeTerminal, labels []string) *Detect {
	d := NewDetect(out, labels)
	d.enabled = true
	d.running = true
	d.done = make(chan struct{})
	return d
}
