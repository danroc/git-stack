//go:build integration

package stack

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danroc/git-stack/internal/git"
)

func TestFoldIntegration_LeafSquash(t *testing.T) {
	g, dir := initFoldRepo(t)
	runFoldGit(t, dir, "checkout", "-q", "-b", "feat-1")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runFoldGit(t, dir, "add", "f")
	runFoldGit(t, dir, "commit", "-m", "f1")
	if err := g.RecordStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}

	s, err := New(g, "main")
	if err != nil {
		t.Fatal(err)
	}
	opts := FoldOptions{Squash: true, DeleteBranch: true}
	if err := s.Fold("feat-1", opts, nil); err != nil {
		t.Fatal(err)
	}

	current, err := g.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if current != "main" {
		t.Fatalf("HEAD = %q, want main", current)
	}
	branches := runFoldGit(t, dir, "branch", "--list")
	if strings.Contains(branches, "feat-1") {
		t.Fatalf("feat-1 should be deleted, branches: %q", branches)
	}
	count := runFoldGit(t, dir, "rev-list", "--count", "main")
	if count != "2" {
		t.Fatalf("main commits = %s, want 2", count)
	}
}

func initFoldRepo(t *testing.T) (*git.Client, string) {
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
	return git.NewClient(dir), dir
}

func runFoldGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimRight(string(out), "\r\n")
}
