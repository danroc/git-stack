# Graph-First Stack Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the graph authoritative for stack discovery, with `stackParent` config acting as a tiebreaker only in the two specific ambiguous cases (co-located branches; parent divergence). Expand the loaded graph to include base-branch commits down to the merge-base of all branches.

**Architecture:** Two-phase rewrite inside `pkg/git/graph.go` and `pkg/discovery/engine.go`. Phase 1 expands graph loading and changes `Graph.branchAt` to support multiple branches per commit; it is behavior-preserving for existing tests. Phase 2 rewrites ancestor and descendant resolution to be graph-first with config tiebreak, renames `traceAncestors` to `traceChainTo`, adds a `persistParent` helper that writes `branch.*.stackParent` after every discovery, and updates tests and `SPEC.md`.

**Tech Stack:** Go 1.22+, standard library only (the binary depends on `spf13/cobra` but this change area does not). All git operations go through `os/exec` via `pkg/git.Client`. Tests use real `git init` temp repos for anything that needs git behavior, and in-memory `git.NewGraph` for pure-graph logic.

**Design reference:** `docs/superpowers/specs/2026-04-20-graph-first-discovery-design.md`

---

## Preconditions

- [ ] Working tree is clean or any pending changes are unrelated to discovery.
- [ ] `make test` passes on `main` before starting.
- [ ] You understand the design spec linked above — re-read §5.1 and §5.2 if anything below is unclear.

---

## Phase 1 — Graph Loading

### Task 1: Support multiple branches per commit in `Graph.branchAt`

**Goal:** Change `Graph.branchAt` from `map[string]string` to `map[string][]string` so that two branches sharing a HEAD are both retained. This is a prerequisite for case-1 handling in Phase 2.

**Files:**
- Modify: `pkg/git/graph.go` (struct field, `NewGraph`, `BranchAt`, `buildGraph`)
- Modify: `pkg/git/graph_test.go` (fixture, `TestGraph_BranchAt`)
- Modify: `pkg/discovery/engine.go` (one caller at `traceAncestors` line ~132)

- [ ] **Step 1: Write a failing test for multi-branch BranchAt**

Append to `pkg/git/graph_test.go`:

```go
func TestGraph_BranchAt_MultipleBranches(t *testing.T) {
	g := NewGraph(
		map[string][]string{"c1": {"c0"}},
		map[string]string{
			"main":   "c0",
			"feat-a": "c1",
			"feat-b": "c1", // same HEAD as feat-a
		},
	)
	branches, ok := g.BranchAt("c1")
	if !ok {
		t.Fatal("expected c1 to have branches")
	}
	if len(branches) != 2 {
		t.Fatalf("got %d branches at c1, want 2: %v", len(branches), branches)
	}
	// Names returned in any order; check set membership.
	seen := map[string]bool{branches[0]: true, branches[1]: true}
	if !seen["feat-a"] || !seen["feat-b"] {
		t.Errorf("got %v, want {feat-a, feat-b}", branches)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/git/ -run TestGraph_BranchAt_MultipleBranches -v`
Expected: FAIL — the current `BranchAt` returns `(string, bool)`, so the test won't even compile yet. That's the correct failing state.

- [ ] **Step 3: Change `branchAt` field type and `NewGraph`**

In `pkg/git/graph.go`, change the `Graph` struct and `NewGraph` constructor:

```go
type Graph struct {
	parents  map[string][]string // commit_hash → parent_hashes
	heads    map[string]string   // branch_name → commit_hash
	branchAt map[string][]string // commit_hash → branch_names (sorted)
}

// NewGraph constructs a Graph from raw commit data. When two branches share a
// HEAD, both are retained in branchAt at that commit, sorted alphabetically.
func NewGraph(parents map[string][]string, heads map[string]string) *Graph {
	branchAt := make(map[string][]string, len(heads))
	for branch, hash := range heads {
		branchAt[hash] = append(branchAt[hash], branch)
	}
	for _, names := range branchAt {
		slices.Sort(names)
	}
	return &Graph{parents: parents, heads: heads, branchAt: branchAt}
}
```

Import `slices` at the top of `graph.go` if it isn't already there (it is already imported).

- [ ] **Step 4: Change `BranchAt` signature**

Replace the existing `BranchAt` method in `pkg/git/graph.go`:

```go
// BranchAt returns all branches whose HEAD is at hash, sorted alphabetically.
// Returns (nil, false) when no branch points at hash.
func (g *Graph) BranchAt(hash string) ([]string, bool) {
	branches, ok := g.branchAt[hash]
	return branches, ok
}
```

- [ ] **Step 5: Update `buildGraph` to populate the new shape**

In `pkg/git/graph.go` `buildGraph`, change the population loop from:

```go
for branch, hash := range heads {
    graph.branchAt[hash] = branch
}
```

to:

```go
for branch, hash := range heads {
    graph.branchAt[hash] = append(graph.branchAt[hash], branch)
}
for _, names := range graph.branchAt {
    slices.Sort(names)
}
```

- [ ] **Step 6: Update the `linearGraph` fixture in `graph_test.go`**

Replace the literal initializer for `branchAt` in the `linearGraph` helper:

```go
branchAt: map[string][]string{
    "c0": {"main"},
    "c1": {"feat-1"},
    "c2": {"feat-2"},
},
```

- [ ] **Step 7: Update `TestGraph_BranchAt` to the new signature**

Replace the existing `TestGraph_BranchAt` body:

```go
func TestGraph_BranchAt(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		hash string
		want []string
		ok   bool
	}{
		{"c1", []string{"feat-1"}, true},
		{"c2", []string{"feat-2"}, true},
		{"c99", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.hash, func(t *testing.T) {
			got, ok := g.BranchAt(tt.hash)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, b := range tt.want {
				if got[i] != b {
					t.Errorf("[%d] = %q, want %q", i, got[i], b)
				}
			}
		})
	}
}
```

- [ ] **Step 8: Update the one caller in `engine.go` (`traceAncestors`)**

The existing call at `pkg/discovery/engine.go` around line 132 is:

```go
if branch, ok := e.graph.BranchAt(commit); ok {
    chain = append(chain, Branch{Name: branch, Head: commit})
}
```

Change to:

```go
if branches, ok := e.graph.BranchAt(commit); ok {
    for _, branch := range branches {
        chain = append(chain, Branch{Name: branch, Head: commit})
    }
}
```

This is a temporary shim — Phase 2 Task 8 will re-shape the enumeration logic. For now it preserves current behavior for single-branch commits and returns all co-located branches (sorted alphabetically) when multiple share a HEAD.

- [ ] **Step 9: Run the new test to verify it passes**

Run: `go test ./pkg/git/ -run TestGraph_BranchAt_MultipleBranches -v`
Expected: PASS.

- [ ] **Step 10: Run the full test suite**

Run: `make test`
Expected: PASS. If `TestBuildTree_CoEqualBranches` or related tests fail, that's a regression in Step 8 — re-check the enumeration order.

- [ ] **Step 11: Commit**

```bash
git add pkg/git/graph.go pkg/git/graph_test.go pkg/discovery/engine.go
git commit -m "refactor(git): support multiple branches per commit in Graph"
```

---

### Task 2: Add `MergeBaseOctopus` helper on `git.Client`

**Goal:** Expose `git merge-base --octopus <refs...>` so `buildGraph` can compute the floor commit.

**Files:**
- Modify: `pkg/git/git.go` (new method at the bottom)
- Modify: `pkg/git/git_test.go` (create if it doesn't exist — check first)

- [ ] **Step 1: Check whether `pkg/git/git_test.go` exists**

Run: `ls pkg/git/`
Expected output includes `git_test.go` or does not. If it does not exist, the test file will be created in Step 2.

- [ ] **Step 2: Write a failing test using a real temp repo**

Create or append to `pkg/git/git_test.go`:

```go
package git

import (
	"os/exec"
	"testing"
)

// initRepo initializes a temp git repo and returns a client. Configures user
// identity so commits work under CI.
func initRepo(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "c0")
	return NewClient(dir), dir
}

// runGit runs a git command in dir and fails the test on error. Returns trimmed
// stdout.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestMergeBaseOctopus_LinearHistory(t *testing.T) {
	c, dir := initRepo(t)
	// Create two branches off the same commit.
	c0 := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "checkout", "-q", "-b", "feat-a")
	runGit(t, dir, "commit", "--allow-empty", "-m", "a1")
	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "checkout", "-q", "-b", "feat-b")
	runGit(t, dir, "commit", "--allow-empty", "-m", "b1")

	base, err := c.MergeBaseOctopus("main", "feat-a", "feat-b")
	if err != nil {
		t.Fatal(err)
	}
	// main is still at c0; its head equals the merge-base.
	if base+"\n" != c0 && base != trimNL(c0) {
		t.Errorf("merge-base = %q, want %q", base, trimNL(c0))
	}
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
```

If `pkg/git/git_test.go` already has a `package git` header, skip the header in the append.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./pkg/git/ -run TestMergeBaseOctopus -v`
Expected: FAIL — `MergeBaseOctopus` is undefined (compile error is the failing state).

- [ ] **Step 4: Implement `MergeBaseOctopus` on `Client`**

Append to `pkg/git/git.go`:

```go
// MergeBaseOctopus returns the best common ancestor of two or more refs, using
// the octopus algorithm (same semantics as `git merge-base --octopus`). Returns
// an error if any two refs have disjoint histories.
func (g *Client) MergeBaseOctopus(refs ...string) (string, error) {
	if len(refs) == 0 {
		return "", fmt.Errorf("MergeBaseOctopus: no refs provided")
	}
	args := append([]string{"merge-base", "--octopus"}, refs...)
	return g.run(args...)
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./pkg/git/ -run TestMergeBaseOctopus -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/git/git.go pkg/git/git_test.go
git commit -m "feat(git): add MergeBaseOctopus client method"
```

---

### Task 3: Expand `buildGraph` to include base commits down to the floor

**Goal:** Change the log range so the graph contains commits from every branch head down to the octopus merge-base of all heads (including the base head), inclusive of the floor commit.

**Files:**
- Modify: `pkg/git/graph.go` (`buildGraph`)
- Modify: `pkg/git/graph_test.go` (new integration test using a real repo)

- [ ] **Step 1: Write a failing test that creates a diverged base**

Append to `pkg/git/graph_test.go`:

```go
import (
	"os/exec"
	"strings"
	"testing"
)

// initGraphRepo initializes a temp repo identical to initRepo but scoped to
// graph tests. Duplicated to keep git package test helpers non-exported.
func initGraphRepo(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "c0")
	return NewClient(dir), dir
}

func rev(t *testing.T, dir, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", ref)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s: %v\n%s", ref, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestLoadGraph_IncludesBaseDownToFloor verifies the loaded graph contains
// base-branch commits down to the octopus merge-base (inclusive).
func TestLoadGraph_IncludesBaseDownToFloor(t *testing.T) {
	c, dir := initGraphRepo(t)
	gi := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Layout: c0 (main@fork) ─ m1 ─ m2 (main@tip)
	//                       └ f1 (feat-1@tip)
	fork := rev(t, dir, "HEAD") // c0
	gi("checkout", "-q", "-b", "feat-1")
	gi("commit", "--allow-empty", "-m", "f1")
	featTip := rev(t, dir, "HEAD")
	gi("checkout", "-q", "main")
	gi("commit", "--allow-empty", "-m", "m1")
	gi("commit", "--allow-empty", "-m", "m2")
	mainTip := rev(t, dir, "HEAD")

	graph, err := c.LoadGraph("main")
	if err != nil {
		t.Fatal(err)
	}

	// Floor commit (c0) must now be contained.
	if !graph.Contains(fork) {
		t.Errorf("graph must contain floor commit %s (fork point)", fork)
	}
	// Base-above-floor commits (m1, m2) must be contained.
	if !graph.Contains(mainTip) {
		t.Errorf("graph must contain main tip %s", mainTip)
	}
	// Feature tip must be contained.
	if !graph.Contains(featTip) {
		t.Errorf("graph must contain feat-1 tip %s", featTip)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/git/ -run TestLoadGraph_IncludesBaseDownToFloor -v`
Expected: FAIL with `graph must contain floor commit …` — the current log range excludes anything reachable from main, so `c0` is not in `parents`.

- [ ] **Step 3: Rewrite `buildGraph` to compute the floor and expand the log range**

Replace the body of `buildGraph` in `pkg/git/graph.go`:

```go
func (g *Client) buildGraph(
	baseBranch string,
	heads map[string]string,
) (*Graph, error) {
	graph := &Graph{
		parents:  make(map[string][]string),
		heads:    heads,
		branchAt: make(map[string][]string),
	}
	if len(heads) == 0 {
		return graph, nil
	}

	for branch, hash := range heads {
		graph.branchAt[hash] = append(graph.branchAt[hash], branch)
	}
	for _, names := range graph.branchAt {
		slices.Sort(names)
	}

	// Compute the floor: the merge-base of every branch head (including the base
	// branch). Commits at and above the floor are loaded into the graph.
	refs := slices.Sorted(maps.Values(heads))
	floor, err := g.MergeBaseOctopus(refs...)
	if err != nil {
		return nil, fmt.Errorf("computing graph floor: %w", err)
	}

	// Determine whether the floor has a parent to anchor ^<floor>^. A root commit
	// has no parents, in which case we drop the exclusion.
	hasParent, err := g.commitHasParent(floor)
	if err != nil {
		return nil, fmt.Errorf("inspecting floor parent: %w", err)
	}

	args := []string{"log", "--format=%H %P"}
	args = append(args, refs...)
	if hasParent {
		args = append(args, "^"+floor+"^")
	}

	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}
		graph.parents[parts[0]] = parts[1:]
	}
	return graph, nil
}
```

Add `"fmt"` to imports in `graph.go` if missing.

- [ ] **Step 4: Add the `commitHasParent` helper on `Client`**

Append to `pkg/git/git.go`:

```go
// commitHasParent reports whether hash has at least one parent commit.
func (g *Client) commitHasParent(hash string) (bool, error) {
	out, err := g.run("rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return false, err
	}
	// Output is "<hash> <parent> [<parent>...]" or "<hash>" for a root commit.
	return len(strings.Fields(out)) > 1, nil
}
```

- [ ] **Step 5: Run the new test to verify it passes**

Run: `go test ./pkg/git/ -run TestLoadGraph_IncludesBaseDownToFloor -v`
Expected: PASS.

- [ ] **Step 6: Run the full git package tests**

Run: `go test ./pkg/git/... -v`
Expected: PASS for all. The floor now sits at `c0` for the standard linear fixture; since the fixture is in-memory (no real repo, no call to `buildGraph`), nothing breaks there.

- [ ] **Step 7: Run the full suite**

Run: `make test`
Expected: PASS. The in-memory test graphs in `pkg/discovery/` bypass `buildGraph`, so the discovery tests are unaffected at this stage.

- [ ] **Step 8: Commit**

```bash
git add pkg/git/graph.go pkg/git/graph_test.go pkg/git/git.go
git commit -m "feat(git): load graph down to merge-base of all branches"
```

---

### Task 4: Remove base-head special cases from `engine.go`

**Goal:** Since `baseHead` is now always in the loaded graph, the `parentHead == baseHead` carve-out in `isAbove` and the `ancestor == baseBranch` carve-out in `IsBranchDescendant` are no longer needed. Removing them simplifies the code and removes a source of divergent behavior.

**Files:**
- Modify: `pkg/discovery/engine.go` (`isAbove`, `IsBranchDescendant`)

- [ ] **Step 1: Simplify `isAbove`**

In `pkg/discovery/engine.go`, replace `isAbove` (currently around line 218):

```go
// isAbove reports whether candidateHead is strictly above parentHead in the
// commit graph (ancestor-of, and not equal).
func (e *Engine) isAbove(parentHead, candidateHead string) bool {
	if candidateHead == parentHead {
		return false
	}
	return e.graph.IsAncestor(parentHead, candidateHead)
}
```

- [ ] **Step 2: Simplify `IsBranchDescendant`**

Replace `IsBranchDescendant` (currently around line 343):

```go
// IsBranchDescendant reports whether descendant is strictly above ancestor in
// the commit graph.
func (e *Engine) IsBranchDescendant(ancestor, descendant string) bool {
	ancestorHead, ok := e.graph.HeadOf(ancestor)
	if !ok {
		return false
	}
	descHead, ok := e.graph.HeadOf(descendant)
	if !ok {
		return false
	}
	if ancestorHead == descHead {
		return false
	}
	return e.graph.IsAncestor(ancestorHead, descHead)
}
```

Note: the stack-tree fallback (config chain) lands in Task 11. For now this is a pure graph primitive.

- [ ] **Step 3: Run the discovery tests**

Run: `go test ./pkg/discovery/... -v`
Expected: PASS. Existing tests use in-memory graphs that include intermediate commits but not `c0`; `TestIsBranchDescendant` still passes because the in-memory graph's `c1`/`c2` are above `c0` via the existing parent chain, and `IsAncestor` on those commits still works.

If tests related to `isAbove` fail (they shouldn't since test fixtures don't trigger the `parentHead == baseHead` branch), re-verify the replacement.

- [ ] **Step 4: Commit**

```bash
git add pkg/discovery/engine.go
git commit -m "refactor(discovery): remove base-head carve-outs from graph helpers"
```

---

### Task 5: Phase 1 regression sweep

**Goal:** Full-suite green + a manual smoke test that nothing user-visible changed.

- [ ] **Step 1: Run all tests**

Run: `make test`
Expected: PASS across the board.

- [ ] **Step 2: Run linters**

Run: `make lint`
Expected: no errors. Fix any formatting/lint issues inline.

- [ ] **Step 3: Build the binary**

Run: `make build`
Expected: builds to `dist/git-stack`.

- [ ] **Step 4: Smoke-test `git-stack view` in a temp repo**

In a throwaway shell:

```bash
cd $(mktemp -d) && git init -q -b main
git commit --allow-empty -m c0
git checkout -q -b feat-1 && git commit --allow-empty -m f1
git checkout -q main && git commit --allow-empty -m m1
/Users/daniel/code/github.com/danroc/git-stack/dist/git-stack view
```

Expected: tree renders; no crash. Exact output under Phase 1 should match pre-change behavior (base divergence isn't yet surfaced — that's Phase 2's job).

- [ ] **Step 5: Confirm Phase 1 is clean**

No commit here — Phase 1 is complete. Proceed to Phase 2.

---

## Phase 2 — Graph-First Discovery

### Task 6: Rename `traceAncestors` → `traceChainTo`

**Goal:** Pure rename to align the identifier with the semantics (returns the full chain from base up to and including the target branch). No behavior change.

**Files:**
- Modify: `pkg/discovery/engine.go` (function, doc comment, two internal callers)
- Modify: `pkg/discovery/engine_test.go` (one caller)

- [ ] **Step 1: Rename the function and update its doc comment**

In `pkg/discovery/engine.go`, replace the `traceAncestors` declaration (and its doc comment above it). Rename the argument from `currentBranch` to `target` for consistency:

```go
// traceChainTo returns the chain from baseBranch (inclusive) up to target
// (inclusive), bottom-to-top. It walks the first-parent commit chain for the
// primary trace, then falls back to stackParent config for any branches the
// graph walk missed due to divergence.
func (e *Engine) traceChainTo(target string) ([]Branch, error) {
```

Inside the body, rename every `currentBranch` local to `target`.

- [ ] **Step 2: Update the two callers in `engine.go`**

In `DiscoverStack` (around line 98):

```go
ancestors, err := e.traceChainTo(currentBranch)
```

In `Parent` (around line 332):

```go
ancestors, err := e.traceChainTo(branch)
```

- [ ] **Step 3: Update the test caller**

In `pkg/discovery/engine_test.go` `TestTraceAncestors`, rename the test and the call:

```go
func TestTraceChainTo(t *testing.T) {
    // ...
    got, err := e.traceChainTo(tt.current)
    // ...
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./pkg/discovery/... -v`
Expected: PASS. Pure rename — no behavior change.

- [ ] **Step 5: Commit**

```bash
git add pkg/discovery/engine.go pkg/discovery/engine_test.go
git commit -m "refactor(discovery): rename traceAncestors to traceChainTo"
```

---

### Task 7: Introduce `persistParent` and wire it into existing `directChildren`

**Goal:** Centralize the "always write `stackParent`" step into a single helper, and switch the current `directChildren` write site to use it. No semantic change yet; this prepares the code for Task 8 and Task 9 to share one write path.

**Files:**
- Modify: `pkg/discovery/engine.go`

- [ ] **Step 1: Add the helper near the bottom of `engine.go`**

Append at the end of `pkg/discovery/engine.go`:

```go
// persistParent records child's immediate stack parent in git config. Errors
// are silently swallowed: config is a hint, never a hard dependency.
func (e *Engine) persistParent(child, parent string) {
	_ = e.git.SetStackParent(child, parent)
}
```

- [ ] **Step 2: Replace the existing write loop in `directChildren`**

The current loop (around line 319):

```go
for _, child := range direct {
    _ = e.git.SetStackParent(child, parent)
}
```

becomes:

```go
for _, child := range direct {
    e.persistParent(child, parent)
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./pkg/discovery/... -v`
Expected: PASS — no behavior change.

- [ ] **Step 4: Commit**

```bash
git add pkg/discovery/engine.go
git commit -m "refactor(discovery): extract persistParent helper"
```

---

### Task 8: Multi-branch ancestor walk in `traceChainTo`

**Goal:** When walking the first-parent chain, enumerate all branches at each commit; include a co-located sibling in `target`'s chain only when `target.stackParent` explicitly names it. Also write `persistParent` for every adjacent pair in the final chain.

Termination: stop when the first-parent walk falls off the loaded graph (i.e., `FirstParent` returns `ok=false` for an in-graph commit, or the commit is no longer in the graph). This covers reaching `baseHead`, reaching the merge-base floor (base-diverged case), and root commits uniformly.

**Files:**
- Modify: `pkg/discovery/engine.go` (`traceChainTo`)
- Modify: `pkg/discovery/engine_test.go` (new test for co-located config-parent)

- [ ] **Step 1: Write the failing test for co-located, configured sibling**

Append to `pkg/discovery/engine_test.go`:

```go
// TestTraceChainTo_CoLocatedConfiguredSibling verifies that when two branches
// share a HEAD and config says one is the stack parent of the other, the
// configured parent appears in the traced chain immediately below the target.
func TestTraceChainTo_CoLocatedConfiguredSibling(t *testing.T) {
	// main(c0) ← feat-1(c1)
	//                  feat-2(c1)  // same commit as feat-1
	g := git.NewGraph(
		map[string][]string{"c1": {"c0"}},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c1",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(chain))
	for i, b := range chain {
		got[i] = b.Name
	}
	want := []string{"main", "feat-1", "feat-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestTraceChainTo_CoLocatedSiblingsByDefault verifies that without a config
// hint, a co-located sibling does NOT appear in the target's chain (they are
// siblings, not parent/child).
func TestTraceChainTo_CoLocatedSiblingsByDefault(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c1": {"c0"}},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c1",
		},
	)
	e := newTestEngine(t, g, "main")

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range chain {
		if b.Name == "feat-1" {
			t.Errorf("feat-1 should not appear in feat-2's chain (siblings)")
		}
	}
	// The chain should be [main, feat-2].
	if len(chain) != 2 || chain[0].Name != "main" || chain[1].Name != "feat-2" {
		names := make([]string, len(chain))
		for i, b := range chain {
			names[i] = b.Name
		}
		t.Errorf("got %v, want [main feat-2]", names)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./pkg/discovery/ -run TestTraceChainTo_ -v`
Expected: `TestTraceChainTo_CoLocatedConfiguredSibling` FAILS (feat-1 isn't included); `TestTraceChainTo_CoLocatedSiblingsByDefault` may pass or fail depending on current enumeration order — the goal is that both pass after Step 3.

- [ ] **Step 3: Rewrite `traceChainTo` with per-commit enumeration**

Replace the body of `traceChainTo` in `pkg/discovery/engine.go`:

```go
func (e *Engine) traceChainTo(target string) ([]Branch, error) {
	baseHead, ok := e.graph.HeadOf(e.baseBranch)
	if !ok {
		return nil, fmt.Errorf("base branch %q not found in graph", e.baseBranch)
	}
	targetHead, ok := e.graph.HeadOf(target)
	if !ok {
		return nil, fmt.Errorf("branch %q not found in graph", target)
	}

	if target == e.baseBranch {
		return []Branch{{Name: e.baseBranch, Head: baseHead}}, nil
	}

	// Walk first-parent from target. At each commit, enumerate branches at that
	// commit; only include target and its configured co-located parent.
	targetConfigParent, _ := e.git.GetStackParent(target)

	var chain []Branch
	for commit := targetHead; e.graph.Contains(commit); {
		branches, ok := e.graph.BranchAt(commit)
		if ok {
			for _, name := range branches {
				switch {
				case name == target:
					chain = append(chain, Branch{Name: name, Head: commit})
				case commit == targetHead && name == targetConfigParent:
					// Configured parent co-located with target.
					chain = append(chain, Branch{Name: name, Head: commit})
				case commit == targetHead:
					// Co-located sibling, not the configured parent → skip.
					continue
				default:
					// Below target's head: ordinary ancestor on the chain.
					chain = append(chain, Branch{Name: name, Head: commit})
				}
			}
		}
		parent, ok := e.graph.FirstParent(commit)
		if !ok {
			break
		}
		commit = parent
	}

	// chain is top-to-bottom; reverse to bottom-to-top.
	slices.Reverse(chain)

	// If the walk did not bottom out at baseBranch (base-diverged case or a
	// diverged intermediate branch), recover via config chain.
	var bottomName string
	if len(chain) > 0 {
		bottomName = chain[0].Name
	} else {
		bottomName = target
	}
	if bottomName != e.baseBranch {
		recovered := e.recoverViaConfig(bottomName)
		chain = append(recovered, chain...)
	}

	// Ensure baseBranch sits at the bottom.
	if len(chain) == 0 || chain[0].Name != e.baseBranch {
		chain = append([]Branch{{Name: e.baseBranch, Head: baseHead}}, chain...)
	}

	// Sanity check: chain must start at baseBranch.
	if chain[0].Name != e.baseBranch {
		return nil, fmt.Errorf("branch %q is not connected to base %q", target, e.baseBranch)
	}

	// Persist every adjacent (child, parent) pair.
	for i := 1; i < len(chain); i++ {
		e.persistParent(chain[i].Name, chain[i-1].Name)
	}

	return chain, nil
}

// recoverViaConfig walks branch's stackParent config chain until it reaches the
// base branch or a branch whose parent is unknown. Returned slice is
// bottom-to-top and excludes branch itself.
func (e *Engine) recoverViaConfig(branch string) []Branch {
	var chain []Branch
	seen := map[string]bool{branch: true}
	for current := branch; ; {
		parent, ok := e.git.GetStackParent(current)
		if !ok || seen[parent] {
			return chain
		}
		seen[parent] = true
		parentHead, _ := e.graph.HeadOf(parent)
		chain = append([]Branch{{Name: parent, Head: parentHead}}, chain...)
		if parent == e.baseBranch {
			return chain
		}
		current = parent
	}
}
```

Note: `recoverViaConfig` returns only the branches *below* the bottom; the bottom branch itself stays in `chain` (it was put there by the graph walk, or — if the graph walk found nothing — the caller falls through to the "prepend baseBranch" step after a no-op recovery).

Also remove the old divergence-recovery code at the bottom of the previous `traceChainTo` (the `fallback` loop using `e.git.GetStackParent(branch)` and `slices.Reverse(fallback)`). The new `recoverViaConfig` replaces it.

- [ ] **Step 4: Handle the edge case where the graph walk finds no branches at all**

If `target` is absent from the graph (shouldn't happen — we checked `HeadOf(target)` above) or its first-parent chain has no branches besides base, the chain might be empty before recovery. The algorithm above handles this: `bottomName` defaults to `target`, `recoverViaConfig(target)` walks up from `target` via config, and the final prepend ensures base is at the bottom. Verify this path is correct by running tests below.

- [ ] **Step 5: Run the new tests to verify they pass**

Run: `go test ./pkg/discovery/ -run TestTraceChainTo_ -v`
Expected: both tests PASS.

- [ ] **Step 6: Run the full discovery suite**

Run: `go test ./pkg/discovery/... -v`
Expected: mostly PASS. `TestTraceChainTo` (the original renamed test) should still pass. Other tests may still rely on the old config-first behavior — that's Task 12's job. If a test fails because of the new persist-in-traceChainTo writes, note it and proceed; most tests should be unaffected.

- [ ] **Step 7: Commit**

```bash
git add pkg/discovery/engine.go pkg/discovery/engine_test.go
git commit -m "feat(discovery): multi-branch enumeration in traceChainTo with config recovery"
```

---

### Task 9: Rewrite `directChildren` for graph-first semantics

**Goal:** Graph is authoritative. Config is consulted only for case 1 (co-located siblings sharing a HEAD with a `stackParent` relation between them) and case 2 (divergence — branches whose config parent is `parent` but that are not in the graph-above set). Remove the current "config-excludes-branch-from-parent" gate. Handle the edge case where a child sits above a co-located pair.

**Files:**
- Modify: `pkg/discovery/engine.go` (`directChildren`)

- [ ] **Step 1: Write a failing test for case-2 divergence recovery**

Append to `pkg/discovery/engine_test.go`:

```go
// TestDirectChildren_DivergedChildViaConfig verifies that a branch whose
// stackParent config names `parent` but whose head has fallen behind `parent`
// (parent advanced) is still included as a direct child.
func TestDirectChildren_DivergedChildViaConfig(t *testing.T) {
	// main(c0) ← feat-1(c2)   [advanced: c1 was the old head]
	//                     ← feat-2(c1) [branched from feat-1's old head]
	// c2 is reachable from feat-1; c1 is NOT above c2 (feat-1 moved past it).
	g := git.NewGraph(
		map[string][]string{
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c2",
			"feat-2": "c1", // stayed behind at c1
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	got := e.directChildren("feat-1")
	found := false
	for _, b := range got {
		if b == "feat-2" {
			found = true
		}
	}
	if !found {
		t.Errorf("directChildren(feat-1) = %v, must include feat-2 via config", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/discovery/ -run TestDirectChildren_DivergedChildViaConfig -v`
Expected: FAIL — current code doesn't recognize feat-2 as a child because its head (c1) is not above feat-1's head (c2); the existing config-fallback loop only triggers when branch is not in `aboveSet` via graph, which *happens* to be true here, but its config check is `configParent == parent` — let's see: yes actually the current code *does* handle this (lines 264-275). So the test might pass under current code. In that case, substitute a different failing scenario that distinguishes old from new behavior.

If it passes: **instead** write this test (which the old config-excludes logic WILL fail):

```go
// TestDirectChildren_StaleConfigLosesToUnambiguousGraph verifies that when
// config declares a branch's parent incorrectly and the graph unambiguously
// shows a different parent, the graph wins and config gets rewritten.
func TestDirectChildren_StaleConfigLosesToUnambiguousGraph(t *testing.T) {
	// Linear: main(c0) ← feat-1(c1) ← feat-2(c2)
	// feat-2.stackParent is set to "main" (wrong); graph shows feat-2 above feat-1.
	g := linearTestGraph()
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}

	children := e.directChildren("feat-1")
	if len(children) != 1 || children[0] != "feat-2" {
		t.Errorf("directChildren(feat-1) = %v, want [feat-2]", children)
	}
	// Config should have been repaired.
	got, ok := e.git.GetStackParent("feat-2")
	if !ok || got != "feat-1" {
		t.Errorf("feat-2 stackParent = %q (ok=%v), want feat-1", got, ok)
	}
}
```

Under current code this would likely fail because the `configParent != parent` gate excludes feat-2 from feat-1's children when its config points to main.

- [ ] **Step 3: Replace `directChildren` with the graph-first algorithm**

In `pkg/discovery/engine.go`, replace the entire `directChildren` function body:

```go
// directChildren returns branches stacked directly on top of parent. The graph
// is authoritative: a branch is a direct child if it is above parent in the
// graph and no other branch sits strictly between them. Config is consulted
// only for two ambiguities:
//
//  1. Co-located branches: when two branches share a HEAD and one's
//     stackParent names the other, the named one stays as direct child of
//     parent; the other demotes to a child of its config-parent.
//  2. Divergence: when a branch's stackParent config names parent but the
//     branch is not in the graph-above set (parent has moved past it).
//
// If a branch above parent sits at a commit shared by multiple branches, the
// edge case "child above co-located pair" applies: the child is assigned to
// its configured parent if named, else to the alphabetically-first co-located
// branch, and that choice is persisted to config.
//
// Every branch in the final set has its stackParent written to config.
func (e *Engine) directChildren(parent string) []string {
	parentHead, _ := e.graph.HeadOf(parent)

	// Step 1 — graph-above set.
	above := make(map[string]bool)
	for _, branch := range e.graph.Branches() {
		if branch == parent || branch == e.baseBranch {
			continue
		}
		head, ok := e.graph.HeadOf(branch)
		if !ok {
			continue
		}
		if e.isAbove(parentHead, head) {
			above[branch] = true
		}
	}

	// Step 2 — co-location handling among branches already in the set.
	// If X and Y share a HEAD and one's stackParent names the other, demote
	// the configured-child.
	for _, b := range e.graph.Branches() {
		if !above[b] {
			continue
		}
		bHead, _ := e.graph.HeadOf(b)
		coLocated, _ := e.graph.BranchAt(bHead)
		if len(coLocated) < 2 {
			continue
		}
		configParent, ok := e.git.GetStackParent(b)
		if !ok {
			continue
		}
		// Is configParent one of the co-located branches AND currently in the set?
		if above[configParent] && configParent != b {
			delete(above, b)
		}
	}

	// Step 3 — graph-direct filter. Drop C if some D in the set sits strictly
	// between parent and C. Intermediates are checked against every branch,
	// not just members of `above`, so a non-candidate still blocks.
	direct := make(map[string]bool)
	for candidate := range above {
		cHead, _ := e.graph.HeadOf(candidate)
		isDirect := true
		for _, other := range e.graph.Branches() {
			if other == candidate || other == e.baseBranch || other == parent {
				continue
			}
			oHead, _ := e.graph.HeadOf(other)
			if oHead == parentHead || oHead == cHead {
				continue
			}
			if e.isAbove(parentHead, oHead) && e.graph.IsAncestor(oHead, cHead) {
				isDirect = false
				break
			}
		}
		if isDirect {
			direct[candidate] = true
		}
	}

	// Step 3.5 — "child above co-located pair" edge case.
	// If candidate C's first-parent chain lands on a commit shared by multiple
	// branches, C would be reported as a direct child of each of them. Assign
	// C to its configured parent if named; otherwise to the alphabetically-
	// first co-located branch (and ensure C is a direct child only of the
	// owner — but that logic lives at the caller level, BuildTree, because
	// each invocation of directChildren only sees one parent at a time).
	//
	// Here we handle it defensively: if candidate C is above multiple
	// co-located branches, and parent is one of them but not the owner per
	// config/alphabetical rule, drop C from this parent's direct set.
	for candidate := range direct {
		cHead, _ := e.graph.HeadOf(candidate)
		owner := e.coLocatedOwnerFor(candidate, cHead)
		if owner != "" && owner != parent {
			// Only exclude if `parent` is one of the co-located siblings of owner.
			ownerHead, _ := e.graph.HeadOf(owner)
			if parentHead == ownerHead {
				delete(direct, candidate)
			}
		}
	}

	// Step 4 — divergence recovery. Branches whose stackParent config names
	// parent but that aren't in the direct set.
	for _, branch := range e.graph.Branches() {
		if direct[branch] || branch == parent || branch == e.baseBranch {
			continue
		}
		configParent, ok := e.git.GetStackParent(branch)
		if ok && configParent == parent {
			direct[branch] = true
		}
	}

	// Finalize and sort.
	result := make([]string, 0, len(direct))
	for b := range direct {
		result = append(result, b)
	}
	slices.Sort(result)

	// Step 5 — persist.
	for _, child := range result {
		e.persistParent(child, parent)
	}

	return result
}

// coLocatedOwnerFor determines which co-located branch (if any) "owns"
// candidate when candidate sits above multiple branches sharing the same HEAD
// commit in candidate's first-parent chain.
//
// Returns "" if candidate's first-parent chain passes through a commit with at
// most one branch at it (no ambiguity). Otherwise returns the owning branch:
// the one named by candidate.stackParent if it is among the co-located, else
// the alphabetically-first co-located branch.
func (e *Engine) coLocatedOwnerFor(candidate, candidateHead string) string {
	configParent, hasConfig := e.git.GetStackParent(candidate)
	// Walk candidate's first-parent chain looking for a commit with multiple
	// co-located branches.
	for commit, ok := e.graph.FirstParent(candidateHead); ok; commit, ok = e.graph.FirstParent(commit) {
		branches, haveBranches := e.graph.BranchAt(commit)
		if !haveBranches {
			continue
		}
		if len(branches) < 2 {
			// Only one branch at this commit — candidate's parent is unambiguous
			// at this step. Return to look further up? No — once we find the
			// nearest branch, we stop (that's its topological parent).
			return ""
		}
		// Multiple branches at this commit. Determine the owner.
		if hasConfig {
			for _, b := range branches {
				if b == configParent {
					return b
				}
			}
		}
		// branches is sorted alphabetically by Graph.NewGraph / buildGraph.
		return branches[0]
	}
	return ""
}
```

Double-check: `coLocatedOwnerFor` above iterates from candidate's first-parent downward. The loop condition uses `FirstParent(candidateHead)` once then `FirstParent(commit)` on each iteration; ensure the loop body is correctly ordered.

- [ ] **Step 4: Run the new test to verify it passes**

Run: `go test ./pkg/discovery/ -run TestDirectChildren_StaleConfigLosesToUnambiguousGraph -v`
Expected: PASS.

- [ ] **Step 5: Run the full discovery suite (expect some pre-existing tests to break)**

Run: `go test ./pkg/discovery/... -v`
Expected: the following tests are likely to FAIL because they encode config-first behavior that this task deliberately removes:
- `TestDirectChildren_ConfigExcludedIntermediateBlocks` — config-excludes logic removed.
- `TestBuildTree_CoEqualBranches_VirtualStack` — config no longer demotes feat-1 from direct child of main.

Record which tests fail; they will be updated or replaced in Task 12. Don't commit yet.

- [ ] **Step 6: Commit the new logic (and expect test failures to be tracked)**

If the only failures are the two listed in Step 5, proceed:

```bash
git add pkg/discovery/engine.go pkg/discovery/engine_test.go
git commit -m "feat(discovery): graph-first directChildren with config tiebreak"
```

If other tests fail, pause and investigate before committing.

---

### Task 10: Simplify `Parent(branch)` to use `traceChainTo`

**Goal:** Remove the config-first short-circuit in `Parent`. Always call `traceChainTo` and return the second-to-last entry. Correct behavior for divergence flows through naturally.

**Files:**
- Modify: `pkg/discovery/engine.go` (`Parent`)

- [ ] **Step 1: Replace `Parent` body**

In `pkg/discovery/engine.go`, replace the `Parent` function:

```go
// Parent returns the immediate stack parent of branch by walking the full chain
// from base to branch via traceChainTo. Under graph-first semantics, this is
// the correct way to resolve the parent: the graph walk plus config recovery
// handles divergence and co-location ambiguities uniformly.
func (e *Engine) Parent(branch string) (string, error) {
	chain, err := e.traceChainTo(branch)
	if err != nil {
		return "", err
	}
	if len(chain) < 2 {
		return "", fmt.Errorf("branch %q has no parent in the stack", branch)
	}
	return chain[len(chain)-2].Name, nil
}
```

- [ ] **Step 2: Run the existing `Parent` tests**

Run: `go test ./pkg/discovery/ -run TestParent -v`
Expected: both `TestParent_FromConfig` and `TestParent_FromGraph` PASS. They don't distinguish which path the answer came from — both end up with `feat-1`.

- [ ] **Step 3: Commit**

```bash
git add pkg/discovery/engine.go
git commit -m "refactor(discovery): Parent() uses traceChainTo instead of config short-circuit"
```

---

### Task 11: Extend `IsBranchDescendant` with config-chain fallback

**Goal:** `IsBranchDescendant` becomes a stack-tree query. Returns true if either the graph shows ancestry OR the descendant's `stackParent` config chain reaches ancestor. This is required for correct cycle detection in `stack.Move` when branches have diverged.

**Files:**
- Modify: `pkg/discovery/engine.go` (`IsBranchDescendant` — restore stack-semantic behavior after Task 4 stripped it down to a graph primitive)
- Modify: `pkg/discovery/engine_test.go` (new test)

- [ ] **Step 1: Write a failing test**

Append to `pkg/discovery/engine_test.go`:

```go
// TestIsBranchDescendant_ViaConfigChain verifies that a diverged descendant
// still reports as a descendant via its stackParent config chain.
func TestIsBranchDescendant_ViaConfigChain(t *testing.T) {
	// main(c0) ← feat-1(c2) [advanced]
	//                   feat-2(c1) [stayed behind, config parent = feat-1]
	g := git.NewGraph(
		map[string][]string{
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c2",
			"feat-2": "c1",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}
	if !e.IsBranchDescendant("feat-1", "feat-2") {
		t.Error("feat-2 should still be a descendant of feat-1 via config chain")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/discovery/ -run TestIsBranchDescendant_ViaConfigChain -v`
Expected: FAIL — after Task 4 the function is pure-graph; feat-1's head (c2) is not an ancestor of feat-2's head (c1), so it returns false.

- [ ] **Step 3: Extend `IsBranchDescendant`**

Replace the `IsBranchDescendant` function body in `pkg/discovery/engine.go`:

```go
// IsBranchDescendant reports whether descendant is strictly below ancestor in
// the stack tree. This is true if the commit graph shows descendant above
// ancestor, OR descendant's stackParent config chain reaches ancestor.
//
// The config-chain case covers divergence: when a parent branch advances past
// a child, graph ancestry alone loses the relationship, but the stack tree
// still considers the child a descendant. Callers like stack.Move rely on this
// for cycle detection.
func (e *Engine) IsBranchDescendant(ancestor, descendant string) bool {
	ancestorHead, ok := e.graph.HeadOf(ancestor)
	if !ok {
		return false
	}
	descHead, ok := e.graph.HeadOf(descendant)
	if !ok {
		return false
	}
	if ancestorHead == descHead {
		return false
	}
	if e.graph.IsAncestor(ancestorHead, descHead) {
		return true
	}

	// Config-chain fallback: walk descendant's stackParent upward looking for
	// ancestor. Guarded against cycles by a visited set.
	visited := map[string]bool{descendant: true}
	for current := descendant; ; {
		parent, ok := e.git.GetStackParent(current)
		if !ok || visited[parent] {
			return false
		}
		if parent == ancestor {
			return true
		}
		visited[parent] = true
		current = parent
	}
}
```

- [ ] **Step 4: Run the new test to verify it passes**

Run: `go test ./pkg/discovery/ -run TestIsBranchDescendant -v`
Expected: PASS for both the new test and the original `TestIsBranchDescendant`.

- [ ] **Step 5: Commit**

```bash
git add pkg/discovery/engine.go pkg/discovery/engine_test.go
git commit -m "feat(discovery): IsBranchDescendant falls back to config chain for divergence"
```

---

### Task 12: Update pre-existing tests that encoded config-first behavior

**Goal:** The two tests flagged in Task 9 Step 5 test behaviors that this rewrite deliberately reverses. Replace them with assertions that match the new semantics, keeping coverage equivalent.

**Files:**
- Modify: `pkg/discovery/engine_test.go`

- [ ] **Step 1: Update `TestDirectChildren_ConfigExcludedIntermediateBlocks`**

The current test asserts that `feat-B.stackParent = some-other-parent` excludes feat-B from feat-A's direct children. Under graph-first, feat-B IS a direct child of feat-A, and the config gets repaired.

Replace the test:

```go
// TestDirectChildren_GraphWinsOverStaleIntermediateConfig verifies that when a
// topological intermediate has a stale stackParent config pointing elsewhere,
// the graph still places it as a direct child of its real parent, and the
// stale config is repaired.
func TestDirectChildren_GraphWinsOverStaleIntermediateConfig(t *testing.T) {
	// main(c0) ← feat-A(c1) ← feat-B(c2) ← feat-C(c3)
	g := git.NewGraph(
		map[string][]string{
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c2"},
		},
		map[string]string{
			"main":   "c0",
			"feat-A": "c1",
			"feat-B": "c2",
			"feat-C": "c3",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-B", "some-other-parent"); err != nil {
		t.Fatal(err)
	}

	got := e.directChildren("feat-A")
	if len(got) != 1 || got[0] != "feat-B" {
		t.Errorf("directChildren(feat-A) = %v, want [feat-B]", got)
	}
	// feat-C is not a direct child of feat-A: feat-B is between.
	if slices.Contains(got, "feat-C") {
		t.Errorf("feat-C must not be a direct child of feat-A")
	}
	// Stale config on feat-B was repaired.
	got2, ok := e.git.GetStackParent("feat-B")
	if !ok || got2 != "feat-A" {
		t.Errorf("feat-B stackParent = %q (ok=%v), want feat-A", got2, ok)
	}
}
```

Add `"slices"` to imports in `engine_test.go` if not already there.

- [ ] **Step 2: Update `TestBuildTree_CoEqualBranches_VirtualStack`**

The current test asserts that `feat-1.stackParent = feat-inter` creates a virtual stack where main's only direct child is feat-inter. Under graph-first, feat-inter and feat-1 are both direct children of main (they don't sit on each other's graph chain).

Replace the test:

```go
// TestBuildTree_SiblingBranches_BothDirectChildren verifies that two branches
// forking off main at the same commit and diverging are both direct children of
// main, regardless of any stackParent config (graph wins).
func TestBuildTree_SiblingBranches_BothDirectChildren(t *testing.T) {
	// main(c0) ─┬─ feat-inter(c1)
	//           └─ feat-1(c2)    ← feat-2a(c3)
	//                           ← feat-2b(c3)  (co-located with feat-2a)
	g := virtualStackTestGraph()
	e := newTestEngine(t, g, "main")
	// Set stale config that contradicts graph — must be overridden.
	if err := e.git.SetStackParent("feat-1", "feat-inter"); err != nil {
		t.Fatal(err)
	}

	root := e.BuildTree()
	names := make([]string, len(root.Children))
	for i, c := range root.Children {
		names[i] = c.Branch.Name
	}
	want := []string{"feat-1", "feat-inter"}
	if len(names) != len(want) {
		t.Fatalf("root.Children = %v, want %v", names, want)
	}
	slices.Sort(names)
	for i, w := range want {
		if names[i] != w {
			t.Errorf("[%d] = %q, want %q", i, names[i], w)
		}
	}

	// feat-1 still has feat-2a and feat-2b as children.
	var feat1 *TreeNode
	for _, c := range root.Children {
		if c.Branch.Name == "feat-1" {
			feat1 = c
		}
	}
	if feat1 == nil || len(feat1.Children) != 2 {
		t.Fatalf("feat-1 must have 2 children, got %v", feat1)
	}
}
```

- [ ] **Step 3: Update `TestDirectChildren_ConfigParentOverridesTopology`**

This test should still pass under the new design — two co-located branches with a `stackParent` relation between them trigger case-1 handling (§5.2.4 step 2) and the configured-child demotes. Verify by running it; no code change expected. If it fails, re-read the test topology carefully and adapt the expected values if they depended on the old config-first logic.

Run: `go test ./pkg/discovery/ -run TestDirectChildren_ConfigParentOverridesTopology -v`
Expected: PASS.

- [ ] **Step 4: Run the full discovery suite**

Run: `go test ./pkg/discovery/... -v`
Expected: PASS. If anything is still red, inspect and update accordingly.

- [ ] **Step 5: Commit**

```bash
git add pkg/discovery/engine_test.go
git commit -m "test(discovery): update tests for graph-first semantics"
```

---

### Task 13: Add acceptance tests for the new spec cases

**Goal:** Add tests 1, 4, 5, 6, 9, and 10 from the spec's §6 testing section that aren't yet covered by Tasks 8, 9, 11, or 12. Some require a real git repo (for `stackParent` persistence across runs).

**Files:**
- Modify: `pkg/discovery/engine_test.go`

- [ ] **Step 1: Add the diverged-parent full-suite test (spec §6 #4)**

Append to `pkg/discovery/engine_test.go`:

```go
// TestDivergedParent_FullSuite covers spec §6 #4: feat-1 advances past the point
// where feat-2 branched off it; traceChainTo, directChildren, and
// IsBranchDescendant must all still reflect the stack relationship.
func TestDivergedParent_FullSuite(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c1": {"c0"},
			"c2": {"c1"}, // feat-1 advanced here
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c2",
			"feat-2": "c1", // diverged: was at c1 when feat-1 was there
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(chain))
	for i, b := range chain {
		got[i] = b.Name
	}
	want := []string{"main", "feat-1", "feat-2"}
	if len(got) != len(want) {
		t.Fatalf("traceChainTo(feat-2) = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q, want %q", i, got[i], w)
		}
	}

	dc := e.directChildren("feat-1")
	if !slices.Contains(dc, "feat-2") {
		t.Errorf("directChildren(feat-1) = %v, must include feat-2", dc)
	}

	if !e.IsBranchDescendant("feat-1", "feat-2") {
		t.Error("feat-2 must be a descendant of feat-1")
	}
}
```

- [ ] **Step 2: Add the ancestor-persistence test (spec §6 #6)**

Append:

```go
// TestDiscoverStack_PersistsAncestorChain covers spec §6 #6: after
// DiscoverStack runs, every adjacent (child, parent) pair in the ancestor
// chain has its stackParent written to config.
func TestDiscoverStack_PersistsAncestorChain(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	_, err := e.DiscoverStack("feat-2", func(_ string, _ []string) (string, error) {
		t.Fatal("chooseBranch should not be called for linear stack")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	p1, ok := e.git.GetStackParent("feat-1")
	if !ok || p1 != "main" {
		t.Errorf("feat-1.stackParent = %q (ok=%v), want main", p1, ok)
	}
	p2, ok := e.git.GetStackParent("feat-2")
	if !ok || p2 != "feat-1" {
		t.Errorf("feat-2.stackParent = %q (ok=%v), want feat-1", p2, ok)
	}
}
```

- [ ] **Step 3: Add the child-above-co-located-pair test (spec §6 #9)**

Append:

```go
// TestBuildTree_ChildAboveCoLocatedPair covers spec §6 #9 (edge case in
// §5.2.4): when a branch T sits above two co-located branches X and Y, T must
// appear exactly once in the tree — under whichever T.stackParent names, else
// under the alphabetically-first co-located branch.
func TestBuildTree_ChildAboveCoLocatedPair(t *testing.T) {
	// main(c0) ← alpha(c1), beta(c1)  // co-located
	//                   ← tail(c2)
	g := git.NewGraph(
		map[string][]string{
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":  "c0",
			"alpha": "c1",
			"beta":  "c1",
			"tail":  "c2",
		},
	)
	e := newTestEngine(t, g, "main")

	root := e.BuildTree()

	// Count occurrences of tail in the tree.
	var count int
	var walk func(n *TreeNode)
	walk = func(n *TreeNode) {
		if n.Branch.Name == "tail" {
			count++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)

	if count != 1 {
		t.Errorf("tail appears %d times in tree, want 1", count)
	}

	// With no config, tail must be under alpha (alphabetical default).
	var tailParent string
	var findParent func(n *TreeNode, parent string)
	findParent = func(n *TreeNode, parent string) {
		if n.Branch.Name == "tail" {
			tailParent = parent
			return
		}
		for _, c := range n.Children {
			findParent(c, n.Branch.Name)
		}
	}
	findParent(root, "")
	if tailParent != "alpha" {
		t.Errorf("tail's parent = %q, want alpha (alphabetical default)", tailParent)
	}
}
```

- [ ] **Step 4: Add the base-branch divergence test (spec §6 #10)**

This one needs a real git repo because the in-memory `NewGraph` doesn't model the floor/expansion behavior.

First, add a `Dir()` accessor to `git.Client` so tests can run git subcommands in the same working directory. Append to `pkg/git/git.go`:

```go
// Dir returns the working directory this client operates in.
func (g *Client) Dir() string { return g.dir }
```

Then append this test to `pkg/discovery/engine_test.go` (and add `"os/exec"`, `"strings"` to imports if they aren't already there — `"os/exec"` already is):

```go
// TestTraceChainTo_BaseDivergence covers spec §6 #10: when main has advanced
// past where feat-1 was branched, the graph walk cannot reach baseHead; the
// divergence-recovery step completes the chain via stackParent config.
func TestTraceChainTo_BaseDivergence(t *testing.T) {
	c := initTestRepo(t)
	dir := c.Dir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "user.email", "t@t")
	run("config", "user.name", "T")
	run("commit", "--allow-empty", "-m", "c0")
	run("checkout", "-q", "-b", "feat-1")
	run("checkout", "-q", "main")
	run("commit", "--allow-empty", "-m", "m1")

	graph, err := c.LoadGraph("main")
	if err != nil {
		t.Fatal(err)
	}
	baseHead, _ := graph.HeadOf("main")
	e := &Engine{
		git:        c,
		baseBranch: "main",
		baseHead:   baseHead,
		graph:      graph,
	}
	if err := c.SetStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}

	chain, err := e.traceChainTo("feat-1")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(chain))
	for i, b := range chain {
		got[i] = b.Name
	}
	want := []string{"main", "feat-1"}
	if len(got) != len(want) {
		t.Fatalf("traceChainTo(feat-1) = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q, want %q", i, got[i], w)
		}
	}
}
```

Note: `initTestRepo` in `engine_test.go` only runs `git init` without creating the `main` branch. If the default branch name on your git version differs, add `-b main` to the init invocation in `initTestRepo` or rename the branch after init. Check by running the test; if it fails with "ambiguous argument 'main'", patch `initTestRepo` accordingly.

- [ ] **Step 5: Run the new tests**

Run: `go test ./pkg/discovery/ -run 'TestDivergedParent_FullSuite|TestDiscoverStack_PersistsAncestorChain|TestBuildTree_ChildAboveCoLocatedPair|TestTraceChainTo_BaseDivergence' -v`
Expected: all PASS. If `TestBuildTree_ChildAboveCoLocatedPair` fails with "tail appears 2 times", the `coLocatedOwnerFor` logic in Task 9 needs fixing — `BuildTree` must consult it before adding a child to a parent that isn't the owner.

If that's the case, revisit Task 9's Step 3.5 (child-above-co-located-pair edge case) and ensure `directChildren(alpha)` returns `[tail]` while `directChildren(beta)` returns `[]` when alpha is the alphabetical-default owner.

- [ ] **Step 6: Add the cycle-detection test for divergence (spec §6 #8)**

This lives at the `pkg/stack` level. The existing `fakeDiscoverer` already exposes `IsBranchDescendant` backed by a `descendants map[[2]string]bool`, and `newMoveStack(repo, disc)` wires a Stack for Move tests. Append to `pkg/stack/stack_test.go`:

```go
// TestMove_CycleDetectionViaConfigChain verifies that cycle detection blocks a
// Move even when the child has diverged from the parent. The fakeDiscoverer's
// IsBranchDescendant method encodes the stack-tree relationship; the real
// Engine implementation (Task 11) uses the config chain for the same purpose.
func TestMove_CycleDetectionViaConfigChain(t *testing.T) {
	disc := &fakeDiscoverer{
		descendants: map[[2]string]bool{
			{"feat-1", "feat-2"}: true, // feat-2 is a descendant of feat-1
		},
	}
	err := newMoveStack(&fakeRepository{currentBranch: "main"}, disc).
		Move("feat-1", "feat-2", nil)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("Move must fail with cycle error, got: %v", err)
	}
}
```

- [ ] **Step 7: Run the stack tests**

Run: `go test ./pkg/stack/ -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/discovery/engine_test.go pkg/stack/stack_test.go pkg/git/git.go
git commit -m "test: cover spec §6 cases (divergence, persistence, co-location edge)"
```

---

### Task 14: Update `SPEC.md`

**Goal:** Reflect the new discovery semantics in the canonical spec so users reading it get accurate behavior.

**Files:**
- Modify: `SPEC.md`

- [ ] **Step 1: Rewrite the "Upward Trace" subsection in §4.2**

Replace the current "Upward Trace (base → current)" paragraph block in `SPEC.md` with:

```markdown
#### Upward Trace (base → current)

Walk the first-parent chain from the current branch's HEAD back toward the
loaded graph's floor. At each commit, enumerate every local branch whose HEAD
sits there. Typical behavior:

- If the current branch itself shares a commit with another branch, include the
  other branch in the chain only when the current branch's `branch.<name>.stackParent`
  config names it. Otherwise treat them as siblings — the other branch does not
  appear in the current branch's chain.
- All other branches encountered during the walk (strictly below the current
  branch) are included in the chain.

If the walk does not bottom out at the base branch — either because the base
has advanced past the current branch's fork point, or because an intermediate
branch has advanced past one of its children — resolve the remaining gap by
walking the `stackParent` config chain upward from the bottom-most collected
branch until the base is reached.
```

- [ ] **Step 2: Rewrite "Downward Trace (current → top)"**

Replace with:

```markdown
#### Downward Trace (current → top)

Scan all entries in `refs/heads/*`. A candidate branch `B` is a descendant of
the current branch if its HEAD is strictly above the current branch in the
commit graph.

Reduce to direct children: drop any candidate `B` for which another candidate
`D` sits strictly between the current branch and `B`. When two candidates share
a HEAD and one's `stackParent` names the other, the named one stays and the
other demotes (it becomes a child of its configured parent). When two
candidates share a HEAD with no config relation, both remain as siblings.

Add diverged children: for every branch whose `stackParent` config names the
current branch but which is not in the graph-above set, include it as a direct
child. These are branches that were children of the current branch before the
current branch advanced past them.

Recurse from each chosen child until no further descendants are found.
```

- [ ] **Step 3: Rewrite "Ambiguity Handling"**

Replace with:

```markdown
#### Ambiguity Handling

If more than one direct child is found at any step in the downward trace,
prompt the user:

> _"Multiple branches detected. Which branch do you want to [action]?_
> _(1) branch-alpha_
> _(2) branch-beta"_

The selection applies to that single invocation only and is not persisted.
Co-located branches (two or more branches sharing a HEAD) never trigger a
prompt on their own: absent a `stackParent` config hint, they are treated as
siblings.
```

- [ ] **Step 4: Add a "Persistence" subsection**

After "Ambiguity Handling" within §4.2, insert:

```markdown
#### Persistence

Every discovery writes `branch.<name>.stackParent` to local git config for each
resolved parent/child relationship. This record is consulted only when the
commit graph is ambiguous: (1) two branches share a HEAD and we need to
distinguish which is the parent; (2) a parent branch has advanced past a child
so the graph alone no longer shows the stack relationship. When the graph
gives an unambiguous answer that contradicts config, the graph wins and config
is repaired.
```

- [ ] **Step 5: Remove the "persisting user choices" line from §7**

In `SPEC.md` §7 "Out of Scope", delete the bullet:

```
- Persisting user choices (e.g. disambiguation selections) across invocations.
```

Leave the other bullets untouched.

- [ ] **Step 6: Verify the spec still reads coherently**

Open `SPEC.md` and scan the rewritten sections. Fix any leftover wording that contradicts the new behavior (e.g., references to "the tool writes no metadata files" in §3 now need a small qualifier — change to "the tool writes no tool-specific metadata files; it records branch relationships in local git config under `branch.<name>.stackParent`").

- [ ] **Step 7: Commit**

```bash
git add SPEC.md
git commit -m "docs: update SPEC for graph-first discovery with config persistence"
```

---

### Task 15: Final integration check

**Goal:** Full-suite green, lint clean, binary builds, smoke test exercises the new base-divergence handling end-to-end.

- [ ] **Step 1: Run all tests**

Run: `make test`
Expected: PASS everywhere.

- [ ] **Step 2: Run linters**

Run: `make lint`
Expected: no errors.

- [ ] **Step 3: Build**

Run: `make build`
Expected: builds successfully to `dist/git-stack`.

- [ ] **Step 4: End-to-end smoke test for base divergence**

In a throwaway shell:

```bash
cd $(mktemp -d) && git init -q -b main
git config user.email t@t && git config user.name T
git commit --allow-empty -m c0
git checkout -q -b feat-1 && git commit --allow-empty -m f1
git checkout -q main && git commit --allow-empty -m m1 && git commit --allow-empty -m m2
/Users/daniel/code/github.com/danroc/git-stack/dist/git-stack view
```

Expected: `feat-1` appears under `main` in the tree, with a correct `(+N)` ahead count reflecting that feat-1 is 1 commit ahead of its fork point. No crash. The `stackParent` for `feat-1` should now be persisted — verify with:

```bash
git config --local --get branch.feat-1.stackParent
```

Expected: `main`.

- [ ] **Step 5: Confirm the rollout plan**

Per the spec (§8), Phase 1 and Phase 2 were intended to ship as separate PRs. The commits from Tasks 1–5 form Phase 1; Tasks 6–15 form Phase 2. If you're shipping in two PRs, branch Phase 2 off Phase 1's merge base. If shipping as one, proceed to PR creation.

- [ ] **Step 6: Celebrate responsibly**

---

## Self-Review Notes

Gaps to double-check during execution:

1. **`coLocatedOwnerFor` correctness:** Task 9's edge-case handling is the least-tested piece. Task 13 Step 5 explicitly exercises it; if that test is red, pause and rework Step 3.5 before moving on. The fundamental question is: in `BuildTree`, when we call `directChildren(alpha)` and `directChildren(beta)` where alpha/beta are co-located, does `tail` appear as a child of only one of them?

2. **Persistence during `traceChainTo` in edge cases:** If `recoverViaConfig` returns an empty chain (config is absent), we fall through to the "prepend baseBranch" step. Verify the persist loop doesn't write bogus pairs in that case — the loop iterates over the final chain, so as long as that chain is clean, writes are correct.

3. **Backward compatibility of the smoke test output:** If any user has existing `stackParent` entries from the pre-change code, Task 9's graph-first logic will overwrite them with graph-derived answers where unambiguous. This is intended behavior (the spec's §5.2.6 option A), but worth confirming once on a real local repo before shipping.

4. **`git merge-base --octopus` with disjoint histories:** Task 3 propagates the error. If a user's repo has a branch that doesn't share history with the base, the tool will now fail louder than before. Document this as a known limitation in the PR description.
