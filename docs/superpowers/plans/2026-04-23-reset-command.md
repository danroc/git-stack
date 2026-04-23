# `git-stack reset` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `reset` command that removes all `branch.*.stackParent` and `branch.*.stackParentMergeBase` entries from local git config.

**Architecture:** Add `ResetStackConfig()` to `git.Client` that parses local config, finds stack-related keys, unsets them, and returns affected branch names. Wire a `cmdReset()` Cobra command that calls this method and prints per-branch output.

**Tech Stack:** Go 1.26, `spf13/cobra`, `os/exec`

---

### File Map

| File | Change |
|------|--------|
| `pkg/git/git.go` | Add `ResetStackConfig() ([]string, error)` method |
| `pkg/git/git_test.go` | Add `TestResetStackConfig_RemovesAllEntries` and `TestResetStackConfig_NoOpWhenEmpty` |
| `cmd/git-stack/main.go` | Add `cmdReset()` function and wire to root command |

---

### Task 1: Write failing test for ResetStackConfig with multiple branches

**Files:**
- Test: `pkg/git/git_test.go`

- [ ] **Step 1: Write the failing test**

Add this test to `pkg/git/git_test.go`:

```go
func TestResetStackConfig_RemovesAllEntries(t *testing.T) {
	c, dir := initRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat-1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")
	runGit(t, dir, "checkout", "-q", "-b", "feat-2")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c2")

	// Set up stack config for both branches.
	if err := c.RecordStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	// Create a fresh client so the cache is reloaded after reset.
	branches, err := c.ResetStackConfig()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the returned branch list.
	if len(branches) != 2 {
		t.Fatalf("got %d branches, want 2", len(branches))
	}
	if branches[0] != "feat-1" || branches[1] != "feat-2" {
		t.Fatalf("branches = %v, want [feat-1 feat-2]", branches)
	}

	// Verify config is actually gone by reloading.
	c2 := NewClient(dir)
	if _, ok := c2.StackParent("feat-1"); ok {
		t.Error("feat-1 stackParent should be unset")
	}
	if _, ok := c2.StackParent("feat-2"); ok {
		t.Error("feat-2 stackParent should be unset")
	}
	if _, ok := c2.StackMergeBase("feat-1"); ok {
		t.Error("feat-1 stackParentMergeBase should be unset")
	}
	if _, ok := c2.StackMergeBase("feat-2"); ok {
		t.Error("feat-2 stackParentMergeBase should be unset")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/git/ -run TestResetStackConfig_RemovesAllEntries -v`
Expected: FAIL — `ResetStackConfig` method does not exist yet

- [ ] **Step 3: Commit the test**

```bash
git add pkg/git/git_test.go
git commit -m "test: add failing test for ResetStackConfig"
```

---

### Task 2: Implement ResetStackConfig

**Files:**
- Modify: `pkg/git/git.go`

- [ ] **Step 1: Write the implementation**

Add this method to `pkg/git/git.go`:

```go
// ResetStackConfig removes all stackParent and stackParentMergeBase config entries
// from the local git config. Returns the sorted list of branches that had entries
// removed, or an empty slice if none were found.
func (g *Client) ResetStackConfig() ([]string, error) {
	out, err := g.run("config", "--local", "--list")
	if err != nil {
		return nil, err
	}

	affected := make(map[string]struct{})

	for line := range strings.SplitSeq(out, "\n") {
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		branch, variable, ok := parseBranchConfigKey(key)
		if !ok {
			continue
		}
		switch {
		case strings.EqualFold(variable, "stackParent"):
			affected[branch] = struct{}{}
		case strings.EqualFold(variable, "stackParentMergeBase"):
			affected[branch] = struct{}{}
		}
	}

	if len(affected) == 0 {
		return nil, nil
	}

	for branch := range affected {
		// Unset both keys; exit code 1 means the key was already absent, which is fine.
		_, _ = g.run("config", "--local", "--unset", "branch."+branch+".stackParent")
		_, _ = g.run("config", "--local", "--unset", "branch."+branch+".stackParentMergeBase")
	}

	result := make([]string, 0, len(affected))
	for branch := range affected {
		result = append(result, branch)
	}
	sort.Strings(result)
	return result, nil
}
```

Add `"sort"` to the import block if not already present.

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./pkg/git/ -run TestResetStackConfig_RemovesAllEntries -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add pkg/git/git.go
git commit -m "feat: add ResetStackConfig to clear all stack config entries"
```

---

### Task 3: Write test for empty config case

**Files:**
- Test: `pkg/git/git_test.go`

- [ ] **Step 1: Write the test**

Add this test to `pkg/git/git_test.go`:

```go
func TestResetStackConfig_NoOpWhenEmpty(t *testing.T) {
	c, _ := initRepo(t)

	branches, err := c.ResetStackConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 0 {
		t.Fatalf("got %d branches, want 0", len(branches))
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./pkg/git/ -run TestResetStackConfig_NoOpWhenEmpty -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add pkg/git/git_test.go
git commit -m "test: add ResetStackConfig empty config test"
```

---

### Task 4: Add cmdReset and wire to root command

**Files:**
- Modify: `cmd/git-stack/main.go`

- [ ] **Step 1: Add cmdReset function and wire it**

Add this function to `cmd/git-stack/main.go`:

```go
func cmdReset() *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Remove all saved stack config from the local git config",
		RunE: func(_ *cobra.Command, _ []string) error {
			g := git.NewClient(".")
			branches, err := g.ResetStackConfig()
			if err != nil {
				return err
			}
			for _, branch := range branches {
				fmt.Fprintf(os.Stdout, "Removing stack config for %s\n", branch)
			}
			return nil
		},
	}
}
```

Add `cmdReset()` to the `root.AddCommand()` call:

```go
root.AddCommand(
	cmdAdd(),
	cmdMove(),
	cmdView(),
	cmdPush(),
	cmdPull(),
	cmdRebase(),
	cmdReset(),
)
```

- [ ] **Step 2: Run all tests**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Build the binary**

Run: `make build`
Expected: SUCCESS — binary built at `dist/git-stack`

- [ ] **Step 4: Manual smoke test**

Run in a test repo:
```bash
# Set up config
git config branch.feat-1.stackParent main
git config branch.feat-1.stackParentMergeBase $(git rev-parse main)
# Run reset
./dist/git-stack reset
# Verify output contains "Removing stack config for feat-1"
# Verify config is gone
git config --local --list | grep stackParent  # should produce no output
```

- [ ] **Step 5: Commit**

```bash
git add cmd/git-stack/main.go
git commit -m "feat: add reset command to clear all stack config"
```

---

### Task 5: Run linter and final verification

- [ ] **Step 1: Run linter**

Run: `make lint`
Expected: PASS (fix any lint issues inline)

- [ ] **Step 2: Run full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Commit any lint fixes**

If lint fixes were needed:
```bash
git add -A
git commit -m "chore: fix lint issues"
```
