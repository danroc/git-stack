# CommitsAhead → CommitsBetween Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Change `(*Graph).CommitsAhead` to return both ahead and behind counts computed from the closest common ancestor of two branches, rename the function and related fields accordingly.

**Architecture:** Replace the single-int return with a `CommitsBetweenResult` struct. Find the closest common ancestor via bidirectional BFS from both branch heads through the parent DAG. Count first-parent chain distance from each head to that ancestor. Update `TreeNode` and `TreeEntry` fields to use the new struct.

**Tech Stack:** Go, testing via `go test`

---

## Files Changed

| File | Change |
|------|--------|
| `pkg/git/graph.go` | Rename `CommitsAhead` → `CommitsBetween`, return `CommitsBetweenResult` |
| `pkg/git/graph_test.go` | Update tests for new return type and behavior |
| `pkg/discovery/engine.go` | Rename `TreeNode.CommitsAhead` → `AheadCount`, update call site |
| `cmd/git-stack/main.go` | No change needed (already uses `AheadCount`) |

---

### Task 1: Add `CommitsBetweenResult` type and rename function in graph.go

**Files:**
- Modify: `pkg/git/graph.go:186-200`

- [ ] **Step 1: Add the result type and rewrite `CommitsBetween`**

Add after line 119 (after `NewGraph`):

```go
// CommitsBetweenResult holds the count of commits ahead and behind
// two branches, relative to their closest common ancestor.
type CommitsBetweenResult struct {
	Ahead  int
	Behind int
}
```

Replace `CommitsAhead` (lines 186-200) with:

```go
// CommitsBetween returns the number of commits between branchHash and
// parentHash relative to their closest common ancestor in the graph.
//
// Ahead is the first-parent chain distance from the common ancestor to
// branchHash. Behind is the first-parent chain distance from the common
// ancestor to parentHash.
func (g *Graph) CommitsBetween(branchHash, parentHash string) CommitsBetweenResult {
	// Find the closest common ancestor via bidirectional BFS.
	commonAncestor := g.closestCommonAncestor(branchHash, parentHash)
	if commonAncestor == "" {
		return CommitsBetweenResult{}
	}

	var ahead, behind int

	// Count ahead: first-parent chain from branchHash to common ancestor.
	hash := branchHash
	for g.Contains(hash) && hash != commonAncestor {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		ahead++
		hash = parent
	}

	// Count behind: first-parent chain from parentHash to common ancestor.
	hash = parentHash
	for g.Contains(hash) && hash != commonAncestor {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		behind++
		hash = parent
	}

	return CommitsBetweenResult{Ahead: ahead, Behind: behind}
}

// closestCommonAncestor finds the nearest commit reachable from both
// branchHash and parentHash by walking parent chains. It uses bidirectional
// BFS to minimize the number of graph nodes visited.
func (g *Graph) closestCommonAncestor(a, b string) string {
	if a == b {
		return a
	}

	aVisited := map[string]bool{a: true}
	bVisited := map[string]bool{b: true}
	aQueue := []string{a}
	bQueue := []string{b}

	for len(aQueue) > 0 && len(bQueue) > 0 {
		// Expand the shorter frontier first for efficiency.
		if len(bQueue) < len(aQueue) {
			aQueue, bQueue = bQueue, aQueue
			aVisited, bVisited = bVisited, aVisited
		}

		nextAQueue := make([]string, 0, len(aQueue))
		for _, commit := range aQueue {
			// Check intersection before expanding.
			if bVisited[commit] {
				return commit
			}
			parent, ok := g.FirstParent(commit)
			if !ok {
				continue
			}
			if !g.Contains(parent) {
				continue
			}
			if aVisited[parent] {
				continue
			}
			aVisited[parent] = true
			// Check intersection on the parent.
			if bVisited[parent] {
				return parent
			}
			nextAQueue = append(nextAQueue, parent)
		}
		aQueue = nextAQueue
	}

	return ""
}
```

- [ ] **Step 2: Run tests to confirm nothing breaks yet (expected: compile errors)**

Run: `go build ./...`
Expected: compile errors for `CommitsAhead` (renamed), wrong return type

---

### Task 2: Update `graph_test.go` for new function

**Files:**
- Modify: `pkg/git/graph_test.go:160-180`

- [ ] **Step 1: Rename test and update for `CommitsBetween` with new return type**

Replace `TestGraph_CommitsAhead` (lines 160-180) with:

```go
func TestGraph_CommitsBetween(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		name     string
		branch   string
		parent   string
		want     CommitsBetweenResult
	}{
		{"one ahead, zero behind", "c1", "c0", CommitsBetweenResult{Ahead: 1, Behind: 0}},
		{"one behind, zero ahead", "c0", "c1", CommitsBetweenResult{Ahead: 0, Behind: 1}},
		{"two ahead, zero behind", "c2", "c0", CommitsBetweenResult{Ahead: 2, Behind: 0}},
		{"same commit", "c2", "c2", CommitsBetweenResult{Ahead: 0, Behind: 0}},
		{"one ahead one behind", "c1", "c2", CommitsBetweenResult{Ahead: 1, Behind: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.CommitsBetween(tt.branch, tt.parent)
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Add test for divergent branches with common ancestor**

Add after the existing test block, before line 182:

```go
func TestGraph_CommitsBetween_Divergent(t *testing.T) {
	// Layout:
	//   c0 (main)
	//   ├── c1 (main-dev)
	//   │   └── c2 (main-dev tip)
	//   └── c3 (feat)
	//   └── c4 (feat tip)
	g := NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c0"},
			"c4": {"c3"},
		},
		map[string]string{
			"main":     "c0",
			"main-dev": "c2",
			"feat":     "c4",
		},
	)
	tests := []struct {
		name   string
		a      string
		b      string
		want   CommitsBetweenResult
	}{
		{"same branch", "c0", "c0", CommitsBetweenResult{Ahead: 0, Behind: 0}},
		{"main vs main-dev: 2 ahead, 0 behind", "c2", "c0", CommitsBetweenResult{Ahead: 2, Behind: 0}},
		{"main vs feat: 1 ahead, 0 behind", "c4", "c0", CommitsBetweenResult{Ahead: 1, Behind: 0}},
		{"main-dev vs feat: 2 ahead, 1 behind", "c2", "c4", CommitsBetweenResult{Ahead: 2, Behind: 1}},
		{"feat vs main-dev: 1 ahead, 2 behind", "c4", "c2", CommitsBetweenResult{Ahead: 1, Behind: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.CommitsBetween(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/git/ -run TestGraph_CommitsBetween -v`
Expected: PASS

---

### Task 3: Rename `TreeNode.CommitsAhead` → `AheadCount` and update call site

**Files:**
- Modify: `pkg/discovery/engine.go:79` (struct field rename)
- Modify: `pkg/discovery/engine.go:285` (call site update)
- Modify: `pkg/discovery/engine_test.go` (update test expectations)

- [ ] **Step 1: Rename struct field in engine.go**

In `pkg/discovery/engine.go:79`, change:

```go
// Before:
CommitsAhead int

// After:
AheadCount int
```

- [ ] **Step 2: Update call site in buildChildren**

In `pkg/discovery/engine.go:285`, change:

```go
// Before:
CommitsAhead: e.graph.CommitsAhead(node.Branch.Head, childHash),

// After:
AheadCount: e.graph.CommitsBetween(childHash, node.Branch.Head).Ahead,
```

- [ ] **Step 3: Update `engine_test.go` — rename `CommitsAhead` field references**

In `pkg/discovery/engine_test.go`, replace all `CommitsAhead` field accesses with `AheadCount`:

```go
// Line ~431:
// Before:
if feat1.CommitsAhead != 2 {
    t.Errorf("feat-1 CommitsAhead = %d, want 2", feat1.CommitsAhead)
}

// After:
if feat1.AheadCount != 2 {
    t.Errorf("feat-1 AheadCount = %d, want 2", feat1.AheadCount)
}

// Line ~437:
// Before:
if feat1.Children[0].CommitsAhead != 1 {
    t.Errorf("feat-2 CommitsAhead = %d, want 1", feat1.Children[0].CommitsAhead)
}

// After:
if feat1.Children[0].AheadCount != 1 {
    t.Errorf("feat-2 AheadCount = %d, want 1", feat1.Children[0].AheadCount)
}
```

Also rename the test function:

```go
// Before (line ~405):
func TestBuildTree_CommitsAhead(t *testing.T) {

// After:
func TestBuildTree_AheadCount(t *testing.T) {
```

- [ ] **Step 4: Verify no remaining `CommitsAhead` references in engine package**

Run: `grep -n "CommitsAhead" pkg/discovery/engine.go pkg/discovery/engine_test.go`
Expected: no output (all references updated)

- [ ] **Step 5: Run engine tests**

Run: `go test ./pkg/discovery/ -v`
Expected: PASS

---

### Task 4: Run full test suite

- [ ] **Step 1: Run all tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 2: Verify no remaining `CommitsAhead` references in codebase**

Run: `grep -rn "CommitsAhead" --include="*.go" .`
Expected: only `git.go:247-248` (the `*Client` method, which is unrelated)

---

## Self-Review

**1. Spec coverage:**
- Returns both ahead AND behind counts ✓
- Computed from closest common ancestor (bidirectional BFS) ✓
- Function renamed to `CommitsBetween` ✓
- `TreeNode.CommitsAhead` renamed to `AheadCount` ✓
- Callers updated (engine.go, engine_test.go) ✓
- `TreeEntry.AheadCount` already uses the field name, no change needed ✓

**2. Placeholder scan:**
- No placeholders found. All code is concrete.

**3. Type consistency:**
- `CommitsBetweenResult` struct defined with `Ahead int` and `Behind int` ✓
- `AheadCount int` field in `TreeNode` matches `TreeEntry.AheadCount` ✓
- Call site extracts `.Ahead` from the result struct ✓

**4. Behavior preserved for existing callers:**
- `buildChildren` extracts `.Ahead` only, same as before (ahead count was the only value returned) ✓
- Existing test cases in `TestBuildTree_AheadCount` verify same numeric values ✓
