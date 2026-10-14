// Package browser opens a URL in the user's default browser.
//
// Opening a page is one of the few things a CLI cannot do portably through a
// library call, so this is a thin wrapper over whatever the platform offers:
// xdg-open on Linux, `open` on macOS, rundll32 on Windows. It does not wait
// for the browser to exit — the point is to hand the URL over and let the
// user carry on — and a failure is reported rather than swallowed, because the
// user asked for something visible and did not get it.
package browser

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unicode"
)

// Open hands url to the default browser. It returns an error when no opener is
// available or none of the candidates could be started.
func Open(url string) error {
	return open(url, candidates())
}

// open is Open with the opener list injected, which is what lets the tests
// exercise a browser without one.
func open(url string, cmds [][]string) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("no URL to open")
	}
	var err error
	for _, argv := range cmds {
		if len(argv) == 0 || argv[0] == "" {
			continue
		}
		err = start(argv[0], append(append([]string{}, argv[1:]...), url))
		if err == nil {
			return nil
		}
	}
	if err == nil {
		err = fmt.Errorf("no browser opener is available")
	}
	return fmt.Errorf("open %s: %w", url, err)
}

// start launches the opener and lets it go. The child's output goes nowhere,
// so a browser that cannot start reports its own error to nobody and fth's
// message is the only one the user sees.
func start(name string, args []string) error {
	cmd := exec.Command(name, args...)
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		defer func() { _ = devnull.Close() }()
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the child in the background rather than leaving a zombie, but do not
	// wait: xdg-open blocks until the browser exits on some desktops, and fth
	// has nothing left to do once the page is handed over.
	go func() { _ = cmd.Wait() }()
	return nil
}

// candidates returns the openers to try, in order, for this platform.
func candidates() [][]string {
	// $BROWSER comes first because it is the one the user set deliberately.
	if out := browserCandidates(); len(out) > 0 {
		return out
	}
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"open"}}
	case "windows":
		return [][]string{{"rundll32", "url.dll,FileProtocolHandler"}}
	default:
		// gio knows about Flatpak and Snap installs that xdg-open does not, and
		// www-browser / x-www-browser are the Debian and X11 fallbacks. They are
		// tried in that order because a desktop-native opener handles the URL
		// types these do not.
		return [][]string{
			{"xdg-open"},
			{"gio", "open"},
			{"x-www-browser"},
			{"www-browser"},
			{"sensible-browser"},
		}
	}
}

// browserCandidates reads $BROWSER, or returns nil when it is unset or empty.
//
// The colon-separated list is a Unix convention, and the first entry that starts
// wins, which is what the list is for. Windows has no such convention and its
// paths carry colons of their own — "C:\Browser\browser.exe" would be cut in
// half — so there the value is taken whole and split on whitespace alone.
func browserCandidates() [][]string {
	env := strings.TrimSpace(os.Getenv("BROWSER"))
	if env == "" {
		return nil
	}
	entries := []string{env}
	if runtime.GOOS != "windows" {
		entries = strings.Split(env, ":")
	}
	var out [][]string
	for _, entry := range entries {
		if argv := fields(entry); len(argv) > 0 {
			out = append(out, argv)
		}
	}
	return out
}

// fields splits an opener entry into a command and its arguments.
//
// Quoted runs are kept whole and unquoted, so a browser installed under a path
// with a space in it — "/opt/My Browser/firefox" — arrives as one argument
// rather than three, which is what splitting first and trimming quotes after
// would do. Backslash is left alone: it is a path separator on Windows, and
// exec passes arguments literally anyway, so there is nothing to unescape.
func fields(entry string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote rune
		open  bool // a word has started, even if it is still empty
	)
	flush := func() {
		if open {
			if cur.Len() > 0 {
				out = append(out, cur.String())
			}
			cur.Reset()
			open = false
		}
	}
	for _, r := range entry {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, open = r, true
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			open = true
		}
	}
	flush()
	return out
}
