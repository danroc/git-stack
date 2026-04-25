# git-stack

A CLI for managing stacks of interdependent Git branches.

When a feature is split into a sequence of PRs (`main` → `feat-1` → `feat-2` →
`feat-3`), every change to an earlier branch forces a manual rebase of everything above
it. `git-stack` automates that cascade, along with pushing, pulling, and reparenting
operations across the whole stack.

It operates entirely on top of local Git primitives. There are no external APIs, no
hidden state files, and no server-side components. Parent/child relationships are
persisted in local Git config under `branch.<name>.stackParent`.

## Install

```sh
brew install danroc/tap/git-stack
```

With Go:

```sh
go install github.com/danroc/git-stack/cmd/git-stack@latest
```

From source:

```sh
git clone https://github.com/danroc/git-stack
cd git-stack
make install   # Builds and installs to ~/.local/bin
```

## Example

Build a stack on top of `main`:

```sh
git-stack add feat-1  # Branch off main
# Commits...

git-stack add feat-2  # Branch off feat-1
# Commits...

git-stack add feat-3  # Branch off feat-2
# Commits...

git-stack view
git-stack push  # Pushes all three, setting upstreams on first push
```

After `main` advances, pull it, check out the top of the stack, and rebase bottom-up:

```sh
git checkout main && git pull
git checkout feat-3
git-stack rebase
git-stack push
```

Reparent a branch in the middle of the stack:

```sh
git-stack move feat-2 main
```

This rebases `feat-2` from `feat-1` onto `main`, then cascades the rebase through
`feat-3` so the chain stays linear.

## Commands

| Command                      | Description                                                          |
| ---------------------------- | -------------------------------------------------------------------- |
| `add <name>`                 | Create a new branch off `HEAD`.                                      |
| `view`                       | Print the stack with ahead counts per branch.                        |
| `rebase`                     | Rebase every branch in the stack onto its parent, bottom to top.     |
| `push`                       | Push every branch. Sets upstream to `origin/<branch>` on first push. |
| `pull`                       | Pull every branch with `--rebase`, bottom to top.                    |
| `move [branch] <new-parent>` | Reparent a branch and cascade the rebase through its descendants.    |
| `reset`                      | Remove all `stackParent` entries from local Git config.              |
| `version`                    | Print the version.                                                   |

All commands accept `--base <branch>` to override base-branch detection. The default
base is `main`, falling back to `master`.

## Conflicts

If any `git pull --rebase` or `git rebase` step fails, `git-stack` stops immediately,
leaves you on the failing branch with the rebase in progress, and exits non-zero.
Resolve it with the usual `git rebase --continue` or `git rebase --abort`, then re-run
the `git-stack` command.

## Stack discovery

The stack is derived from the Git commit graph at invocation time. `git-stack` walks
first-parent history upward from `HEAD` until it reaches the base branch, and scans
`refs/heads/*` downward for local branches whose HEAD sits strictly above the current
one.

When the graph is ambiguous (two branches share a HEAD, or a parent has advanced past a
child) the `stackParent` config from previous runs is consulted. When the graph gives an
unambiguous answer, the graph wins and stale config is repaired. If multiple direct
children exist at any step, `git-stack` prompts for a selection.

Full design details are in [`SPEC.md`](SPEC.md).

## Development

```sh
make build  # Binary at ./dist/git-stack
make test
make lint
make help  # List all targets
```

Requires Go 1.26 and `golangci-lint`.

## License

[MIT](LICENSE)
