package scaffold

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Options describes the local repository to create.
type Options struct {
	// Dir is the directory to create (created if missing).
	Dir string
	// Name is the repository name, used in the README title.
	Name string
	// Description is an optional subtitle.
	Description string
	// LicenseSPDX selects an embedded license to write.
	LicenseSPDX string
	// LicenseHolder is the copyright holder.
	LicenseHolder string
	// CopyrightYear overrides the year used in the license.
	CopyrightYear int
	// Language seeds a .gitignore (see templates.Gitignore).
	Language string
	// DefaultBranch is the initial branch name.
	DefaultBranch string
	// WriteReadme controls whether a README.md is generated.
	WriteReadme bool
	// RemoteName is the git remote to add.
	RemoteName string
	// RemoteURL is the clone URL for that remote.
	RemoteURL string
	// Force allows writing into a directory that is not empty.
	Force bool
}

// Result records what was created.
type Result struct {
	Dir          string
	Branch       string
	RemoteName   string
	RemoteURL    string
	FilesCreated []string
	Initialised  bool
	RemoteAdded  bool
	Committed    bool
	// Attached reports that fth only added a remote to an existing
	// repository instead of scaffolding a new one.
	Attached bool
}

// git runs a git command inside dir and returns trimmed output.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var sb strings.Builder
	cmd.Stdout = &sb
	cmd.Stderr = &sb
	cmd.Stdin = nil
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(sb.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return msg, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(sb.String()), nil
}

func gitExists() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Precheck validates that dir can be scaffolded, without modifying anything.
// It is run before the remote repository is created so that a bad local
// destination fails fast instead of leaving an orphaned remote behind.
//
// An existing git repository is deliberately allowed through: fth inspects it
// with Inspect and then attaches a remote instead of scaffolding over it.
// Nesting inside a repository rooted further up is refused, because adding a
// remote there would modify that outer repository.
func Precheck(dir string, force bool) error {
	if !gitExists() {
		return fmt.Errorf("git is not installed on PATH")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if os.IsNotExist(err) {
		return nil // will be created
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s exists and is not a directory", abs)
	}
	state, err := Inspect(abs)
	if err != nil {
		return err
	}
	if state.NestedInRepo {
		return fmt.Errorf("%s is inside a git repository rooted elsewhere; run fth from the repository itself or use -dir", abs)
	}
	if state.IsRepo && state.HasCommits {
		return nil // fth will attach a remote rather than scaffold
	}
	if !force {
		if n, err := effectiveEntryCount(abs); err == nil && n > 0 {
			return fmt.Errorf("%s is not empty (use -force to scaffold anyway)", abs)
		}
	}
	return nil
}

// Create performs the local scaffold: it makes the directory, writes the
// starter files, initialises git, makes the first commit and wires the remote.
func Create(opts Options, renderLicense func(spdx string, year int, holder string) (string, error), renderGitignore func(language string) string) (*Result, error) {
	abs, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}

	// Validate before touching the filesystem, and again after creating the
	// directory in case it appeared in the meantime.
	if err := Precheck(abs, opts.Force); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", abs, err)
	}
	if n, err := effectiveEntryCount(abs); err == nil && n > 0 && !opts.Force {
		return nil, fmt.Errorf("%s is not empty (use -force to scaffold anyway)", abs)
	}

	res := &Result{Dir: abs, Branch: opts.DefaultBranch, RemoteName: opts.RemoteName, RemoteURL: opts.RemoteURL}

	// Initialise git first so we can bail out early on a bad state.
	if _, err := git(abs, "init", "--quiet"); err != nil {
		return nil, err
	}
	res.Initialised = true

	branch := opts.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	if _, err := git(abs, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		// Older git without symbolic-ref support: fall back to checkout.
		_, _ = git(abs, "checkout", "-B", branch)
	}
	res.Branch = branch

	files, err := writeStarterFiles(abs, opts, renderLicense, renderGitignore)
	if err != nil {
		return res, err
	}
	res.FilesCreated = files

	if _, err := git(abs, "add", "-A"); err != nil {
		return res, err
	}
	if _, err := git(abs, "commit", "--quiet", "-m", "Initial commit"); err != nil {
		// An empty commit is not fatal; the user may have --force'd past files.
		if !strings.Contains(err.Error(), "nothing to commit") &&
			!strings.Contains(err.Error(), "empty initial commit") {
			return res, err
		}
	} else {
		res.Committed = true
	}

	if opts.RemoteURL != "" {
		if err := setRemote(abs, opts.RemoteName, opts.RemoteURL); err != nil {
			return res, err
		}
		res.RemoteAdded = true
	}
	return res, nil
}

// writeStarterFiles creates README/.gitignore/LICENSE and returns the list of
// relative paths written.
func writeStarterFiles(dir string, opts Options,
	renderLicense func(string, int, string) (string, error),
	renderGitignore func(string) string) ([]string, error) {

	var written []string

	if opts.WriteReadme && (opts.Description != "" || opts.Name != "") {
		var b strings.Builder
		b.WriteString("# " + opts.Name + "\n\n")
		if opts.Description != "" {
			b.WriteString(opts.Description + "\n")
		}
		path := filepath.Join(dir, "README.md")
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			return written, err
		}
		written = append(written, "README.md")
	}

	if gi := renderGitignore(opts.Language); gi != "" {
		path := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(path, []byte(gi), 0o644); err != nil {
			return written, err
		}
		written = append(written, ".gitignore")
	}

	if opts.LicenseSPDX != "" {
		text, err := renderLicense(opts.LicenseSPDX, opts.CopyrightYear, opts.LicenseHolder)
		if err != nil {
			return written, err
		}
		path := filepath.Join(dir, "LICENSE")
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return written, err
		}
		written = append(written, "LICENSE")
	}
	return written, nil
}

// Push pushes branch to remote.
//
// setUpstream decides whether git records branch as tracking remote. Callers
// pass false when the repository already has an upstream they want left
// alone, so pushing somewhere new cannot silently move it.
func Push(dir, remote, branch string, setUpstream bool) error {
	if remote == "" {
		remote = "origin"
	}
	if branch == "" {
		branch = "main"
	}
	args := []string{"push"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, remote, branch)
	_, err := git(dir, args...)
	return err
}
