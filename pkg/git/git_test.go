package git

import (
	"os/exec"
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
