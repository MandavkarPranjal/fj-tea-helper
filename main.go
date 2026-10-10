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
	"github.com/himmel/fj-tea-helper/internal/prompt"
	"github.com/himmel/fj-tea-helper/internal/scaffold"
	"github.com/himmel/fj-tea-helper/internal/templates"
)

const version = "0.1.0"

type options struct {
	name        string
	tool        string
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
	ssh         bool
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
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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

	fs.StringVar(&o.tool, "tool", "auto", "backend CLI to use: auto, tea, or fj")
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
	fs.BoolVar(&o.ssh, "ssh", false, "use the SSH clone URL for the remote")
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
	explicitRemote := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "remote" {
			explicitRemote = true
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
	tools := []forge.Tool{forge.ToolFj, forge.ToolTea}
	if o.tool != "" && o.tool != "auto" {
		tools = []forge.Tool{forge.Tool(o.tool)}
	}
	candidates, err := forge.Detect(ctx, tools)
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

	selected, err := forge.Select(ctx, candidates, o.tool)
	if err != nil {
		return reportNoBackend(candidates, err)
	}
	if !selected.Verified {
		for _, n := range selected.Notes {
			fmt.Fprintf(os.Stderr, "warning: %s\n", n)
		}
	}

	login, err := forge.ResolveLogin(selected, o.host)
	if err != nil {
		return err
	}

	owner := o.owner
	if owner == "" {
		owner = selected.User(login.Name)
	}
	if owner == "" {
		return fmt.Errorf("could not determine the owner; pass -owner (logins: %s)", strings.Join(selected.LoginNames(), ", "))
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

	// Inspect the destination before planning anything. An existing
	// repository is attached to rather than scaffolded over, and asking for a
	// remote name now keeps every prompt ahead of the first side effect.
	ask := prompt.New(os.Stdin, os.Stderr)
	if o.yes {
		ask = prompt.NonInteractive(os.Stderr)
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
		Tool:        selected.Name(),
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
		SSH:         o.ssh,
		Attach:      attach,
		Upstream:    setUpstream,
	}
	printPlan(plan, selected)

	if o.dryRun {
		fmt.Println("\n(dry run: nothing was created)")
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
		Account:       selected.User(login.Name),
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
		if selected.Name() == string(forge.ToolTea) && (o.lang != "" || licenseID != "") && !o.readme {
			req.Init = true
			req.Gitignore = o.lang
			req.License = licenseID
		}
	}

	logf(o.verbose, "creating %s/%s on %s via %s...\n", owner, name, login.Name, selected.Name())
	repo, err := selected.Backend.CreateRepo(ctx, req)
	if err != nil {
		return fmt.Errorf("create remote repository: %w", err)
	}
	fmt.Printf("✓ created %s on %s (%s)\n", repo.FullName, login.Name, repo.WebURL)

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
		RemoteURL:     pickRemoteURL(repo, o.ssh),
		Force:         o.force,
	}

	var res *scaffold.Result
	if attach {
		logf(o.verbose, "attaching remote %s to %s\n", remoteName, dir)
		res, err = scaffold.Attach(dir, so)
		if err != nil {
			return fmt.Errorf("attach remote: %w", err)
		}
		fmt.Printf("✓ attached to existing repository %s (branch %s)\n", res.Dir, res.Branch)
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
		fmt.Printf("✓ scaffolded %s (branch %s)\n", res.Dir, res.Branch)
		for _, f := range res.FilesCreated {
			fmt.Printf("  · %s\n", f)
		}
	}
	if res.RemoteAdded {
		fmt.Printf("  · remote %s → %s\n", res.RemoteName, res.RemoteURL)
	}

	// 3. Push, unless suppressed.
	if o.noPush {
		fmt.Println("\nskipping push (-no-push)")
		return nil
	}
	logf(o.verbose, "pushing to %s (set-upstream=%v)\n", res.RemoteName, setUpstream)
	if err := scaffold.Push(res.Dir, res.RemoteName, res.Branch, setUpstream); err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if setUpstream {
		fmt.Printf("✓ pushed %s to %s (tracking %s)\n", res.Branch, res.RemoteName, res.RemoteName)
	} else {
		// Report what the upstream actually is, which may well be origin
		// rather than nothing at all.
		up := scaffold.CurrentUpstream(res.Dir, res.Branch)
		if up == "" {
			fmt.Printf("✓ pushed %s to %s (no upstream set)\n", res.Branch, res.RemoteName)
		} else {
			fmt.Printf("✓ pushed %s to %s (upstream %s)\n", res.Branch, res.RemoteName, up)
		}
	}
	return nil
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

func pickRemoteURL(repo *forge.Repo, ssh bool) string {
	if ssh {
		if repo.SSHURL != "" {
			return repo.SSHURL
		}
	}
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
	// Attach reports that an existing repository is being given a new
	// remote rather than scaffolded from scratch.
	Attach bool
	// Upstream reports whether the pushed branch will track the remote.
	Upstream bool
}

func printPlan(p createPlan, c forge.Candidate) {
	vis := "public"
	if p.Private {
		vis = "private"
	}
	fmt.Printf("Plan\n")
	fmt.Printf("  backend   %s\n", p.Tool)
	fmt.Printf("  instance  %s\n", p.Host)
	fmt.Printf("  repository %s/%s (%s)\n", p.Owner, p.Name, vis)
	if p.Description != "" {
		fmt.Printf("  summary   %s\n", p.Description)
	}
	if p.Scaffold {
		if p.Attach {
			fmt.Printf("  local dir %s (existing repo, branch %s)\n", p.Dir, p.Branch)
		} else {
			fmt.Printf("  local dir %s (branch %s)\n", p.Dir, p.Branch)
		}
		fmt.Printf("  remote    %s\n", p.Remote)
		push := "yes"
		if !p.Push {
			push = "no"
		}
		fmt.Printf("  push      %s\n", push)
		if p.Push && p.Attach {
			up := "no (origin stays upstream)"
			if p.Upstream {
				up = p.Remote
			}
			fmt.Printf("  upstream  %s\n", up)
		}
	}
	fmt.Println()
}

func printList(candidates []forge.Candidate) {
	fmt.Println("Backends")
	for _, c := range candidates {
		fmt.Printf("  %-4s %s\n", c.Name(), c.Status())
		for _, l := range c.Logins {
			user := c.Users[l.Name]
			if user == "" {
				user = "?"
			}
			mark := " "
			if user == "?" {
				mark = "!"
			}
			fmt.Printf("       %s %-24s user=%s\n", mark, l.Name, user)
		}
		for _, n := range c.Notes {
			fmt.Printf("       note: %s\n", n)
		}
	}
	fmt.Println()
	fmt.Println("Licenses")
	for _, id := range templates.LicenseIDs() {
		l := templates.Licenses[id]
		fmt.Printf("  %-14s %s\n", l.ID, l.Title)
	}
	fmt.Println()
}

func logf(verbose bool, format string, args ...any) {
	if verbose {
		fmt.Fprintf(os.Stderr, format, args...)
	}
}

func usage(fs *flag.FlagSet) {
	fmt.Fprintf(os.Stderr, `fth %s — create a Forgejo/Gitea repo via fj or tea

Usage:
  fth [flags] <name>
  fth [flags] <owner>/<name>
  fth -list

It detects whether 'fj' (Forgejo) or 'tea' (Gitea) is authenticated, creates
the remote repository, scaffolds a local git repo with README/.gitignore/
LICENSE, adds the remote and pushes the initial commit.

Flags:
`, version)
	fs.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  fth myproject
  fth -tool fj -host codeberg.org -private -lang go myproject
  fth -tool tea -license MIT -lang node myproject
  fth -no-push -no-scaffold myproject
`)
	_ = time.Now
}
