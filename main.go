// Command fth (fj-tea-helper) creates a new repository on a Forgejo or Gitea
// instance using the `fj` or `tea` command line tools, then scaffolds a local
// clone with a README, .gitignore and LICENSE, wires the remote and pushes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/himmel/fj-tea-helper/internal/forge"
	"github.com/himmel/fj-tea-helper/internal/progress"
	"github.com/himmel/fj-tea-helper/internal/prompt"
	"github.com/himmel/fj-tea-helper/internal/scaffold"
	"github.com/himmel/fj-tea-helper/internal/templates"
	"github.com/himmel/fj-tea-helper/internal/ui"
)

const version = "0.1.0"

type options struct {
	name        string
	tool        string
	cafeBin     string
	host        string
	owner       string
	description string
	visibility  string
	license     string
	licenseFile string
	lang        string
	branch      string
	remote      string
	dir         string
	trustModel  string
	template    bool
	readme      bool
	// url selects which clone URL the remote is wired to: "ssh" or "https".
	// SSH is the default; fth asks before using it.
	url         string
	noPush      bool
	noScaffold  bool
	force       bool
	year        int
	holder      string
	dryRun      bool
	yes         bool
	list        bool
	showVersion bool
	verbose     bool
}

func main() {
	err := run(os.Args[1:])
	forge.Cleanup()
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Errorf("%v", err))
		os.Exit(1)
	}
}

// reorderArgs makes flags work before or after the positional arguments,
// which is what users expect even though the flag package only accepts flags
// first. Everything after a bare "--" is treated as positional.
func reorderArgs(fs *flag.FlagSet, argv []string) []string {
	isBool := func(name string) bool {
		f := fs.Lookup(name)
		if f == nil {
			return false
		}
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		return ok && b.IsBoolFlag()
	}

	var flags, positional []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]

		if a == "--" {
			positional = append(positional, argv[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}

		name := strings.TrimLeft(a, "-")
		// Split "flag=value" into its parts.
		if j := strings.IndexByte(name, '='); j >= 0 {
			flags = append(flags, a)
			continue
		}
		flags = append(flags, a)
		// A non-boolean flag written without "=" consumes the next token.
		if !isBool(name) && i+1 < len(argv) {
			i++
			flags = append(flags, argv[i])
		}
	}

	out := append(flags, positional...)
	// A positional that begins with "-" can only have come after "--", so we
	// must re-emit the terminator or the flag parser would treat it as a flag.
	if len(positional) > 0 && strings.HasPrefix(positional[0], "-") {
		out = append(append(flags, "--"), positional...)
	}
	return out
}

func run(argv []string) error {
	var o options
	fs := flag.NewFlagSet("fth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fs.StringVar(&o.tool, "tool", "auto", "backend CLI to use: auto, tea, fj, or cafe")
	fs.StringVar(&o.cafeBin, "cafe-bin", "", "path to the cafe binary, if it is not on PATH")
	fs.StringVar(&o.host, "host", "", "instance host to create on (e.g. gitea.com)")
	fs.StringVar(&o.owner, "owner", "", "repository owner (default: your account)")
	fs.StringVar(&o.description, "description", "", "short repository description")
	fs.StringVar(&o.visibility, "visibility", "public", "visibility: public or private")
	fs.StringVar(&o.license, "license", "none", "license SPDX id (see -list), or 'none'")
	fs.StringVar(&o.licenseFile, "license-file", "", "path to a license file to copy instead of an SPDX one")
	fs.StringVar(&o.lang, "lang", "", "seed a .gitignore for: go, node, python, rust")
	fs.StringVar(&o.branch, "branch", "main", "default branch name")
	fs.StringVar(&o.remote, "remote", "origin", "git remote name")
	fs.StringVar(&o.dir, "dir", "", "local directory (default: ./<name>)")
	fs.StringVar(&o.trustModel, "trust-model", "", "Gitea trust model (tea only)")
	fs.BoolVar(&o.template, "template", false, "mark the repository as a template (tea only)")
	fs.BoolVar(&o.readme, "readme", true, "write a README.md")
	fs.StringVar(&o.url, "url", "ssh", "remote URL to use: ssh or https")
	fs.BoolVar(&o.noPush, "no-push", false, "create the remote but do not push")
	fs.BoolVar(&o.noScaffold, "no-scaffold", false, "only create the remote; no local directory")
	fs.BoolVar(&o.force, "force", false, "scaffold even if the local directory is not empty")
	fs.IntVar(&o.year, "year", 0, "copyright year for the license (default: this year)")
	fs.StringVar(&o.holder, "holder", "", "copyright holder for the license (default: your account)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "show what would happen without changing anything")
	fs.BoolVar(&o.yes, "yes", false, "accept the default answer for every prompt (for scripts and CI)")
	fs.BoolVar(&o.list, "list", false, "list detected backends, logins and licenses, then exit")
	fs.BoolVar(&o.showVersion, "version", false, "print the helper version and exit")
	fs.BoolVar(&o.verbose, "verbose", false, "print the underlying CLI commands")

	fs.Usage = func() { usage(fs) }

	// Support flags appearing before or after the repository name.
	if err := fs.Parse(reorderArgs(fs, argv)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// fs.Visit only reports flags the user actually passed, which is how we
	// tell an explicit -remote from its default. That distinction decides
	// whether a taken remote name is a conflict or merely something to ask
	// about.
	explicitRemote, explicitURL := false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "remote":
			explicitRemote = true
		case "url":
			explicitURL = true
		}
	})

	if o.showVersion {
		fmt.Printf("fth %s\n", version)
		return nil
	}

	rest := fs.Args()
	if o.list {
		// fall through: -list does not need a name
	} else {
		switch len(rest) {
		case 0:
			fs.Usage()
			return errors.New("a repository name is required")
		case 1:
			// Allow `fth owner/name` as a shorthand.
			if owner, name, ok := strings.Cut(rest[0], "/"); ok && owner != "" && name != "" {
				if o.owner != "" && o.owner != owner {
					return fmt.Errorf("conflicting owners: -owner %q and positional %q", o.owner, owner)
				}
				o.owner, o.name = owner, name
			} else {
				o.name = rest[0]
			}
		default:
			fs.Usage()
			return fmt.Errorf("unexpected arguments: %s", strings.Join(rest, " "))
		}
	}

	ctx := context.Background()

	// Detection is needed for -list and for the real run. When the backend is
	// named explicitly there is no need to probe the other CLI.
	tools := forge.AllTools()
	if o.tool != "" && o.tool != "auto" {
		tools = []forge.Tool{forge.Tool(o.tool)}
	}
	// Probing logins means a handful of network round-trips, so it is worth
	// showing that something is happening rather than a blank screen.
	spin := progress.New(os.Stderr)
	spin.Start("detecting backend")
	candidates, err := forge.Detect(ctx, tools, forge.WithCafeBin(o.cafeBin))
	spin.Stop()
	if err != nil {
		return err
	}

	if o.list {
		printList(candidates)
		return nil
	}

	name, err := forge.SanitizeName(o.name)
	if err != nil {
		return err
	}

	// Ask which instance to create on before anything else, since it decides
	// the owner, the remote URL and where the repository ends up. Everything
	// is optional: -tool and -host narrow the list, -yes or a pipe takes the
	// default, and an explicit -tool -host pair skips the question entirely.
	ask := prompt.New(os.Stdin, os.Stderr)
	if o.yes {
		ask = prompt.NonInteractive(os.Stderr)
	}

	targets, err := forge.Filter(candidates, forge.Targets(candidates), o.tool, o.host)
	if err != nil {
		return reportNoBackend(candidates, err)
	}
	if len(targets) == 0 {
		return reportNoBackend(candidates, forge.NoTargetsError(candidates))
	}
	target := pickTarget(ask, targets)

	login := target.Login
	owner := o.owner
	if owner == "" {
		owner = target.Account
	}
	if owner == "" {
		return fmt.Errorf("could not determine the owner; pass -owner (logins: %s)", strings.Join(target.LoginNames(), ", "))
	}

	private := strings.EqualFold(o.visibility, "private")
	if !private && !strings.EqualFold(o.visibility, "public") {
		return fmt.Errorf("-visibility must be public or private, got %q", o.visibility)
	}

	branch := o.branch
	if branch == "" {
		branch = "main"
	}

	licenseID := ""
	if o.licenseFile != "" {
		licenseID = "custom"
	} else if o.license != "" && !strings.EqualFold(o.license, "none") {
		if _, ok := templates.LicenseByID(o.license); !ok {
			return fmt.Errorf("unknown license %q (see -list)", o.license)
		}
		licenseID = o.license
	}

	dir := o.dir
	if dir == "" {
		dir = name
	}

	// SSH is the preferred remote, but the user gets to choose, so ask before
	// anything is created. -url skips the question entirely. With -no-scaffold
	// there is no local remote to wire up, so there is nothing to ask about.
	scheme, previewURL, err := chooseScheme(ask, o.url, explicitURL, login.Name, owner, name, !o.noScaffold)
	if err != nil {
		return err
	}

	var local *scaffold.LocalState
	if !o.noScaffold {
		state, err := scaffold.Inspect(dir)
		if err != nil {
			return err
		}
		local = state
	}

	remoteName := o.remote
	if remoteName == "" {
		remoteName = "origin"
	}
	localBranch := branch
	setUpstream := true
	attach := local != nil && local.IsRepo && local.HasCommits

	if attach {
		localBranch = local.Branch
		if localBranch == "" {
			return fmt.Errorf("%s has no branch to push; make a commit first", local.Dir)
		}
		if local.RemoteExists(remoteName) {
			if explicitRemote {
				return fmt.Errorf("remote %q already exists in %s; pick another name with -remote", remoteName, local.Dir)
			}
			remoteName = askForRemoteName(ask, local, login.Name, remoteName)
			// The repository already has an origin and we are not taking it
			// over, so tracking stays with origin unless the user says
			// otherwise.
			setUpstream = false
			if remoteName != "origin" {
				setUpstream = ask.YesNo(
					fmt.Sprintf("Set %q as upstream for branch %s?", remoteName, localBranch), false)
			}
		}
	}

	// Build the plan before touching anything, so -dry-run is meaningful.
	plan := createPlan{
		Tool:        string(target.Tool()),
		Host:        login.Name,
		Owner:       owner,
		Name:        name,
		Description: o.description,
		Private:     private,
		Branch:      localBranch,
		Dir:         dir,
		Remote:      remoteName,
		Push:        !o.noPush,
		Scaffold:    !o.noScaffold,
		SSH:         scheme == "ssh",
		RemoteURL:   previewURL,
		Attach:      attach,
		Upstream:    setUpstream,
	}
	printPlan(plan)

	if o.dryRun {
		fmt.Println(ui.Dim("(dry run: nothing was created)"))
		return nil
	}

	// Validate the local destination before creating anything remote, so a bad
	// -dir cannot leave an orphaned repository behind on the server.
	if !o.noScaffold {
		if err := scaffold.Precheck(dir, o.force); err != nil {
			return fmt.Errorf("cannot scaffold %s: %w", dir, err)
		}
	}

	// 1. Create the remote repository.
	req := forge.CreateRequest{
		Login:         login.Name,
		Owner:         owner,
		Account:       target.Account,
		Name:          name,
		Description:   o.description,
		Private:       private,
		DefaultBranch: branch,
		TrustModel:    o.trustModel,
		Template:      o.template,
	}
	if !o.noScaffold {
		// Let the backend seed template files only when the user asked for
		// tea's richer template support; otherwise we write them locally.
		if target.Tool() == forge.ToolTea && (o.lang != "" || licenseID != "") && !o.readme {
			req.Init = true
			req.Gitignore = o.lang
			req.License = licenseID
		}
	}

	logf(o.verbose, "creating %s/%s on %s via %s...\n", owner, name, login.Name, target.Tool())
	spin.Start("creating " + owner + "/" + name + " on " + login.Name)
	repo, err := target.Backend.CreateRepo(ctx, req)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("create remote repository: %w", err)
	}
	fmt.Println(ui.Done(fmt.Sprintf("created %s on %s (%s)", repo.FullName, login.Name, ui.Dim(repo.WebURL))))

	if o.noScaffold {
		return nil
	}

	// 2. Wire up the local repository: scaffold a new one, or just attach the
	// remote to the repository the user already has.
	holder := o.holder
	if holder == "" {
		holder = owner
	}
	so := scaffold.Options{
		Dir:           dir,
		Name:          name,
		Description:   o.description,
		WriteReadme:   o.readme,
		LicenseSPDX:   licenseID,
		LicenseHolder: holder,
		CopyrightYear: o.year,
		Language:      o.lang,
		DefaultBranch: localBranch,
		RemoteName:    remoteName,
		RemoteURL:     pickRemoteURL(repo, scheme),
		Force:         o.force,
	}

	var res *scaffold.Result
	if attach {
		logf(o.verbose, "attaching remote %s to %s\n", remoteName, dir)
		res, err = scaffold.Attach(dir, so)
		if err != nil {
			return fmt.Errorf("attach remote: %w", err)
		}
		fmt.Println(ui.Done("attached to existing repository " + res.Dir + " " + ui.Dim("(branch "+res.Branch+")")))
	} else {
		logf(o.verbose, "scaffolding %s\n", dir)
		res, err = scaffold.Create(so,
			func(spdx string, year int, holder string) (string, error) {
				if spdx == "custom" {
					return readLicenseFile(o.licenseFile)
				}
				l, _ := templates.LicenseByID(spdx)
				return l.Render(year, holder)
			},
			templates.Gitignore)
		if err != nil {
			return fmt.Errorf("scaffold: %w", err)
		}
		fmt.Println(ui.Done("scaffolded " + res.Dir + " " + ui.Dim("(branch "+res.Branch+")")))
		for _, f := range res.FilesCreated {
			fmt.Println("  " + ui.Dot(f))
		}
	}
	if res.RemoteAdded {
		fmt.Println("  " + ui.Dot("remote "+res.RemoteName+" → "+res.RemoteURL))
	}

	// 3. Push, unless suppressed.
	if o.noPush {
		fmt.Println("\n" + ui.Dim("skipping push (-no-push)"))
		return nil
	}
	logf(o.verbose, "pushing to %s (set-upstream=%v)\n", res.RemoteName, setUpstream)
	spin.Start("pushing " + res.Branch + " to " + res.RemoteName)
	err = scaffold.Push(res.Dir, res.RemoteName, res.Branch, setUpstream)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if setUpstream {
		fmt.Println(ui.Done("pushed " + res.Branch + " to " + res.RemoteName + " " + ui.Dim("(tracking "+res.RemoteName+")")))
	} else {
		// Report what the upstream actually is, which may well be origin
		// rather than nothing at all.
		up := scaffold.CurrentUpstream(res.Dir, res.Branch)
		if up == "" {
			fmt.Println(ui.Done("pushed " + res.Branch + " to " + res.RemoteName + " " + ui.Dim("(no upstream set)")))
		} else {
			fmt.Println(ui.Done("pushed " + res.Branch + " to " + res.RemoteName + " " + ui.Dim("(upstream "+up+")")))
		}
	}
	return nil
}

// pickTarget shows every instance fth can create on and returns the chosen one.
//
// Both axes matter: tea and fj are routinely logged into several hosts at once,
// so "which backend" is not really the question — "which account on which host"
// is. Offering the pairing means the user never has to know that `-tool tea
// -host git.acme.com` is two flags for one decision.
//
// The list is already narrowed by -tool/-host before it gets here, and the
// first entry is the one fth would have picked on its own.
func pickTarget(ask *prompt.Prompter, targets []forge.Target) forge.Target {
	if len(targets) == 1 || !ask.Interactive() {
		return targets[0]
	}
	opts := make([]prompt.Option, 0, len(targets))
	for _, t := range targets {
		opts = append(opts, prompt.Option{
			Label:  fmt.Sprintf("%s  %s", ui.Pad(string(t.Tool()), 4), t.Login.Name),
			Value:  t.ID(),
			Detail: t.Account,
		})
	}
	got := ask.Select("Create the repository on", opts, targets[0].ID())
	for _, t := range targets {
		if t.ID() == got {
			return t
		}
	}
	return targets[0]
}

// askForRemoteName prompts for a name for the new remote, offering one
// derived from the forge host and skipping names already in use. Pressing
// Enter takes that suggestion.
func askForRemoteName(ask *prompt.Prompter, local *scaffold.LocalState, host, taken string) string {
	suggest := scaffold.SuggestRemoteName(host, local.RemoteExists)
	label := fmt.Sprintf("remote %q already exists in %s. Name for the new remote?", taken, local.Dir)
	for {
		answer := ask.Line(label, suggest)
		if scaffold.ValidRemoteName(answer) && !local.RemoteExists(answer) {
			return answer
		}
		// Without a terminal there is nobody to re-ask, so stop and use the
		// suggestion rather than spinning.
		if !ask.Interactive() {
			return suggest
		}
		if !scaffold.ValidRemoteName(answer) {
			fmt.Fprintf(os.Stderr, "fth: %q is not a valid remote name; try again\n", answer)
		} else {
			fmt.Fprintf(os.Stderr, "fth: remote %q already exists; try again\n", answer)
		}
	}
}

// chooseScheme resolves which clone URL the remote should use.
//
// SSH is the default because it does not need a stored credential helper entry
// and does not care about token expiry, but the choice is the user's: they are
// shown both URLs, with the one they would end up with, before anything is
// created. Passing -url skips the question.
//
// It returns the scheme and the URL it corresponds to, so the plan can show
// exactly what the remote will point at.
func chooseScheme(ask *prompt.Prompter, flagValue string, explicit bool, host, owner, name string, relevant bool) (scheme, remoteURL string, err error) {
	want := strings.ToLower(strings.TrimSpace(flagValue))
	if want == "" {
		want = "ssh"
	}
	if want != "ssh" && want != "https" {
		return "", "", fmt.Errorf("unknown -url %q (want ssh or https)", flagValue)
	}
	sshURL, httpsURL := forge.PreviewURLs(host, owner, name)
	url := sshURL
	if want == "https" {
		url = httpsURL
	}
	if !relevant {
		return want, url, nil
	}

	// Only one form is available; using the other is impossible.
	if sshURL == "" {
		return "https", httpsURL, nil
	}
	if httpsURL == "" {
		return "ssh", sshURL, nil
	}

	// -yes, a pipe, or CI: take the flag's answer without asking. The check is
	// on explicitness rather than on flagValue being non-empty, because -url
	// has a default of "ssh" and is therefore never empty.
	if explicit || !ask.Interactive() {
		return want, url, nil
	}

	got := ask.Select("Remote URL", []prompt.Option{
		{Label: "ssh", Value: "ssh", Detail: sshURL},
		{Label: "https", Value: "https", Detail: httpsURL},
	}, want)
	if got == "https" {
		return "https", httpsURL, nil
	}
	return "ssh", sshURL, nil
}

// pickRemoteURL returns the clone URL the remote should point at. The backend's
// own answer wins when it has one, since only the server knows for sure; the
// derived URL is the fallback.
func pickRemoteURL(repo *forge.Repo, scheme string) string {
	wantSSH := scheme != "https"
	if wantSSH && repo.SSHURL != "" {
		return repo.SSHURL
	}
	if !wantSSH && repo.CloneURL != "" {
		return repo.CloneURL
	}
	// The requested form was not available; take whatever the backend gave us.
	if repo.CloneURL != "" {
		return repo.CloneURL
	}
	return repo.SSHURL
}

func readLicenseFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read license file: %w", err)
	}
	return string(b), nil
}

func reportNoBackend(candidates []forge.Candidate, err error) error {
	fmt.Fprintln(os.Stderr)
	printList(candidates)
	fmt.Fprintln(os.Stderr)
	return err
}

type createPlan struct {
	Tool, Host, Owner, Name, Description, Branch, Dir, Remote string
	Private                                                   bool
	Push, Scaffold, SSH                                       bool
	// RemoteURL is the clone URL the remote will be wired to. It is derived
	// from the host before creation, so the plan shows the real target.
	RemoteURL string
	// Attach reports that an existing repository is being given a new
	// remote rather than scaffolded from scratch.
	Attach bool
	// Upstream reports whether the pushed branch will track the remote.
	Upstream bool
}

func printPlan(p createPlan) {
	vis := "public"
	if p.Private {
		vis = "private"
	}
	fmt.Println(ui.Heading("Plan"))
	fmt.Println(ui.Field("backend", ui.Value(p.Tool)))
	fmt.Println(ui.Field("instance", ui.Value(p.Host)))
	fmt.Println(ui.Field("repository", ui.Value(p.Owner+"/"+p.Name)+" "+ui.Dim("("+vis+")")))
	if p.Description != "" {
		fmt.Println(ui.Field("summary", ui.Value(p.Description)))
	}
	if p.Scaffold {
		if p.Attach {
			fmt.Println(ui.Field("local dir", ui.Value(p.Dir)+" "+ui.Dim("(existing repo, branch "+p.Branch+")")))
		} else {
			fmt.Println(ui.Field("local dir", ui.Value(p.Dir)+" "+ui.Dim("(branch "+p.Branch+")")))
		}
		fmt.Println(ui.Field("remote", ui.Value(p.Remote)))
		if p.RemoteURL != "" {
			fmt.Println(ui.Field("remote url", ui.Dim(p.RemoteURL)))
		}
		push := ui.Success("yes")
		if !p.Push {
			push = ui.Dim("no")
		}
		fmt.Println(ui.Field("push", push))
		if p.Push && p.Attach {
			up := ui.Dim("no (origin stays upstream)")
			if p.Upstream {
				up = ui.Value(p.Remote)
			}
			fmt.Println(ui.Field("upstream", up))
		}
	}
	fmt.Println()
}

func printList(candidates []forge.Candidate) {
	fmt.Println(ui.Heading("Backends"))
	for _, c := range candidates {
		fmt.Printf("  %s %s\n", ui.Label(ui.Pad(c.Name(), 4)), ui.Value(c.Status()))
		for _, l := range c.Logins {
			user := c.Users[l.Name]
			if user == "" {
				user = "?"
			}
			mark := ui.Success("✓")
			if user == "?" {
				mark = ui.Warn("!")
			}
			fmt.Printf("     %s %s %s\n", mark, ui.Value(ui.Pad(l.Name, 24)), ui.Dim("user="+user))
		}
		for _, n := range c.Notes {
			fmt.Printf("       %s %s\n", ui.Warn("note:"), ui.Dim(n))
		}
	}
	fmt.Println()
	fmt.Println(ui.Heading("Licenses"))
	for _, id := range templates.LicenseIDs() {
		l := templates.Licenses[id]
		fmt.Printf("  %s %s\n", ui.Value(ui.Pad(l.ID, 14)), ui.Dim(l.Title))
	}
	fmt.Println()
}

func logf(verbose bool, format string, args ...any) {
	if verbose {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func usage(fs *flag.FlagSet) {
	fmt.Fprintf(os.Stderr, `fth %s — create a repo on Forgejo, Gitea or GitCafe

Usage:
  fth [flags] <name>
  fth [flags] <owner>/<name>
  fth -list

It detects every 'fj' (Forgejo), 'tea' (Gitea) and 'cafe' (GitCafe) login you
have, asks which instance to create on, creates the remote repository,
scaffolds a local git repo with README/.gitignore/LICENSE, adds the remote and
pushes the initial commit.

Flags:
`, version)
	fs.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  fth myproject
  fth -tool fj -host codeberg.org -private -lang go myproject
  fth -tool tea -license MIT -lang node myproject
  fth -tool cafe -cafe-bin ~/.bun/install/global/node_modules/@gitcafe/cli/bin/cafe.js myproject
  fth -no-push -no-scaffold myproject
`)
	_ = time.Now
}
