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
func Detect(ctx context.Context, tools []Tool) ([]Candidate, error) {
	var out []Candidate
	for _, t := range tools {
		b, err := Discover(t)
		if err != nil {
			return nil, err
		}
		if !b.Available(ctx) {
			out = append(out, Candidate{Backend: b, Users: map[string]string{},
				Notes: []string{fmt.Sprintf("%s is not installed on PATH", t)}})
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

// Select chooses a tool, honouring an explicit --tool override and otherwise
// taking the highest-priority candidate that actually authenticates.
func Select(ctx context.Context, candidates []Candidate, want string) (Candidate, error) {
	if want != "" && want != "auto" {
		for _, c := range candidates {
			if c.Name() == want {
				if !c.Backend.Available(ctx) {
					return c, fmt.Errorf("%s is not installed on PATH", want)
				}
				return c, nil
			}
		}
		return Candidate{}, fmt.Errorf("unknown tool %q (want tea or fj)", want)
	}
	var usable []Candidate
	for _, c := range candidates {
		if c.Backend.Available(ctx) && c.Verified {
			usable = append(usable, c)
		}
	}
	if len(usable) == 0 {
		return Candidate{}, fmt.Errorf("no authenticated %s login found; log in first (tea login add / fj auth login)", strings.Join(toolNames(candidates), " or "))
	}
	return usable[0], nil
}

// ResolveLogin picks the instance to use, honouring an explicit --host.
func ResolveLogin(c Candidate, want string) (Login, error) {
	if len(c.Logins) == 0 {
		return Login{}, fmt.Errorf("%s has no configured logins", c.Name())
	}
	if want != "" {
		for _, l := range c.Logins {
			if strings.EqualFold(l.Name, want) || strings.EqualFold(l.URL, want) {
				return l, nil
			}
			if h := hostOf(l); h != "" && strings.EqualFold(h, want) {
				return l, nil
			}
		}
		return Login{}, fmt.Errorf("%s has no login for %q (known: %s)", c.Name(), want, strings.Join(c.LoginNames(), ", "))
	}
	for _, l := range c.Logins {
		if l.Default {
			return l, nil
		}
	}
	// Prefer the login whose stored credential actually worked.
	for _, l := range c.Logins {
		if c.Users[l.Name] != "" {
			return l, nil
		}
	}
	return c.Logins[0], nil
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
