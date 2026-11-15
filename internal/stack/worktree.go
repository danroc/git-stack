package stack

import "fmt"

// worktreeGitOps wraps a Repository and transparently handles branches that are checked
// out in other git worktrees. When Checkout is called for such a branch, it switches
// the active Repository to one scoped to that worktree's directory (skipping the actual
// checkout since the branch is already active there). Pull and Rebase then run against
// the active instance.
type worktreeGitOps struct {
	primary    Repository
	worktrees  map[string]string           // branch name → worktree absolute path
	currentDir string                      // absolute path of the primary worktree
	newGit     func(dir string) Repository // factory for creating directory-scoped Repository
	active     Repository                  // the Repository that Pull/Rebase should use
	activeDir  string                      // path of the active worktree ("" = primary)
}

// newWorktreeGitOps creates a new worktreeGitOps that wraps primary and handles
// branches checked out in other worktrees.
func newWorktreeGitOps(
	primary Repository,
	worktrees map[string]string,
	currentDir string,
	newGit func(dir string) Repository,
) *worktreeGitOps {
	return &worktreeGitOps{
		primary:    primary,
		worktrees:  worktrees,
		currentDir: currentDir,
		newGit:     newGit,
		active:     primary,
	}
}

// CurrentBranch always queries the primary worktree, since branch info is shared.
func (w *worktreeGitOps) CurrentBranch() (string, error) {
	return w.primary.CurrentBranch()
}

// Checkout switches the active worktree context if the branch is checked out elsewhere,
// or performs a normal checkout if the branch is local to this worktree.
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

// Push always pushes from the primary worktree.
func (w *worktreeGitOps) Push(branch string) error {
	return w.primary.Push(branch)
}

// wrapErr annotates err with the active worktree path when the operation ran outside
// the primary worktree.
func (w *worktreeGitOps) wrapErr(err error) error {
	if err != nil && w.activeDir != "" {
		return fmt.Errorf("in worktree %s: %w", w.activeDir, err)
	}
	return err
}

// Pull pulls in the active worktree context.
func (w *worktreeGitOps) Pull() error {
	return w.wrapErr(w.active.Pull())
}

// Rebase rebases in the active worktree context.
func (w *worktreeGitOps) Rebase(onto string) error {
	return w.wrapErr(w.active.Rebase(onto))
}

// RebaseOnto rebases in the active worktree context.
func (w *worktreeGitOps) RebaseOnto(newBase, upstream, branch string) error {
	return w.wrapErr(w.active.RebaseOnto(newBase, upstream, branch))
}

// MergeSquash merges in the active worktree context.
func (w *worktreeGitOps) MergeSquash(ref string) error {
	return w.wrapErr(w.active.MergeSquash(ref))
}

// HasStagedChanges queries the active worktree context.
func (w *worktreeGitOps) HasStagedChanges() (bool, error) {
	has, err := w.active.HasStagedChanges()
	return has, w.wrapErr(err)
}

// Commit commits in the active worktree context.
func (w *worktreeGitOps) Commit(message string) error {
	return w.wrapErr(w.active.Commit(message))
}

// MergeFF merges in the active worktree context.
func (w *worktreeGitOps) MergeFF(ref string) error {
	return w.wrapErr(w.active.MergeFF(ref))
}

// DeleteBranch always deletes from the primary worktree.
func (w *worktreeGitOps) DeleteBranch(name string) error {
	return w.primary.DeleteBranch(name)
}

// UnsetStackConfig always unsets stack config from the primary worktree.
func (w *worktreeGitOps) UnsetStackConfig(branch string) error {
	return w.primary.UnsetStackConfig(branch)
}
