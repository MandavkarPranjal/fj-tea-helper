# fth

**fth** (*fj-tea-helper*) creates a new repository on Forgejo, Gitea, GitCafe,
GitHub or Trylle in one command, using the `fj`, `tea`, `cafe`, `gh` or `try`
CLI as the engine.

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
needed. At runtime the binary shells out to whichever of `fj`, `tea`, `cafe`,
`gh` or `try` you have authenticated. `git` must be on `PATH`.

## Usage

```
fth [flags] <name>
fth [flags] <owner>/<name>
fth [flags]
fth [flags] .
fth -list
```

With no name — or with `.` — `fth` publishes the directory it runs from: in
`~/project/demo` it creates `<owner>/demo`, and the repository is named after
that directory rather than after anything typed on the command line.

```console
$ cd ~/project/demo
$ fth
Plan
  backend    fj
  instance   codeberg.org
  repository you/demo (public)
  local dir  /home/you/project/demo (existing repo, branch main)
  ...
```

`fth demo` inside `~/project/demo` means the same thing. Typing the name of the
directory you are standing in is asking to publish *that* directory, not to
build a second one inside it. Any other name still scaffolds a new `./<name>`
beside where you stand, so `fth demo` from `~/project` is unchanged.

The name follows the directory the repository will live in, so an explicit
`-dir` is what gets named: `fth -dir ~/project/other` publishes `other` even
from somewhere else.

Because the name comes from the directory, it has to still describe that
directory once derived. A directory whose name cannot be a repository name is
refused with the obvious fix rather than an invented one — a filesystem root, a
name with a space in it, or leading and trailing whitespace:

```console
$ fth -dir ' demo '
error: cannot derive a repository name from /home/you/project/ demo  (" demo " has
leading or trailing whitespace); rename the directory or pass a name
```

Padding is refused rather than trimmed because trimming would publish `demo`
into a directory called `" demo "` — a repository whose name does not describe
the directory holding it.

Flags may appear before or after the name, so `fth myproject -private` and
`fth -private myproject` are equivalent.

### Flags

| Flag | Default | Description |
| --- | --- | --- |
| `-tool` | `auto` | Backend CLI: `auto`, `tea`, `fj`, `cafe`, `gh`, or `try`; narrows the instance picker |
| `-cafe-bin` | `cafe` on PATH | Explicit path to the `cafe` binary |
| `-try-bin` | `trylle-cli` on PATH | Explicit path to the Trylle binary (`trylle-cli` or `try`) |
| `-host` | detected | Instance host, e.g. `gitea.com` or `github.com`; narrows the instance picker |
| `-owner` | your account | Repository owner or organisation |
| `-description` | – | Short repository description |
| `-visibility` | `public` | `public` or `private` |
| `-license` | `none` | SPDX id: `MIT`, `Apache-2.0`, `BSD-3-Clause`, `GPL-3.0-only`, `Unlicense` |
| `-license-file` | – | Use a license file from disk instead of an SPDX id |
| `-lang` | – | Seed a `.gitignore`: `go`, `node`, `python`, `rust` |
| `-branch` | `main` | Default branch name |
| `-remote` | `origin` | Git remote name |
| `-dir` | `./<name>`, or `.` with no name | Local directory to scaffold; with no name it also names the repository |
| `-readme` | `true` | Write a `README.md` |
| `-url` | `ssh` | Clone URL to wire up: `ssh` or `https` |
| `-trust-model` | – | Gitea trust model (`tea` only; ignored elsewhere) |
| `-template` | `false` | Mark as a template repository (`tea` only; ignored elsewhere) |
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

# Force GitCafe, using a cafe binary that is not on PATH
fth -tool cafe -cafe-bin ~/.bun/install/global/node_modules/@gitcafe/cli/bin/cafe.js myservice

# Public repo on GitHub
fth -tool gh -license MIT -lang go myservice

# Private repo on a GitHub Enterprise Server instance
fth -tool gh -host ghe.example.com -visibility private -owner myorg myservice

# Public repo on Trylle
fth -tool try -license MIT -lang go myservice

# Create the repo and local dir, but decide about pushing later
fth -no-push myservice

# Publish the directory you are standing in, named after it
cd ~/project/demo && fth

# Same, without leaving the directory
fth -dir ~/project/other

# Publish a git repo you already have (see below)
cd ~/code/myservice && fth

# See what would happen
fth myservice -dry-run
```

## Publishing an existing git repository

If the target directory is already a git repository with at least one commit,
`fth` stops scaffolding and switches to **attach** mode:

```console
$ cd ~/code/myservice
$ fth
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
  (up/down or j/k to move, enter to choose)
Remote URL: ssh
```

Use `↑`/`↓` or `j`/`k` to move, or press the first letter of a choice (`s` for
ssh, `h` for https). Pass `-url ssh` or `-url https` to skip the question
entirely.

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
- `Esc` at a prompt means "never mind": it takes the highlighted (default)
  answer and carries on, so a stray keystroke can't leave a half-created
  repository behind.
- `Ctrl-C` cancels the whole run. fth exits with status 130 and prints
  `cancelled; nothing was created`. Because every question is asked before
  anything is created, stopping at a prompt always means nothing was made —
  the remote is never half-built.

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

fth probes every installed CLI and then asks **which instance to create on**:

```console
$ fth myproject
Create the repository on
  > fj    codeberg.org  MandavkarPranjal
    tea   gitea.com     MandavkarPranjal
    cafe  git.cafe      mandavkarpranjal
    gh    github.com    MandavkarPranjal
    try   trylle.com    mandavkarpranjal
  (up/down or j/k to move, enter to choose)
Create the repository on: fj    codeberg.org
```

Each row is a real pairing of a CLI with one of its logins, because that is the
actual decision: `tea` and `fj` are both routinely signed into several hosts at
once, so "which backend" is the wrong question to ask on its own. The account
you are authenticated as is shown beside each host. `↑`/`↓` or `j`/`k` to move,
the first letter of a row to jump straight to it, Enter to choose.

How the list is built:

1. Show a progress row per CLI while the probes run, settling each row as that
   CLI answers, so the backends do not all appear at once when the slowest one
   finally lands. The display is erased once detection is done, before the
   instance picker is drawn.
2. Run `fj auth list` / `tea logins list -o json` / `cafe auth status --json` /
   `gh auth status --json hosts` / `try auth status --json` to find configured
   logins.
3. Confirm each login actually works — for `fj` this means `fj whoami`, because
   `fj auth list` happily reports instances whose stored OAuth grant has been
   revoked. A login that fails is left out of the list rather than offered and
   then failing later.
4. Order the results by tool (`fj`, then `tea`, `cafe`, `gh`, `try`), and within
   each tool put the CLI's own default login first. That first row is the one
   Enter accepts, so pressing Enter twice always does something sensible.

`gh` and `try` are probed **last** deliberately. `gh` is by far the most likely
of these to be installed, and `try` is a name other programs also answer to, so
putting either first would quietly change what Enter picks for someone who came
to work on their Forgejo or Gitea host.

### How fast it is

Every CLI is asked at the same time, so the wait is the slowest one rather than
the sum of all five — roughly a second with all five installed, and
milliseconds if only one is.

That remaining second is network time that does not go away: each backend is a
separate process, each opens its own connection, and every one of them pays a
full TLS handshake. Repeating the call does not warm it up.

So results are also **memoised** in `fth/detect.json` inside your platform's user
cache directory — on Linux, `~/.cache/fth/detect.json` unless `XDG_CACHE_HOME`
points elsewhere — for 10 minutes, per tool and per `-cafe-bin`/`-try-bin`. A
second run inside that window skips the probes entirely and finishes in a couple
of milliseconds. The file records which accounts and hosts you are signed into,
so it is written `0600`, and it holds no credentials.

When you have just fixed a credential, run with `-no-cache` to ignore the stored
result, re-probe every CLI, and refresh it:

```console
$ fth myproject -no-cache
```

`-tool` and `-host` narrow the list before it is shown, and are what you want in
a script:

```console
$ fth -tool tea myproject        # only tea's logins, or the default if there is just one
$ fth -host codeberg.org myproject
```

A flag that matches nothing is an error rather than a silent fallback to a
different instance:

```console
error: no login for "nope.example" (known: fj/codeberg.org, tea/gitea.com, cafe/git.cafe, gh/github.com)
```

With `-yes`, or when stdin is not a terminal, fth takes the first entry and says
so, so nothing hangs in CI.

Use `-list` to see exactly what was detected:

```console
$ fth -list
Backends
  fj   fj 0.6.0
        ✓ codeberg.org             user=MandavkarPranjal
  tea  tea 0.15.1
        ✓ gitea.com               user=MandavkarPranjal
  cafe cafe 0.6.0
        ✓ git.cafe                user=mandavkarpranjal
  gh   gh 2.100.0
        ✓ github.com             user=MandavkarPranjal
  try  try 1.2.0
        ✓ trylle.com             user=mandavkarpranjal
```

A backend that is missing is reported with how to install it:

```console
  cafe not installed on PATH; install it with: bun install -g @gitcafe/cli
  gh not installed on PATH; install it from https://cli.github.com
  try not installed on PATH; install it with: npm i -g @trylle/trylle-cli
```

### GitCafe

`cafe` ([git.cafe](https://git.cafe)) ships as the npm package `@gitcafe/cli`
and runs under Bun, not Node. Install it globally:

```sh
bun install -g @gitcafe/cli
```

`fth` never falls back to `bunx @gitcafe/cli`, because that re-resolves a
multi-megabyte bundle on every invocation — far more expensive than the
repository creation it would be doing. If you keep the CLI somewhere unusual,
point `fth` straight at it:

```sh
fth -tool cafe -cafe-bin /path/to/cafe.js myproject
```

Note that `cafe auth status` prints an access token, even in `--json` form.
`fth` scrubs credential-shaped values out of everything it captures before it
reaches an error message or the log, but treat any `cafe` output you paste into
a bug report with the same care.

Unlike `tea`, `cafe` accepts a personal account for its owner flag, so `--org`
is only sent for real organisations. `fj` and `cafe` cannot seed templates
server-side (see below), so both take the local-scaffold path.

### GitHub

`gh` ([GitHub CLI](https://cli.github.com)) is maintained by GitHub itself:

```sh
brew install gh          # or: sudo apt install gh
gh auth login
```

Unlike the other three, `gh` is not tied to one instance — it is authenticated
against `github.com` **and any number of GitHub Enterprise hosts**, so it is
the backend where the instance picker earns its keep:

```console
$ fth myproject
Create the repository on
  > fj    codeberg.org  MandavkarPranjal
    gh    github.com    MandavkarPranjal
    gh    ghe.acme.io   octocat
```

Each `gh` row is a host `gh` holds a working token for. A host whose active
account has expired is left out rather than offered and then failing later.

Three things behave differently from the Forgejo backends:

- **The host comes from the environment, not a flag.** `gh` selects its target
  with `GH_HOST`, so `fth` sets it for you. `-host ghe.acme.io` works with or
  without `-tool gh`, and Enterprise repos are created on the right instance.
  Within `gh`, `github.com` is listed first so it is the default.
- **Visibility is always passed explicitly.** Given neither `--public` nor
  `--private`, `gh repo create` falls back to interactive prompts. `fth` never
  lets a CLI take over the terminal, so it always states which one it wants.
- **`README`/`.gitignore`/`LICENSE` are written locally, never by the server.**
  `gh`'s `--add-readme`, `--gitignore` and `--license` all create an initial
  commit on the new repository, which would then collide with the commit `fth`
  pushes from the local scaffold. So `fth` takes the local-scaffold path, the
  same one `fj` and `cafe` already use.

Because `gh auth status` can print a live token (`--show-token`), its output
goes through the same credential scrubber as every other backend's.

### Trylle

`try` ([trylle.com](https://trylle.com)) ships as the npm package
`@trylle/trylle-cli` (also on PyPI as `trylle-cli`):

```sh
npm i -g @trylle/trylle-cli
trylle-cli auth login  # paste a CLI Key from https://trylle.com/settings/cli
```

The package installs **two** executables, `try` and `trylle-cli`, and `fth`
prefers `trylle-cli`.

- **`try` is not a distinctive name.** An unrelated workspace manager ships a
  program by that name, and on a machine that has it, `try --version` answers
  `try v1.10.1 - ephemeral workspace manager`. `fth` checks the identity of a
  binary it found under the short name before driving it, and reports the
  collision rather than relaying that program's errors as if they were yours.
  `-try-bin` points `fth` at a specific binary if you need to be explicit.
- **One credential, one instance.** Unlike `gh` there is no list of hosts to
  enumerate: the CLI holds a single API key pointed at a single endpoint, which
  is `trylle.com` unless a self-hosted deployment saved its own. The instance
  name comes from the web URL the CLI reports, so `-host` works for either.
- **The two clone URLs are on different hosts.** The web app and the SSH remote
  live on `trylle.com`, but HTTPS clone URLs are served from the API host
  (`https://api.trylle.com/git/OWNER/NAME.git`). `fth` takes each from the right
  one, so both the preview and the remote it writes actually resolve. The API
  endpoint comes from `try auth status`, so a self-hosted deployment whose API
  sits somewhere other than `api.WEB-HOST` gets its own; only when the CLI
  reports no endpoint at all does `fth` fall back to that convention.
- **`--auto-init` is never passed.** It would create a server-side commit that
  collides with the one `fth` pushes from the local scaffold, so
  `README`/`.gitignore`/`LICENSE` are written locally, like every non-tea path.
- **Every new repository starts with a placeholder commit.** Trylle seeds an
  `Initial commit` README regardless of `--auto-init`, so the first push of an
  unrelated local history is rejected as non-fast-forward. `fth` notices that
  the remote branch holds exactly one parentless commit and replaces it,
  reporting `replaced the placeholder commit try created`. A branch holding
  real history is never overwritten — the push fails as usual and tells you to
  pull first.

`try auth token` can print the credential, so its output goes through the same
scrubber as every other backend's.

## Behaviour notes

- **Nothing is created if the local directory is unusable.** The destination is
  validated *before* the remote repository is created, so a bad `-dir` can't
  leave an orphan repo on your server. An existing git repository *is* usable —
  see [Publishing an existing git repository](#publishing-an-existing-git-repository).
- **Templates are generated locally, not by the server.** `tea` can seed
  `.gitignore`/`LICENSE`/`README` server-side, but `fj`, `cafe`, `gh` and `try`
  cannot do so without creating a commit that would clash with the one being
  pushed, so
  every path behaves the same: the helper writes the files and pushes them.
- **Backend commands run from a scratch directory.** Every backend CLI can infer a
  repository from `$PWD`; running them from a neutral directory stops an
  unrelated local checkout from leaking into the operation.
- **Credentials are scrubbed from captured output.** `run` folds a command's
  output into the error it returns, so credential-shaped values are redacted
  before anything becomes user-visible. `cafe auth status` prints a live token
  even in JSON form, which makes this a live concern rather than a precaution.
- **Remotes are never repointed.** If the chosen remote name already exists with
  a different URL, `fth` stops with an error instead of rewriting your remote.
- **Real remote history is never overwritten.** A non-fast-forward push fails
  with git's own error rather than clobbering history on the server. There is
  exactly one guarded exception: Trylle seeds every new repository with a
  placeholder commit, so `fth` replaces that single parentless commit with a
  `--force-with-lease`. The lease is what makes it safe — the replacement only
  happens while the remote branch still holds exactly that one placeholder
  commit, and it aborts the moment anything else is there.
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
- `internal/forge` — backend abstraction over `tea`, `fj`, `cafe`, `gh` and `try`
  (`forge.go`, `tea.go`, `fj.go`, `cafe.go`, `gh.go`, `try.go`, `detect.go`,
  `exec.go`)
- `internal/scaffold` — local git repository creation and remote attach
  (`scaffold.go`, `repo.go`)
- `internal/prompt` — Bubble Tea prompts: text input and single-choice selection
- `internal/ui` — colour and alignment, with automatic plain-text fallback
- `internal/progress` — indeterminate spinner for the network-bound steps
- `internal/templates` — embedded SPDX licenses and `.gitignore` templates