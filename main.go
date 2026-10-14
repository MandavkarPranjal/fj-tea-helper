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
	"path/filepath"
	"strings"
	"time"

	"github.com/himmel/fj-tea-helper/internal/browser"
	"github.com/himmel/fj-tea-helper/internal/config"
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
	tryBin      string
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
	noCache     bool
	// open and noOpen override the config file's "open" setting for one run.
	// Neither is defaulted, because a false default is what "not set" means
	// here; explicitness is decided by fs.Visit.
	open   bool
	noOpen bool
}

func main() {
	err := run(os.Args[1:])
	forge.Cleanup()
	if err == nil {
		return
	}
	// Ctrl+C is a decision, not a failure: say so plainly and leave without a
	// stack of red text. Exit status 130 is what a shell expects from a
	// SIGINT-terminated command.
	if errors.Is(err, prompt.ErrAborted) {
		fmt.Fprintln(os.Stderr, ui.Warnf("cancelled; nothing was created"))
		os.Exit(130)
	}
	fmt.Fprintln(os.Stderr, ui.Errorf("%v", err))
	os.Exit(1)
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

	fs.StringVar(&o.tool, "tool", "auto", "backend CLI to use: auto, tea, fj, cafe, gh, or try")
	fs.StringVar(&o.cafeBin, "cafe-bin", "", "path to the cafe binary, if it is not on PATH")
	fs.StringVar(&o.tryBin, "try-bin", "", "path to the Trylle binary (trylle-cli or try), if it is not on PATH")
	fs.StringVar(&o.host, "host", "", "instance host to create on (e.g. gitea.com)")
	fs.StringVar(&o.owner, "owner", "", "repository owner (default: your account)")
	fs.StringVar(&o.description, "description", "", "short repository description")
	fs.StringVar(&o.visibility, "visibility", "public", "visibility: public or private")
	fs.StringVar(&o.license, "license", "none", "license SPDX id (see -list), or 'none'")
	fs.StringVar(&o.licenseFile, "license-file", "", "path to a license file to copy instead of an SPDX one")
	fs.StringVar(&o.lang, "lang", "", "seed a .gitignore for: go, node, python, rust")
	fs.StringVar(&o.branch, "branch", "main", "default branch name")
	fs.StringVar(&o.remote, "remote", "origin", "git remote name")
	fs.StringVar(&o.dir, "dir", "", "local directory (default: . with no name, otherwise ./<name>)")
	fs.StringVar(&o.trustModel, "trust-model", "", "Gitea trust model (tea only; ignored by other backends)")
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
	fs.BoolVar(&o.noCache, "no-cache", false, "always probe every CLI, ignoring the recent-results cache")
	fs.BoolVar(&o.open, "open", false, "open the new repository in a browser when the run finishes")
	fs.BoolVar(&o.noOpen, "no-open", false, "do not open a browser, whatever the config file says")

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
	explicitRemote, explicitURL, explicitOpen := false, false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "remote":
			explicitRemote = true
		case "url":
			explicitURL = true
		case "open", "no-open":
			explicitOpen = true
		}
	})

	if o.showVersion {
		fmt.Printf("fth %s\n", version)
		return nil
	}

	// -open and -no-open are two spellings of one decision, so passing both is
	// a contradiction rather than a last-one-wins.
	if o.open && o.noOpen {
		return fmt.Errorf("-open and -no-open cannot both be given")
	}

	rest := fs.Args()

	// `fth config <setting>` is about fth itself rather than about a
	// repository, so it is answered before the positional is read as a name.
	// One bare "config" is left alone: it is a perfectly good repository name,
	// and a second word is what makes the subcommand unambiguous.
	if len(rest) >= 2 && rest[0] == "config" {
		return runConfig(rest[1:], o.dryRun)
	}

	// The config file is read before anything slow happens, so a typo in it is
	// reported long before the backend probing is over. A file that cannot be
	// understood is a warning rather than a failure: the run is about a
	// repository, and refusing to create one over an unreadable preference would
	// be the tail wagging the dog. The default is the safe side of the switch
	// anyway, and the created repository's URL is printed either way.
	shouldOpen := false
	if !o.list {
		cfg, cfgErr := config.Load()
		if cfgErr != nil && !errors.Is(cfgErr, config.ErrNoHome) {
			fmt.Fprintln(os.Stderr, ui.Warnf("%v; using the defaults for this run", cfgErr))
			cfg = config.Default()
		}
		// A flag is a statement about this run and outranks a stored
		// preference; with neither, the config file decides.
		shouldOpen = cfg.Open
		if explicitOpen {
			shouldOpen = o.open
		}
	}
	if p, err := config.Path(); err == nil && !o.list {
		logf(o.verbose, "config %s\n", p)
	}

	// A bare `fth`, an explicit `fth .`, and a name that is exactly the
	// current directory's own name all mean the same thing: publish where I am
	// standing. Run from ~/project/demo each of them asks for <owner>/demo in
	// that same directory. The third case matters because `fth demo` inside
	// ~/project/demo would otherwise quietly build a second repository inside
	// the first, which is never what someone typing that means.
	inPlace := !o.list && namesLocalDir(rest, o.dir)
	if inPlace {
		rest = nil
	}

	switch {
	case o.list:
		// -list does not need a name
	case inPlace || len(rest) == 0:
		// No name was typed, so it comes from the directory the repository
		// will live in.
		src := o.dir
		if src == "" {
			src = "."
		}
		name, err := repoNameForDir(src)
		if err != nil {
			return err
		}
		o.name, o.dir = name, src
	case len(rest) == 1:
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

	ctx := context.Background()

	// Probing costs a TLS handshake per instance, so a recent result is reused
	// unless the user asked for a live look. -no-cache also refreshes the stored
	// result, so it is the way to recover from a credential that was just fixed.
	if o.noCache {
		forge.DisableCache()
	}

	// Detection is needed for -list and for the real run. When the backend is
	// named explicitly there is no need to probe the other CLI.
	tools := forge.AllTools()
	toolName := o.tool
	// Normalise before deciding whether to narrow. Comparing the raw flag
	// would let `-tool AUTO` or `-tool " auto "` fall through as if a backend
	// had been named, and the literal "auto" would then reach Discover as an
	// unknown tool instead of probing every backend.
	if t, err := forge.ParseTool(o.tool); err != nil {
		return err
	} else if t != "" && t != "auto" {
		toolName, tools = string(t), []forge.Tool{t}
	}
	// Probing logins means a handful of network round-trips, and the backends
	// finish at wildly different speeds: fj and tea are resolved in tens of
	// milliseconds while cafe can spend over a second in a TLS handshake. A
	// spinner would hold all of that back until the slowest one lands, so each
	// backend is drawn the moment it is known instead.
	det := progress.NewDetect(os.Stderr, toolLabels(tools))
	det.Start()
	candidates, err := collectDetect(ctx, tools, det,
		forge.WithCafeBin(o.cafeBin), forge.WithTryBin(o.tryBin))
	det.Stop()
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

	targets, err := forge.Filter(candidates, forge.Targets(candidates), toolName, o.host)
	if err != nil {
		return reportNoBackend(candidates, err)
	}
	if len(targets) == 0 {
		return reportNoBackend(candidates, forge.NoTargetsError(candidates))
	}
	target, err := pickTarget(ask, targets)
	if err != nil {
		return err
	}

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
	scheme, previewURL, err := chooseScheme(ask, o.url, explicitURL, target.Backend, login, owner, name, !o.noScaffold)
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
			remoteName, err = askForRemoteName(ask, local, login.Name, remoteName)
			if err != nil {
				return err
			}
			// The repository already has an origin and we are not taking it
			// over, so tracking stays with origin unless the user says
			// otherwise.
			setUpstream = false
			if remoteName != "origin" {
				setUpstream, err = ask.YesNo(
					fmt.Sprintf("Set %q as upstream for branch %s?", remoteName, localBranch), false)
				if err != nil {
					return err
				}
			}
		}
	}

	// The inspected state carries the absolute path, so the plan can name the
	// directory in full. A bare `fth` works in "." and saying so is no help
	// once the user is looking at a dozen of them.
	planDir := dir
	if local != nil {
		planDir = local.Dir
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
		Dir:         planDir,
		Remote:      remoteName,
		Push:        !o.noPush,
		Scaffold:    !o.noScaffold,
		SSH:         scheme == "ssh",
		RemoteURL:   previewURL,
		Attach:      attach,
		Upstream:    setUpstream,
		Open:        shouldOpen,
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
	spin := progress.New(os.Stderr)
	spin.Start("creating " + owner + "/" + name + " on " + login.Name)
	repo, err := target.Backend.CreateRepo(ctx, req)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("create remote repository: %w", err)
	}
	fmt.Println(ui.Done(fmt.Sprintf("created %s on %s (%s)", repo.FullName, login.Name, ui.Dim(repo.WebURL))))

	// The page is opened by every path that leaves with a repository created,
	// which is why it is a closure over the resolved intent rather than a flag
	// on the options.
	showRepo := func() {
		if shouldOpen {
			openInBrowser(repo.WebURL)
		}
	}

	if o.noScaffold {
		showRepo()
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
		showRepo()
		return nil
	}
	logf(o.verbose, "pushing to %s (set-upstream=%v)\n", res.RemoteName, setUpstream)
	spin.Start("pushing " + res.Branch + " to " + res.RemoteName)
	// Trylle and friends always leave a placeholder commit on a new
	// repository, so the plain push is rejected even for a brand new repo.
	// PushReconcilingSeed recognises that case and replaces the placeholder,
	// while still refusing to overwrite a branch that holds real history.
	var replacedSeed bool
	if forge.SeedsFor(target.Backend) {
		replacedSeed, err = scaffold.PushReconcilingSeed(res.Dir, res.RemoteName, res.Branch, setUpstream)
	} else {
		err = scaffold.Push(res.Dir, res.RemoteName, res.Branch, setUpstream)
	}
	spin.Stop()
	if err != nil {
		return fmt.Errorf("push: %w", err)
	}
	if replacedSeed {
		fmt.Println(ui.Dot("replaced the placeholder commit " + string(target.Backend.Tool()) + " created"))
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
	// Only a run that reached the end: if the scaffolding or the push failed
	// above, the repository exists but is not worth looking at yet, and opening
	// an empty page is a worse answer than opening nothing.
	showRepo()
	return nil
}

// openInBrowser hands the new repository's page to the desktop.
//
// A failure here is a nuisance rather than a failed run: everything the user
// came for is already done and the URL has been printed, so it is reported on
// stderr and swallowed rather than turned into an error over an exit status.
func openInBrowser(url string) {
	if url == "" {
		return
	}
	if err := browser.Open(url); err != nil {
		fmt.Fprintln(os.Stderr, ui.Warnf("%v", err))
		return
	}
	fmt.Println(ui.Done("opened " + url))
}

// configUsage lists the config subcommands, in one place so the error message
// and -help cannot drift apart.
var configUsage = []string{"show", "path", "open", "no-open"}

// runConfig handles `fth config <subcommand>`.
//
// Settings are read fresh rather than from the copy the create path already
// holds: here a broken file is the whole question the user came to ask, so it
// is an error instead of the warning it is over there.
func runConfig(args []string, dryRun bool) error {
	sub := args[0]
	rest := args[1:]
	if len(rest) > 0 {
		return fmt.Errorf("config %s takes no arguments (got %q)", sub, strings.Join(rest, " "))
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	path, err := config.Path()
	if err != nil {
		return err
	}

	switch sub {
	case "show":
		fmt.Println(path)
		fmt.Printf("open = %t\n", cfg.Open)
		return nil
	case "path":
		fmt.Println(path)
		return nil
	case "open", "no-open":
		want := sub == "open"
		if cfg.Open == want {
			fmt.Println(ui.Dim(fmt.Sprintf("open = %t already", want)))
			return nil
		}
		if dryRun {
			fmt.Println(ui.Dim(fmt.Sprintf("(dry run: %s would be set to %t)", path, want)))
			return nil
		}
		cfg.Open = want
		if err := config.Save(cfg); err != nil {
			return err
		}
		fmt.Println(ui.Done(fmt.Sprintf("open = %t (%s)", want, path)))
		return nil
	default:
		return fmt.Errorf("unknown config command %q (want %s)", sub, strings.Join(configUsage, ", "))
	}
}

// namesLocalDir reports whether the positional arguments ask for the directory
// fth runs from rather than for a new repository beside it: no name at all, an
// explicit ".", or a name that is precisely the current directory's own name.
//
// An explicit -dir wins over anything typed, since it is the user's statement
// about where the repository goes. So does owner/name, which names an owner and
// so can never be shorthand for a location. Anything else stays a repository
// name: `fth other` in ~/demo is a new ./other, as it has always been.
func namesLocalDir(args []string, dirFlag string) bool {
	if strings.TrimSpace(dirFlag) != "" {
		return false
	}
	switch len(args) {
	case 0:
		return true
	case 1:
	default:
		return false // fth reports surplus arguments itself
	}
	arg := strings.TrimSpace(args[0])
	if arg == "" {
		return false
	}
	// "." and anything that cleans to it names the current directory. This
	// comes before the separator check, since spellings like "./." clean to
	// "." while still containing one.
	if filepath.Clean(arg) == "." {
		return true
	}
	// Any other path is a name with a separator in it, not a location.
	if strings.Contains(arg, "/") {
		return false
	}
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	return arg == filepath.Base(wd)
}

// repoNameForDir derives a repository name from a local directory: its last
// path element, which is what a bare `fth` means by "this one". Run from
// ~/project/demo it therefore asks for <owner>/demo.
//
// The directory named is the one that will hold the repository, so an explicit
// -dir is what gets named — `fth -dir ~/project/other` publishes "other"
// rather than whichever directory fth happened to be invoked from.
//
// The name must still describe that directory. SanitizeName trims, so a
// basename with padding would derive a name that no longer matches it: -dir
// " demo " would publish "demo" inside a directory called " demo ", and -dir " "
// would fall back to the enclosing directory's name for a directory called " ".
// Silently naming a repository after a directory other than the one holding it
// is worse than refusing, so any basename that trimming would change is
// rejected along with the obvious ones (a filesystem root, or a name with a
// space in it) — all with the same remedy, which is to pass a name.
func repoNameForDir(dir string) (string, error) {
	// Only a genuinely empty -dir means the working directory. A whitespace
	// -dir is a directory whose name is whitespace, and trimming it here would
	// quietly aim the repository somewhere else entirely.
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	if trimmed := strings.TrimSpace(base); trimmed != base {
		return "", fmt.Errorf("cannot derive a repository name from %s (%q has leading or trailing whitespace); rename the directory or pass a name", abs, base)
	}
	name, err := forge.SanitizeName(base)
	if err != nil {
		return "", fmt.Errorf("cannot derive a repository name from %s (%v); pass a name", abs, err)
	}
	return name, nil
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
func pickTarget(ask *prompt.Prompter, targets []forge.Target) (forge.Target, error) {
	if len(targets) == 1 || !ask.Interactive() {
		return targets[0], nil
	}
	opts := make([]prompt.Option, 0, len(targets))
	for _, t := range targets {
		opts = append(opts, prompt.Option{
			Label:  fmt.Sprintf("%s  %s", ui.Pad(string(t.Tool()), 4), t.Login.Name),
			Value:  t.ID(),
			Detail: t.Account,
		})
	}
	got, err := ask.Select("Create the repository on", opts, targets[0].ID())
	if err != nil {
		return forge.Target{}, err
	}
	for _, t := range targets {
		if t.ID() == got {
			return t, nil
		}
	}
	return targets[0], nil
}

// toolLabels names the backends being probed, in the order they will be drawn.
func toolLabels(tools []forge.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = string(t)
	}
	return out
}

// collectDetect drains the detection stream, settling each row of the live display
// as its result arrives, and returns the candidates in the original preference
// order.
//
// The result's Index — not its arrival order — is what places it, so a backend
// that answers late updates its own row and leaves the order alone.
func collectDetect(ctx context.Context, tools []forge.Tool, det *progress.Detect,
	opts ...forge.Option) ([]forge.Candidate, error) {
	results, err := forge.DetectStream(ctx, tools, opts...)
	if err != nil {
		return nil, err
	}
	out := make([]forge.Candidate, len(tools))
	for r := range results {
		out[r.Index] = r.Candidate
		det.Settle(r.Index, detectDetail(r.Candidate), !r.Candidate.Verified)
	}
	return out, nil
}

// detectDetail summarises a resolved backend for one row of the display.
//
// It reuses Status rather than reformatting the candidate, so what the live
// display showed and what -list prints cannot drift apart. It reads only what
// probe recorded, so it costs no process.
func detectDetail(c forge.Candidate) string { return c.Status() }

// askForRemoteName prompts for a name for the new remote, offering one
// derived from the forge host and skipping names already in use. Pressing
// Enter takes that suggestion.
func askForRemoteName(ask *prompt.Prompter, local *scaffold.LocalState, host, taken string) (string, error) {
	suggest := scaffold.SuggestRemoteName(host, local.RemoteExists)
	label := fmt.Sprintf("remote %q already exists in %s. Name for the new remote?", taken, local.Dir)
	for {
		answer, err := ask.Line(label, suggest)
		if err != nil {
			return "", err
		}
		if scaffold.ValidRemoteName(answer) && !local.RemoteExists(answer) {
			return answer, nil
		}
		// Without a terminal there is nobody to re-ask, so stop and use the
		// suggestion rather than spinning.
		if !ask.Interactive() {
			return suggest, nil
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
func chooseScheme(ask *prompt.Prompter, flagValue string, explicit bool, backend forge.Backend, login forge.Login, owner, name string, relevant bool) (scheme, remoteURL string, err error) {
	want := strings.ToLower(strings.TrimSpace(flagValue))
	if want == "" {
		want = "ssh"
	}
	if want != "ssh" && want != "https" {
		return "", "", fmt.Errorf("unknown -url %q (want ssh or https)", flagValue)
	}
	sshURL, httpsURL := forge.PreviewFor(backend, login, owner, name)
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

	got, err := ask.Select("Remote URL", []prompt.Option{
		{Label: "ssh", Value: "ssh", Detail: sshURL},
		{Label: "https", Value: "https", Detail: httpsURL},
	}, want)
	if err != nil {
		return "", "", err
	}
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
	// Open reports that the new repository's page will be handed to a browser
	// once the run is done. It is shown because it is the one thing the run
	// does outside the terminal, and -dry-run is how a user sees it coming.
	Open bool
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
	// Only ever shown when it is on: a plan full of things that are not going to
	// happen is noise, and a line reading "browser no" invites the question of
	// what the alternative was.
	if p.Open {
		fmt.Println(ui.Field("browser", ui.Value("yes")+ui.Dim(" (open when done)")))
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
	fmt.Fprintf(os.Stderr, `fth %s — create a repo on Forgejo, Gitea, GitCafe, GitHub or Trylle

Usage:
  fth [flags] <name>
  fth [flags] <owner>/<name>
  fth [flags]
  fth [flags] .
  fth -list
  fth config <show|path|open|no-open>

With no name, with ".", or with the name of the directory you are standing in,
the repository is published where you stand: in ~/project/demo it creates
<owner>/demo in that same directory. Any other name scaffolds a new ./<name>
beside you instead.

It detects every 'fj' (Forgejo), 'tea' (Gitea), 'cafe' (GitCafe), 'gh' (GitHub)
and 'try' (Trylle) login you have, asks which instance to create on, creates the
remote repository, scaffolds a local git repo with README/.gitignore/LICENSE,
adds the remote and pushes the initial commit.

Settings live in ~/.config/fth/config.json (or $XDG_CONFIG_HOME/fth/config.json)
and are all off by default; 'fth config open' turns one on. -open and -no-open
override the stored value for a single run.

Flags:
`, version)
	fs.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Examples:
  fth myproject
  cd ~/project/demo && fth
  cd ~/project/demo && fth demo
  fth -dir ~/project/other
  fth -tool fj -host codeberg.org -visibility private -lang go myproject
  fth -tool tea -license MIT -lang node myproject
  fth -tool cafe -cafe-bin ~/.bun/install/global/node_modules/@gitcafe/cli/bin/cafe.js myproject
  fth -tool gh -host github.com -license MIT myproject
  fth -tool gh -host ghe.example.com -visibility private myproject
  fth -tool try -license MIT -lang go myproject
  fth -no-push -no-scaffold myproject
  fth config open          # open each new repository in a browser
  fth -no-open myproject   # this run only
`)
	_ = time.Now
}
