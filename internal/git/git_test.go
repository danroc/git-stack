package git

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// initRepo initializes a temp git repo and returns a client. Configures user
// identity so commits work under CI.
func initRepo(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		full := append([]string{"-C", dir}, args...)
		cmd := exec.Command("git", full...) //nolint:gosec
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
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\r\n")
}

// setupTwoFeats creates two feature branches (feat-1, feat-2) off main, each
// with one empty commit. Branches are created from main (not from each other).
func setupTwoFeats(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "checkout", "-q", "-b", "feat-1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")
	runGit(t, dir, "checkout", "-q", "main")
	runGit(t, dir, "checkout", "-q", "-b", "feat-2")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c2")
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
	if base != c0 {
		t.Errorf("merge-base = %q, want %q", base, c0)
	}
}

func TestRecordStackParent_StoresMergeBase(t *testing.T) {
	c, dir := initRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat-1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")
	runGit(t, dir, "checkout", "-q", "-b", "feat-2")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c2")

	if err := c.RecordStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	parent, ok := c.StackParent("feat-2")
	if !ok || parent != "feat-1" {
		t.Fatalf("GetStackParent(feat-2) = %q (ok=%v), want feat-1", parent, ok)
	}
	mergeBase, ok := c.StackMergeBase("feat-2")
	if !ok {
		t.Fatal("expected stored merge-base for feat-2")
	}
	if mergeBase != runGit(t, dir, "rev-parse", "feat-1") {
		t.Fatalf("merge-base = %q, want feat-1 head", mergeBase)
	}
}

func TestStackParent_RetriesAfterInitialConfigFailure(t *testing.T) {
	dir := t.TempDir()
	c := NewClient(dir)

	if parent, ok := c.StackParent("feat-2"); ok || parent != "" {
		t.Fatalf("initial StackParent = %q (ok=%v), want empty and false", parent, ok)
	}

	// Turn the directory into a git repo after the first failed lookup.
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "branch.feat-2.stackParent", "feat-1")

	parent, ok := c.StackParent("feat-2")
	if !ok || parent != "feat-1" {
		t.Fatalf("StackParent(feat-2) = %q (ok=%v), want feat-1", parent, ok)
	}
}

func TestParseBranchConfigKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        string
		wantBranch string
		wantVar    string
		wantOK     bool
	}{
		{
			name:       "basic stack parent key",
			key:        "branch.feature.stackParent",
			wantBranch: "feature",
			wantVar:    "stackParent",
			wantOK:     true,
		},
		{
			name:       "branch name with dots",
			key:        "branch.feature.foo.stackMergeBase",
			wantBranch: "feature.foo",
			wantVar:    "stackMergeBase",
			wantOK:     true,
		},
		{
			name:       "section matching is case insensitive",
			key:        "Branch.Feature.STACKPARENT",
			wantBranch: "Feature",
			wantVar:    "STACKPARENT",
			wantOK:     true,
		},
		{
			name:   "non branch section",
			key:    "remote.origin.url",
			wantOK: false,
		},
		{
			name:   "empty branch name",
			key:    "branch..stackParent",
			wantOK: false,
		},
		{
			name:   "missing variable",
			key:    "branch.feature.",
			wantOK: false,
		},
		{
			name:   "missing section delimiter",
			key:    "branchfeature.stackParent",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			branch, variable, ok := parseBranchConfigKey(tc.key)
			if ok != tc.wantOK {
				t.Fatalf(
					"parseBranchConfigKey(%q) ok=%v, want %v",
					tc.key, ok, tc.wantOK,
				)
			}
			if !ok {
				return
			}
			if branch != tc.wantBranch || variable != tc.wantVar {
				t.Fatalf(
					"parseBranchConfigKey(%q) = (%q, %q), want (%q, %q)",
					tc.key,
					branch,
					variable,
					tc.wantBranch,
					tc.wantVar,
				)
			}
		})
	}
}

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
		t.Error("feat-1 stackMergeBase should be unset")
	}
	if _, ok := c2.StackMergeBase("feat-2"); ok {
		t.Error("feat-2 stackMergeBase should be unset")
	}
}

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

func TestError_Error(t *testing.T) {
	t.Parallel()

	err := &Error{Args: []string{"checkout", "main"}, Err: fmt.Errorf("exit status 1")}
	if got := err.Error(); got != "git checkout main: exit status 1" {
		t.Errorf("Error() = %q, want %q", got, "git checkout main: exit status 1")
	}
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	inner := fmt.Errorf("underlying")
	err := &Error{Args: []string{"push"}, Err: inner}
	if !errors.Is(err, inner) {
		t.Error("Unwrap() should return the underlying error")
	}
}

func TestIsOneOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  int
		values []int
		want   bool
	}{
		{"first match", 1, []int{1, 2, 3}, true},
		{"middle match", 2, []int{1, 2, 3}, true},
		{"last match", 3, []int{1, 2, 3}, true},
		{"no match", 4, []int{1, 2, 3}, false},
		{"single element match", 5, []int{5}, true},
		{"single element no match", 5, []int{1}, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isOneOf(tc.value, tc.values...); got != tc.want {
				t.Errorf(
					"isOneOf(%d, %v) = %v, want %v",
					tc.value,
					tc.values,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestIsOneOf_String(t *testing.T) {
	t.Parallel()

	if !isOneOf("foo", "foo", "bar") {
		t.Error("isOneOf should work with strings")
	}
	if isOneOf("baz", "foo", "bar") {
		t.Error("isOneOf should return false for non-matching string")
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single line",
			input: "hello",
			want:  []string{"hello"},
		},
		{
			name:  "multiple lines unix",
			input: "a\nb\nc",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "multiple lines windows",
			input: "a\r\nb\r\nc",
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "trailing newline",
			input: "a\nb\n",
			want:  []string{"a", "b"},
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitLines(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("splitLines(%q) = %v, want %v", tc.input, got, tc.want)
			}
			for i, v := range tc.want {
				if got[i] != v {
					t.Errorf("[%d] = %q, want %q", i, got[i], v)
				}
			}
		})
	}
}

func TestClient_Dir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	c := NewClient(dir)
	if c.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", c.Dir(), dir)
	}
}

func TestClient_ListBranches(t *testing.T) {
	c, dir := initRepo(t)

	branches, err := c.ListBranches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0] != "main" {
		t.Errorf("ListBranches() = %v, want [main]", branches)
	}

	setupTwoFeats(t, dir)

	branches, err = c.ListBranches()
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 3 {
		t.Fatalf("ListBranches() = %v, want 3 branches", branches)
	}
}

func TestClient_Checkout(t *testing.T) {
	c, dir := initRepo(t)

	runGit(t, dir, "checkout", "-q", "-b", "feat-1")

	if err := c.Checkout("feat-1"); err != nil {
		t.Fatal(err)
	}
	branch, err := c.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if branch != "feat-1" {
		t.Errorf("CurrentBranch() = %q, want feat-1", branch)
	}
}

func TestClient_CreateBranch(t *testing.T) {
	c, dir := initRepo(t)

	if err := c.CreateBranch("new-branch"); err != nil {
		t.Fatal(err)
	}
	branch, err := c.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if branch != "new-branch" {
		t.Errorf("CurrentBranch() = %q, want new-branch", branch)
	}
	if !runGitHasBranch(dir, "new-branch") {
		t.Error("new-branch should exist after CreateBranch")
	}
}

func TestClient_SetStackParent(t *testing.T) {
	c, dir := initRepo(t)

	if err := c.SetStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}

	parent, ok := c.StackParent("feat-1")
	if !ok || parent != "main" {
		t.Errorf("StackParent(feat-1) = %q (ok=%v), want main", parent, ok)
	}

	// Verify it's in the git config directly.
	cfg := runGit(t, dir, "config", "--get", "branch.feat-1.stackParent")
	if cfg != "main" {
		t.Errorf("git config = %q, want main", cfg)
	}
}

func TestClient_SetStackMergeBase(t *testing.T) {
	c, dir := initRepo(t)

	if err := c.SetStackMergeBase("feat-1", "abc123"); err != nil {
		t.Fatal(err)
	}

	base, ok := c.StackMergeBase("feat-1")
	if !ok || base != "abc123" {
		t.Errorf("StackMergeBase(feat-1) = %q (ok=%v), want abc123", base, ok)
	}

	cfg := runGit(t, dir, "config", "--get", "branch.feat-1.stackMergeBase")
	if cfg != "abc123" {
		t.Errorf("git config = %q, want abc123", cfg)
	}
}

func TestClient_Push(t *testing.T) {
	c, dir := initRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat-1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")

	// No upstream configured — should push to origin/feat-1 with --force-with-lease.
	err := c.Push("feat-1")
	if err == nil {
		t.Fatal("expected push to fail (no origin remote configured)")
	}
	var gitErr *Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("expected *git.Error, got %T", err)
	}
	wantArgs := []string{"push", "--force-with-lease", "-u", "origin", "feat-1"}
	if !slices.Equal(gitErr.Args, wantArgs) {
		t.Errorf("args = %v, want %v", gitErr.Args, wantArgs)
	}
}

func TestClient_CommitsAhead(t *testing.T) {
	c, dir := initRepo(t)

	// main is at c0, so 0 commits ahead of itself.
	n, err := c.CommitsAhead("main", "main")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("CommitsAhead(main, main) = %d, want 0", n)
	}

	setupTwoFeats(t, dir)

	// feat-2 was branched from main (c0), so it's 1 commit ahead of main.
	n, err = c.CommitsAhead("main", "feat-2")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CommitsAhead(main, feat-2) = %d, want 1", n)
	}

	// feat-1 is also 1 commit ahead of main.
	n, err = c.CommitsAhead("main", "feat-1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CommitsAhead(main, feat-1) = %d, want 1", n)
	}

	// feat-1 and feat-2 diverge from main but feat-2 has c2 which is not
	// reachable from feat-1, so it's 1 commit ahead.
	n, err = c.CommitsAhead("feat-1", "feat-2")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CommitsAhead(feat-1, feat-2) = %d, want 1", n)
	}
}

func TestClient_commitHasParent(t *testing.T) {
	c, dir := initRepo(t)

	c0 := runGit(t, dir, "rev-parse", "HEAD")

	has, err := c.commitHasParent(c0)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Errorf("root commit c0 should have no parent")
	}

	runGit(t, dir, "checkout", "-q", "-b", "feat-1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")
	c1 := runGit(t, dir, "rev-parse", "HEAD")

	has, err = c.commitHasParent(c1)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Errorf("c1 should have a parent")
	}
}

func TestClient_getUpstream_NoUpstream(t *testing.T) {
	c, _ := initRepo(t)

	remote, merge, err := c.getUpstream("main")
	if err != nil {
		t.Fatal(err)
	}
	if remote != "" || merge != "" {
		t.Errorf("getUpstream(main) = (%q, %q), want empty", remote, merge)
	}
}

func TestClient_getUpstream_WithUpstream(t *testing.T) {
	c, dir := initRepo(t)

	runGit(t, dir, "config", "--local", "branch.main.remote", "origin")
	runGit(t, dir, "config", "--local", "branch.main.merge", "refs/heads/main")

	remote, merge, err := c.getUpstream("main")
	if err != nil {
		t.Fatal(err)
	}
	if remote != "origin" {
		t.Errorf("remote = %q, want origin", remote)
	}
	if merge != "main" {
		t.Errorf("merge = %q, want main", merge)
	}
}

func TestClient_Push_WithUpstream(t *testing.T) {
	c, dir := initRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat-1")
	runGit(t, dir, "commit", "--allow-empty", "-m", "c1")

	// Configure an upstream.
	runGit(t, dir, "config", "--local", "branch.feat-1.remote", "origin")
	runGit(t, dir, "config", "--local", "branch.feat-1.merge", "refs/heads/feat-1")

	err := c.Push("feat-1")
	if err == nil {
		t.Fatal("expected push to fail (no origin remote configured)")
	}
	var gitErr *Error
	if !errors.As(err, &gitErr) {
		t.Fatalf("expected *git.Error, got %T", err)
	}
	wantArgs := []string{"push", "--force-with-lease", "origin", "feat-1:feat-1"}
	if !slices.Equal(gitErr.Args, wantArgs) {
		t.Errorf("args = %v, want %v", gitErr.Args, wantArgs)
	}
}

func TestClient_WorktreeList(t *testing.T) {
	c, _ := initRepo(t)

	// With no worktrees, should return empty map.
	// Note: the primary worktree itself is included in porcelain output.
	list, err := c.WorktreeList()
	if err != nil {
		t.Fatal(err)
	}
	// At minimum, the primary worktree should be listed.
	if len(list) == 0 {
		t.Error("WorktreeList() should include the primary worktree")
	}
}

func TestClient_LoadGraph(t *testing.T) {
	c, dir := initRepo(t)

	graph, err := c.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}

	if !graph.HasBranch("main") {
		t.Error("graph should have main branch")
	}
	if len(graph.Branches()) != 1 {
		t.Errorf("graph.Branches() = %v, want [main]", graph.Branches())
	}

	// Add more branches and reload.
	setupTwoFeats(t, dir)

	graph, err = c.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Branches()) != 3 {
		t.Errorf("graph.Branches() = %v, want 3 branches", graph.Branches())
	}
}

func runGitHasBranch(dir, branch string) bool {
	cmd := exec.Command( //nolint:gosec,golines
		"git", "-C", dir, "rev-parse", "--verify", "refs/heads/"+branch,
	)
	return cmd.Run() == nil
}
