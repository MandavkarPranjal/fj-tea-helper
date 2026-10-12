package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stagedBackend is a Backend that reports a known user and finishes after a
// delay, so a test can decide the order backends complete in.
type stagedBackend struct {
	Tool_name Tool
	Delay     time.Duration
	User      string
}

func (b stagedBackend) Tool() Tool                         { return b.Tool_name }
func (b stagedBackend) LoginHint() string                  { return "test auth login" }
func (b stagedBackend) Available(ctx context.Context) bool { return true }
func (b stagedBackend) Version(ctx context.Context) string { return "9.9.9" }
func (b stagedBackend) Logins(ctx context.Context) ([]Login, error) {
	if err := wait(ctx, b.Delay); err != nil {
		return nil, err
	}
	return []Login{{Name: string(b.Tool_name) + ".example", User: b.User}}, nil
}
func (b stagedBackend) WhoAmI(ctx context.Context, l string) (string, error) {
	return b.User, nil
}
func (b stagedBackend) CreateRepo(ctx context.Context, r CreateRequest) (*Repo, error) {
	return &Repo{}, nil
}

// A fast backend must reach the caller while a slow one is still probing. This
// is the whole reason DetectStream exists: the spinner this replaced showed
// nothing until the slowest backend answered, which on a machine with cafe
// installed meant over a second of a blank line for results that were already in.
func TestDetectStreamDeliversFastBeforeSlow(t *testing.T) {
	DisableCache()
	slow := stagedBackend{Tool_name: ToolCafe, Delay: 300 * time.Millisecond, User: "a"}
	fast := stagedBackend{Tool_name: ToolFj, Delay: 5 * time.Millisecond, User: "b"}

	// Cafe is first in the list, so if delivery waited on it, fj would be second.
	results := detectStream(context.Background(), []Backend{slow, fast})

	start := time.Now()
	first := <-results
	if got := first.Candidate.Backend.Tool(); got != ToolFj {
		t.Fatalf("first result was %s after %v, want fj immediately", got, time.Since(start))
	}
	if first.Index != 1 {
		t.Errorf("Index = %d, want 1 — its position in the list, not its arrival order", first.Index)
	}
}

// Ordering by arrival is convenient for drawing; ordering by Index is what the
// caller's final list must use. A backend that answers last still has to land in
// its original position, or Enter would default to a different instance than it
// did yesterday.
func TestDetectStreamIndexPreservesOrder(t *testing.T) {
	DisableCache()
	gh := stagedBackend{Tool_name: ToolGh, Delay: 150 * time.Millisecond, User: "a"}
	tea := stagedBackend{Tool_name: ToolTea, Delay: 5 * time.Millisecond, User: "b"}

	results := detectStream(context.Background(), []Backend{gh, tea})

	out := make([]Candidate, 2)
	var arrival []Tool
	for r := range results {
		out[r.Index] = r.Candidate
		arrival = append(arrival, r.Candidate.Backend.Tool())
	}
	if len(arrival) != 2 || arrival[0] != ToolTea {
		t.Errorf("arrival order = %v, want tea first (it is the faster probe)", arrival)
	}
	if out[0].Backend.Tool() != ToolGh || out[1].Backend.Tool() != ToolTea {
		t.Errorf("reassembled order = %s, %s; want gh, tea",
			out[0].Backend.Tool(), out[1].Backend.Tool())
	}
}

// By the time the channel closes, the memo must already be on disk.
//
// This ordering is load-bearing. A caller stops ranging the instant the channel
// closes and then exits, so a write scheduled after the close loses the race
// against process exit and the memo is silently lost — which presents as "the
// cache does nothing", not as an error. Asserting the file after the range is how
// that shows up here instead of on a user's second run.
func TestDetectStreamPersistsTheMemoBeforeClosing(t *testing.T) {
	dir := isolateCache(t)
	results := detectStream(t.Context(), []Backend{
		stagedBackend{Tool_name: ToolFj, User: "anna"},
		stagedBackend{Tool_name: ToolTea, User: "bob"},
	})
	for range results {
	}

	raw, err := os.ReadFile(filepath.Join(dir, "fth", "detect.json"))
	if err != nil {
		t.Fatalf("the channel closed without the memo on disk: %v", err)
	}
	for _, key := range []string{"fj", "tea"} {
		if !strings.Contains(string(raw), `"`+key) {
			t.Errorf("memo is missing the %s entry: %s", key, raw)
		}
	}
}

// Every backend must arrive, including ones that fail, and the channel must close
// so a caller ranging over it terminates.
func TestDetectStreamDeliversEveryBackendAndCloses(t *testing.T) {
	DisableCache()
	backends := []Backend{
		stagedBackend{Tool_name: ToolFj, Delay: 40 * time.Millisecond, User: "a"},
		stagedBackend{Tool_name: ToolTea, Delay: 5 * time.Millisecond, User: "b"},
		stagedBackend{Tool_name: ToolGh, Delay: 20 * time.Millisecond, User: "c"},
	}
	results := detectStream(context.Background(), backends)
	n := 0
	for range results {
		n++
	}
	if n != len(backends) {
		t.Errorf("got %d results, want %d", n, len(backends))
	}
}

// A caller must not have to wait for the slowest backend to know that one has
// failed. That is the case a user hits when their credentials have expired on the
// very instance they meant to use.
func TestDetectStreamReportsFailureImmediately(t *testing.T) {
	DisableCache()
	broken := &failingBackend{Tool_name: ToolFj, Delay: 20 * time.Millisecond}
	// The healthy backend is the slow one, so the failure really is the first
	// result. If delivery still waited on the slower of the two, the test would
	// see a Verified candidate and fail.
	slow := stagedBackend{Tool_name: ToolCafe, Delay: 400 * time.Millisecond, User: "a"}

	results := detectStream(context.Background(), []Backend{broken, slow})
	first := <-results
	if first.Candidate.Verified {
		t.Error("a backend that failed to log in must not be Verified")
	}
	if len(first.Candidate.Notes) == 0 {
		t.Error("a failure must be explained in Notes, not silently dropped")
	}
}

// failingBackend is installed and answers, but cannot authenticate.
type failingBackend struct {
	Tool_name Tool
	Delay     time.Duration
}

func (b failingBackend) Tool() Tool                         { return b.Tool_name }
func (b failingBackend) LoginHint() string                  { return "test auth login" }
func (b failingBackend) Available(ctx context.Context) bool { return true }
func (b failingBackend) Version(ctx context.Context) string { return "1.0.0" }
func (b failingBackend) Logins(ctx context.Context) ([]Login, error) {
	if err := wait(ctx, b.Delay); err != nil {
		return nil, err
	}
	// No User, so the probe has to ask WhoAmI — which is where this fails.
	return []Login{{Name: "dead.example"}}, nil
}
func (b failingBackend) WhoAmI(ctx context.Context, l string) (string, error) {
	return "", errTestAuth
}
func (b failingBackend) CreateRepo(ctx context.Context, r CreateRequest) (*Repo, error) {
	return &Repo{}, nil
}

var errTestAuth = errorString("credentials rejected")

type errorString string

func (e errorString) Error() string { return string(e) }
