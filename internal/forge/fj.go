package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type fjBackend struct{}

// Tool implements Backend.
func (fjBackend) Tool() Tool { return ToolFj }

// Available implements Backend.
func (fjBackend) Available(ctx context.Context) bool {
	_, err := lookPath("fj")
	return err == nil
}

// Version implements Backend.
func (b fjBackend) Version(ctx context.Context) string {
	out, err := run(ctx, nil, "fj", "version")
	if err != nil {
		return "unknown"
	}
	// Output looks like "fj v0.6.0" (possibly followed by extra lines).
	for _, line := range strings.Split(out, "\n") {
		if v := parseVersion(line); v != "" {
			return v
		}
	}
	return "unknown"
}

// Logins implements Backend.
//
// `fj auth list` prints one instance host per line and, notably, lists hosts
// whose stored grant may have expired; callers must still verify with WhoAmI.
func (b fjBackend) Logins(ctx context.Context) ([]Login, error) {
	out, err := run(ctx, minEnv(), "fj", "--style", "minimal", "auth", "list")
	if err != nil {
		return nil, wrapAuthError(ToolFj, "", err)
	}
	var logins []Login
	for _, line := range strings.Split(out, "\n") {
		host := strings.TrimSpace(line)
		if host == "" || strings.HasPrefix(host, "#") {
			continue
		}
		logins = append(logins, Login{
			Name: host,
			URL:  "https://" + host,
		})
	}
	return logins, nil
}

// WhoAmI implements Backend by asking the instance directly.
func (b fjBackend) WhoAmI(ctx context.Context, login string) (string, error) {
	out, err := run(ctx, minEnv(), "fj", "--style", "minimal", "--host", login, "whoami")
	if err != nil {
		return "", wrapAuthError(ToolFj, login, err)
	}
	user := firstUserLine(out)
	if user == "" {
		return "", fmt.Errorf("could not determine fj user for %s", login)
	}
	return user, nil
}

// whoamiRe matches the shape used by `fj whoami` in minimal style:
//
//	currently signed into <user>@<host>
//
// The real output wraps each name in Unicode bidi isolates (U+2068/U+2069),
// which are stripped before this runs.
var whoamiRe = regexp.MustCompile(`(?i)signed\s+in(?:to)?\s+([^\s@]+)(?:@([^\s]+))?`)

// bareUserRe matches a standalone token that looks like user@host, used when
// the output is not in the familiar "signed into" phrasing.
var bareUserRe = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9._-]*)@([A-Za-z0-9.-]+\.[A-Za-z]{2,})\b`)

// firstUserLine picks the account name out of `fj whoami` output.
func firstUserLine(out string) string {
	out = clean(out)
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.ContainsAny(line, "│┌└├─") {
			continue
		}
		// Preferred: the "currently signed into user@host" sentence.
		if m := whoamiRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
		// Any user@host shaped token elsewhere in the output.
		if m := bareUserRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
		// Last resort: a line holding nothing but the account name, which is
		// how the fancy/heading styles render it. URLs are skipped, as are
		// "Key:" style metadata labels.
		cand := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if cand != "" && !strings.ContainsAny(cand, " \t") &&
			!strings.Contains(cand, "://") && !strings.HasSuffix(cand, ":") {
			return cand
		}
	}
	return ""
}

// CreateRepo implements Backend.
//
// fj takes the repository as a positional "owner/name" argument and selects the
// instance with --host.
func (b fjBackend) CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error) {
	full := req.Owner + "/" + req.Name
	args := []string{"--style", "minimal", "repo", "create", full}
	if req.Login != "" {
		args = append(args, "--host", req.Login)
	}
	if req.Description != "" {
		args = append(args, "--description", req.Description)
	}
	if req.Private {
		args = append(args, "--private")
	}

	out, err := run(ctx, minEnv(), "fj", args...)
	if err != nil {
		return nil, wrapAuthError(ToolFj, req.Login, err)
	}
	return parseFjRepo(out, req)
}

// fjRepoJSON is used opportunistically in case fj gains --output json.
type fjRepoJSON struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
	SSHURL   string `json:"ssh_url"`
	CloneURL string `json:"clone_url"`
	Owner    struct {
		Login string `json:"login"`
	} `json:"owner"`
	Name string `json:"name"`
}

// urlRe matches a URL printed on its own line by `fj`, covering the https,
// ssh, git and file schemes as well as scp-style git@host:path remotes.
var urlRe = regexp.MustCompile(`(?m)^\s*((?:[a-zA-Z][a-zA-Z0-9+.-]*://|git@)\S+)\s*$`)

func parseFjRepo(out string, req CreateRequest) (*Repo, error) {
	cleanOut := clean(out)

	// If fj ever emits JSON, use it.
	if strings.HasPrefix(strings.TrimSpace(cleanOut), "{") {
		var raw fjRepoJSON
		if err := json.Unmarshal([]byte(cleanOut), &raw); err == nil {
			owner := raw.Owner.Login
			if owner == "" {
				owner = req.Owner
			}
			name := raw.Name
			if name == "" {
				name = req.Name
			}
			return &Repo{
				FullName: firstNonEmpty(raw.FullName, owner+"/"+name),
				WebURL:   raw.HTMLURL,
				SSHURL:   raw.SSHURL,
				CloneURL: raw.CloneURL,
				Owner:    owner,
				Name:     name,
			}, nil
		}
	}

	// Otherwise harvest any clone URLs fj printed. Start from an empty Repo so
	// that harvested values win over anything we derive ourselves.
	repo := &Repo{Owner: req.Owner, Name: req.Name, FullName: req.Owner + "/" + req.Name}
	for _, m := range urlRe.FindAllStringSubmatch(cleanOut, -1) {
		u := strings.TrimSpace(m[1])
		switch {
		case strings.HasPrefix(u, "git@"), strings.HasPrefix(u, "ssh://"):
			if repo.SSHURL == "" {
				repo.SSHURL = u
			}
		case strings.HasSuffix(u, ".git"):
			if repo.CloneURL == "" {
				repo.CloneURL = u
			}
		default:
			if repo.WebURL == "" {
				repo.WebURL = u
			}
		}
	}

	// Fill any gaps from what we know about the instance.
	derived := repoFromParts(req)
	if repo.WebURL == "" {
		repo.WebURL = derived.WebURL
	}
	if repo.CloneURL == "" {
		repo.CloneURL = derived.CloneURL
	}
	if repo.SSHURL == "" {
		repo.SSHURL = derived.SSHURL
	}
	// An https URL without a .git suffix is the web page, not a clone URL.
	if !strings.HasSuffix(repo.CloneURL, ".git") && strings.HasPrefix(repo.WebURL, "http") {
		repo.CloneURL = repo.WebURL + ".git"
	}
	return repo, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
