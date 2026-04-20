# Graph-First Stack Discovery

**Date:** 2026-04-20
**Status:** Draft

## 1. Summary

Two coupled changes to the discovery engine:

1. **Expand graph loading** to include base-branch commits down to the merge-base of all branches, so divergence from the base is visible.
2. **Make graph authoritative** for parent and child resolution; consult `stackParent` config only when the graph is genuinely ambiguous.

The result is a discovery engine whose primary reasoning is topological, with a narrow, documented set of cases where persisted config resolves ambiguity the graph cannot express.

## 2. Motivation

Today `buildGraph` calls `git log heads... ^baseBranch`, excluding everything reachable from the base branch. This leaves the engine blind to base-branch advancement: if `main` moves forward past the point where a feature branched off, the feature still looks fine topologically. Loading base commits down to the common merge-base fixes this.

Separately, the current discovery logic treats `stackParent` config as a first-class source of truth — it can override the graph, exclude candidates, and cause behavior to drift from what the commit DAG actually shows. A prior plan ("config-first") leaned further into this direction and was not shipped. The simpler model is the opposite: the graph is the source of truth, and config is a tiebreaker for two specific ambiguities (co-located branches; diverged parent).

## 3. Scope

**In scope:**
- `pkg/git/graph.go`: expand the log range; support multiple branches per commit.
- `pkg/discovery/engine.go`: rewrite descendant resolution to be graph-first with config tiebreak; tighten ancestor resolution; persist relationships after every discovery.
- `SPEC.md`: update §4.2 (Discovery Engine) and §7 (Out of Scope) to match new behavior.
- Tests for the new ambiguity cases.

**Out of scope:**
- CLI surface changes (no new commands, flags, or user prompts introduced).
- Remote/worktree behavior changes.
- Any change to `push`/`pull`/`rebase` orchestration logic.

## 4. Approach

Phased rewrite. Phase 1 expands the graph and updates callers that had base-boundary special cases. Phase 2 rewrites discovery semantics. Each phase is independently testable.

## 5. Design

### 5.1 Phase 1 — Graph loading

**`pkg/git/graph.go`:**

Replace the current log range with one that includes base commits down to the common merge-base of all branch heads.

Steps in `buildGraph`:

1. Collect all branch heads (existing).
2. Compute the octopus merge-base of every branch head (including the base branch's head): `git merge-base --octopus <all-heads>`. Call the result `floor`.
3. Run `git log --format=%H %P <all-heads> ^<floor>^`. The `^floor^` exclusion means `floor` itself is included in output. If `floor` has no parents (root commit), drop the `^floor^` exclusion.
4. Populate `parents`, `heads`, `branchAt` as today.

**Change `Graph.branchAt` to support multiple branches at the same commit:**

```go
branchAt map[string][]string  // was map[string]string
```

`BranchAt(hash)` returns `([]string, bool)`. Callers that previously took one branch and discarded the rest now handle the slice explicitly.

**Consequences for `engine.go`:**

- `isAbove` (line 218): delete the special case for `parentHead == baseHead`. Since baseHead is now in the graph, `IsAncestor(baseHead, x)` works uniformly.
- `IsBranchDescendant` (line 343): delete the `ancestor == baseBranch` special case for the same reason.
- Audit all uses of `graph.Contains` to confirm none relied on "not in graph" as a proxy for "is the base."

**Edge cases:**

- Only the base branch exists → merge-base is `baseHead`, log output is empty or a single commit. Graph ends up with `baseHead` in it, no other commits. OK.
- Disjoint histories → `git merge-base --octopus` fails. Propagate the error with context.

Phase 1 is behavior-preserving for existing tests: the discovery code doesn't yet use the new base commits; they're just loaded.

### 5.2 Phase 2 — Graph-first discovery

#### 5.2.1 Rule

For every `(parent, child)` relationship the engine resolves:

1. If the graph gives an unambiguous answer, use it. Overwrite any disagreeing config.
2. The graph is ambiguous only in two cases:
   - **Case 1 — co-located branches:** two or more branches point to the same commit.
   - **Case 2 — divergence:** the declared parent has commits the child does not (parent advanced after the child was branched).
3. In ambiguous cases, consult `stackParent` config. If config names a candidate that is plausible given the graph (same-commit sibling in case 1; branch sharing ancestry in case 2), use it.
4. If multiple graph-direct children remain *after* config tiebreaking, prompt the user (existing behavior).
5. Co-located branches with no disambiguating config are treated as siblings. No prompt.

#### 5.2.2 Rename: `traceAncestors` → `traceChainTo`

The function returns the chain from `baseBranch` up to and including its argument. The current name is misleading (a branch is not its own ancestor). Rename to `traceChainTo(target string)` and update its two callers (`DiscoverStack`, `Parent`) and tests. No behavioral change.

#### 5.2.3 `traceChainTo` (formerly `traceAncestors`)

Algorithm:

1. Start at `target`'s head. Walk first-parent commits.
2. At each commit, enumerate all branches with their HEAD at that commit. Append each to the chain (order among siblings: if one of them is `target` and another is named by `target.stackParent`, place the configured parent immediately below `target`; otherwise, siblings are skipped except for `target` itself).
3. Stop when the walk reaches `baseHead` (now loaded).
4. Reverse the chain so it's bottom-to-top.
5. **Divergence recovery.** If the bottom of the reversed chain is not `baseBranch`, the walk missed branches due to case-2 divergence. Walk `stackParent` config from the bottom-most collected branch until reaching `baseBranch` or giving up; prepend the recovered segment.
6. Prepend `baseBranch` itself.
7. Persist: for every adjacent pair `(child, parent)` in the final chain, call `persistParent(child, parent)` (see §5.2.6).

Error surface: if neither graph nor config reaches the base, return an error naming the orphan branch.

#### 5.2.4 `directChildren(parent)`

Algorithm:

1. **Graph-above set.** For each branch `B ≠ parent, B ≠ baseBranch`: if `IsAncestor(parentHead, B.head)` and `B.head != parentHead`, add `B` to the set.
2. **Co-location handling (case 1).** For any pair of branches in the set sharing a HEAD: if one's `stackParent` names the other, the named one stays; the other demotes (it's a child of its config-parent, not of `parent`). If no config relation exists, both remain — they are siblings.
3. **Graph-direct filter.** Drop any candidate `C` for which another candidate `D` sits strictly between `parent` and `C` (i.e., `D` is above `parent` and `C` is above `D`). Same logic as today, minus the base-boundary carve-out.
4. **Divergence recovery (case 2).** For every branch whose `stackParent` config names `parent` but which is not in the current direct-children set, add it. These are branches that were children of `parent` before `parent` advanced past them.
5. **Persist.** For every branch in the final direct-children set, `persistParent(child, parent)`.

**Edge case — child above a co-located pair.** If `X` and `Y` share a HEAD and branch `T` sits above them, the graph sees `T` as a direct child of both `X` and `Y`. This would duplicate `T` in the tree. Resolution: if `T.stackParent` names one of them, use it. Otherwise, assign `T` deterministically to the alphabetically-first co-located branch, and let `persistParent` write the choice so subsequent runs are unambiguous.

The removed piece: today's "config-exclude" step that drops a candidate if its config points elsewhere. Under graph-first, the graph's unambiguous answer stands; stale config is repaired by the persist step.

#### 5.2.5 `Parent(branch)` and `IsBranchDescendant(ancestor, descendant)`

- **`Parent`**: drop the config-first short-circuit. Call `traceChainTo(branch)` and return the second-to-last entry (or an error if the chain has length < 2). Config-driven correctness now flows through `traceChainTo` uniformly.

- **`IsBranchDescendant`**: returns true if either:
  - `IsAncestor(ancestorHead, descHead)` — standard graph case, OR
  - `descendant`'s `stackParent` config chain reaches `ancestor` — covers divergence.

  This is a stack-tree query, not a graph-primitive query. Update the doc comment to say "below ancestor in the stack tree." Callers (specifically `Move`'s cycle check in `stack.go:185`) depend on the stack-tree semantics for correctness.

#### 5.2.6 Config persistence

Single helper:

```go
func (e *Engine) persistParent(child, parent string) {
    _ = e.git.SetStackParent(child, parent)
}
```

Called from:
- `directChildren`, for every branch in the final direct-children set.
- `traceChainTo`, for every adjacent pair in the final chain.

Option A semantics: always write. Repairs stale configs automatically; refreshes the in-memory cache; turns divergence into a resolvable situation on the next run.

### 5.3 SPEC.md updates

- **§4.2 Discovery Engine**:
  - Rewrite "Upward Trace" to describe the graph-first walk with per-commit branch enumeration, config tiebreak for co-located branches, and divergence recovery.
  - Rewrite "Downward Trace" to describe graph-above-set filtering with co-location handling and divergence recovery via config.
  - Add a "Persistence" subsection documenting `branch.<name>.stackParent` write policy and the cases where it's consulted.
- **§7 Out of Scope**:
  - Remove "Persisting user choices (e.g. disambiguation selections) across invocations." We now persist `stackParent` on every discovery.

## 6. Testing

Additive to existing `engine_test.go`:

1. **Base advancement.** Stack `main → feat-1 → feat-2`. Advance `main` past the fork point. `traceChainTo(feat-2)` returns `[main, feat-1, feat-2]` via config recovery; `BuildTree` shows `feat-2` under `main`.

2. **Co-located siblings (case 1, no config).** `feat-1` and `feat-2` both at the same commit above `main`, no `stackParent` between them. `directChildren(main)` returns both as siblings. `traceChainTo(feat-2)` does not include `feat-1`.

3. **Co-located, config disambiguates.** Same topology, `feat-2.stackParent = feat-1`. `directChildren(main)` returns only `feat-1`. `traceChainTo(feat-2)` returns `[main, feat-1, feat-2]`.

4. **Divergent parent (case 2).** `feat-1` off `main`, `feat-2` off `feat-1`, advance `feat-1`. With `feat-2.stackParent = feat-1`: `traceChainTo(feat-2) == [main, feat-1, feat-2]`, `directChildren(feat-1)` includes `feat-2`, `IsBranchDescendant(feat-1, feat-2)` is true.

5. **Stale config overridden.** `feat-2.stackParent = main` in config, but graph unambiguously shows `feat-2` above `feat-1` above `main`. `Parent(feat-2)` returns `feat-1`; the persisted config is rewritten to `feat-1`.

6. **Ancestor persistence.** Fresh repo, no config. Run `DiscoverStack(feat-2)`. Afterwards, `stackParent` is set for every pair in the ancestor chain.

7. **Phase-1 regression.** Existing `TestBuildTree_*` cases pass unchanged after the graph expansion.

8. **Cycle detection with divergence.** Stack `main → feat-1 → feat-2`, advance `feat-1`, then attempt `Move(feat-1, feat-2)`. Must fail with a cycle error (validates the `IsBranchDescendant` change).

9. **Child above co-located pair.** `X` and `Y` at the same commit above `main`, `T` branched from `X` with one new commit. Assert `T` appears exactly once in `BuildTree()`, under `X` (alphabetical default) or under whichever `T.stackParent` names.

Integration test (optional, if the existing suite supports it): shell script creating a real repo, advancing `main`, and asserting `git-stack view` shows the correct tree.

## 7. Risks

- **Merge-base performance.** `git merge-base --octopus` on many branch heads is `O(n·commits)`. For reasonable repo sizes (low hundreds of local branches) this is negligible; flag if we ever see slowdowns in the wild.
- **Disjoint histories.** Repos with branches that don't share history with the base will hit errors from `merge-base --octopus`. Today's behavior on such repos is already broken in different ways; the error message must name the offending branches clearly.
- **Stale config left behind by deleted branches.** Branches deleted outside the tool leave orphan `branch.<name>.stackParent` entries. Not introduced by this change, but worth mentioning as a known limitation.

## 8. Rollout

Two PRs, one per phase. Phase 1 ships first and should produce no user-visible behavior change. Phase 2 ships once Phase 1 has landed and been exercised in normal use.
