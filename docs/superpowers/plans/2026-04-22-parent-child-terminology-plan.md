# Parent/Child Terminology Consistency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace mixed spatial/ancestry metaphors (ancestor/descendant, above/below) with consistent parent/child terminology throughout the codebase.

**Architecture:** Three public methods in `discovery.Engine` are renamed for clarity. Call sites in `stack` and `cmd` packages are updated. Comments are cleaned up to remove spatial references. The persisted config key `stackParent` stays unchanged.

**Tech Stack:** Go, Go's standard library, no external dependencies for this change.

---

### Task 1: Rename `IsBranchDescendant` to `IsChildOf` in engine.go

**Files:**
- Modify: `pkg/discovery/engine.go:229-244`

- [ ] **Step 1: Rename the method and flip parameter order**

Replace lines 229-244:

```go
// Before:
// IsBranchDescendant reports whether descendant is strictly below ancestor in the
// resolved stack tree.
func (e *Engine) IsBranchDescendant(ancestor, descendant string) bool {
	if ancestor == descendant {
		return false
	}
	found := false
	err := e.walkResolvedParents(descendant, func(parent string) error {
		if parent == ancestor {
			found = true
			return errStopWalk
		}
		return nil
	})
	return found && err == errStopWalk
}

// After:
// IsChildOf reports whether child is a direct or transitive child of parent
// in the resolved stack tree (i.e., child is strictly below parent).
func (e *Engine) IsChildOf(child, parent string) bool {
	if child == parent {
		return false
	}
	found := false
	err := e.walkResolvedParents(child, func(p string) error {
		if p == parent {
			found = true
			return errStopWalk
		}
		return nil
	})
	return found && err == errStopWalk
}
```

Key changes:
- Method name: `IsBranchDescendant` → `IsChildOf`
- Parameter order: `(ancestor, descendant)` → `(child, parent)` — child first to match the name
- Internal: `walkResolvedParents(descendant, ...)` → `walkResolvedParents(child, ...)`
- Comparison: `parent == ancestor` → `p == parent`
- Doc comment: replaces "descendant is strictly below ancestor" with "child is a direct or transitive child of parent"

- [ ] **Step 2: Run tests to verify compilation (will fail at call sites)**

Run: `go build ./pkg/discovery/`
Expected: PASS (the method is self-contained)

---

### Task 2: Update `IsChildOf` call site in stack.go

**Files:**
- Modify: `pkg/stack/stack.go:199`

- [ ] **Step 1: Update the call site**

Replace line 199:

```go
// Before:
if s.disc.IsBranchDescendant(branch, newParent) {

// After:
if s.disc.IsChildOf(newParent, branch) {
```

The error message on lines 200-204 stays the same — it already says "cannot move %s onto %s: would create a cycle" which is clear.

- [ ] **Step 2: Run tests**

Run: `go test ./pkg/stack/ -v`
Expected: FAIL — `fakeDiscoverer` still implements the old method name (handled in Task 5)

---

### Task 3: Rename `traceDescendants` to `traceChildren` in engine.go

**Files:**
- Modify: `pkg/discovery/engine.go:148-174`

- [ ] **Step 1: Rename method**

Replace lines 148-174:

```go
// Before:
func (e *Engine) traceDescendants(
	branch string,
	chooseBranch ChooseBranchFn,
) ([]Branch, error) {
	var result []Branch
	for {
		children := e.directChildren(branch)
		if len(children) == 0 {
			return result, nil
		}

		var chosen string
		if len(children) == 1 {
			chosen = children[0]
		} else {
			var err error
			chosen, err = chooseBranch("traverse", children)
			if err != nil {
				return nil, err
			}
		}

		chosenHash, _ := e.graph.HeadOf(chosen)
		result = append(result, Branch{Name: chosen, Head: chosenHash})
		branch = chosen
	}
}

// After:
func (e *Engine) traceChildren(
	branch string,
	chooseBranch ChooseBranchFn,
) ([]Branch, error) {
	var result []Branch
	for {
		children := e.directChildren(branch)
		if len(children) == 0 {
			return result, nil
		}

		var chosen string
		if len(children) == 1 {
			chosen = children[0]
		} else {
			var err error
			chosen, err = chooseBranch("traverse", children)
			if err != nil {
				return nil, err
			}
		}

		chosenHash, _ := e.graph.HeadOf(chosen)
		result = append(result, Branch{Name: chosen, Head: chosenHash})
		branch = chosen
	}
}
```

- [ ] **Step 2: Update the call site in `DiscoverStack`**

Replace lines 104 and 109:

```go
// Before (line 104):
descendants, err := e.traceDescendants(currentBranch, chooseBranch)

// After:
children, err := e.traceChildren(currentBranch, chooseBranch)

// Before (line 109):
return append(ancestors, descendants...), nil

// After:
return append(ancestors, children...), nil
```

- [ ] **Step 3: Update doc comment on `DiscoverStack`**

Replace lines 89-94:

```go
// Before:
// DiscoverStack identifies the full linear stack that contains currentBranch.
//
// Two passes build the result. The first (base → currentBranch) walks the first-parent
// chain collecting branch heads from the bottom up. The second (currentBranch → tip)
// follows graph ancestry to find branches stacked above currentBranch, calling
// chooseBranch at any bifurcation.

// After:
// DiscoverStack identifies the full linear stack that contains currentBranch.
//
// Two passes build the result. The first (base → currentBranch) walks the first-parent
// chain collecting branch heads from the bottom up. The second (currentBranch → tip)
// follows the parent/child tree to find child branches above currentBranch, calling
// chooseBranch at any bifurcation.
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/discovery/ -v -run TestTraceDescendants`
Expected: FAIL — test function still uses `traceDescendants` name (handled in Task 4)

---

### Task 4: Rename `SubtreeMembers` to `SubtreeChildren` in engine.go

**Files:**
- Modify: `pkg/discovery/engine.go:246-261, 290-302`

- [ ] **Step 1: Rename `SubtreeMembers` and update its doc comment**

Replace lines 246-261:

```go
// Before:
// SubtreeMembers returns all branches in the subtree rooted at branchName (excluding
// the root itself), each paired with their immediate parent, in pre-order (parents
// before children).
func (e *Engine) SubtreeMembers(branchName string) []BranchWithParent {
	root := e.BuildTree()
	node := findTreeNode(root, branchName)
	if node == nil {
		return nil
	}

	var result []BranchWithParent
	for _, child := range node.Children {
		collectSubtreeMembers(child, branchName, &result)
	}
	return result
}

// After:
// SubtreeChildren returns all branches in the subtree rooted at branchName (excluding
// the root itself), each paired with their immediate parent, in pre-order (parents
// before children).
func (e *Engine) SubtreeChildren(branchName string) []BranchWithParent {
	root := e.BuildTree()
	node := findTreeNode(root, branchName)
	if node == nil {
		return nil
	}

	var result []BranchWithParent
	for _, child := range node.Children {
		collectSubtreeChildren(child, branchName, &result)
	}
	return result
}
```

- [ ] **Step 2: Rename `collectSubtreeMembers` helper**

Replace line 290:

```go
// Before:
func collectSubtreeMembers(node *TreeNode, parent string, result *[]BranchWithParent) {

// After:
func collectSubtreeChildren(node *TreeNode, parent string, result *[]BranchWithParent) {
```

- [ ] **Step 3: Update the call site in stack.go**

Replace lines 216 and 234 in `pkg/stack/stack.go`:

```go
// Before (line 216):
descendants := s.disc.SubtreeMembers(branch)

// After:
children := s.disc.SubtreeChildren(branch)

// Before (line 234):
for _, dep := range descendants {

// After:
for _, dep := range children {
```

- [ ] **Step 4: Run tests**

Run: `go test ./pkg/discovery/ -v -run TestSubtreeMembers`
Expected: FAIL — test still uses old name (handled in Task 5)

---

### Task 5: Update engine_test.go for renamed methods

**Files:**
- Modify: `pkg/discovery/engine_test.go`

- [ ] **Step 1: Rename `TestTraceDescendants_Bifurcation` test**

Replace lines 130-153:

```go
// Before:
func TestTraceDescendants_Bifurcation(t *testing.T) {
	e := newTestEngine(t, branchingTestGraph(), "main")
	chooseCalled := false

	_, err := e.traceDescendants(
		"main",
		func(action string, choices []string) (string, error) {
			chooseCalled = true
			if action != "traverse" {
				t.Fatalf("action = %q, want traverse", action)
			}
			if len(choices) != 2 {
				t.Fatalf("choices = %v, want 2 branches", choices)
			}
			return choices[0], nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !chooseCalled {
		t.Fatal("chooseBranch should be called for bifurcation")
	}
}

// After:
func TestTraceChildren_Bifurcation(t *testing.T) {
	e := newTestEngine(t, branchingTestGraph(), "main")
	chooseCalled := false

	_, err := e.traceChildren(
		"main",
		func(action string, choices []string) (string, error) {
			chooseCalled = true
			if action != "traverse" {
				t.Fatalf("action = %q, want traverse", action)
			}
			if len(choices) != 2 {
				t.Fatalf("choices = %v, want 2 branches", choices)
			}
			return choices[0], nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !chooseCalled {
		t.Fatal("chooseBranch should be called for bifurcation")
	}
}
```

- [ ] **Step 2: Rename `TestIsBranchDescendant_FollowsResolvedParents`**

Replace lines 255-267:

```go
// Before:
func TestIsBranchDescendant_FollowsResolvedParents(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if !e.IsBranchDescendant("main", "feat-2") {
		t.Fatal("feat-2 should be below main")
	}

	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}
	if e.IsBranchDescendant("feat-1", "feat-2") {
		t.Fatal("feat-2 should no longer be below feat-1 once config points to main")
	}
}

// After:
func TestIsChildOf_FollowsResolvedParents(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if !e.IsChildOf("feat-2", "main") {
		t.Fatal("feat-2 should be a child of main")
	}

	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}
	if e.IsChildOf("feat-1", "feat-2") {
		t.Fatal("feat-1 should no longer be a child of feat-2 once config points to main")
	}
}
```

Note: Parameter order is flipped. `IsBranchDescendant("main", "feat-2")` (is main an ancestor of feat-2?) becomes `IsChildOf("feat-2", "main")` (is feat-2 a child of main?).

- [ ] **Step 3: Rename `TestSubtreeMembers_Linear`**

Replace lines 315-328:

```go
// Before:
func TestSubtreeMembers_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	members := e.SubtreeMembers("feat-1")
	if len(members) != 1 {
		t.Fatalf("got %d members, want 1", len(members))
	}
	if members[0].Branch.Name != "feat-2" || members[0].Parent != "feat-1" {
		t.Fatalf(
			"got {%q %q}, want {feat-2 feat-1}",
			members[0].Branch.Name,
			members[0].Parent,
		)
	}
}

// After:
func TestSubtreeChildren_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	children := e.SubtreeChildren("feat-1")
	if len(children) != 1 {
		t.Fatalf("got %d children, want 1", len(children))
	}
	if children[0].Branch.Name != "feat-2" || children[0].Parent != "feat-1" {
		t.Fatalf(
			"got {%q %q}, want {feat-2 feat-1}",
			children[0].Branch.Name,
			children[0].Parent,
		)
	}
}
```

- [ ] **Step 4: Update `TestDirectChildren_InferenceOnlyUsesAncestorCandidates` comment**

Replace line 169:

```go
// Before:
func TestDirectChildren_InferenceOnlyUsesAncestorCandidates(t *testing.T) {

// After:
func TestDirectChildren_InferenceOnlyUsesParentCandidates(t *testing.T) {
```

- [ ] **Step 5: Run all discovery tests**

Run: `go test ./pkg/discovery/ -v`
Expected: All tests pass

---

### Task 6: Update `fakeDiscoverer` in stack_test.go

**Files:**
- Modify: `pkg/stack/stack_test.go:31-49`

- [ ] **Step 1: Rename methods in fakeDiscoverer**

Replace lines 31-33 and 42-44:

```go
// Before:
func (f *fakeDiscoverer) IsBranchDescendant(ancestor, descendant string) bool {
	return f.descendants[[2]string{ancestor, descendant}]
}

func (f *fakeDiscoverer) SubtreeMembers(name string) []discovery.BranchWithParent {
	return f.subtrees[name]
}

// After:
func (f *fakeDiscoverer) IsChildOf(child, parent string) bool {
	return f.descendants[[2]string{child, parent}]
}

func (f *fakeDiscoverer) SubtreeChildren(name string) []discovery.BranchWithParent {
	return f.subtrees[name]
}
```

The `descendants` map key changes from `{ancestor, descendant}` to `{child, parent}` to match the new parameter order.

- [ ] **Step 2: Update the `descendants` map initializers in tests**

Replace line 133:

```go
// Before:
descendants: map[[2]string]bool{{"feat-1", "feat-2"}: true},

// After:
descendants: map[[2]string]bool{{"feat-2", "feat-1"}: true},
```

Replace lines 231-232:

```go
// Before:
descendants: map[[2]string]bool{
	{"feat-1", "feat-2"}: true, // feat-2 is a descendant of feat-1
},

// After:
descendants: map[[2]string]bool{
	{"feat-2", "feat-1"}: true, // feat-2 is a child of feat-1
},
```

- [ ] **Step 3: Run all stack tests**

Run: `go test ./pkg/stack/ -v`
Expected: All tests pass

---

### Task 7: Update `Discoverer` interface in stack.go

**Files:**
- Modify: `pkg/stack/stack.go:26-36`

- [ ] **Step 1: Rename methods in the interface**

Replace lines 26-36:

```go
// Before:
type Discoverer interface {
	BaseBranch() string
	DiscoverStack(
		currentBranch string,
		chooseBranch discovery.ChooseBranchFn,
	) ([]discovery.Branch, error)
	IsBranchDescendant(ancestor, descendant string) bool
	Parent(branch string) (string, error)
	SubtreeMembers(branchName string) []discovery.BranchWithParent
	SetParent(branch, parent string) error
}

// After:
type Discoverer interface {
	BaseBranch() string
	DiscoverStack(
		currentBranch string,
		chooseBranch discovery.ChooseBranchFn,
	) ([]discovery.Branch, error)
	IsChildOf(child, parent string) bool
	Parent(branch string) (string, error)
	SubtreeChildren(branchName string) []discovery.BranchWithParent
	SetParent(branch, parent string) error
}
```

- [ ] **Step 2: Run all stack tests**

Run: `go test ./pkg/stack/ -v`
Expected: All tests pass

---

### Task 8: Update comments in engine.go

**Files:**
- Modify: `pkg/discovery/engine.go`

- [ ] **Step 1: Update `directChildren` doc comment**

Replace lines 202-206:

```go
// Before:
// directChildren returns branches whose resolved parent is parent.
//
// Stored relationships are authoritative. The graph is only used to fill in branches
// that do not have a stored parent, using a deterministic merge-base-based inference.

// After:
// directChildren returns branches whose resolved parent is parent (i.e., branches
// directly above parent in the stack).
//
// Stored relationships are authoritative. The graph is only used to fill in branches
// that do not have a stored parent, using a deterministic merge-base-based inference.
```

- [ ] **Step 2: Update `traceChainTo` doc comment**

Replace lines 112-118:

```go
// Before:
// traceChainTo returns the chain from baseBranch (inclusive) up to target (inclusive),
// bottom-to-top.

// After:
// traceChainTo returns the chain from baseBranch (inclusive) up to target (inclusive),
// bottom-to-top (base → ... → target).
```

- [ ] **Step 3: Run all discovery tests**

Run: `go test ./pkg/discovery/ -v`
Expected: All tests pass

---

### Task 9: Update comments in stack.go

**Files:**
- Modify: `pkg/stack/stack.go`

- [ ] **Step 1: Update `branchAction` doc comment**

Replace lines 78-80:

```go
// Before:
// branchAction is called for each non-base branch in the stack. It receives the branch
// name and its immediate parent's name.

// After:
// branchAction is called for each non-base branch in the stack. It receives the branch
// name and its parent (the branch below it in the stack).
```

- [ ] **Step 2: Update `Step` struct field comments**

Replace lines 38-43:

```go
// Before:
// Step describes one unit of work within a bulk operation.
type Step struct {
	Branch string
	Parent string // rebase target or old parent; empty for Push and Pull
	To     string // new parent; set only for the initial step of a Move
}

// After:
// Step describes one unit of work within a bulk operation.
type Step struct {
	Branch string
	Parent string // rebase target or old parent (branch below); empty for Push and Pull
	To     string // new parent (branch below); set only for the initial step of a Move
}
```

- [ ] **Step 3: Update `Move` method doc comment**

Replace lines 185-187:

```go
// Before:
// Move rebases branch from its current parent onto newParent, then cascades the rebase
// to all of branch's descendants. On conflict it halts leaving the repo in the
// in-progress rebase state. On full success it restores the original branch.

// After:
// Move rebases branch from its current parent onto newParent, then cascades the rebase
// to all of branch's children. On conflict it halts leaving the repo in the
// in-progress rebase state. On full success it restores the original branch.
```

- [ ] **Step 4: Run all stack tests**

Run: `go test ./pkg/stack/ -v`
Expected: All tests pass

---

### Task 10: Update comments in main.go

**Files:**
- Modify: `cmd/git-stack/main.go`

- [ ] **Step 1: Update `cmdMove` short description**

Replace lines 203-207:

```go
// Before:
func cmdMove() *cobra.Command {
	return &cobra.Command{
		Use:   "move [branch] <new-parent>",
		Short: "Move a branch to a different parent, rebasing it and its descendants",
		Args:  cobra.RangeArgs(1, 2),

// After:
func cmdMove() *cobra.Command {
	return &cobra.Command{
		Use:   "move [branch] <new-parent>",
		Short: "Move a branch to a different parent, rebasing it and its children",
		Args:  cobra.RangeArgs(1, 2),
```

- [ ] **Step 2: Run full build**

Run: `go build ./...`
Expected: PASS

---

### Task 11: Add Terminology section and clean up SPEC.md

**Files:**
- Modify: `SPEC.md`

- [ ] **Step 1: Insert Terminology section after §1. Overview**

Insert a new section as §2 (shifting all subsequent sections down by one):

```markdown
## 2. Terminology

The following terms are used throughout this specification and the codebase.
All refer to positions in the stack hierarchy, defined from bottom to top.

- **base** — The bottom-most branch in the stack. Resolved as `main`, `master`,
  or via the `--base` flag. The base has no parent.
- **parent** — The branch *below* in the stack. A branch's parent is the branch
  it was branched FROM. In a stack `main → A → B`, `A` is the parent of `B`.
- **child** — A branch *above* in the stack. A branch's child was branched off
  from it. In a stack `main → A → B`, `B` is a child of `A`.
- **stack direction** — `base → parent → child → child` (bottom to top).
  Operations iterate from parent to child (bottom-to-top).

The config key `branch.<name>.stackParent` stores the parent (branch below) for
each branch. This key name uses the parent/child metaphor exclusively.
```

- [ ] **Step 2: Renumber sections**

Change all section headers to shift down by one:
- `## 2. Problem Statement` → `## 3. Problem Statement`
- `## 3. Solution` → `## 4. Solution`
- `## 4. Core Functional Requirements` → `## 5. Core Functional Requirements`
- `### 4.1 Command Interface` → `### 5.1 Command Interface`
- `### 4.2 The Discovery Engine` → `### 5.2 The Discovery Engine`
- `### 4.3 Conflict and Error Management` → `### 5.3 Conflict and Error Management`
- `## 5. Technical Architecture (Go)` → `## 6. Technical Architecture (Go)`
- `### 5.1 Project Structure` → `### 6.1 Project Structure`
- `### 5.2 Implementation Details` → `### 6.2 Implementation Details`
- `## 6. Testing Strategy` → `## 7. Testing Strategy`
- `## 7. Out of Scope` → `## 8. Out of Scope`

- [ ] **Step 3: Update cross-reference**

Change line 27:

```markdown
<!-- Before -->
All commands accept a `--base <branch>` flag to override base branch detection (see §4.2).

<!-- After -->
All commands accept a `--base <branch>` flag to override base branch detection (see §5.2).
```

- [ ] **Step 4: Clean up spatial references in spec**

Replace lines 95-96:

```markdown
<!-- Before -->
- All other branches encountered during the walk (strictly below the current
  branch) are included in the chain.

<!-- After -->
- All other branches encountered during the walk (strictly below the current
  branch as children) are included in the chain.
```

Replace lines 106-108:

```markdown
<!-- Before -->
Scan all entries in `refs/heads/*`. A candidate branch `B` is a descendant of
the current branch if its HEAD is strictly above the current branch in the
commit graph.

<!-- After -->
Scan all entries in `refs/heads/*`. A candidate branch `B` is a child of
the current branch if its HEAD is strictly above the current branch in the
commit graph.
```

Replace line 121:

```markdown
<!-- Before -->
Recurse from each chosen child until no further descendants are found.

<!-- After -->
Recurse from each chosen child until no further children are found.
```

- [ ] **Step 5: Verify the spec**

Run: `cat SPEC.md | head -150`
Expected: Section numbers are sequential 2-8, terminology section is present, no "descendant" used in stack context (only in "is a descendant of" where it's been replaced with "child")

---

## Self-Review

### Spec coverage
- Task 1-2: `IsBranchDescendant` → `IsChildOf` rename + call site ✓
- Task 3: `traceDescendants` → `traceChildren` rename + call site ✓
- Task 4: `SubtreeMembers` → `SubtreeChildren` rename + call site ✓
- Task 5: engine_test.go updates ✓
- Task 6: stack_test.go fakeDiscoverer updates ✓
- Task 7: Discoverer interface updates ✓
- Task 8: engine.go comment cleanup ✓
- Task 9: stack.go comment cleanup ✓
- Task 10: main.go comment cleanup ✓
- Task 11: SPEC.md terminology section + cleanup ✓

### Placeholder scan
- No "TBD", "TODO", "fill in", or "similar to" patterns found
- All code changes shown with before/after
- All test updates shown with complete code

### Type consistency
- `IsChildOf(child, parent)` — parameter order consistent across engine.go, stack.go call site, fakeDiscoverer, and tests
- `traceChildren` — consistent rename across engine.go and test
- `SubtreeChildren` — consistent rename across engine.go, stack.go, fakeDiscoverer, and test
- `discoverer.BranchWithParent` — not renamed (Parent field already uses correct metaphor)
