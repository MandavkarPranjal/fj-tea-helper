package progress

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// newTest returns a spinner forced on, writing to buf. Forcing it on avoids
// needing a real terminal to assert on what gets drawn. The style is left
// zero-valued so the output is plain and easy to assert on.
func newTest() (*Spinner, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &Spinner{out: buf, enabled: true}, buf
}

// A non-terminal stream must produce no output at all, so that piped logs and
// CI output stay exactly as plain as they were before progress reporting.
func TestInertWhenNotATerminal(t *testing.T) {
	buf := &bytes.Buffer{}
	s := New(buf)
	if s.Enabled() {
		t.Fatal("a bytes.Buffer must not be treated as a terminal")
	}
	s.Start("working")
	s.Stop()
	if buf.Len() != 0 {
		t.Errorf("wrote %q to a non-terminal, want nothing", buf.String())
	}
}

func TestNilSpinnerIsSafe(t *testing.T) {
	var s *Spinner
	if s.Enabled() {
		t.Error("a nil spinner must not report itself enabled")
	}
	s.Start("x")
	s.Stop() // must not panic
}

func TestDrawsMessageAndErasesOnStop(t *testing.T) {
	s, buf := newTest()
	s.Start("creating acme/widget")
	got := buf.String()

	if !strings.Contains(got, "creating acme/widget") {
		t.Errorf("output missing the message: %q", got)
	}
	if !strings.HasPrefix(got, eraseLine) {
		t.Errorf("output should start by clearing the line: %q", got)
	}

	buf.Reset()
	s.Stop()
	if got := buf.String(); got != eraseLine {
		t.Errorf("Stop wrote %q, want just the erase sequence", got)
	}
}

// The animation goroutine must be finished before Stop returns, otherwise it
// can draw over whatever fth prints next.
func TestStopWaitsForTheAnimationGoroutine(t *testing.T) {
	s, buf := newTest()
	s.Start("pushing")
	time.Sleep(150 * time.Millisecond) // let several frames animate
	s.Stop()

	at := buf.Len()
	time.Sleep(200 * time.Millisecond)
	if buf.Len() != at {
		t.Errorf("still drawing after Stop returned: %d new bytes", buf.Len()-at)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	s, buf := newTest()
	s.Start("x")
	s.Stop()
	buf.Reset()
	s.Stop() // must not panic or draw
	if buf.Len() != 0 {
		t.Errorf("second Stop wrote %q, want nothing", buf.String())
	}
}

func TestStopWithoutStartIsSafe(t *testing.T) {
	s, buf := newTest()
	s.Stop()
	if buf.Len() != 0 {
		t.Errorf("Stop before Start wrote %q, want nothing", buf.String())
	}
}

func TestStartWhileRunningIsIgnored(t *testing.T) {
	s, buf := newTest()
	s.Start("first")
	buf.Reset()
	s.Start("second")
	got := buf.String()
	if strings.Contains(got, "second") {
		t.Errorf("a second Start took effect mid-run: %q", got)
	}
	s.Stop()
}

// New must bind its style to the stream it writes to, so colour survives when
// fth's progress goes to stderr while stdout is piped elsewhere.
func TestStyleIsBoundToTheOutputStream(t *testing.T) {
	s := New(os.Stderr)
	if s.style.GetForeground() == nil {
		t.Error("expected New to install a foreground colour")
	}
}
