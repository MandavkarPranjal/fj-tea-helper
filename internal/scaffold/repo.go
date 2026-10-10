package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// LocalState describes what is already present at a target directory. It is
// gathered with Inspect before anything is created, so fth can decide whether
// to scaffold a fresh repository or merely attach a remote to an existing one.
type LocalState struct {
	// Dir is the absolute path that was inspected.
	Dir string
	// Exists reports whether Dir is already present on disk.
	Exists bool
	// IsRepo reports whether Dir is itself the root of a git repository.
	IsRepo bool
	// NestedInRepo reports whether Dir sits inside a repository rooted
	// further up the filesystem. Adding a remote there would silently
	// modify that outer repository, so fth refuses.
	NestedInRepo bool
	// Branch is the current branch name, or the unborn branch when the
	// repository has no commits yet.
	Branch string
	// HasCommits reports whether HEAD resolves to a commit.
	HasCommits bool
	// Remotes lists the configured remote names.
	Remotes []string
}

// RemoteExists reports whether a remote with the given name is configured.
func (s *LocalState) RemoteExists(name string) bool {
	for _, r := range s.Remotes {
		if r == name {
			return true
		}
	}
	return false
}

// Inspect reports the current state of dir without modifying anything.
func Inspect(dir string) (*LocalState, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	st := &LocalState{Dir: abs}

	fi, err := os.Stat(abs)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return st, fmt.Errorf("%s exists and is not a directory", abs)
	}
	st.Exists = true

	if _, err := git(abs, "rev-parse", "--is-inside-work-tree"); err != nil {
		return st, nil // not a repository at all
	}
	top, err := git(abs, "rev-parse", "--show-toplevel")
	if err != nil {
		return st, nil
	}
	if !sameDir(top, abs) {
		// A repository owns this directory but is rooted above it.
		st.NestedInRepo = true
		return st, nil
	}
	st.IsRepo = true

	if b, err := git(abs, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		st.Branch = b
	}
	if _, err := git(abs, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		st.HasCommits = true
	} else if st.Branch == "" {
		st.Branch = "main"
	}
	if out, err := git(abs, "remote"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if name := strings.TrimSpace(line); name != "" {
				st.Remotes = append(st.Remotes, name)
			}
		}
	}
	return st, nil
}

func sameDir(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// Attach wires a remote into an existing repository.
//
// It deliberately writes no starter files, creates no commit and never
// repoints a remote that already exists: the repository belongs to the user
// and fth only adds a destination for it.
func Attach(dir string, opts Options) (*Result, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	res := &Result{
		Dir:        abs,
		Branch:     opts.DefaultBranch,
		RemoteName: opts.RemoteName,
		RemoteURL:  opts.RemoteURL,
		Attached:   true,
	}
	if opts.RemoteURL == "" {
		return res, nil
	}
	if err := setRemote(abs, opts.RemoteName, opts.RemoteURL); err != nil {
		return res, err
	}
	res.RemoteAdded = true
	return res, nil
}

// setRemote adds a remote named name pointing at url.
//
// An existing remote of the same name is never repointed. Silently moving
// where an established remote points is far more likely to be a mistake than
// an intention, so fth asks for a different name instead. An identical URL is
// accepted, which keeps re-running fth idempotent.
func setRemote(dir, name, url string) error {
	if existing, err := git(dir, "remote", "get-url", name); err == nil {
		if strings.TrimSpace(existing) == url {
			return nil // already correct
		}
		return fmt.Errorf("remote %q already points at %s; refusing to repoint it to %s", name, existing, url)
	}
	_, err := git(dir, "remote", "add", name, url)
	return err
}

var remoteNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// CurrentUpstream reports the remote-tracking branch that branch currently
// follows in dir, or "" when it follows none. It lets fth tell the user what
// their upstream actually is after a push, instead of guessing.
func CurrentUpstream(dir, branch string) string {
	if branch == "" {
		branch = "main"
	}
	out, err := git(dir, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ValidRemoteName reports whether name is usable as a git remote name.
func ValidRemoteName(name string) bool {
	return remoteNameRe.MatchString(name)
}

// SuggestRemoteName derives a readable default remote name from the forge
// host, skipping any name already in use.
//
// codeberg.org becomes "codeberg", so the remote says which host it reaches.
// Collisions fall back through forge, upstream and then suffixed variants.
func SuggestRemoteName(host string, taken func(string) bool) string {
	base := "forge"
	if h := strings.TrimSpace(host); h != "" {
		label := h
		if i := strings.IndexAny(label, ".:/"); i > 0 {
			label = label[:i]
		}
		label = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				return r
			case r >= 'A' && r <= 'Z':
				return r + ('a' - 'A')
			default:
				return -1
			}
		}, label)
		if ValidRemoteName(label) && strings.ContainsFunc(label, unicode.IsLetter) {
			base = label
		}
	}
	if taken == nil || !taken(base) {
		return base
	}
	for _, alt := range []string{"forge", "upstream", "mirror"} {
		if !taken(alt) {
			return alt
		}
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
}

// effectiveEntryCount counts the entries in dir, ignoring .git. A directory
// holding nothing but a repository is still an empty directory as far as
// scaffolding is concerned.
func effectiveEntryCount(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		n++
	}
	return n, nil
}
