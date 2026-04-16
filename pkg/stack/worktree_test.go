package stack

import (
	"testing"

	"git-stack/pkg/gitutils"
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
			got := gitutils.ParseWorktreeList(tt.input)
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

// fakeGitOps records method calls for verification.
type fakeGitOps struct {
	currentBranch string
	calls         []string
}

func (f *fakeGitOps) CurrentBranch() (string, error) {
	f.calls = append(f.calls, "CurrentBranch")
	return f.currentBranch, nil
}

func (f *fakeGitOps) Checkout(branch string) error {
	f.calls = append(f.calls, "Checkout:"+branch)
	return nil
}

func (f *fakeGitOps) Push(branch string) error {
	f.calls = append(f.calls, "Push:"+branch)
	return nil
}

func (f *fakeGitOps) Pull() error {
	f.calls = append(f.calls, "Pull")
	return nil
}

func (f *fakeGitOps) Rebase(onto string) error {
	f.calls = append(f.calls, "Rebase:"+onto)
	return nil
}

func TestWorktreeGitOps_CheckoutDelegatesToRemote(t *testing.T) {
	primary := &fakeGitOps{currentBranch: "main"}
	remote := &fakeGitOps{}

	w := newWorktreeGitOps(primary, map[string]string{
		"main":   "/repo",
		"feat-1": "/repo-feat",
	}, "/repo", func(dir string) GitOps {
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

func TestWorktreeGitOps_CheckoutLocalBranch(t *testing.T) {
	primary := &fakeGitOps{currentBranch: "main"}

	w := newWorktreeGitOps(primary, map[string]string{
		"main": "/repo",
	}, "/repo", func(_ string) GitOps {
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
	primary := &fakeGitOps{currentBranch: "main"}
	remote := &fakeGitOps{}

	w := newWorktreeGitOps(primary, map[string]string{
		"main":   "/repo",
		"feat-1": "/repo-feat",
	}, "/repo", func(_ string) GitOps {
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
	primary := &fakeGitOps{currentBranch: "main"}

	w := newWorktreeGitOps(primary, map[string]string{
		"feat-1": "/repo-feat",
	}, "/repo", func(_ string) GitOps {
		return &fakeGitOps{currentBranch: "feat-1"}
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
	primary := &fakeGitOps{currentBranch: "main"}

	w := newWorktreeGitOps(primary, map[string]string{
		"main": "/repo",
	}, "/repo", func(_ string) GitOps {
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
