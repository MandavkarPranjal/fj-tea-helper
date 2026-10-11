package forge

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Candidate is a tool plus the instances it is logged into.
type Candidate struct {
	Backend Backend
	Logins  []Login
	// Users maps login name -> account name, filled in on a best-effort basis.
	Users map[string]string
	// Verified reports whether at least one login authenticated successfully.
	Verified bool
	// Notes records non-fatal problems worth surfacing to the user.
	Notes []string
}

// Status reports whether the tool's binary was found.
func (c Candidate) Status() string {
	if !c.Backend.Available(context.Background()) {
		return "not installed"
	}
	if !c.Verified {
		return "installed, no working login"
	}
	return fmt.Sprintf("%s %s", c.Backend.Tool(), c.Backend.Version(context.Background()))
}

// Name returns the tool identifier.
func (c Candidate) Name() string { return string(c.Backend.Tool()) }

// Detect inspects every supported tool and returns the candidates, ordered by
// preference (Forgejo's `fj` first, since it is the platform-native client).
func Detect(ctx context.Context, tools []Tool, opts ...Option) ([]Candidate, error) {
	var out []Candidate
	for _, t := range tools {
		b, err := Discover(t, opts...)
		if err != nil {
			return nil, err
		}
		if !b.Available(ctx) {
			out = append(out, Candidate{Backend: b, Users: map[string]string{},
				Notes: []string{unavailableNote(ctx, b)}})
			continue
		}
		c := Candidate{Backend: b, Users: map[string]string{}}
		logins, err := b.Logins(ctx)
		if err != nil {
			c.Notes = append(c.Notes, fmt.Sprintf("could not list logins: %v", err))
			out = append(out, c)
			continue
		}
		c.Logins = logins
		for _, l := range logins {
			// tea reports the user inline; fj requires a round trip.
			if l.User != "" {
				c.Users[l.Name] = l.User
				c.Verified = true
				continue
			}
			user, err := b.WhoAmI(ctx, l.Name)
			if err != nil {
				c.Notes = append(c.Notes, fmt.Sprintf("login %q unusable: %s", l.Name, firstLine(err.Error())))
				continue
			}
			c.Users[l.Name] = user
			c.Verified = true
		}
		out = append(out, c)
	}
	return out, nil
}

// Target is a concrete destination: one backend CLI logged into one instance.
// It is what fth actually acts on, so the user can be offered every pairing
// rather than being asked to name a tool in advance.
type Target struct {
	Candidate
	// Login is the instance to create on.
	Login Login
	// Account is the authenticated user on that instance.
	Account string
}

// Tool returns the CLI that would be used.
func (t Target) Tool() Tool { return t.Backend.Tool() }

// ID is the "tool/host" spelling, which is unique across every target.
func (t Target) ID() string { return string(t.Tool()) + "/" + t.Login.Name }

// Targets flattens the candidates into the list of instances fth can create on.
//
// A login is only offered when its credential actually worked: `fj` in
// particular lists hosts whose OAuth grant has been revoked, and offering one of
// those would just produce a confusing failure later. Within each tool the
// CLI's own default login comes first, so the first target overall is the one
// that would be picked without asking.
func Targets(candidates []Candidate) []Target {
	var out []Target
	for _, c := range candidates {
		if !c.Verified {
			continue
		}
		for _, l := range orderedLogins(c) {
			if c.Users[l.Name] == "" {
				continue
			}
			out = append(out, Target{Candidate: c, Login: l, Account: c.Users[l.Name]})
		}
	}
	return out
}

// orderedLogins returns the logins with the CLI's default first.
func orderedLogins(c Candidate) []Login {
	out := make([]Login, 0, len(c.Logins))
	for _, l := range c.Logins {
		if l.Default {
			out = append(out, l)
		}
	}
	for _, l := range c.Logins {
		if !l.Default {
			out = append(out, l)
		}
	}
	return out
}

// Filter narrows targets to the ones matching an explicit tool and/or host. An
// empty or "auto" tool matches everything. It returns an error rather than an
// empty list when nothing matches, so the caller can explain how to fix it
// rather than falling back to a different instance than the one asked for.
func Filter(candidates []Candidate, targets []Target, tool, host string) ([]Target, error) {
	if tool != "" && tool != "auto" {
		var found *Candidate
		for i, c := range candidates {
			if c.Name() == tool {
				found = &candidates[i]
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("unknown tool %q (want %s)", tool, strings.Join(toolNames(candidates), ", "))
		}
		if !found.Backend.Available(context.Background()) {
			return nil, fmt.Errorf("%s is not installed on PATH%s", tool, hintSuffix(found.Backend.Tool()))
		}
		targets = byTool(targets, tool)
		if len(targets) == 0 {
			return nil, fmt.Errorf("%s has no working login; log in with: %s", tool, loginHint(found.Backend.Tool()))
		}
	}
	if host != "" {
		matched := byHost(targets, host)
		if len(matched) == 0 {
			return nil, fmt.Errorf("no login for %q (known: %s)", host, strings.Join(targetIDs(targets), ", "))
		}
		targets = matched
	}
	return targets, nil
}

func byTool(targets []Target, tool string) []Target {
	var out []Target
	for _, t := range targets {
		if t.Name() == tool {
			out = append(out, t)
		}
	}
	return out
}

// byHost matches a login by its name, its URL or its bare host, since all three
// are spellings a user might reasonably type into -host. The comparison is on
// the normalised host so "gitea.com", "https://gitea.com" and a login URL all
// land on the same instance.
func byHost(targets []Target, host string) []Target {
	want := bareHost(host)
	var out []Target
	for _, t := range targets {
		if strings.EqualFold(t.Login.Name, host) ||
			strings.EqualFold(t.Login.URL, host) ||
			(want != "" && strings.EqualFold(hostOf(t.Login), want)) {
			out = append(out, t)
		}
	}
	return out
}

// bareHost strips a scheme and any path from a URL-ish string.
func bareHost(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.TrimSuffix(s, "/")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

// NoTargetsError reports that nothing is usable at all, naming every tool that
// was checked so the user knows where to log in.
func NoTargetsError(candidates []Candidate) error {
	return fmt.Errorf("no authenticated login found; log in first (%s)",
		strings.Join(loginHints(candidates), " / "))
}

// targetIDs lists the "tool/host" spelling of every target.
func targetIDs(targets []Target) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.ID())
	}
	return out
}

// LoginNames lists the configured login names.
func (c Candidate) LoginNames() []string {
	names := make([]string, 0, len(c.Logins))
	for _, l := range c.Logins {
		names = append(names, l.Name)
	}
	sort.Strings(names)
	return names
}

// User returns the best known account name for a login.
func (c Candidate) User(login string) string { return c.Users[login] }

func hostOf(l Login) string {
	if l.Name != "" && !strings.Contains(l.Name, "://") {
		return l.Name
	}
	if u := strings.TrimPrefix(l.URL, "https://"); u != l.URL {
		return strings.TrimPrefix(u, "http://")
	}
	return ""
}

func toolNames(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name())
	}
	return out
}

// loginHint is the command that establishes credentials for a tool.
func loginHint(t Tool) string {
	switch t {
	case ToolTea:
		return "tea login add"
	case ToolFj:
		return "fj auth login"
	case ToolCafe:
		return "cafe auth login"
	case ToolGh:
		return "gh auth login"
	case ToolTry:
		// The npm package @trylle/trylle-cli installs two executables, `try`
		// and `trylle-cli`, and on real machines `try` is very often an
		// unrelated program (e.g. an "ephemeral workspace manager" at 1.10.1).
		// fth refuses to trust a bare `try` on PATH for exactly that reason
		// (see isTrylleCLI in try.go), so pointing the user at `try` could
		// invoke the wrong program. `trylle-cli` is the unambiguous name.
		return "trylle-cli auth login"
	}
	return fmt.Sprintf("%s login", t)
}

// loginHints lists the login command for every tool that is actually
// installed, so a user without any of them is told what to go install first.
func loginHints(cs []Candidate) []string {
	var out []string
	for _, c := range cs {
		if !c.Backend.Available(context.Background()) {
			continue
		}
		out = append(out, loginHint(c.Backend.Tool()))
	}
	if len(out) == 0 {
		for _, c := range cs {
			out = append(out, loginHint(c.Backend.Tool()))
		}
	}
	return out
}

// unavailableNote explains why a backend could not be used. Most are simply
// missing; a backend that has something specific to say about it (the Trylle
// CLI being shadowed by an unrelated program named `try`, say) adds it here, so
// the listing shows the real obstacle rather than a flat "not installed".
func unavailableNote(ctx context.Context, b Backend) string {
	note := fmt.Sprintf("%s is not installed on PATH%s", b.Tool(), hintSuffix(b.Tool()))
	if n, ok := b.(Noter); ok {
		if extra := n.UnavailableNote(ctx); extra != "" {
			note += "; " + extra
		}
	}
	return note
}

// hintSuffix turns an InstallHint into a trailing clause for an error message.
func hintSuffix(t Tool) string {
	h := InstallHint(t)
	if h == "" {
		return ""
	}
	return "; " + h
}
