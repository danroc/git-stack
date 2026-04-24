# Specification: Git Stack CLI

## 1. Overview

Git Stack CLI is a lightweight, platform-agnostic command-line tool for managing
"stacks" of interdependent Git branches. It uses the Git commit graph as the sole source
of truth — no tool-specific metadata files are written.

## 2. Terminology

The following terms are used throughout this specification and the codebase. All refer
to positions in the stack hierarchy, defined from bottom to top.

- **base** — The bottom-most branch in the stack. Resolved as `main`, `master`, or via
  the `--base` flag. The base has no parent.
- **parent** — The branch _below_ in the stack. A branch's parent is the branch it was
  branched FROM. In a stack `main → A → B`, `A` is the parent of `B`.
- **child** — A branch _above_ in the stack. A branch's child was branched off from it.
  In a stack `main → A → B`, `B` is a child of `A`.
- **stack direction** — `base → parent → child → child` (bottom to top). Operations
  iterate from parent to child (bottom-to-top).

The config key `branch.<name>.stackParent` stores the parent (branch below) for each
branch. This key name uses the parent/child metaphor exclusively.

## 3. Problem Statement

Developers working on sequential, interdependent features often maintain a "stack" of
branches where branch B is branched off branch A. Managing these manually is error-prone
when rebasing or syncing with remotes. Existing solutions (e.g. `gh stack`) are tied to
specific platforms, making them unusable in generic Git workflows or other hosting
providers (GitLab, Bitbucket, etc.).

## 4. Solution

A stateless CLI tool that leverages Git primitives (`git rev-list`, `git merge-base`,
`refs/heads/*`) to:

1. Identify a linear chain of branches by traversing the commit graph upward and
   downward from the current `HEAD`.
2. Automate pushing and pulling for all branches in the identified stack.
3. Handle branch ambiguity through interactive user prompts.

"Stateless" means the tool writes no tool-specific metadata files; branch relationships
are persisted in local git config under `branch.<name>.stackParent`, which is a standard
git configuration key that other tools can read if they wish.

## 5. Core Functional Requirements

### 5.1 Command Interface

All commands accept a `--base <branch>` flag to override base branch detection (see
§5.2).

---

**`git-stack add <branch-name>`**

Creates a new branch from the current `HEAD`. This is equivalent to
`git checkout -b <branch-name>`: uncommitted working-tree changes carry over to the new
branch. No validation of stack membership is performed.

---

**`git-stack push`**

Iterates through all feature branches in the identified stack — excluding the base
branch — from bottom to top and executes `git push` for each. If no upstream is
configured for a branch, sets it to `origin/<branch-name>`. Halts immediately and
reports the failing branch if any push operation fails.

---

**`git-stack pull`**

Iterates through all feature branches in the identified stack — excluding the base
branch — from bottom to top. For each branch: checks it out, then executes
`git pull --rebase`. If a conflict or error occurs, halts immediately and leaves the
user on the failing branch with the rebase in-progress, so they can resolve it with
`git rebase --continue` or `git rebase --abort` before re-running.

---

**`git-stack rebase`**

Rebases each feature branch in the stack onto the current tip of its immediate parent,
bottom-to-top. Given a stack `main -> A -> B -> C`, the sequence is:

1. Check out A, rebase onto `main`.
2. Check out B, rebase onto the updated tip of A.
3. Check out C, rebase onto the updated tip of B.

This ensures every branch in the stack sits directly on top of the latest commit of the
branch below it. If a rebase produces conflicts, the command halts immediately and
leaves the repository in the in-progress rebase state. The user resolves conflicts with
`git rebase --continue` or `git rebase --abort` and re-runs the command. On full
success, the originally checked-out branch is restored.

---

**`git-stack move [branch] <new-parent>`**

Reparents a branch within the stack by rebasing it from its current parent onto
`<new-parent>`, then cascades that rebase through every descendant so the stack remains
linear. If `<branch>` is omitted, the currently checked out branch is moved.

Given a stack `main -> A -> B -> C`, `git-stack move B main` runs this sequence:

1. Check out B and rebase it from A onto `main`.
2. Check out C and rebase it onto the updated tip of B.

On full success, the originally checked-out branch is restored. If the moved branch is
checked out in another Git worktree, the command runs the rebase in that worktree
automatically instead of requiring the user to rerun the command there. If any rebase
produces conflicts, the command halts immediately and leaves the failing worktree in the
in-progress rebase state so the user can resolve it and continue manually.

---

**`git-stack view`**

Renders a linear representation of the current stack. The active branch is marked with
`*`. Each branch (except the base) shows how many commits it is ahead of its immediate
parent in the stack.

Example:

```text
main -> feat-1 (+2) -> *feat-2 (+1) -> feat-3 (+3)
```

---

### 5.2 The Discovery Engine

The engine identifies the active stack without reading any tool-specific files. All
information is derived from the Git commit graph and `refs/heads/*`.

#### Base Branch Resolution

The base branch anchors the bottom of the stack. It is resolved in the following order:

1. The value of the `--base` flag, if provided.
2. `main`, if it exists as a local branch.
3. `master`, if it exists as a local branch.
4. If none of the above: exit with error — "could not detect base branch; use --base to
   specify."

#### Upward Trace (base → current)

Walk the first-parent chain from the current branch's HEAD back toward the loaded
graph's floor. At each commit, enumerate every local branch whose HEAD sits there.
Typical behavior:

- If the current branch itself shares a commit with another branch, include the other
  branch in the chain only when the current branch's `branch.<name>.stackParent` config
  names it. Otherwise treat them as siblings — the other branch does not appear in the
  current branch's chain.
- All other branches encountered during the walk (strictly below the current branch as
  children) are included in the chain.

If the walk does not bottom out at the base branch — either because the base has
advanced past the current branch's fork point, or because an intermediate branch has
advanced past one of its children — resolve the remaining gap by walking the
`stackParent` config chain upward from the bottom-most collected branch until the base
is reached.

#### Downward Trace (current → top)

Scan all entries in `refs/heads/*`. A candidate branch `B` is a child of the current
branch if its HEAD is strictly above the current branch in the commit graph.

Reduce to direct children: drop any candidate `B` for which another candidate `D` sits
strictly between the current branch and `B`. When two candidates share a HEAD and one's
`stackParent` names the other, the named one stays and the other demotes (it becomes a
child of its configured parent). When two candidates share a HEAD with no config
relation, both remain as siblings.

Add diverged children: for every branch whose `stackParent` config names the current
branch but which is not in the graph-above set, include it as a direct child. These are
branches that were children of the current branch before the current branch advanced
past them.

Recurse from each chosen child until no further children are found.

#### Ambiguity Handling

If more than one direct child is found at any step in the downward trace, prompt the
user:

> _"Multiple branches detected. Which branch do you want to [action]?_ _(1)
> branch-alpha_ _(2) branch-beta"_

The selection applies to that single invocation only and is not persisted. Co-located
branches (two or more branches sharing a HEAD) never trigger a prompt on their own:
absent a `stackParent` config hint, they are treated as siblings.

#### Persistence

Every discovery writes `branch.<name>.stackParent` to local git config for each resolved
parent/child relationship. This record is consulted only when the commit graph is
ambiguous: (1) two branches share a HEAD and we need to distinguish which is the parent;
(2) a parent branch has advanced past a child so the graph alone no longer shows the
stack relationship. When the graph gives an unambiguous answer that contradicts config,
the graph wins and config is repaired.

### 5.3 Conflict and Error Management

If any operation (`push`, `pull --rebase`, `rebase`) fails for any reason:

- Halt execution immediately; do not process remaining branches.
- Print the name of the branch that failed along with the error.
- For `pull --rebase` conflicts: leave the repository in the in-progress rebase state so
  the user can resolve conflicts and continue manually.
- Exit with a non-zero status code.

## 6. Technical Architecture (Go)

### 6.1 Project Structure

- `cmd/git-stack/`: Cobra command definitions and CLI entry point.
- `internal/discovery/`: Stack discovery and lineage over the commit graph.
- `internal/git/`: Git command execution and graph loading via `os/exec`.
- `internal/stack/`: Push, pull, rebase, and move across the discovered stack.
- `internal/ui/`: Stack tree rendering and interactive disambiguation prompts.

### 6.2 Implementation Details

- **Language**: Go (static binaries, portable, strong CLI support).
- **Dependencies**: `spf13/cobra` for command parsing and flag handling.
- **Git Interface**: Direct execution of Git subprocesses to preserve compatibility with
  the user's existing Git configuration and hooks.

## 7. Testing Strategy

- **Unit Tests**: Test the `discovery` package using mocked Git outputs to verify
  lineage tracing, direct-child filtering, and ambiguity detection across known
  topologies.
- **Integration Tests**: Shell scripts that:
  1. Initialize real Git repositories.
  2. Create known topologies (linear, bifurcated, disconnected).
  3. Assert that `add`, `push`, `pull`, `rebase`, and `view` produce correct output and
     exit codes per this specification.

## 8. Out of Scope

- Processing multiple stack paths simultaneously within a single command (bifurcations
  are handled by prompting the user to select one path).
- Integration with any third-party APIs (GitHub, GitLab, etc.).
- Management of branches outside of the identified stack lineage.
