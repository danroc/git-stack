# CommitsBetween Refactoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the duplicated first-parent chain walking logic from `CommitsBetween` into a reusable `countStepsToAncestor` helper.

**Architecture:** The two loops in `CommitsBetween` (lines 212-221 and 223-232) are identical except for the starting hash and counter variable name. Extract them into a private method `countStepsToAncestor(hash, target string) int` on `Graph`. No new files needed.

**Tech Stack:** Go, existing test suite in `graph_test.go`.

---

### Task 1: Add the `countStepsToAncestor` helper method

**Files:**
- Modify: `pkg/git/graph.go:235` (insert after `CommitsBetween`)

- [ ] **Step 1: Write the helper method**

Insert this private method after line 235 (after the closing `}` of `CommitsBetween`):

```go
// countStepsToAncestor counts the number of first-parent steps from hash
// up to (but not including) target along the first-parent chain.
func (g *Graph) countStepsToAncestor(hash, target string) int {
	var count int
	for g.Contains(hash) && hash != target {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		count++
		hash = parent
	}
	return count
}
```

- [ ] **Step 2: Run tests to verify no regression**

Run: `go test ./pkg/git/ -run TestGraph_CommitsBetween -v`
Expected: All existing tests PASS

---

### Task 2: Refactor `CommitsBetween` to use the helper

**Files:**
- Modify: `pkg/git/graph.go:202-235`

- [ ] **Step 1: Replace the two loops with calls to the helper**

Replace lines 210-232 with:

```go
	return CommitsBetweenResult{
		Ahead:  g.countStepsToAncestor(a, commonAncestor),
		Behind: g.countStepsToAncestor(b, commonAncestor),
	}
```

The full refactored function:

```go
func (g *Graph) CommitsBetween(a, b string) CommitsBetweenResult {
	commonAncestor := g.closestCommonAncestor(a, b)
	if commonAncestor == "" {
		return CommitsBetweenResult{}
	}

	return CommitsBetweenResult{
		Ahead:  g.countStepsToAncestor(a, commonAncestor),
		Behind: g.countStepsToAncestor(b, commonAncestor),
	}
}
```

The full refactored function should look like:

```go
func (g *Graph) CommitsBetween(a, b string) CommitsBetweenResult {
	commonAncestor := g.closestCommonAncestor(a, b)
	if commonAncestor == "" {
		return CommitsBetweenResult{}
	}

	var ahead, behind int

	ahead = g.countStepsToAncestor(a, commonAncestor)
	behind = g.countStepsToAncestor(b, commonAncestor)

	return CommitsBetweenResult{Ahead: ahead, Behind: behind}
}
```

- [ ] **Step 2: Run all graph tests**

Run: `go test ./pkg/git/ -v`
Expected: All tests PASS

---

### Task 3: Run full test suite

**Files:**
- Run: entire test suite

- [ ] **Step 1: Run all tests**

Run: `go test ./...`
Expected: All tests PASS
