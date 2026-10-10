// Package forge provides a thin, uniform wrapper over the `tea` (Gitea) and
// `fj` (Forgejo) command line tools so a caller can create a repository
// without caring which CLI is installed or how its flags differ.
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
)

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

// Discover returns a backend for the given tool if its binary is present.
func Discover(tool Tool) (Backend, error) {
	var b Backend
	switch tool {
	case ToolTea:
		b = teaBackend{}
	case ToolFj:
		b = fjBackend{}
	default:
		return nil, fmt.Errorf("unknown tool %q", tool)
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
