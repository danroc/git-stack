# Specification: Git Stack CLI

## 1. Overview

Git Stack CLI is a lightweight, platform-agnostic command-line tool designed to manage "stacks" of interdependent Git branches. Unlike existing tools (e.g., `gh stack`), it does not rely on any third-party APIs or external metadata files. It uses the Git commit graph itself as the single source of truth for defining and traversing branch stacks.

## 2. Problem Statement

Developers often work on sequential, interdependent features that require a "stack" of branches (where Branch B is branched from Branch A). Managing these manually is error-prone, especially when rebasing or syncing with remotes. Existing solutions are often tied to specific platforms like GitHub, making them unusable in generic Git workflows or other platforms (GitLab, Bitbucket, etc.).

## /3. Solution

A stateless CLI tool that leverages Git primitives (`git rev-list`, `git merge-base`, `refs/heads/*`) to:

1. Identify a linear chain of branches by traversing ancestors and descendants from the current `HEAD`.
2. Automate rebasing, pushing, and pulling for all branches within the identified stack.
3. Handle branch ambiguity through interactive user prompts.

## 4. Core Functional Requirements

### 4.1 Command Interface

* **`git-stack add <branch-name>`**: Creates a new branch from the current `HEAD`, extending the stack.
* **`git-stack push`**: Iterates through all branches in the identified stack and performs `git push`. Automatically configures upstreams if missing.
* **`git-stack pull`**: Iterates through all branches in the identified stack and performs `git pull` from their respective upstreams.
* **`git-stack view`**: Renders a visual representation of the current stack lineage (e.g., `main -> feat-1 -> feat-2`).

### 4.2 The Discovery Engine (The "Stateless" Logic)

The tool must identify the "active stack" without reading any local configuration files:

* **Upward Trace**: Starting from `HEAD`, use `git rev-list --first-parent` to find all ancestor commits back to a stable base (e'g., `main`).
* **Downward Trace**: Scan `refs/heads/*`. A branch is part of the current stack if `git merge-base --is-ancestor <current_branch> <candidate_branch>` returns true.
* **Ambiguity Handling**: If the tool detects multiple potential branch lineages (bifurcations) originating from the identified lineage, it must prompt the user:
    *"Multiple stacks detected. Which stack do you want to [action]? (A) stack-alpha (B) stack-beta"*.

### 4.3 Conflict Management

* If an automated rebase or pull fails due to merge conflicts, the tool must **halt execution immediately** and notify the user of the specific branch requiring intervention.

## 5. Technical Architecture (Go)

### 5.1 Project Structure

* `cmd/`: Cobra command definitions and CLI entry point.
* `pkg/engine/`: The core discovery and lineage-tracing algorithms.
* `pkg/gitutils/`: An abstraction layer for executing Git commands via `os/exec`.
* `pkg/ui/`: Logic for rendering the stack view and handling interactive prompts.

### 5.2 Implementation Details

* **Language**: Go (for portability, static binaries, and excellent CLI support).
* **Dependencies**: `spf13/cobra` (CLI framework), `spf13/pflag` (Flag parsing).
* **Git Interface**: Direct execution of Git commands to ensure compatibility with the user's existing Git configuration.

## 6. Testing Strategy

* **Unit Tests**: Test the `engine` logic using mocked Git outputs to verify lineage tracing and ambiguity detection.
* **Integration Tests**: A suite of shell scripts that:
    1. Initialize real Git repositories.
    2. Create various topologies (Linear, Bifurcated, Disconnected).
    3. Verify that `add`, `push`, `pull`, and `view` behave correctly according to the specification.

## 7. Out of Scope

* Support for non-linear/bifurcated stacks within a single command execution.
* Integration with any third-party APIs (GitHub, GitLab).
* Management of branches outside of the identified stack lineage.
