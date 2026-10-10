package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// cafeBackend wraps the `cafe` CLI, the client for GitCafe (git.cafe).
//
// It is the easiest of the three backends to drive because `cafe` speaks JSON:
// `cafe auth status --json` and `cafe repo create --json` both emit a stable
// envelope, so nothing here has to scrape prose out of a human-readable line
// the way the fj backend must.
//
// Unlike tea and fj, `cafe` is not expected to be installed globally. It
// requires Bun to run, and fth deliberately never shells out to `bunx` as a
// fallback: that would re-resolve a multi-megabyte bundle on every invocation,
// which costs more than the repository creation it would be performing. If the
// binary is missing, fth says so and tells the user how to install it.
type cafeBackend struct {
	// bin is an optional explicit path to the cafe binary, set via WithCafeBin.
	bin string
}

// cafeInstallHint is shown when the binary is absent. It is the fix, not just
// the diagnosis.
const cafeInstallHint = "install it with: bun install -g @gitcafe/cli " +
	"(or point fth at it: fth -cafe-bin /path/to/cafe)"

func (cafeBackend) Tool() Tool { return ToolCafe }

func (cafeBackend) LoginHint() string { return "cafe auth login" }

// exe returns the binary to invoke: an explicit path when one was supplied,
// otherwise whatever `cafe` is on PATH. `cafe` is distributed through npm and
// driven by Bun, so it is often installed somewhere unusual or not at all.
func (c cafeBackend) exe() string {
	if c.bin != "" {
		return c.bin
	}
	return "cafe"
}

func (c cafeBackend) Available(ctx context.Context) bool {
	_, err := lookPath(c.exe())
	return err == nil
}

func (c cafeBackend) Version(ctx context.Context) string {
	out, err := run(ctx, nil, c.exe(), "--version")
	if err != nil {
		return "unknown"
	}
	return parseVersion(out)
}

// Logins reports the single GitCafe host. `cafe` keeps one canonical host
// rather than a list, overridable with --host / CAFE_HOST, so there is nothing
// to enumerate here.
func (c cafeBackend) Logins(ctx context.Context) ([]Login, error) {
	st, err := c.status(ctx)
	if err != nil {
		return nil, err
	}
	if st.Data.Host == "" {
		return nil, fmt.Errorf("cafe did not report a host")
	}
	return []Login{{
		Name:    st.Data.Host,
		URL:     "https://" + st.Data.Host,
		User:    st.Data.Username,
		Default: true,
	}}, nil
}

// WhoAmI returns the account name `cafe` is logged in as. Unlike fj, this needs
// no parsing heuristics: the CLI reports the username as a JSON field.
func (c cafeBackend) WhoAmI(ctx context.Context, login string) (string, error) {
	st, err := c.status(ctx)
	if err != nil {
		return "", err
	}
	if st.Data.Username == "" {
		return "", fmt.Errorf("cafe did not report a username; log in with: cafe auth login")
	}
	if login != "" && st.Data.Host != "" && !strings.EqualFold(login, st.Data.Host) {
		return "", fmt.Errorf("cafe is logged into %s, not %s", st.Data.Host, login)
	}
	return st.Data.Username, nil
}

// CreateRepo creates the repository and returns where it lives.
func (c cafeBackend) CreateRepo(ctx context.Context, req CreateRequest) (*Repo, error) {
	args := []string{"--no-update-check", "--json", "repo", "create", req.Name}
	if req.Private {
		args = append(args, "--visibility", "private")
	} else {
		args = append(args, "--visibility", "public")
	}
	if req.Description != "" {
		args = append(args, "--description", req.Description)
	}
	if req.DefaultBranch != "" {
		args = append(args, "--default-branch", req.DefaultBranch)
	}
	// cafe defaults --org to the personal account, so only pass it for a real
	// organisation.
	if req.Owner != "" && !strings.EqualFold(req.Owner, req.Account) {
		args = append(args, "--org", req.Owner)
	}

	out, err := run(ctx, nil, c.exe(), args...)
	if err != nil {
		wrapped := wrapAuthError(ToolCafe, hostOfLogin(req), err)
		if hint := cafeCreateHint(out); hint != "" {
			wrapped = fmt.Errorf("%w\n%s", wrapped, hint)
		}
		return nil, wrapped
	}

	repo := &Repo{Owner: req.Owner, Name: req.Name, FullName: req.Owner + "/" + req.Name}

	// Prefer whatever cafe actually reported over anything we derive; it is the
	// only authority on where the repository actually landed.
	var res cafeRepo
	if err := json.Unmarshal([]byte(clean(out)), &res); err == nil {
		owner, name := res.Data.Resource.Owner, res.Data.Resource.Name
		if full := res.Data.FullName; full != "" {
			if o, n, ok := strings.Cut(full, "/"); ok {
				owner, name = o, n
			}
			repo.FullName = full
		} else if owner != "" && name != "" {
			repo.FullName = owner + "/" + name
		}
		if owner != "" {
			repo.Owner = owner
		}
		if name != "" {
			repo.Name = name
		}
		repo.WebURL, repo.CloneURL, repo.SSHURL = res.Data.URL, res.Data.CloneURL, res.Data.SSHURL
	}

	// Fill whatever the CLI left out from the parts we already know.
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

// cafeCreateHint recognises a failure we can explain. cafe exits non-zero with
// a message on stdout, so the hint has to be derived from the captured output
// rather than the error.
func cafeCreateHint(out string) string {
	if out == "" {
		return ""
	}
	if strings.Contains(out, "already exists") {
		return "\nhint: a repository with that name already exists; pick another name"
	}
	return ""
}

// cafeRepo is the `cafe repo create --json` result. cafe reports the identity
// of what it created but not the clone URLs, so those are derived from the
// host; both url fields are decoded anyway in case a later version adds them.
type cafeRepo struct {
	Data struct {
		Resource struct {
			Owner string `json:"owner"`
			Name  string `json:"name"`
		} `json:"resource"`
		FullName string `json:"fullName"`
		URL      string `json:"url"`
		CloneURL string `json:"cloneUrl"`
		SSHURL   string `json:"sshUrl"`
	} `json:"data"`
}

// cafeStatus is the envelope `cafe --json` wraps every result in. Only the
// fields fth needs are decoded.
type cafeStatus struct {
	Data struct {
		Host     string `json:"host"`
		APIURL   string `json:"apiUrl"`
		Username string `json:"username"`
		// Token is deliberately NOT decoded. `cafe auth status` prints a
		// credential, so the payload is scrubbed of it before it can reach an
		// error message, a log line, or -verbose output. Decoding it into a
		// struct field would put it one careless %+v away from leaking.
		ExpiresAt string `json:"expiresAt"`
	} `json:"data"`
}

func (c cafeBackend) status(ctx context.Context) (cafeStatus, error) {
	var st cafeStatus
	out, err := run(ctx, nil, c.exe(), "--no-update-check", "--json", "auth", "status")
	if err != nil {
		return st, fmt.Errorf("cafe auth status: %w", err)
	}
	if err := json.Unmarshal([]byte(clean(out)), &st); err != nil {
		return st, fmt.Errorf("cafe auth status: could not parse output: %w", err)
	}
	return st, nil
}
