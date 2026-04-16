package stack

import "fmt"

// worktreeGitOps wraps a GitOps and transparently handles branches that are
// checked out in other git worktrees. When Checkout is called for such a
// branch, it switches the active GitOps to one scoped to that worktree's
// directory (skipping the actual checkout since the branch is already active
// there). Pull and Rebase then run against the active instance.
type worktreeGitOps struct {
	primary    GitOps
	worktrees  map[string]string       // branch name → worktree absolute path
	currentDir string                  // absolute path of the primary worktree
	newGit     func(dir string) GitOps // factory for creating directory-scoped GitOps
	active     GitOps                  // the GitOps that Pull/Rebase should use
	activeDir  string                  // path of the active worktree ("" = primary)
}

func newWorktreeGitOps(
	primary GitOps,
	worktrees map[string]string,
	currentDir string,
	newGit func(dir string) GitOps,
) *worktreeGitOps {
	return &worktreeGitOps{
		primary:    primary,
		worktrees:  worktrees,
		currentDir: currentDir,
		newGit:     newGit,
		active:     primary,
	}
}

func (w *worktreeGitOps) CurrentBranch() (string, error) {
	return w.primary.CurrentBranch()
}

func (w *worktreeGitOps) Checkout(branch string) error {
	if dir, ok := w.worktrees[branch]; ok && dir != w.currentDir {
		// Branch is checked out in another worktree — switch context there.
		w.active = w.newGit(dir)
		w.activeDir = dir
		return nil
	}
	// Branch is local to this worktree (or not in any worktree).
	w.active = w.primary
	w.activeDir = ""
	return w.primary.Checkout(branch)
}

func (w *worktreeGitOps) Push(branch string) error {
	return w.primary.Push(branch)
}

func (w *worktreeGitOps) Pull() error {
	err := w.active.Pull()
	if err != nil && w.activeDir != "" {
		return fmt.Errorf("in worktree %s: %w", w.activeDir, err)
	}
	return err
}

func (w *worktreeGitOps) Rebase(onto string) error {
	err := w.active.Rebase(onto)
	if err != nil && w.activeDir != "" {
		return fmt.Errorf("in worktree %s: %w", w.activeDir, err)
	}
	return err
}
