package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// run executes a command and returns its trimmed combined stdout/stderr.
//
// The command is always run from a neutral directory: both CLIs derive a
// repository context from $PWD, which would otherwise leak an unrelated local
// repo into our lookups. We always pass explicit owner/name/host arguments,
// so no local context is ever needed.
//
// Interactive streams are suppressed so the helper never blocks on a prompt.
func run(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	if dir, err := neutralDir(); err == nil {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.Stdin = nil
	err := cmd.Run()
	out := clean(buf.String())
	out = strings.TrimSpace(out)
	if err != nil {
		if out == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return out, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, firstLine(out))
	}
	return out, nil
}

// neutralDir returns a scratch directory used as the working directory for
// every backend invocation.
func neutralDir() (string, error) {
	once.Do(func() {
		dir, err := os.MkdirTemp("", "fj-tea-helper-")
		if err == nil {
			neutral = dir
		}
	})
	if neutral == "" {
		return "", errors.New("could not create scratch directory")
	}
	return neutral, nil
}

var (
	once    sync.Once
	neutral string
)

// Cleanup removes the scratch directory created for backend invocations.
func Cleanup() {
	if neutral != "" {
		_ = os.RemoveAll(neutral)
		neutral = ""
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// invisibleRe matches zero-width and bidi control characters. Forgejo and
// Gitea wrap usernames in U+2068/U+2069 isolates to prevent spoofing, so they
// must be removed before a name can be compared or displayed.
var invisibleRe = regexp.MustCompile("[\u200B-\u200F\u2060-\u2069\u00AD\uFEFF]")

// stripANSI removes terminal colour/cursor escape sequences.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// clean removes ANSI escapes and invisible control characters.
func clean(s string) string { return invisibleRe.ReplaceAllString(stripANSI(s), "") }

// versionTokenRe matches a bare version number such as "0.15.1" or "v0.6.0".
var versionTokenRe = regexp.MustCompile(`^v?\d+(\.\d+)+`)

// parseVersion scans a line left to right and returns the first token that
// looks like a version number. This tolerates prefixes such as tea's
// "Version:\t0.15.1" and fj's "fj v0.6.0".
func parseVersion(line string) string {
	for _, tok := range strings.Fields(line) {
		tok = strings.Trim(tok, ",")
		if versionTokenRe.MatchString(tok) {
			return strings.TrimPrefix(tok, "v")
		}
	}
	return ""
}

// firstLine returns the first non-empty, non-boilerplate line of s, which is
// where both CLIs put the human readable error.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line
	}
	return s
}

// lookPath resolves a binary on PATH.
func lookPath(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return p, nil
}

// minEnv returns the current environment with any inherited FORCE_COLOR style
// variables removed, so output we parse stays clean.
func minEnv() []string {
	skip := map[string]bool{
		"FORCE_COLOR":    true,
		"CLICOLOR":       true,
		"CLICOLOR_FORCE": true,
		"NO_COLOR":       true,
		"TERM":           true,
	}
	var out []string
	for _, kv := range os.Environ() {
		if kv == "" {
			continue
		}
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if skip[key] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// authHint returns a human friendly next step for authentication failures.
func authHint(tool Tool, host string) string {
	if tool == ToolTea {
		return fmt.Sprintf("Authenticate with: tea login add --name %s --url https://%s", host, host)
	}
	return fmt.Sprintf("Authenticate with: fj auth login --host %s", host)
}

// isAuthError recognises the common "not logged in" failures from both CLIs.
func isAuthError(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{
		"grant does not exist",
		"unauthorized",
		"401",
		"403",
		"could not find host",
		"no such host",
		"token expired",
		"authentication",
		"not logged in",
		"please login",
		"requires authentication",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// execError is returned when a backend command fails.
type execError struct {
	Tool Tool
	Host string
	Err  error
}

func (e *execError) Error() string { return e.Err.Error() }
func (e *execError) Unwrap() error { return e.Err }

// wrapAuthError attaches an actionable hint when the failure looks like an
// authentication problem.
func wrapAuthError(tool Tool, host string, err error) error {
	if err == nil {
		return nil
	}
	if isAuthError(err.Error()) {
		return fmt.Errorf("%w\n%s", err, authHint(tool, host))
	}
	return err
}

var _ = errors.Is
