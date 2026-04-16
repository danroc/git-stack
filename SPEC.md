# Specification: Git Stack CLI

## 1. Overview

Git Stack CLI is a lightweight, platform-agnostic command-line tool for managing "stacks" of interdependent Git branches. It uses the Git commit graph as the sole source of truth — no tool-specific metadata files are written.

## 2. Problem Statement

Developers working on sequential, interdependent features often maintain a "stack" of branches where branch B is branched off branch A. Managing these manually is error-prone when rebasing or syncing with remotes. Existing solutions (e.g. `gh stack`) are tied to specific platforms, making them unusable in generic Git workflows or other hosting providers (GitLab, Bitbucket, etc.).

## 3. Solution

A stateless CLI tool that leverages Git primitives (`git rev-list`, `git merge-base`, `refs/heads/*`) to:

1. Identify a linear chain of branches by traversing the commit graph upward and downward from the current `HEAD`.
2. Automate pushing and pulling for all branches in the identified stack.
3. Handle branch ambiguity through interactive user prompts.

"Stateless" means the tool writes no metadata files. The Git commit graph is the only persistent state.

## 4. Core Functional Requirements

### 4.1 Command Interface

All commands accept a `--base <branch>` flag to override base branch detection (see §4.2).

---

**`git-stack add <branch-name>`**

Creates a new branch from the current `HEAD`. This is equivalent to `git checkout -b <branch-name>`: uncommitted working-tree changes carry over to the new branch. No validation of stack membership is performed.

---

**`git-stack push`**

Iterates through all feature branches in the identified stack — excluding the base branch — from bottom to top and executes `git push` for each. If no upstream is configured for a branch, sets it to `origin/<branch-name>`. Halts immediately and reports the failing branch if any push operation fails.

---

**`git-stack pull`**

Iterates through all feature branches in the identified stack — excluding the base branch — from bottom to top. For each branch: checks it out, then executes `git pull --rebase`. If a conflict or error occurs, halts immediately and leaves the user on the failing branch with the rebase in-progress, so they can resolve it with `git rebase --continue` or `git rebase --abort` before re-running.

---

**`git-stack rebase`**

Rebases each feature branch in the stack onto the current tip of its immediate parent, bottom-to-top. Given a stack `main -> A -> B -> C`, the sequence is:

1. Check out A, rebase onto `main`.
2. Check out B, rebase onto the updated tip of A.
3. Check out C, rebase onto the updated tip of B.

This ensures every branch in the stack sits directly on top of the latest commit of the branch below it. If a rebase produces conflicts, the command halts immediately and leaves the repository in the in-progress rebase state. The user resolves conflicts with `git rebase --continue` or `git rebase --abort` and re-runs the command. On full success, the originally checked-out branch is restored.

---

**`git-stack view`**

Renders a linear representation of the current stack. The active branch is marked with `*`. Each branch (except the base) shows how many commits it is ahead of its immediate parent in the stack.

Example:
```
main -> feat-1 (+2) -> *feat-2 (+1) -> feat-3 (+3)
```

---

### 4.2 The Discovery Engine

The engine identifies the active stack without reading any tool-specific files. All information is derived from the Git commit graph and `refs/heads/*`.

#### Base Branch Resolution

The base branch anchors the bottom of the stack. It is resolved in the following order:

1. The value of the `--base` flag, if provided.
2. `main`, if it exists as a local branch.
3. `master`, if it exists as a local branch.
4. If none of the above: exit with error — "could not detect base branch; use --base to specify."

#### Upward Trace (base → current)

Use `git rev-list --first-parent <base>..<current>` to obtain the first-parent commit history between the base and the current branch. Walk these commits from oldest to newest. Whenever a commit hash matches the HEAD of a local branch, that branch is a member of the stack. The resulting ordered list — from base to current branch — forms the lower portion of the stack.

#### Downward Trace (current → top)

Scan all entries in `refs/heads/*`. A candidate branch `B` is a descendant of the current branch if `git merge-base --is-ancestor <current> <B>` returns true.

Filter the candidates to **direct children** only: exclude any candidate `B` if another candidate `C` satisfies `git merge-base --is-ancestor <C> <B>` (i.e. `C` sits between the current branch and `B` in the history). Recurse from the chosen direct child until no further descendants are found.

#### Ambiguity Handling

If more than one direct child is found at any step in the downward trace, prompt the user:

> _"Multiple branches detected. Which branch do you want to [action]?_
> _(1) branch-alpha_
> _(2) branch-beta"_

The selection applies to that single invocation only and is not persisted.

### 4.3 Conflict and Error Management

If any operation (`push`, `pull --rebase`, `rebase`) fails for any reason:

- Halt execution immediately; do not process remaining branches.
- Print the name of the branch that failed along with the error.
- For `pull --rebase` conflicts: leave the repository in the in-progress rebase state so the user can resolve conflicts and continue manually.
- Exit with a non-zero status code.

## 5. Technical Architecture (Go)

### 5.1 Project Structure

- `cmd/`: Cobra command definitions and CLI entry point.
- `pkg/engine/`: Core discovery and lineage-tracing algorithms.
- `pkg/gitutils/`: Abstraction layer for executing Git commands via `os/exec`.
- `pkg/ui/`: Stack rendering and interactive prompt logic.

### 5.2 Implementation Details

- **Language**: Go (static binaries, portable, strong CLI support).
- **Dependencies**: `spf13/cobra` for command parsing and flag handling.
- **Git Interface**: Direct execution of Git subprocesses to preserve compatibility with the user's existing Git configuration and hooks.

## 6. Testing Strategy

- **Unit Tests**: Test the `engine` package using mocked Git outputs to verify lineage tracing, direct-child filtering, and ambiguity detection across known topologies.
- **Integration Tests**: Shell scripts that:
  1. Initialize real Git repositories.
  2. Create known topologies (linear, bifurcated, disconnected).
  3. Assert that `add`, `push`, `pull`, `rebase`, and `view` produce correct output and exit codes per this specification.

## 7. Out of Scope

- Processing multiple stack paths simultaneously within a single command (bifurcations are handled by prompting the user to select one path).
- Integration with any third-party APIs (GitHub, GitLab, etc.).
- Management of branches outside of the identified stack lineage.
- Persisting user choices (e.g. disambiguation selections) across invocations.
