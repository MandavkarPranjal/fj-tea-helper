// Package forge provides a thin, uniform wrapper over the `tea` (Gitea),
// `fj` (Forgejo), `cafe` (GitCafe), `gh` (GitHub) and `try` (Trylle) command
// line tools so a caller can create a repository without caring which CLI is
// installed or how its flags differ.
package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Tool identifies a backend CLI.
type Tool string

const (
	// ToolTea is the Gitea `tea` CLI.
	ToolTea Tool = "tea"
	// ToolFj is the Forgejo `fj` CLI.
	ToolFj Tool = "fj"
	// ToolCafe is the GitCafe `cafe` CLI.
	ToolCafe Tool = "cafe"
	// ToolGh is GitHub's own `gh` CLI.
	ToolGh Tool = "gh"
	// ToolTry is the Trylle CLI, installed as either `trylle-cli` or `try`.
	ToolTry Tool = "try"
)

// AllTools lists every supported backend, in the order `-tool auto` probes
// them. gh and try come last so that their very wide reach — gh is by far the
// most commonly installed of these, and `try` is a name other programs also
// answer to — never quietly changes which instance Enter picks for someone who
// came here to work on their Forgejo or Gitea host.
func AllTools() []Tool { return []Tool{ToolFj, ToolTea, ToolCafe, ToolGh, ToolTry} }

// toolAliases maps the alternative spellings a user is likely to type for
// -tool onto the canonical tool identifier.
//
// Trylle needs this more than the others do: the CLI installs under two
// different executable names (`trylle-cli` everywhere, `try` from npm and uv),
// so both are what people reach for.
var toolAliases = map[string]Tool{
	"trylle":     ToolTry,
	"trylle-cli": ToolTry,
}

// ParseTool resolves the value of -tool to a backend, accepting the canonical
// names as well as the aliases people type for them. An empty string or "auto"
// means "probe everything" and comes back unchanged, since that is not a tool.
func ParseTool(s string) (Tool, error) {
	key := strings.ToLower(strings.TrimSpace(s))
	if key == "" || key == "auto" {
		return Tool(key), nil
	}
	if alias, ok := toolAliases[key]; ok {
		return alias, nil
	}
	t := Tool(key)
	for _, known := range AllTools() {
		if t == known {
			return known, nil
		}
	}
	return "", fmt.Errorf("unknown tool %q (want %s)", s, AllTools())
}

// InstallHint returns the one-line "here is how to get this tool" message for
// a backend. It is the fix, not just the diagnosis, so every "not installed"
// path should surface it.
func InstallHint(tool Tool) string {
	switch tool {
	case ToolTea:
		return "install it with: go install code.gitea.io/tea/cmd/tea@latest"
	case ToolFj:
		return "install it from https://codeberg.org/forgejo/forgejo-cli/releases"
	case ToolCafe:
		return cafeInstallHint
	case ToolGh:
		return ghInstallHint
	case ToolTry:
		return tryInstallHint
	}
	return ""
}

// Noter is implemented by a backend that can say something more specific than
// "not installed" about why it cannot run. fth appends the note to the backend
// listing so the user sees the actual obstacle and the way around it.
type Noter interface {
	// UnavailableNote is called only when Available reports false. An empty
	// string means the backend has nothing to add.
	UnavailableNote(ctx context.Context) string
}

// ErrNotFound is returned when a requested tool is not installed on PATH.
var ErrNotFound = errors.New("cli not found on PATH")

// Backend is implemented by each supported CLI.
type Backend interface {
	// Tool returns the identifier of the CLI.
	Tool() Tool
	// Available reports whether the CLI binary can be executed.
	Available(ctx context.Context) bool
	// Version returns a short version string (best effort).
	Version(ctx context.Context) string
	// Logins lists the instances the user is authenticated against.
	Logins(ctx context.Context) ([]Login, error)
	// CreateRepo creates a repository and returns where it lives.
	CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error)
	// WhoAmI returns the authenticated user for a given host (best effort).
	WhoAmI(ctx context.Context, login string) (string, error)
	// LoginHint returns the command that authenticates this tool, used when no
	// working login could be found.
	LoginHint() string
}

// Login describes one authenticated instance.
type Login struct {
	// Name is the CLI's identifier for the login (host, e.g. "gitea.com").
	Name string
	// URL is the base HTTPS URL of the instance.
	URL string
	// User is the account name, when the CLI reports one.
	User string
	// Default marks the login the CLI uses when none is given.
	Default bool
}

// CreateRequest is the tool-agnostic description of a repository to create.
type CreateRequest struct {
	// Login is the instance to create on.
	Login string
	// Owner is the account or organisation that will own the repo.
	Owner string
	// Account is the authenticated user on Login. It is used to tell an
	// organisation apart from the user's own account, which some backends
	// require: `tea repos create --owner` only accepts organisations.
	Account string
	// Name is the repository name.
	Name string
	// Description is an optional short summary.
	Description string
	// Private creates a private repository.
	Private bool
	// Gitignore optionally seeds a .gitignore ("Go", "Node", ...).
	Gitignore string
	// License optionally seeds a LICENSE (SPDX id, e.g. "MIT").
	License string
	// Readme, when non-empty, seeds a README (usually "Default").
	Readme string
	// DefaultBranch optionally sets the initial branch name.
	DefaultBranch string
	// TrustModel is Gitea-specific (committer, collaborator, ...).
	TrustModel string
	// Template marks the repository as a template (tea only).
	Template bool
	// Init asks the backend to seed the repository with template files.
	Init bool
}

// Repo is where a created repository lives.
type Repo struct {
	// FullName is "owner/name".
	FullName string
	// CloneURL is the HTTPS clone URL.
	CloneURL string
	// SSHURL is the SSH clone URL.
	SSHURL string
	// WebURL is the browser URL.
	WebURL string
	// Owner and Name split FullName.
	Owner string
	Name  string
}

// Previewer is implemented by a backend whose clone URLs cannot be derived from
// the instance host alone. Trylle is the one: its SSH remote lives on the web
// host while its HTTPS clone URL is served from the API host, so the generic
// derivation would show — and offer — a URL that does not resolve.
type Previewer interface {
	// PreviewURLs returns the SSH and HTTPS clone URLs a repository would have,
	// for showing to the user before it exists.
	PreviewURLs(login, owner, name string) (sshURL, httpsURL string)
}

// PreviewFor returns the preview URLs for a backend, falling back to the
// host-based derivation for those that do not need a special case.
func PreviewFor(b Backend, login, owner, name string) (sshURL, httpsURL string) {
	if p, ok := b.(Previewer); ok {
		return p.PreviewURLs(login, owner, name)
	}
	return PreviewURLs(login, owner, name)
}

// Seeder is implemented by a backend whose create call always leaves a commit
// on the new repository's default branch, whether or not one was asked for.
//
// Trylle is the one: `repo create` seeds an "Initial commit" README no matter
// what fth passes, so the first push is never a fast-forward. Knowing this up
// front lets fth deal with it deliberately instead of reporting a rejection the
// user cannot act on.
type Seeder interface {
	// SeedsInitialCommit reports whether a freshly created repository is
	// guaranteed to already contain a commit.
	SeedsInitialCommit() bool
}

// SeedsFor reports whether the backend's create call guarantees a seeded
// commit on the new repository.
func SeedsFor(b Backend) bool {
	s, ok := b.(Seeder)
	return ok && s.SeedsInitialCommit()
}

// Option customises a backend before it is handed out by Discover. It receives
// the backend and returns the configured replacement, since backends are value
// types held in an interface and cannot be mutated in place.
type Option func(Backend) Backend

// WithCafeBin points the cafe backend at an explicit binary. `cafe` ships as
// an npm package driven by Bun, so unlike tea and fj it is not something a user
// is likely to have on PATH already. This lets fth use a known-good location
// instead of refusing to run.
func WithCafeBin(path string) Option {
	return func(b Backend) Backend {
		c, ok := b.(cafeBackend)
		if !ok || path == "" {
			return b
		}
		c.bin = path
		return c
	}
}

// WithTryBin points the Trylle backend at an explicit binary. `trylle-cli` is
// usually on PATH, but the shorter `try` name is taken by other programs, so a
// user whose Trylle install is shadowed needs a way to say which binary they
// mean.
func WithTryBin(path string) Option {
	return func(b Backend) Backend {
		t, ok := b.(tryBackend)
		if !ok || path == "" {
			return b
		}
		t.bin = path
		return t
	}
}

// Discover returns a backend for the given tool if its binary is present.
func Discover(tool Tool, opts ...Option) (Backend, error) {
	var b Backend
	switch tool {
	case ToolTea:
		b = teaBackend{}
	case ToolFj:
		b = fjBackend{}
	case ToolCafe:
		b = cafeBackend{}
	case ToolGh:
		b = ghBackend{}
	case ToolTry:
		b = tryBackend{}
	default:
		return nil, fmt.Errorf("unknown tool %q (want %s)", tool, AllTools())
	}
	for _, o := range opts {
		b = o(b)
	}
	return b, nil
}

// SanitizeName trims a repository name and reports obvious problems.
func SanitizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("repository name is empty")
	}
	if strings.ContainsAny(name, " \t\n") {
		return "", fmt.Errorf("repository name %q contains whitespace", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("repository name %q contains a path separator", name)
	}
	return name, nil
}
