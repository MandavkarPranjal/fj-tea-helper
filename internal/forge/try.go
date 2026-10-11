package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// tryInstallHint is shown when no Trylle CLI is found. The npm and PyPI
// packages install both `try` and `trylle-cli`, but `try` alone is a common
// name for unrelated programs, so `trylle-cli` is the one fth prefers.
const tryInstallHint = "install it with: npm i -g @trylle/trylle-cli " +
	"(it installs both `try` and `trylle-cli`; fth prefers `trylle-cli`, " +
	"since `try` is also the name of unrelated programs)"

// defaultTryWebHost is the instance fth assumes when the CLI reports no web
// URL of its own.
const defaultTryWebHost = "trylle.com"

// tryBackend wraps the Trylle CLI, the client for trylle.com.
//
// Every platform command speaks JSON, so this backend needs no output scraping.
// Two things make it more than a straight translation of gh:
//
//   - The binary has two names, `trylle-cli` and `try`, and `try` is also an
//     unrelated workspace manager that answers to the same word. fth therefore
//     verifies which program it found before trusting it. See exe().
//   - The two clone URLs do not live on the same host. The web app and the SSH
//     remote are on trylle.com, but HTTPS clone URLs are served from the API
//     host, so neither can be derived from the other.
type tryBackend struct {
	// bin is an optional explicit path to the Trylle CLI, set via WithTryBin.
	bin string
}

func (tryBackend) Tool() Tool { return ToolTry }

func (tryBackend) LoginHint() string { return "try auth login" }

// SeedsInitialCommit reports that Trylle always seeds a repository with a
// single "Initial commit" README. Verified against the live API: creating a
// repository with and without --auto-init both leave refs/heads/main pointing
// at that lone root commit, so fth must not expect an empty remote to push
// into.
func (tryBackend) SeedsInitialCommit() bool { return true }

// tryCandidates are the executable names the CLI is published under, in the
// order fth prefers them. `trylle-cli` is unambiguous, so it comes first;
// `try` is only accepted once it has proved it is the Trylle CLI.
var tryCandidates = []string{"trylle-cli", "try"}

// exe returns the binary to invoke: an explicit path when one was supplied,
// otherwise the first name on PATH that is genuinely the Trylle CLI.
//
// It returns "" when nothing qualifies, which callers must treat as "not
// installed" rather than falling back to a bare `try` — that name is exactly
// what may belong to a different program.
func (t tryBackend) exe(ctx context.Context) string {
	if t.bin != "" {
		return t.bin
	}
	for _, name := range tryCandidates {
		p, err := lookPath(name)
		if err != nil {
			continue
		}
		if isTrylleCLI(ctx, p) {
			return p
		}
	}
	return ""
}

// exec runs the Trylle CLI, refusing to do so if no genuine binary was found.
func (t tryBackend) exec(ctx context.Context, args ...string) (string, error) {
	bin := t.exe(ctx)
	if bin == "" {
		return "", fmt.Errorf("the Trylle CLI was not found on PATH%s", hintSuffix(ToolTry))
	}
	return run(ctx, minEnv(), bin, args...)
}

func (t tryBackend) Available(ctx context.Context) bool {
	if t.bin != "" {
		return true
	}
	for _, name := range tryCandidates {
		p, err := lookPath(name)
		if err != nil {
			continue
		}
		if isTrylleCLI(ctx, p) {
			return true
		}
	}
	return false
}

// isTrylleCLI reports whether a binary found on PATH really is the Trylle CLI.
//
// This check exists because `try` is not a distinctive name. An unrelated
// program that owns the word would otherwise be driven as if it were a forge
// client, and fth would report its failures as though they were the user's
// problem. The identity is read from `--version`, which both programs answer
// cheaply and without touching the network or a terminal — deliberately not
// from `about`, whose Trylle implementation prints a banner and whose
// impostor's opens a full-screen selector.
func isTrylleCLI(ctx context.Context, path string) bool {
	if strings.Contains(strings.ToLower(path), "trylle-cli") {
		return true
	}
	out, err := run(ctx, minEnv(), path, "--version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(out), "trylle")
}

func (t tryBackend) Version(ctx context.Context) string {
	out, err := t.exec(ctx, "--version")
	if err != nil {
		return "unknown"
	}
	// "trylle-cli v1.2.0" or "try v1.2.0".
	return parseVersion(out)
}

// tryAuthJSON is the subset of `try auth status --json` that fth needs.
//
// The token is deliberately absent: `try auth token` can print it, and decoding
// it into a field would put a live credential one careless %+v from leaking.
// Every captured payload goes through clean() and its credential scrubber
// regardless.
type tryAuthJSON struct {
	// APIEndpoint is the API base URL, which also serves HTTPS clone URLs.
	APIEndpoint string `json:"apiEndpoint"`
	// WebURL is the browser-facing base URL, the host of the SSH remote.
	WebURL string `json:"webUrl"`
	// Authenticated reports whether a usable credential is configured.
	Authenticated bool `json:"authenticated"`
	Viewer        struct {
		Login string `json:"login"`
	} `json:"viewer"`
}

// Logins reports the single Trylle instance fth found.
//
// Unlike tea and fj there is nothing to enumerate: the CLI holds one credential
// at a time, pointed at one API endpoint. That endpoint may not be trylle.com —
// a self-hosted deployment can save its own — so the instance name comes from
// the reported web URL rather than being hardcoded.
func (t tryBackend) Logins(ctx context.Context) ([]Login, error) {
	st, err := t.status(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Authenticated {
		return nil, fmt.Errorf("trylle has no working credential; log in with: %s", loginHint(ToolTry))
	}
	user := st.Viewer.Login
	if user == "" {
		return nil, fmt.Errorf("trylle did not report an account; log in with: %s", loginHint(ToolTry))
	}
	host := bareHost(st.WebURL)
	if host == "" {
		host = defaultTryWebHost
	}
	return []Login{{
		Name:    host,
		URL:     "https://" + host,
		User:    user,
		Default: true,
	}}, nil
}

// WhoAmI returns the account the CLI acts as.
func (t tryBackend) WhoAmI(ctx context.Context, login string) (string, error) {
	st, err := t.status(ctx)
	if err != nil {
		return "", err
	}
	if st.Viewer.Login == "" {
		return "", fmt.Errorf("trylle did not report an account; log in with: %s", loginHint(ToolTry))
	}
	return st.Viewer.Login, nil
}

// UnavailableNote explains a Trylle-specific way of being absent: the user has
// the CLI's short name taken by a different program, so "not installed" would
// be both true and useless.
func (t tryBackend) UnavailableNote(ctx context.Context) string {
	if t.bin != "" {
		return ""
	}
	p, err := lookPath(tryCandidates[len(tryCandidates)-1])
	if err != nil || isTrylleCLI(ctx, p) {
		return ""
	}
	return fmt.Sprintf("a different program called `try` is on PATH at %s; "+
		"install the Trylle CLI or point fth at it with -try-bin", p)
}

// tryRepoJSON is the repository object `try repo create --json` returns. The
// same shape comes back from `repo view`, which is what fills the gaps.
type tryRepoJSON struct {
	FullName    string `json:"fullName"`
	URL         string `json:"url"`
	CloneURL    string `json:"cloneUrl"`
	SSHCloneURL string `json:"sshCloneUrl"`
	Remote      struct {
		HTTPURL string `json:"httpUrl"`
		SSHURL  string `json:"sshUrl"`
	} `json:"remote"`
}

// CreateRepo implements Backend.
//
// Visibility is always explicit: with neither --public nor --private the
// command's default decides, and fth has already asked the user which one they
// want. Nothing is asked of the CLI to seed files, since --auto-init creates a
// server-side commit that would collide with the one fth pushes from the local
// scaffold. fth writes README, .gitignore and LICENSE locally instead, the same
// as every non-tea path.
func (t tryBackend) CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error) {
	args := []string{"repo", "create", req.Name, "--json"}
	// --owner takes a user or an organisation here, so it is safe to pass
	// unconditionally; unlike tea it is not resolved against an org list.
	if req.Owner != "" {
		args = append(args, "--owner", req.Owner)
	}
	if req.Private {
		args = append(args, "--private")
	} else {
		args = append(args, "--public")
	}
	if req.Description != "" {
		args = append(args, "--description", req.Description)
	}
	if req.DefaultBranch != "" {
		args = append(args, "--default-branch", req.DefaultBranch)
	}

	out, err := t.exec(ctx, args...)
	if err != nil {
		wrapped := wrapAuthError(ToolTry, req.Login, err)
		if hint := tryCreateHint(out); hint != "" {
			wrapped = fmt.Errorf("%w\n%s", wrapped, hint)
		}
		return nil, wrapped
	}

	repo := &Repo{Owner: req.Owner, Name: req.Name, FullName: req.Owner + "/" + req.Name}
	var raw tryRepoJSON
	if err := json.Unmarshal([]byte(clean(out)), &raw); err == nil {
		applyTryRepo(raw, repo)
	}
	// The CLI does not promise clone URLs on create, so ask where the
	// repository actually landed. Only the server knows.
	if repo.CloneURL == "" || repo.SSHURL == "" {
		t.describe(ctx, req, repo)
	}
	fillTryRepoGaps(req, repo)
	return repo, nil
}

// applyTryRepo copies the fields the CLI reported over the derived ones.
func applyTryRepo(raw tryRepoJSON, repo *Repo) {
	if raw.FullName != "" {
		repo.FullName = raw.FullName
		if owner, name, ok := strings.Cut(raw.FullName, "/"); ok {
			repo.Owner, repo.Name = owner, name
		}
	}
	if raw.URL != "" {
		repo.WebURL = strings.TrimSuffix(raw.URL, "/")
	}
	repo.CloneURL = firstNonEmpty(raw.CloneURL, raw.Remote.HTTPURL)
	repo.SSHURL = firstNonEmpty(raw.SSHCloneURL, raw.Remote.SSHURL)
}

// describe asks the CLI about the repository it just created. Failures are not
// fatal: the caller fills the remaining fields, so a hiccup here cannot undo a
// repository that already exists.
func (t tryBackend) describe(ctx context.Context, req CreateRequest, repo *Repo) {
	full := req.Owner + "/" + req.Name
	out, err := t.exec(ctx, "repo", "view", full, "--json")
	if err != nil {
		return
	}
	var raw tryRepoJSON
	if json.Unmarshal([]byte(clean(out)), &raw) != nil {
		return
	}
	before := *repo
	applyTryRepo(raw, repo)
	// Never let a partial view erase what create already told us.
	if repo.CloneURL == "" {
		repo.CloneURL = before.CloneURL
	}
	if repo.SSHURL == "" {
		repo.SSHURL = before.SSHURL
	}
}

// fillTryRepoGaps completes a Repo from the parts fth already knows.
//
// repoFromParts assumes the clone URLs share the instance's host, which is true
// of every other backend but not of Trylle: HTTPS clone URLs are served from
// the API host rather than the web host. The generic derivation would hand the
// user a remote that does not resolve, so this fills only what is genuinely
// derivable — the SSH remote, which does live on the web host — and derives the
// HTTPS form from the API endpoint the CLI reported.
func fillTryRepoGaps(req CreateRequest, repo *Repo) {
	host := bareHost(req.Login)
	if host == "" {
		host = defaultTryWebHost
	}
	if repo.Owner == "" {
		repo.Owner = req.Owner
	}
	if repo.Name == "" {
		repo.Name = req.Name
	}
	if repo.FullName == "" {
		repo.FullName = repo.Owner + "/" + repo.Name
	}
	if repo.WebURL == "" {
		repo.WebURL = "https://" + host + "/" + repo.Owner + "/" + repo.Name
	}
	if repo.SSHURL == "" {
		repo.SSHURL = "git@" + host + ":" + repo.Owner + "/" + repo.Name + ".git"
	}
	if repo.CloneURL == "" {
		api := apiHostFrom(req.Login)
		if api == "" {
			return
		}
		repo.CloneURL = "https://" + api + "/git/" + repo.Owner + "/" + repo.Name + ".git"
	}
}

// apiHostFrom returns the API host for an instance, derived from its web host.
// trylle.com serves the API from api.trylle.com, and that is the only
// relationship the CLI exposes, so this is a best guess rather than a fact: it
// is used only to fill a gap when the CLI reported no clone URL at all. The URL
// fth actually wires up always comes from the server.
func apiHostFrom(webURL string) string {
	host := bareHost(webURL)
	if host == "" || !strings.Contains(host, ".") {
		return ""
	}
	return "api." + host
}

// tryCreateHint recognises a creation failure the user can act on.
// PreviewURLs implements Previewer. It exists because Trylle's two clone URLs
// come from different hosts: SSH is served by the web host, HTTPS by the API
// host. The generic derivation would pair the web host with both, and the
// resulting HTTPS remote would fail on the first push.
func (t tryBackend) PreviewURLs(login, owner, name string) (sshURL, httpsURL string) {
	host := bareHost(login)
	if host == "" {
		host = defaultTryWebHost
	}
	api := apiHostFrom(host)
	if api == "" {
		return "", ""
	}
	return "git@" + host + ":" + owner + "/" + name + ".git",
		"https://" + api + "/git/" + owner + "/" + name + ".git"
}

func tryCreateHint(out string) string {
	if out == "" {
		return ""
	}
	if strings.Contains(out, "already exists") {
		return "hint: a repository with that name already exists; pick another name"
	}
	if strings.Contains(out, "name is reserved") || strings.Contains(out, "invalid name") {
		return "hint: Trylle rejected that repository name; try a different one"
	}
	return ""
}

func (t tryBackend) status(ctx context.Context) (tryAuthJSON, error) {
	var st tryAuthJSON
	out, err := t.exec(ctx, "auth", "status", "--json")
	if err != nil {
		return st, wrapAuthError(ToolTry, "", err)
	}
	if err := json.Unmarshal([]byte(clean(out)), &st); err != nil {
		return st, fmt.Errorf("could not parse `try auth status --json` output: %w", err)
	}
	return st, nil
}
