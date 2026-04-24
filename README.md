# git-stack

Manage stacks of interdependent Git branches from the command line — platform-agnostic, no external services, no metadata files.

A **stack** is a linear chain of branches where each one is built on top of the previous: `main → feat-1 → feat-2 → feat-3`. `git-stack` automates the tedious parts — keeping every branch rebased on its parent, pushing and pulling the whole chain, and moving branches around within it.

## Why

If you split large changes into reviewable PRs, you end up manually rebasing a cascade of branches every time you update any of them. `git-stack` does that for you. Unlike `gh stack`, `spr`, or `graphite`, it talks to Git only — so it works the same with GitHub, GitLab, Bitbucket, Gitea, or a bare remote.

Branch relationships are persisted in standard `git config` (`branch.<name>.stackParent`). There are no hidden state files and no server-side components.

## Install

### Homebrew

```sh
brew tap danroc/tap
brew install git-stack
```

### Go

```sh
go install github.com/danroc/git-stack/cmd/git-stack@latest
```

### From source

```sh
git clone https://github.com/danroc/git-stack
cd git-stack
make install   # builds and copies to ~/.local/bin
```

## Quickstart

```sh
# On main, start a stack
git-stack add feat-1        # create feat-1 off main
# …commit…
git-stack add feat-2        # create feat-2 off feat-1
# …commit…
git-stack add feat-3        # create feat-3 off feat-2
# …commit…

git-stack view              # see the whole stack
git-stack push              # push every branch, setting upstreams as needed
```

Later, after `main` has moved on:

```sh
git checkout main && git pull
git checkout feat-3
git-stack rebase            # rebase feat-1, feat-2, feat-3 onto the new main, in order
git-stack push              # force-push the updated stack (configure push.default as needed)
```

Reparenting a branch mid-stack:

```sh
git-stack move feat-2 main  # detach feat-2 (and its children) and rebase onto main
```

## Commands

| Command | Does |
|---|---|
| `git-stack add <name>` | Create a new branch off `HEAD`, extending the stack. |
| `git-stack view` | Print the full stack tree with ahead/behind counts. |
| `git-stack rebase` | Rebase every branch in the stack onto its parent, bottom-to-top. |
| `git-stack push` | Push every branch in the stack; auto-sets upstream on first push. |
| `git-stack pull` | Pull each branch with `--rebase`, bottom-to-top. |
| `git-stack move [branch] <new-parent>` | Reparent a branch (and its descendants) onto a new parent. |
| `git-stack reset` | Remove all `stackParent` config entries. |
| `git-stack version` | Print the installed version. |

All commands accept `--base <branch>` to override base-branch auto-detection (default: `main`, then `master`).

On any conflict, `git-stack` stops immediately and leaves you in the in-progress rebase on the failing branch. Resolve with `git rebase --continue` / `--abort` and re-run.

## How it works

The active stack is discovered from the Git commit graph and `refs/heads/*`:

- **Upward** from `HEAD`, walking first-parent history until it reaches the base branch.
- **Downward**, scanning all local branches whose HEAD is strictly above the current branch.

When the graph is ambiguous (e.g. two branches share a HEAD, or a parent has advanced past a child), `branch.<name>.stackParent` is consulted; when it isn't, the graph wins and the config is repaired. If multiple direct children exist, you're prompted to pick one.

Full design in [`SPEC.md`](SPEC.md).

## Development

```sh
make build       # binary to ./dist/git-stack
make test        # go test ./...
make lint        # golangci-lint + go mod tidy
make help        # show all targets
```

Requires Go 1.26 and `golangci-lint` for linting.

## License

[MIT](LICENSE)
