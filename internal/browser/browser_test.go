package browser

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeOpener writes an executable that records its arguments, which is the only
// way to see what fth would have handed the desktop.
func fakeOpener(t *testing.T, record string, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake opener is a shell script")
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "opener")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shQuote(record) + "\nexit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
		t.Fatalf("write opener: %v", err)
	}
	return name
}

// shQuote renders s as a single POSIX shell word. The record path is a
// temporary directory chosen by the test framework, but a name with a space in
// it must not silently redirect somewhere else — and a name with a shell
// metacharacter in it must not run anything.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// waitForArgs waits for the opener to record what it was handed. open does not
// block on the child — handing over the URL is the whole job — so the record
// lands a moment after open has already returned.
func waitForArgs(t *testing.T, record string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(record); err == nil && len(raw) > 0 {
			return strings.TrimSpace(string(raw))
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the opener never recorded its arguments")
	return ""
}

func TestOpenPassesTheURLToTheOpener(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	opener := fakeOpener(t, record, 0)

	if err := open("https://codeberg.org/Owner/proj", [][]string{{opener}}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := waitForArgs(t, record); got != "https://codeberg.org/Owner/proj" {
		t.Errorf("opener received %q, want the URL", got)
	}
}

// Extra arguments before the URL are kept: an opener is often named with the
// verb it performs, such as "gio open".
func TestOpenKeepsOpenerArguments(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	opener := fakeOpener(t, record, 0)

	if err := open("https://example.com/a", [][]string{{opener, "open"}}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got, want := waitForArgs(t, record), "open\nhttps://example.com/a"; got != want {
		t.Errorf("opener received %q, want %q", got, want)
	}
}

// A desktop where one opener is missing but another works must still open: the
// candidate list exists for exactly this.
func TestOpenFallsBackToTheNextCandidate(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	opener := fakeOpener(t, record, 0)

	cmds := [][]string{{filepath.Join(dir, "not-installed")}, {opener}}
	if err := open("https://example.com", cmds); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := waitForArgs(t, record); got != "https://example.com" {
		t.Errorf("fallback opener received %q, want the URL", got)
	}
}

// With nothing installed the user is told so, including the URL, which they can
// still open by hand. Losing that is what makes this failure annoying.
func TestOpenWithNoOpenerNamesTheURL(t *testing.T) {
	err := open("https://example.com", [][]string{{filepath.Join(t.TempDir(), "nope")}})
	if err == nil {
		t.Fatal("open succeeded with no opener installed")
	}
	if !strings.Contains(err.Error(), "https://example.com") {
		t.Errorf("error = %v; want it to name the URL", err)
	}
}

// Opening nothing is not a request to open the user's home page.
func TestOpenRefusesAnEmptyURL(t *testing.T) {
	if err := open("  ", [][]string{{"true"}}); err == nil {
		t.Fatal("open accepted an empty URL")
	}
}

// $BROWSER is the user's own statement about which browser they want, so it
// wins over whatever this package would otherwise have picked.
func TestCandidatesPrefersTheBrowserVariable(t *testing.T) {
	t.Setenv("BROWSER", "/opt/firefox/firefox --new-tab")
	got := candidates()
	if len(got) == 0 || got[0][0] != "/opt/firefox/firefox" {
		t.Fatalf("candidates = %v, want $BROWSER first", got)
	}
	if len(got[0]) != 2 || got[0][1] != "--new-tab" {
		t.Errorf("candidates[0] = %v, want the arguments kept", got[0])
	}
}

// The value is a colon-separated list of fallbacks, so the first entry that
// starts is the one that wins.
func TestCandidatesSplitsTheBrowserFallbackList(t *testing.T) {
	t.Setenv("BROWSER", "a-browser:b-browser")
	got := candidates()
	if len(got) != 2 || got[0][0] != "a-browser" || got[1][0] != "b-browser" {
		t.Errorf("candidates = %v, want the two entries in order", got)
	}
}

// A path with a space in it is quoted in the shell that exports $BROWSER, so
// the quotes have to be resolved before the entry is split — otherwise the
// browser is launched under a name that does not exist.
func TestFieldsKeepsAQuotedPathWhole(t *testing.T) {
	for _, in := range []string{
		`"/opt/My Browser/firefox" --new-tab`,
		`'/opt/My Browser/firefox'`,
		`"/opt/My Browser/firefox"`,
	} {
		got := fields(in)
		if len(got) == 0 || got[0] != "/opt/My Browser/firefox" {
			t.Errorf("fields(%q) = %v, want the quoted path intact", in, got)
		}
	}
	if got := fields(`"/opt/My Browser/firefox" --new-tab`); len(got) != 2 || got[1] != "--new-tab" {
		t.Errorf("fields = %v, want the trailing argument kept", got)
	}
}

// A browser under C:\ is one command, not two: the colon is a drive letter,
// not a list separator. Windows has no colon-separated $BROWSER convention, so
// splitting there would only ever corrupt a valid path.
func TestBrowserCandidatesKeepsWindowsPathsWhole(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the colon-list convention is a Unix one")
	}
	t.Setenv("BROWSER", `C:\Browser\browser.exe`)
	got := browserCandidates()
	if len(got) != 1 || got[0][0] != `C:\Browser\browser.exe` {
		t.Errorf("browserCandidates = %v, want the path intact", got)
	}
}

// A colon is legal in a Unix path, so a quoted one must not be mistaken for the
// list separator: $BROWSER='"/opt/My:Browser/firefox"' names a single command,
// and splitting it would leave two fragments nothing could rejoin.
func TestCandidatesKeepsAQuotedColonWhole(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the colon-list convention is a Unix one")
	}
	t.Setenv("BROWSER", `"/opt/My:Browser/firefox" --new-tab:b-browser`)
	got := candidates()
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want two entries", got)
	}
	if got[0][0] != "/opt/My:Browser/firefox" || len(got[0]) != 2 || got[0][1] != "--new-tab" {
		t.Errorf("candidates[0] = %v, want the quoted path and its argument intact", got[0])
	}
	if got[1][0] != "b-browser" {
		t.Errorf("candidates[1] = %v, want the entry after the separator", got[1])
	}
}

func TestSplitFallbacks(t *testing.T) {
	cases := map[string][]string{
		"a:b": {"a", "b"},
		"a":   {"a"},
		// Empty entries are dropped: "a::b" has a blank candidate, not a command
		// named nothing.
		"a::b":    {"a", "b"},
		"":        nil,
		"'a:b'":   {"'a:b'"},
		`"a:b"`:   {`"a:b"`},
		`"a:b:c"`: {`"a:b:c"`},
		// An unterminated quote runs to the end rather than cutting at the next
		// colon: the value is malformed either way, and half a path is worse
		// than a whole one that will not be found.
		`"a:b`: {`"a:b`},
	}
	for in, want := range cases {
		if got := splitFallbacks(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitFallbacks(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFieldsDropsEmptyEntries(t *testing.T) {
	for _, in := range []string{"   ", `""`, "\t\n"} {
		if got := fields(in); len(got) != 0 {
			t.Errorf("fields(%q) = %v, want nothing", in, got)
		}
	}
}

func TestCandidatesIgnoresAnEmptyBrowserVariable(t *testing.T) {
	t.Setenv("BROWSER", "   ")
	got := candidates()
	if len(got) == 0 {
		t.Fatal("candidates is empty")
	}
	for _, argv := range got {
		if argv[0] == "" {
			t.Errorf("candidates = %v, want no empty command", got)
		}
	}
}

// The opener has to be one that exists for this platform, or fth would be
// calling a program that was never shipped here.
func TestCandidatesNamesAPlatformOpener(t *testing.T) {
	t.Setenv("BROWSER", "")
	got := candidates()
	if len(got) == 0 {
		t.Fatal("candidates is empty")
	}
	want := map[string]string{"darwin": "open", "linux": "xdg-open", "windows": "rundll32"}[runtime.GOOS]
	if want != "" && got[0][0] != want {
		t.Errorf("candidates[0] = %v, want %q first on %s", got[0], want, runtime.GOOS)
	}
}
