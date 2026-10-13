package forge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// probeTimeout bounds a single backend's probe: the availability check, the
// login listing and every per-login verification that belongs to it.
//
// Without it, one unreachable instance (a self-hosted Forgejo behind a VPN, a
// GitHub Enterprise host that has stopped resolving) holds the whole run for as
// long as the underlying CLI's own HTTP timeout, or forever. The budget is
// deliberately generous because a slow-but-working host should still be offered;
// it exists to stop an absent one from deciding how long everyone else waits.
var probeTimeout = 8 * time.Second

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
	// installed and version are what Status would otherwise have to ask the
	// backend for, which means another process per candidate. probe records them
	// while it is already talking to the CLI.
	//
	// probed records that they were recorded. A Candidate built by hand (a test
	// fixture, say) has a zero installed, which must not be read as "not
	// installed" — so anything that needs the answer falls back to asking the
	// backend when probe has not run.
	probed    bool
	installed bool
	version   string
}

// isInstalled reports whether the CLI was found, preferring what probe already
// learned and asking the backend only for a Candidate that never went through it.
func (c Candidate) isInstalled() bool {
	if c.probed {
		return c.installed
	}
	return c.Backend.Available(context.Background())
}

// detection captures the Candidate as a memo, stamping it with the moment it was
// measured. Only a probed Candidate is worth recording: one built by hand has no
// facts in it to remember.
func (c Candidate) detection() Detection {
	if !c.probed {
		return Detection{At: time.Time{}}
	}
	return Detection{
		Installed: c.installed,
		Logins:    c.Logins,
		Users:     c.Users,
		Verified:  c.Verified,
		Notes:     c.Notes,
		Version:   c.version,
		At:        time.Now(),
	}
}

// Status reports whether the tool's binary was found.
//
// Everything it needs was collected during Detect, so printing the listing does
// not re-probe anything. That matters: the `--version` call alone is a process
// spawn per backend, and running it serially for the listing added most of a
// second to a run whose detection had already finished.
func (c Candidate) Status() string {
	if !c.isInstalled() {
		return "not installed"
	}
	if !c.Verified {
		return "installed, no working login"
	}
	// An unprobed Candidate has no recorded version, and isInstalled already
	// covers that case by asking the backend — so asking it for the version too
	// keeps Status honest rather than printing a bare tool name with nothing after
	// it. Probed candidates never reach this, which is what keeps the listing free
	// of extra processes.
	version := c.version
	if !c.probed {
		version = c.Backend.Version(context.Background())
	}
	return fmt.Sprintf("%s %s", c.Backend.Tool(), version)
}

// Name returns the tool identifier.
func (c Candidate) Name() string { return string(c.Backend.Tool()) }

// Detect inspects every supported tool and returns the candidates, ordered by
// preference (Forgejo's `fj` first, since it is the platform-native client).
//
// The tools are probed concurrently and so are the logins within each tool, so
// the wall-clock cost is the slowest single probe rather than the sum of every
// probe. Order is fixed by the tools slice, not by whichever goroutine finished
// first, so the listing and the default target stay identical run to run.
//
// A tool that fails to probe is reported in its candidate's Notes rather than
// failing the run: one broken CLI must not hide the four that work.
//
// A backend measured within detectTTL is taken from the memo instead. Probing is
// dominated by one TLS handshake per instance that never gets cheaper, so the
// only way under it is to not make the call.
//
// Detect is DetectStream with the results collected into one slice, for callers
// that have nothing to show until everything is known. A caller that *does* have
// something to show should use DetectStream directly, so a fast backend can be
// drawn while a slow one is still probing.
func Detect(ctx context.Context, tools []Tool, opts ...Option) ([]Candidate, error) {
	results, err := DetectStream(ctx, tools, opts...)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, len(tools))
	for r := range results {
		out[r.Index] = r.Candidate
	}
	return out, nil
}

// detectTTL is how long a memoised detection is trusted. A zero value disables
// the memo entirely, which is what -no-cache sets.
var detectTTL = DefaultCacheTTL

// probe builds the Candidate for one backend: is it installed, which logins does
// it have, and does each of those credentials actually work.
func probe(ctx context.Context, b Backend) Candidate {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	c := Candidate{Backend: b, Users: map[string]string{}, probed: true}
	c.installed = b.Available(ctx)
	if !c.installed {
		c.Notes = append(c.Notes, unavailableNote(ctx, b))
		return c
	}

	// The version is another process, and it is only ever read to print the
	// backend listing. Ask for it alongside the login probe rather than after, so
	// the listing costs no extra wall clock.
	//
	// It writes to a local and is joined explicitly before the successful return,
	// never in a defer: a deferred Wait runs after the return value has been
	// copied, so the goroutine would still be racing to write it. The early
	// returns below leave it running, which is safe because the deferred cancel
	// kills the process it is waiting on.
	var (
		versionWG sync.WaitGroup
		version   string
	)
	versionWG.Add(1)
	go func() {
		defer versionWG.Done()
		version = b.Version(ctx)
	}()

	logins, err := b.Logins(ctx)
	if err != nil {
		c.Notes = append(c.Notes, fmt.Sprintf("could not list logins: %v", err))
		return c
	}
	c.Logins = logins

	// tea and gh report the account inline, so they usually need no round trip
	// at all; fj needs one per host, and a user signed into several Forgejo
	// hosts should not pay for them in sequence.
	users := make([]string, len(logins))
	errs := make([]error, len(logins))
	var wg sync.WaitGroup
	for i, l := range logins {
		if l.User != "" {
			users[i] = l.User
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			users[i], errs[i] = b.WhoAmI(ctx, l.Name)
		}()
	}
	wg.Wait()

	// Notes and Users are filled in login order, not completion order, so two
	// runs against the same hosts print the same thing.
	for i, l := range logins {
		if users[i] == "" {
			note := fmt.Sprintf("login %q unusable", l.Name)
			if errs[i] != nil {
				note += ": " + firstLine(errs[i].Error())
			}
			c.Notes = append(c.Notes, note)
			continue
		}
		c.Users[l.Name] = users[i]
		c.Verified = true
	}

	// Only a verified backend prints a version, so only now is the probe worth
	// joining.
	if c.Verified {
		versionWG.Wait()
		c.version = version
	}
	return c
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
		if !found.isInstalled() {
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
		if !c.isInstalled() {
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
