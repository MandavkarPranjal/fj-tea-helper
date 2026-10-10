package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type teaBackend struct{}

// Tool implements Backend.
func (teaBackend) Tool() Tool { return ToolTea }

// Available implements Backend.
func (b teaBackend) Available(ctx context.Context) bool {
	_, err := lookPath("tea")
	return err == nil
}

// Version implements Backend.
func (b teaBackend) Version(ctx context.Context) string {
	out, err := run(ctx, nil, "tea", "--version")
	if err != nil {
		return "unknown"
	}
	// "Version:\t0.15.1\tgolang: 1.26.5"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Version:") {
			continue
		}
		if v := parseVersion(strings.TrimPrefix(line, "Version:")); v != "" {
			return v
		}
	}
	return "unknown"
}

// teaLoginJSON mirrors the fields we need from `tea logins list -o json`.
type teaLoginJSON struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	SSHHost string `json:"ssh_host"`
	User    string `json:"user"`
	Default string `json:"default"`
}

// Logins implements Backend.
func (b teaBackend) Logins(ctx context.Context) ([]Login, error) {
	out, err := run(ctx, minEnv(), "tea", "logins", "list", "-o", "json")
	if err != nil {
		return nil, wrapAuthError(ToolTea, "", err)
	}
	var raw []teaLoginJSON
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parse `tea logins list` output: %w", err)
	}
	var logins []Login
	for _, l := range raw {
		logins = append(logins, Login{
			Name:    l.Name,
			URL:     l.URL,
			User:    l.User,
			Default: l.Default == "true",
		})
	}
	return logins, nil
}

// WhoAmI implements Backend. `tea whoami` takes no flags and no login, so the
// best source of truth is the login list itself.
func (b teaBackend) WhoAmI(ctx context.Context, login string) (string, error) {
	logins, err := b.Logins(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range logins {
		if l.Name == login {
			if l.User == "" {
				return "", fmt.Errorf("tea login %q does not record a user", login)
			}
			return l.User, nil
		}
	}
	return "", fmt.Errorf("no tea login named %q", login)
}

// teaRepoJSON is the subset of the repo object we need after creation.
type teaRepoJSON struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	SSHURL   string `json:"ssh_url"`
	CloneURL string `json:"clone_url"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Name string `json:"name"`
}

// CreateRepo implements Backend.
func (b teaBackend) CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error) {
	args := []string{"repos", "create", "--output", "json"}
	if req.Name != "" {
		args = append(args, "--name", req.Name)
	}
	// `tea repos create --owner` resolves the owner against the instance's
	// *organisation* list. Passing your own account name there fails with
	// "Error: not found", so omit it when the repo belongs to you and let tea
	// default to the authenticated account.
	if req.Owner != "" && !strings.EqualFold(req.Owner, req.Account) {
		args = append(args, "--owner", req.Owner)
	}
	if req.Description != "" {
		args = append(args, "--description", req.Description)
	}
	if req.Private {
		args = append(args, "--private")
	}
	if req.Gitignore != "" {
		args = append(args, "--gitignores", req.Gitignore)
	}
	if req.License != "" {
		args = append(args, "--license", req.License)
	}
	if req.Readme != "" {
		args = append(args, "--readme", req.Readme)
	}
	if req.DefaultBranch != "" {
		args = append(args, "--branch", req.DefaultBranch)
	}
	if req.TrustModel != "" {
		args = append(args, "--trustmodel", req.TrustModel)
	}
	if req.Template {
		args = append(args, "--template")
	}
	if req.Init {
		args = append(args, "--init")
	}
	if req.Login != "" {
		args = append(args, "--login", req.Login)
	}

	out, err := run(ctx, minEnv(), "tea", args...)
	if err != nil {
		return nil, wrapAuthError(ToolTea, req.Login, err)
	}
	return parseTeaRepo(out, req)
}

func parseTeaRepo(out string, req CreateRequest) (*Repo, error) {
	var raw teaRepoJSON
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		// tea may still print a human summary rather than JSON; fall back to
		// constructing URLs from what we know.
		if strings.TrimSpace(out) == "" {
			return nil, fmt.Errorf("tea returned no output for %s/%s", req.Owner, req.Name)
		}
		return repoFromParts(req), nil
	}
	owner, name := raw.Owner.Login, raw.Name
	if owner == "" {
		owner = req.Owner
	}
	if name == "" {
		name = req.Name
	}
	r := &Repo{
		FullName: raw.FullName,
		WebURL:   raw.HTMLURL,
		SSHURL:   raw.SSHURL,
		CloneURL: raw.CloneURL,
		Owner:    owner,
		Name:     name,
	}
	if r.FullName == "" {
		r.FullName = owner + "/" + name
	}
	return r, nil
}

// repoFromParts derives repository URLs from the login host and owner/name.
// It is only ever used to fill gaps left by the backend's own output.
func repoFromParts(req CreateRequest) *Repo {
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(req.Login, "https://"), "http://"), "/")
	base := "https://" + host
	r := &Repo{
		Owner:    req.Owner,
		Name:     req.Name,
		FullName: req.Owner + "/" + req.Name,
	}
	if host != "" {
		r.WebURL = base + "/" + req.Owner + "/" + req.Name
		r.CloneURL = r.WebURL + ".git"
		r.SSHURL = "git@" + host + ":" + req.Owner + "/" + req.Name + ".git"
	}
	return r
}
