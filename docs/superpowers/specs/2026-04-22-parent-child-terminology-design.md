# Design: Parent/Child Terminology Consistency

## Problem

The codebase mixes two metaphors for describing branch relationships:

- **Parent/child** (family tree) — used in `stackParent`, `Parent()`, `directChildren()`
- **Spatial** (above/below) — used in comments: "branches above", "strictly below"
- **Git ancestry** (ancestor/descendant) — used in `IsBranchDescendant(ancestor, descendant)`

This creates ambiguity: when a comment says "below", is it referring to the parent or the child? The terms `ancestor` and `descendant` come from Git commit graph semantics and don't map cleanly to the stack model where "descendant" in Git (older commit) means "child" in the stack (newer branch above).

## Decision

Use **parent/child** consistently throughout the codebase. This is the existing metaphor in the config key (`stackParent`) and in key function names (`Parent()`, `directChildren()`). It is more precise than spatial terms and aligns with how developers think about branch stacks.

When the parent/child metaphor is used, no additional direction clarification is needed — the terms are self-contained. When spatial terms (above/below) appear in comments or descriptions, they must either be replaced with parent/child or clarified with explicit direction.

## Changes

### 1. API Renames

| Current | New | Rationale |
|---------|-----|-----------|
| `IsBranchDescendant(ancestor, descendant)` | `IsChildOf(child, parent)` | `ancestor`/`descendant` are Git commit terms. The new names match the stack metaphor and make argument order obvious: `IsChildOf(newParent, branch)` reads as "is newParent a child of branch?" |
| `traceDescendants()` | `traceChildren()` | "Descendants" in Git means older commits (below), but in the stack these branches are above (children). `traceChildren()` is unambiguous. |
| `SubtreeMembers()` | `SubtreeChildren()` | "Members" is vague. `SubtreeChildren()` clarifies that all results are in the parent/child tree. |

### 2. Parameter Name Fixes

`IsChildOf(child, parent)` replaces `IsBranchDescendant(ancestor, descendant)`:

```go
// Before:
s.disc.IsBranchDescendant(branch, newParent)
// Reads as: "is branch a descendant of newParent?" — backwards from intent

// After:
s.disc.IsChildOf(newParent, branch)
// Reads as: "is newParent a child of branch?" — matches intent
```

### 3. Comment Cleanup

Replace all spatial references (above/below) in comments and doc comments with parent/child. The examples below show the pattern; apply consistently across the entire codebase:

| Current (in comments) | New |
|----------------------|-----|
| "branches directly above" | "direct children" |
| "strictly below in the stack" | "strictly below as a child" |
| "branches stacked above" | "child branches" |
| "base → currentBranch (upward)" | "base → ... → current (parent chain)" |

### 4. Config Key

The `branch.<name>.stackParent` config key stays unchanged. It is persisted on disk and renaming would require a migration. The key name already uses the parent/child metaphor.

### 5. SPEC.md Update

Add a Terminology section to the spec that explicitly defines:

- **parent** — the branch below in the stack (the one a branch is branched FROM)
- **child** — a branch above in the stack (branched off of this one)
- **base** — the bottom-most branch (main/master)
- Stack direction: `base → parent → child → child` (bottom to top)

## What Does Not Change

- **CLI messages** — Already use parent/child implicitly ("onto", "from...to") and are clear in context
- **Config key names** — `stackParent` and `stackMergeBase` persist on disk; no migration
- **`Parent()`, `SetParent()`, `directChildren()`** — Already use the correct metaphor
- **`branchAction func(branch, parent string)`** — Already uses parent/child
- **`Step.Parent`** — Already uses parent/child

## Scope

This change affects:
- `pkg/discovery/engine.go` — API renames, parameter names, comments
- `pkg/discovery/engine_test.go` — Corresponding test updates
- `pkg/stack/stack.go` — Call site updates for renamed methods
- `pkg/stack/stack_test.go` — Corresponding test updates
- `cmd/git-stack/main.go` — Call site updates
- `SPEC.md` — Terminology section addition
