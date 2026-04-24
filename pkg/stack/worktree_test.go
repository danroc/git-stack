package stack

import (
	"errors"
	"strings"
	"testing"

	"github.com/danroc/git-stack/pkg/git"
)

func TestParseWorktreeList(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect map[string]string
	}{
		{
			name:   "empty",
			input:  "",
			expect: map[string]string{},
		},
		{
			name: "single worktree",
			input: "worktree /home/user/repo\n" +
				"HEAD abc123\n" +
				"branch refs/heads/main\n" +
				"\n",
			expect: map[string]string{"main": "/home/user/repo"},
		},
		{
			name: "multiple worktrees",
			input: "worktree /home/user/repo\n" +
				"HEAD abc123\n" +
				"branch refs/heads/main\n" +
				"\n" +
				"worktree /home/user/repo-feat\n" +
				"HEAD def456\n" +
				"branch refs/heads/feat-1\n" +
				"\n",
			expect: map[string]string{
				"main":   "/home/user/repo",
				"feat-1": "/home/user/repo-feat",
			},
		},
		{
			name: "detached HEAD is skipped",
			input: "worktree /home/user/repo\n" +
				"HEAD abc123\n" +
				"branch refs/heads/main\n" +
				"\n" +
				"worktree /home/user/repo-detached\n" +
				"HEAD def456\n" +
				"detached\n" +
				"\n",
			expect: map[string]string{"main": "/home/user/repo"},
		},
		{
			name: "bare worktree is skipped",
			input: "worktree /home/user/repo.git\n" +
				"HEAD abc123\n" +
				"bare\n" +
				"\n" +
				"worktree /home/user/repo\n" +
				"HEAD def456\n" +
				"branch refs/heads/main\n" +
				"\n",
			expect: map[string]string{"main": "/home/user/repo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := git.ParseWorktreeList(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("got %d entries, want %d: %v", len(got), len(tt.expect), got)
			}
			for k, v := range tt.expect {
				if got[k] != v {
					t.Errorf("branch %q: got %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// fakeRepository records method calls for verification.
type fakeRepository struct {
	currentBranch string
	calls         []string
}

func (f *fakeRepository) CurrentBranch() (string, error) {
	f.calls = append(f.calls, "CurrentBranch")
	return f.currentBranch, nil
}

func (f *fakeRepository) Checkout(branch string) error {
	f.calls = append(f.calls, "Checkout:"+branch)
	return nil
}

func (f *fakeRepository) Push(branch string) error {
	f.calls = append(f.calls, "Push:"+branch)
	return nil
}

func (f *fakeRepository) Pull() error {
	f.calls = append(f.calls, "Pull")
	return nil
}

func (f *fakeRepository) Rebase(onto string) error {
	f.calls = append(f.calls, "Rebase:"+onto)
	return nil
}

func (f *fakeRepository) RebaseOnto(newBase, upstream, branch string) error {
	f.calls = append(f.calls, "RebaseOnto:"+newBase+":"+upstream+":"+branch)
	return nil
}

func TestWorktreeGitOps_CheckoutDelegatesToRemote(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}
	remote := &fakeRepository{}

	w := newWorktreeGitOps(primary, map[string]string{
		"main":   "/repo",
		"feat-1": "/repo-feat",
	}, "/repo", func(dir string) Repository {
		if dir != "/repo-feat" {
			t.Fatalf("unexpected dir %q", dir)
		}
		return remote
	})

	// Checkout a branch in another worktree — should NOT call primary.Checkout
	if err := w.Checkout("feat-1"); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 0 {
		t.Errorf("expected no calls on primary, got %v", primary.calls)
	}

	// Pull should go to the remote instance
	if err := w.Pull(); err != nil {
		t.Fatal(err)
	}
	if len(remote.calls) != 1 || remote.calls[0] != "Pull" {
		t.Errorf("expected Pull on remote, got %v", remote.calls)
	}

	// Rebase should also go to the remote instance
	if err := w.Rebase("main"); err != nil {
		t.Fatal(err)
	}
	if len(remote.calls) != 2 || remote.calls[1] != "Rebase:main" {
		t.Errorf("expected Rebase:main on remote, got %v", remote.calls)
	}
}

func TestWorktreeGitOps_RebaseOntoDelegatesToRemote(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}
	remote := &fakeRepository{}

	w := newWorktreeGitOps(primary, map[string]string{
		"main":   "/repo",
		"feat-1": "/repo-feat",
	}, "/repo", func(dir string) Repository {
		if dir != "/repo-feat" {
			t.Fatalf("unexpected dir %q", dir)
		}
		return remote
	})

	if err := w.Checkout("feat-1"); err != nil {
		t.Fatal(err)
	}
	if err := w.RebaseOnto("main", "feat-0", "feat-1"); err != nil {
		t.Fatal(err)
	}

	if len(primary.calls) != 0 {
		t.Errorf("expected no calls on primary, got %v", primary.calls)
	}
	want := []string{"RebaseOnto:main:feat-0:feat-1"}
	if !equalStrings(remote.calls, want) {
		t.Errorf("expected remote calls %v, got %v", want, remote.calls)
	}
}

func TestWorktreeGitOps_CheckoutLocalBranch(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}

	w := newWorktreeGitOps(primary, map[string]string{
		"main": "/repo",
	}, "/repo", func(_ string) Repository {
		t.Fatal("should not create remote git for local branch")
		return nil
	})

	// Checkout a branch not in any other worktree
	if err := w.Checkout("feat-2"); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 1 || primary.calls[0] != "Checkout:feat-2" {
		t.Errorf("expected Checkout:feat-2 on primary, got %v", primary.calls)
	}

	// Pull should go to primary
	if err := w.Pull(); err != nil {
		t.Fatal(err)
	}
	if primary.calls[1] != "Pull" {
		t.Errorf("expected Pull on primary, got %v", primary.calls)
	}
}

func TestWorktreeGitOps_PushAlwaysPrimary(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}
	remote := &fakeRepository{}

	w := newWorktreeGitOps(primary, map[string]string{
		"main":   "/repo",
		"feat-1": "/repo-feat",
	}, "/repo", func(_ string) Repository {
		return remote
	})

	// Even after switching to a remote worktree, Push goes to primary
	_ = w.Checkout("feat-1")
	if err := w.Push("feat-1"); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 1 || primary.calls[0] != "Push:feat-1" {
		t.Errorf("expected Push:feat-1 on primary, got %v", primary.calls)
	}
	// remote should only have no Push calls
	for _, c := range remote.calls {
		if c == "Push:feat-1" {
			t.Error("Push should not go to remote")
		}
	}
}

func TestWorktreeGitOps_CurrentBranchAlwaysPrimary(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}

	w := newWorktreeGitOps(primary, map[string]string{
		"feat-1": "/repo-feat",
	}, "/repo", func(_ string) Repository {
		return &fakeRepository{currentBranch: "feat-1"}
	})

	_ = w.Checkout("feat-1")
	branch, err := w.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("expected main, got %s", branch)
	}
}

func TestWorktreeGitOps_CheckoutSameWorktree(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}

	w := newWorktreeGitOps(primary, map[string]string{
		"main": "/repo",
	}, "/repo", func(_ string) Repository {
		t.Fatal("should not create remote git for same worktree")
		return nil
	})

	// Checking out a branch that IS in worktrees but at the current dir
	// should delegate to primary (normal checkout).
	if err := w.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 1 || primary.calls[0] != "Checkout:main" {
		t.Errorf("expected Checkout:main on primary, got %v", primary.calls)
	}
}

func TestWorktreeGitOps_ErrorWrapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action func(*worktreeGitOps) error
		fail   error
	}{
		{
			name: "pull",
			action: func(w *worktreeGitOps) error {
				return w.Pull()
			},
			fail: errors.New("remote pull failed"),
		},
		{
			name: "rebase",
			action: func(w *worktreeGitOps) error {
				return w.Rebase("main")
			},
			fail: errors.New("rebase conflict"),
		},
		{
			name: "rebase-onto",
			action: func(w *worktreeGitOps) error {
				return w.RebaseOnto("main", "feat-0", "feat-1")
			},
			fail: errors.New("rebase conflict"),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			primary := &fakeRepository{currentBranch: "main"}
			remote := &failingRepo{
				fakeRepository: fakeRepository{currentBranch: "main"},
				failPull:       tt.fail,
				failRebaseOn:   map[string]error{"main": tt.fail},
				failRebaseOnto: tt.fail,
			}

			w := newWorktreeGitOps(primary, map[string]string{
				"main":   "/repo",
				"feat-1": "/repo-feat",
			}, "/repo", func(dir string) Repository {
				if dir != "/repo-feat" {
					t.Fatalf("unexpected dir %q", dir)
				}
				return remote
			})

			if err := w.Checkout("feat-1"); err != nil {
				t.Fatal(err)
			}

			err := tt.action(w)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "/repo-feat") {
				t.Errorf("error should contain worktree path, got: %v", err)
			}
		})
	}
}

func TestWorktreeGitOps_EmptyWorktrees(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}

	w := newWorktreeGitOps(
		primary,
		map[string]string{},
		"/repo",
		func(_ string) Repository {
			t.Fatal("should not create remote git")
			return nil
		},
	)

	if err := w.Checkout("feat-1"); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 1 || primary.calls[0] != "Checkout:feat-1" {
		t.Errorf("expected Checkout:feat-1 on primary, got %v", primary.calls)
	}
}

func TestWorktreeGitOps_NilWorktrees(t *testing.T) {
	primary := &fakeRepository{currentBranch: "main"}

	w := newWorktreeGitOps(primary, nil, "/repo", func(_ string) Repository {
		t.Fatal("should not create remote git")
		return nil
	})

	if err := w.Checkout("feat-1"); err != nil {
		t.Fatal(err)
	}
	if len(primary.calls) != 1 || primary.calls[0] != "Checkout:feat-1" {
		t.Errorf("expected Checkout:feat-1 on primary, got %v", primary.calls)
	}
}

func TestWorktreeGitOps_PrimaryErrorNoWrapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action func(Repository) error
		fail   error
	}{
		{
			name: "pull",
			action: func(r Repository) error {
				return r.Pull()
			},
			fail: errors.New("primary pull failed"),
		},
		{
			name: "rebase",
			action: func(r Repository) error {
				return r.Rebase("main")
			},
			fail: errors.New("rebase failed"),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			primary := &failingRepo{
				fakeRepository: fakeRepository{currentBranch: "main"},
				failPull:       tt.fail,
				failRebaseOn:   map[string]error{"main": tt.fail},
			}

			w := newWorktreeGitOps(primary, map[string]string{
				"main": "/repo",
			}, "/repo", func(_ string) Repository {
				t.Fatal("should not create remote git")
				return nil
			})

			err := tt.action(w)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			// Error should be the original, not wrapped with worktree path.
			if err.Error() != tt.fail.Error() {
				t.Errorf("error should not be wrapped, got: %v", err)
			}
		})
	}
}
