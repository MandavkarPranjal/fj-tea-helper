# fth

**fth** (*fj-tea-helper*) creates a new repository on Forgejo or Gitea in one
command, using the `fj` (Forgejo) or `tea` (Gitea) CLI as the engine.

It detects which CLI you're authenticated against, creates the remote
repository, scaffolds a local git repository with `README.md`, `.gitignore` and
`LICENSE`, wires up the remote and pushes the initial commit.

```console
$ fth myproject -lang go -license MIT
Plan
  backend   fj
  instance  codeberg.org
  repository you/myproject (public)
  local dir myproject (branch main)
  remote    origin
  push      yes

✓ created you/myproject on codeberg.org (https://codeberg.org/you/myproject)
✓ scaffolded /home/you/myproject (branch main)
  · README.md
  · .gitignore
  · LICENSE
  · remote origin → https://codeberg.org/you/myproject.git
✓ pushed main to origin
```

## Install

This project uses [mise](https://mise.jdx.dev) for the toolchain and tasks:

```sh
mise run build          # produces ./fth
mise run install        # installs into ~/.local/bin
mise run check          # fmtcheck + lint + vet + tests
mise run run -- -list   # build, then run the CLI
mise tasks              # list all tasks
```

`mise.toml` pins Go 1.27 and golangci-lint, so no manual toolchain setup is
needed. At runtime the binary shells out to `fj` and/or `tea`, whichever you
have authenticated. `git` must be on `PATH`.

## Usage

```
fth [flags] <name>
fth [flags] <owner>/<name>
fth -list
```

Flags may appear before or after the name, so `fth myproject -private` and
`fth -private myproject` are equivalent.

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-tool` | `auto` | Backend CLI: `auto`, `tea`, or `fj` |
| `-host` | detected | Instance host, e.g. `gitea.com` |
| `-owner` | your account | Repository owner or organisation |
| `-description` | – | Short repository description |
| `-visibility` | `public` | `public` or `private` |
| `-license` | `none` | SPDX id: `MIT`, `Apache-2.0`, `BSD-3-Clause`, `GPL-3.0-only`, `Unlicense` |
| `-license-file` | – | Use a license file from disk instead of an SPDX id |
| `-lang` | – | Seed a `.gitignore`: `go`, `node`, `python`, `rust` |
| `-branch` | `main` | Default branch name |
| `-remote` | `origin` | Git remote name |
| `-dir` | `./<name>` | Local directory to scaffold |
| `-readme` | `true` | Write a `README.md` |
| `-url` | `ssh` | Clone URL to wire up: `ssh` or `https` |
| `-trust-model` | – | Gitea trust model (`tea` only) |
| `-template` | `false` | Mark as a template repository (`tea` only) |
| `-no-push` | `false` | Create the remote but don't push |
| `-no-scaffold` | `false` | Create only the remote repository |
| `-force` | `false` | Scaffold even if the local directory isn't empty |
| `-yes` | `false` | Accept the default answer for every prompt |
| `-holder` | your account | Copyright holder for the license |
| `-year` | this year | Copyright year |
| `-dry-run` | `false` | Print the plan and exit without changing anything |
| `-list` | – | Show detected backends, logins and licenses |
| `-verbose` | `false` | Log progress |
| `-version` | – | Print the helper version |

### Examples

```sh
# Public Go project on whatever you're logged into
fth myservice -lang go -license MIT

# Private repo on a specific Forgejo instance
fth -tool fj -host codeberg.org -visibility private myservice

# Force Gitea, an org-owned repo, Apache-2.0, no local scaffold
fth -tool tea -owner myorg -license Apache-2.0 -no-scaffold myservice

# Create the repo and local dir, but decide about pushing later
fth -no-push myservice

# Publish a git repo you already have (see below)
cd ~/code/myservice && fth -dir . -tool fj myservice

# See what would happen
fth myservice -dry-run
```

## Publishing an existing git repository

If the target directory is already a git repository with at least one commit,
`fth` stops scaffolding and switches to **attach** mode:

```console
$ cd ~/code/myservice
$ fth -dir . myservice
remote "origin" already exists in /home/you/code/myservice. Name for the new remote? [codeberg]:
Set "codeberg" as upstream for branch main? [y/N]:

Plan
  backend   fj
  instance  codeberg.org
  repository you/myservice (public)
  local dir /home/you/code/myservice (existing repo, branch main)
  remote    codeberg
  push      yes
  upstream  no (origin stays upstream)

✓ created you/myservice on codeberg.org (https://codeberg.org/you/myservice)
✓ attached to existing repository /home/you/code/myservice (branch main)
  · remote codeberg → https://codeberg.org/you/myservice.git
✓ pushed main to codeberg (upstream origin/main)
```

In this mode:

- **No files are written.** Your working tree, index and history are left
  exactly as they are — no `README.md`, `.gitignore` or `LICENSE` is generated,
  and no commit is created.
- **An existing `origin` is never repointed.** The new remote is added under a
  name you choose, and `origin` keeps its original URL.
- **The suggested name comes from the instance host** — `codeberg.org` suggests
  `codeberg`, `git.example.com` suggests `git`. If that's taken, it falls back to
  `forge`, `upstream`, `mirror`, then `codeberg2`, `codeberg3`, and so on.
- **Upstream defaults to unchanged.** Answering `N` pushes without
  `--set-upstream`, so the branch keeps tracking `origin`. Answering `Y` makes
  the new remote the branch's upstream.
- **Adding a remote that already points elsewhere is refused**, never silently
  rewritten. Re-running with the same URL is a no-op, so `fth` is safe to retry.
- A **subdirectory of another repository is refused**, because adding a remote
  there would silently modify the outer repository.

Prompts go to stderr and read defaults from stdin, so `fth` still works in
scripts — it just uses the defaults when there's no terminal. Pass `-yes` to
skip the non-terminal notice entirely.

## Choosing the remote URL

**SSH is the default**, because it needs no credential-helper entry and does
not care about token expiry. You're still asked, and shown the concrete URL
each choice would produce:

```console
$ fth myservice
Remote URL
  > ssh    git@codeberg.org:you/myservice.git
    https  https://codeberg.org/you/myservice.git
  (up/down to move, enter to choose)
Remote URL: ssh
```

Use `↑`/`↓` to move, or press the first letter of a choice (`s` for ssh, `h`
for https). Pass `-url ssh` or `-url https` to skip the question entirely.

## Prompts

Every question is a small [Bubble Tea](https://github.com/charmbracelet/bubbletea)
program: an Elm-architecture loop that takes the terminal only while the question
is on screen, then hands it straight back. That means progress output scrolls
normally instead of being wiped by a full-screen TUI, and the terminal is
restored cleanly even if the question is cancelled.

- **Text questions** show a suggested value in brackets. Press `Enter` to accept
  it; start typing to replace it entirely.
- **Choice questions** highlight the current answer with `>`, accept the first
  letter of any choice as a shortcut, and default to the highlighted one.
- `Esc` or `Ctrl-C` at a prompt keeps the default rather than aborting, so a
  stray keystroke can't leave a half-created repository behind.

Questions are styled too: the question is bold, the highlighted answer is
accent-coloured along with its cursor, option details are faint, and the
confirmed answer is echoed in green once you pick it.

## Progress

The steps that talk to a network show an indeterminate spinner on the line
above the plan or the result:

```console
⠋ detecting backend
⠋ creating you/myservice on codeberg.org
✓ created you/myservice on codeberg.org (https://codeberg.org/you/myservice)
⠋ pushing main to origin
✓ pushed main to origin (tracking origin)
```

There is deliberately **no progress bar**. Git cannot report a meaningful
percentage for a push, and the other steps finish in well under a second, so a
bar would either lie or flash past. A spinner only claims what it knows: the
work is still going.

The spinner draws nothing when output is not a terminal, so piping `fth`
produces exactly the plain text it always did.

## Colour

Output is styled with [lipgloss](https://github.com/charmbracelet/lipgloss),
which detects the terminal once at start-up:

- **Bold** headings (`Plan`, `Backends`, `Licenses`)
- **Faint** field labels, URLs and parenthetical detail, so the eye lands on the
  values
- **Green** check marks for completed steps and confirmed answers
- **Yellow** warnings, **red** errors

The styling is emitted only when the output is a capable terminal. Piping `fth`
into a file or a pager produces plain text, as does `NO_COLOR=1 fth …`.

Prompts detect the profile against the stream they are actually written to
(stderr), not stdout, so questions stay coloured even when fth's progress output
is being piped somewhere else.

Field labels are padded to a common width so values line up in a column
regardless of how long the label is:

```console
$ fth myservice -dry-run
Plan
  backend    fj
  instance   codeberg.org
  repository you/myservice (public)
  local dir  myservice (branch main)
  remote     origin
  remote url git@codeberg.org:you/myservice.git
  push       yes
```

## Backend detection

`-tool auto` probes both CLIs and prefers `fj`, falling back to `tea`:

1. Run `fj auth list` / `tea logins list -o json` to find configured logins.
2. Confirm a login actually works — for `fj` this means `fj whoami`, because
   `fj auth list` happily reports instances whose stored OAuth grant has been
   revoked.
3. Prefer `fj` if it authenticates, otherwise `tea`. If both work, `fj` wins.

Use `-list` to see exactly what was detected:

```console
$ fth -list
Backends
  fj   installed, no working login
       ! codeberg.org             user=?
       note: login "codeberg.org" unusable: api error: grant does not exist
             Authenticate with: fj auth login --host codeberg.org
  tea  tea 0.15.1
         gitea.com                user=MandavkarPranjal
```

## Behaviour notes

- **Nothing is created if the local directory is unusable.** The destination is
  validated *before* the remote repository is created, so a bad `-dir` can't
  leave an orphan repo on your server. An existing git repository *is* usable —
  see [Publishing an existing git repository](#publishing-an-existing-git-repository).
- **Templates are generated locally, not by the server.** `tea` can seed
  `.gitignore`/`LICENSE`/`README` server-side, but `fj` cannot, so both paths
  behave the same: the helper writes the files and pushes them.
- **Backend commands run from a scratch directory.** Both CLIs infer a
  repository from `$PWD`; running them from a neutral directory stops an
  unrelated local checkout from leaking into the operation.
- **Remotes are never repointed.** If the chosen remote name already exists with
  a different URL, `fth` stops with an error instead of rewriting your remote.
- **Pushes are never forced.** A non-fast-forward push fails with git's own
  error rather than overwriting history on the server.
- **`-license` writes the canonical SPDX text** from
  [`license-list-data`](https://github.com/spdx/license-list-data), with the
  year and copyright holder substituted.

## Development

```sh
mise run test      # go test ./...
mise run fmt       # gofmt -w .
mise run lint      # golangci-lint
mise run vet       # go vet ./...
mise run check     # fmtcheck + lint + vet + test
mise run build     # build the binary
mise run clean     # remove the binary
mise run install   # install into ~/.local/bin
```

Layout:

- `main.go` — CLI parsing, flags, orchestration
- `mise.toml` — pinned toolchain (Go, golangci-lint) and task definitions
- `internal/forge` — backend abstraction over `tea` and `fj`
  (`forge.go`, `tea.go`, `fj.go`, `detect.go`, `exec.go`)
- `internal/scaffold` — local git repository creation and remote attach
  (`scaffold.go`, `repo.go`)
- `internal/prompt` — Bubble Tea prompts: text input and arrow-key selection
- `internal/ui` — colour and alignment, with automatic plain-text fallback
- `internal/progress` — indeterminate spinner for the network-bound steps
- `internal/templates` — embedded SPDX licenses and `.gitignore` templates