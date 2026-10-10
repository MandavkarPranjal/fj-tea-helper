# fj-tea-helper

Create a new repository on Forgejo or Gitea in one command, using the `fj`
(Forgejo) or `tea` (Gitea) CLI as the engine.

It detects which CLI you're authenticated against, creates the remote
repository, scaffolds a local git repository with `README.md`, `.gitignore` and
`LICENSE`, wires up the remote and pushes the initial commit.

```console
$ fj-tea-helper myproject -lang go -license MIT
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
mise run build          # produces ./fj-tea-helper
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
fj-tea-helper [flags] <name>
fj-tea-helper [flags] <owner>/<name>
fj-tea-helper -list
```

Flags may appear before or after the name, so `mkrepo myproject -private` and
`mkrepo -private myproject` are equivalent.

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
| `-ssh` | `false` | Use the SSH clone URL for the remote |
| `-trust-model` | – | Gitea trust model (`tea` only) |
| `-template` | `false` | Mark as a template repository (`tea` only) |
| `-no-push` | `false` | Create the remote but don't push |
| `-no-scaffold` | `false` | Create only the remote repository |
| `-force` | `false` | Scaffold even if the local directory isn't empty |
| `-holder` | your account | Copyright holder for the license |
| `-year` | this year | Copyright year |
| `-dry-run` | `false` | Print the plan and exit without changing anything |
| `-list` | – | Show detected backends, logins and licenses |
| `-verbose` | `false` | Log progress |
| `-version` | – | Print the helper version |

### Examples

```sh
# Public Go project on whatever you're logged into
fj-tea-helper myservice -lang go -license MIT

# Private repo on a specific Forgejo instance
fj-tea-helper -tool fj -host codeberg.org -visibility private myservice

# Force Gitea, an org-owned repo, Apache-2.0, no local scaffold
fj-tea-helper -tool tea -owner myorg -license Apache-2.0 -no-scaffold myservice

# Create the repo and local dir, but decide about pushing later
fj-tea-helper -no-push myservice

# See what would happen
fj-tea-helper myservice -dry-run
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
$ fj-tea-helper -list
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
  leave an orphan repo on your server.
- **Templates are generated locally, not by the server.** `tea` can seed
  `.gitignore`/`LICENSE`/`README` server-side, but `fj` cannot, so both paths
  behave the same: the helper writes the files and pushes them.
- **Backend commands run from a scratch directory.** Both CLIs infer a
  repository from `$PWD`; running them from a neutral directory stops an
  unrelated local checkout from leaking into the operation.
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
- `internal/scaffold` — local git repository creation
- `internal/templates` — embedded SPDX licenses and `.gitignore` templates