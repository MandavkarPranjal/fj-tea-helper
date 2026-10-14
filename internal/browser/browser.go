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
	// $BROWSER is the convention on Unix, and it comes first because it is the
	// one the user set deliberately.
	if env := os.Getenv("BROWSER"); strings.TrimSpace(env) != "" {
		// The value is a colon-separated list of fallbacks; the first one that
		// starts wins, which is what the list is for.
		var out [][]string
		for _, entry := range strings.Split(env, ":") {
			if argv := fields(entry); len(argv) > 0 {
				out = append(out, argv)
			}
		}
		if len(out) > 0 {
			return out
		}
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

// fields splits an opener entry into a command and its arguments, dropping
// empty entries. $BROWSER is frequently written with quotes or padding, and
// exec does not want either.
func fields(entry string) []string {
	var out []string
	for _, f := range strings.Fields(entry) {
		if f = strings.Trim(f, `"'`); f != "" {
			out = append(out, f)
		}
	}
	return out
}
