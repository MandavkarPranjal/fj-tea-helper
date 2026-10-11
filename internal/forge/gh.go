package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ghInstallHint is shown when the binary is absent.
const ghInstallHint = "install it from https://cli.github.com " +
	"(e.g. brew install gh, or: sudo apt install gh)"

// ghBackend wraps the `gh` CLI, GitHub's own command line client.
//
// It is the only backend that is not tied to a single instance: gh is
// authenticated against github.com plus any number of GitHub Enterprise hosts,
// so Logins has something real to enumerate. Both `gh auth status` and
// `gh repo view` speak JSON, so nothing here has to scrape a human-readable
// line the way the fj backend must.
//
// gh selects the target host through the GH_HOST environment variable rather
// than a flag, which is why the invocations go through ghEnv.
type ghBackend struct{}

func (ghBackend) Tool() Tool { return ToolGh }

func (ghBackend) LoginHint() string { return "gh auth login" }

func (ghBackend) Available(ctx context.Context) bool {
	_, err := lookPath("gh")
	return err == nil
}

func (ghBackend) Version(ctx context.Context) string {
	out, err := run(ctx, nil, "gh", "--version")
	if err != nil {
		return "unknown"
	}
	// "gh version 2.100.0 (2026-09-04)"
	return parseVersion(out)
}

// ghAuthJSON is `gh auth status --json hosts`: every known host mapped to the
// accounts stored for it.
//
// The token is deliberately absent from this struct. `gh auth status` will
// print one when asked with --show-token, and decoding it into a field would
// put a live credential one careless %+v away from leaking.
type ghAuthJSON struct {
	Hosts map[string][]ghAccount `json:"hosts"`
}

// ghAccount is one stored credential on one host.
type ghAccount struct {
	// State is "success" or a failure such as "HTTP 401".
	State string `json:"state"`
	// Active marks the account gh acts as when targeting this host.
	Active bool `json:"active"`
	// Host is the instance, repeated here so accounts are self-describing.
	Host string `json:"host"`
	// Login is the account name.
	Login string `json:"login"`
	// GitProtocol is the clone protocol configured for pushes ("https" or
	// "ssh"). fth reports it rather than acting on it: the remote URL is the
	// user's decision, and gh's preference is only a default.
	GitProtocol string `json:"gitProtocol"`
}

// Logins lists the hosts gh is authenticated against.
//
// Every known host is returned, including ones whose credential no longer
// works: User is left empty for those, so Detect treats them as unverified and
// leaves them out of the instance picker rather than offering a target that
// would fail later. `gh auth status --json` exits 0 even when an account is
// broken, which is exactly the behaviour fth needs here.
func (b ghBackend) Logins(ctx context.Context) ([]Login, error) {
	raw, err := b.auth(ctx)
	if err != nil {
		return nil, err
	}

	var logins []Login
	for _, host := range ghHostOrder(raw.Hosts) {
		l := Login{Name: host, URL: "https://" + host}
		for _, a := range raw.Hosts[host] {
			// gh keeps several accounts per host; the active one is the only
			// account it will act as.
			if !a.Active || !strings.EqualFold(a.State, "success") {
				continue
			}
			l.User = a.Login
			// Mark it the CLI's own choice for this host, so it leads the list
			// the way tea's and fj's default logins do.
			l.Default = true
			break
		}
		logins = append(logins, l)
	}
	if len(logins) == 0 {
		return nil, fmt.Errorf("gh knows about no GitHub host; log in with: %s", loginHint(ToolGh))
	}
	if !anyUsable(logins) {
		return nil, fmt.Errorf("gh has no working account on %s; log in with: gh auth login --hostname %s",
			strings.Join(loginNames(logins), ", "), loginNames(logins)[0])
	}
	return logins, nil
}

// anyUsable reports whether at least one login resolved to an account.
func anyUsable(logins []Login) bool {
	for _, l := range logins {
		if l.User != "" {
			return true
		}
	}
	return false
}

func loginNames(logins []Login) []string {
	out := make([]string, 0, len(logins))
	for _, l := range logins {
		out = append(out, l.Name)
	}
	return out
}

// ghHostOrder returns host names in a stable, meaningful order: github.com
// first, because it is the canonical instance, then the rest alphabetically.
//
// Sorting matters beyond cosmetics. Decoding into a map leaves the iteration
// order to Go, which is randomised, so an unsorted list would shuffle the
// instance picker between runs and stop the first entry from being a stable
// default for Enter.
func ghHostOrder(hosts map[string][]ghAccount) []string {
	names := make([]string, 0, len(hosts))
	for h := range hosts {
		names = append(names, h)
	}
	sort.Strings(names)

	var first, rest []string
	for _, n := range names {
		if strings.EqualFold(n, "github.com") {
			first = append(first, n)
			continue
		}
		rest = append(rest, n)
	}
	return append(first, rest...)
}

func (b ghBackend) auth(ctx context.Context) (ghAuthJSON, error) {
	var raw ghAuthJSON
	out, err := run(ctx, minEnv(), "gh", "auth", "status", "--json", "hosts")
	if err != nil {
		return raw, wrapAuthError(ToolGh, "", err)
	}
	if err := json.Unmarshal([]byte(clean(out)), &raw); err != nil {
		return raw, fmt.Errorf("could not parse `gh auth status --json` output "+
			"(this needs gh 2.40 or newer): %w", err)
	}
	return raw, nil
}

// WhoAmI returns the account gh acts as on a host.
//
// gh reports the login as a JSON field, so there is nothing to verify
// separately: the same auth status listing that produced the login is the
// authority. This re-reads it rather than caching, so a target that has been
// switched with `gh auth switch` since detection is reported correctly.
func (b ghBackend) WhoAmI(ctx context.Context, login string) (string, error) {
	logins, err := b.Logins(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range logins {
		if strings.EqualFold(l.Name, login) {
			if l.User == "" {
				return "", fmt.Errorf("gh has no working account for %s; log in with: gh auth login --hostname %s", login, login)
			}
			return l.User, nil
		}
	}
	return "", fmt.Errorf("gh is not authenticated to %s", login)
}

// ghRepoJSON is the subset of `gh repo view --json` that fth needs.
type ghRepoJSON struct {
	NameWithOwner string `json:"nameWithOwner"`
	URL           string `json:"url"`
	SSHURL        string `json:"sshUrl"`
}

// CreateRepo implements Backend.
//
// gh takes the owner as part of the name argument ("OWNER/NAME") rather than as
// a flag, and needs the visibility passed explicitly: with neither --public nor
// --private it would drop into its interactive prompts, which fth must never
// reach.
//
// Nothing is asked of gh to seed template files. Its --add-readme, --gitignore
// and --license all create an initial commit on the server, which would then
// collide with the commit fth pushes from the local scaffold. fth writes those
// files locally instead, which is what every non-tea path already does.
func (b ghBackend) CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error) {
	full := req.Name
	if req.Owner != "" {
		full = req.Owner + "/" + req.Name
	}

	args := []string{"repo", "create", full}
	if req.Private {
		args = append(args, "--private")
	} else {
		args = append(args, "--public")
	}
	if req.Description != "" {
		args = append(args, "--description", req.Description)
	}

	out, err := run(ctx, ghEnv(req.Login), "gh", args...)
	if err != nil {
		wrapped := wrapAuthError(ToolGh, hostOfLogin(req), err)
		if hint := ghCreateHint(out); hint != "" {
			wrapped = fmt.Errorf("%w\n%s", wrapped, hint)
		}
		return nil, wrapped
	}

	repo := &Repo{Owner: req.Owner, Name: req.Name, FullName: full}
	// gh confirms the creation but reports no clone URLs, so ask it where the
	// repository actually landed. Only the server knows for sure.
	b.describe(ctx, req, full, repo)
	// Fill anything gh did not tell us from the parts we already have.
	d := repoFromParts(req)
	if repo.Owner == "" {
		repo.Owner, repo.FullName = d.Owner, d.FullName
	}
	if repo.WebURL == "" {
		repo.WebURL = d.WebURL
	}
	if repo.CloneURL == "" {
		repo.CloneURL = d.CloneURL
	}
	if repo.SSHURL == "" {
		repo.SSHURL = d.SSHURL
	}
	return repo, nil
}

// describe asks gh about the repository it just created and records what it
// says. Failures are not fatal: the caller derives the remaining fields, so a
// hiccup on this second call cannot undo a repository that already exists.
func (b ghBackend) describe(ctx context.Context, req CreateRequest, full string, repo *Repo) {
	out, err := run(ctx, ghEnv(req.Login), "gh", "repo", "view", full, "--json", "nameWithOwner,url,sshUrl")
	if err != nil {
		return
	}
	var raw ghRepoJSON
	if json.Unmarshal([]byte(clean(out)), &raw) != nil {
		return
	}
	if raw.NameWithOwner != "" {
		repo.FullName = raw.NameWithOwner
		if owner, name, ok := strings.Cut(raw.NameWithOwner, "/"); ok {
			repo.Owner, repo.Name = owner, name
		}
	}
	if raw.URL != "" {
		repo.WebURL = strings.TrimSuffix(raw.URL, "/")
		// `url` is the repository's web page; its HTTPS clone URL is the same
		// path with a .git suffix.
		repo.CloneURL = repo.WebURL + ".git"
	}
	if raw.SSHURL != "" {
		repo.SSHURL = raw.SSHURL
	}
}

// ghCreateHint explains a creation failure the user can act on. gh puts its
// reasons on stdout, so the hint is derived from the captured output rather
// than from the error.
func ghCreateHint(out string) string {
	switch {
	case out == "":
		return ""
	case strings.Contains(out, "already exists"):
		return "hint: a repository with that name already exists; pick another name"
	case strings.Contains(out, "name is reserved"),
		strings.Contains(out, "invalid repository name"):
		return "hint: GitHub rejected that repository name; try a different one"
	}
	return ""
}

// ghEnv returns the environment a `gh` invocation needs to target a host.
//
// gh takes the host from GH_HOST rather than a flag, and assumes github.com
// when it is unset, so Enterprise hosts have to be named explicitly. The rest
// of the environment is kept as-is apart from the colour variables run already
// strips for parsing.
func ghEnv(host string) []string {
	env := minEnv()
	h := bareHost(host)
	if h == "" || strings.EqualFold(h, "github.com") {
		return env
	}
	return append(env, "GH_HOST="+h)
}
