package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
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
		dir, err := os.MkdirTemp("", "fth-")
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
func clean(s string) string { return redactSecrets(invisibleRe.ReplaceAllString(stripANSI(s), "")) }

// secretFieldRe matches a credential inside a JSON-ish payload. The quote
// characters are captured rather than consumed so that redacting a value does
// not turn valid JSON into invalid JSON.
var secretFieldRe = regexp.MustCompile(`(?i)("?\b(?:access[_-]?token|api[_-]?token|token|secret|password|passwd|authorization)\b"?)(\s*[:=]\s*)("?)[A-Za-z0-9._\-+/=]{8,}("?)`)

// secretTokenRe catches credentials fth has no field name for, such as the
// GitCafe "gct_" prefix.
var secretTokenRe = regexp.MustCompile(`\b(?:gct|gh[pousr]|glpat|forgejo)_[A-Za-z0-9._\-]{8,}`)

// redactSecrets blanks out credentials in captured CLI output.
//
// `run` folds a command's output into the error it returns, so anything the
// CLIs print becomes user-visible. `cafe auth status` prints an access token
// even in its JSON form, which means without this a routine "who am I" check
// could leak a live credential into a terminal, a log, or a bug report. The
// values are never needed by fth, so scrubbing them costs nothing.
func redactSecrets(s string) string {
	s = secretFieldRe.ReplaceAllString(s, "${1}${2}${3}[redacted]${4}")
	return secretTokenRe.ReplaceAllString(s, "[redacted]")
}

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
	if tool == ToolCafe {
		return fmt.Sprintf("Authenticate with: cafe auth login --host https://%s/api", host)
	}
	if tool == ToolGh {
		// gh defaults to github.com, so --hostname is only worth showing for
		// an Enterprise host the user could otherwise never reach. An empty
		// host means the request named none, which for gh is github.com — it
		// must not fall back to a repository owner, since an account name is
		// not a hostname and "gh auth login --hostname octocat" is nonsense.
		if h := bareHost(host); h != "" && !strings.EqualFold(h, defaultGhHost) {
			return fmt.Sprintf("Authenticate with: gh auth login --hostname %s", h)
		}
		return "Authenticate with: gh auth login"
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
		// gh phrases its failures differently from the Forgejo CLIs: it points
		// at the login command by name and calls a missing credential a
		// "bad credentials" or an absent token rather than an expired one.
		"gh auth login",
		"bad credentials",
		"no valid oauth token",
		"you are not logged into any github hosts",
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

// hostOfLogin is the bare host a CreateRequest targets, with any scheme or
// path stripped. Used for auth hints.
//
// A missing login falls back to the owner, which is right for cafe: its owner
// flag doubles as the API host. It is wrong for gh, where the owner is an
// account name and never a hostname, so gh passes its host explicitly instead.
func hostOfLogin(req CreateRequest) string {
	h := req.Login
	if h == "" {
		h = req.Owner
	}
	if u, err := url.Parse(h); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(strings.TrimSuffix(h, "/api"), "https://")
}

var _ = errors.Is
