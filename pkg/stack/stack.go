// Package stack orchestrates bulk operations across a branch stack.
package stack

import (
	"fmt"
	"io"
	"path/filepath"

	"git-stack/pkg/engine"
	"git-stack/pkg/gitutils"
	"git-stack/pkg/ui"
)

// GitOps abstracts the git operations that stack commands need. Satisfied by
// *gitutils.Git in production and by fakes in tests.
type GitOps interface {
	CurrentBranch() (string, error)
	Checkout(branch string) error
	Push(branch string) error
	Pull() error
	Rebase(onto string) error
}

// Stack orchestrates push/pull/rebase across every branch in a discovered stack.
type Stack struct {
	git          GitOps
	disc         *engine.DiscoveryEngine
	disambiguate engine.DisambiguateFn
}

// writer wraps an io.Writer and absorbs write errors after the first failure.
type writer struct {
	w   io.Writer
	err error
}

func (ew *writer) printf(format string, args ...any) {
	if ew.err == nil {
		_, ew.err = fmt.Fprintf(ew.w, format, args...)
	}
}

// New constructs a Stack using the provided Git adapter and base branch,
// wiring the interactive disambiguation prompt. If other worktrees are
// detected, git operations are wrapped to handle branches checked out there.
func New(git *gitutils.Git, base string) (*Stack, error) {
	var ops GitOps = git
	if wts, err := git.WorktreeList(); err == nil && len(wts) > 1 {
		cwd, _ := filepath.Abs(".")
		ops = newWorktreeGitOps(git, wts, cwd, func(dir string) GitOps {
			return gitutils.NewGit(dir)
		})
	}
	disc, err := engine.NewDiscoveryEngine(git, base)
	if err != nil {
		return nil, err
	}
	return &Stack{
		git:          ops,
		disc:         disc,
		disambiguate: ui.Disambiguate,
	}, nil
}

// Push pushes every non-base branch in the stack, bottom-to-top.
// Halts on the first failure and reports the failing branch.
func (s *Stack) Push(w io.Writer) error {
	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}
	members, err := s.disc.DiscoverStack(current, s.disambiguate)
	if err != nil {
		return err
	}
	ew := &writer{w: w}
	for _, m := range members {
		if m.BranchName == s.disc.BaseBranch() {
			continue
		}
		ew.printf("Pushing %s... ", m.BranchName)
		if err := s.git.Push(m.BranchName); err != nil {
			ew.printf("\n")
			return fmt.Errorf("push %s: %w", m.BranchName, err)
		}
		ew.printf("done\n")
	}
	return nil
}

// Rebase rebases each non-base branch onto the current tip of its immediate
// parent, bottom-to-top. On conflict it halts and leaves the repository in the
// in-progress rebase state. On full success it restores the original branch.
func (s *Stack) Rebase(w io.Writer) error {
	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}
	members, err := s.disc.DiscoverStack(current, s.disambiguate)
	if err != nil {
		return err
	}
	ew := &writer{w: w}
	for i, m := range members {
		if m.BranchName == s.disc.BaseBranch() {
			continue
		}
		parent := members[i-1].BranchName
		ew.printf("Rebasing %s onto %s... ", m.BranchName, parent)
		if err := s.git.Checkout(m.BranchName); err != nil {
			ew.printf("\n")
			return fmt.Errorf("checkout %s: %w", m.BranchName, err)
		}
		if err := s.git.Rebase(parent); err != nil {
			ew.printf("\n")
			return fmt.Errorf(
				"rebase %s onto %s: %w\nresolve with: git rebase --continue  or  git rebase --abort",
				m.BranchName,
				parent,
				err,
			)
		}
		ew.printf("done\n")
	}
	if err := s.git.Checkout(current); err != nil {
		return fmt.Errorf("restoring branch %s: %w", current, err)
	}
	return nil
}

// Pull checks out and pulls (--rebase) every non-base branch in order.
// On failure it halts and leaves the repo on the failing branch so the user
// can resolve conflicts and re-run. On full success it restores the original branch.
func (s *Stack) Pull(w io.Writer) error {
	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}
	members, err := s.disc.DiscoverStack(current, s.disambiguate)
	if err != nil {
		return err
	}
	ew := &writer{w: w}
	for _, m := range members {
		if m.BranchName == s.disc.BaseBranch() {
			continue
		}
		ew.printf("Pulling %s... ", m.BranchName)
		if err := s.git.Checkout(m.BranchName); err != nil {
			ew.printf("\n")
			return fmt.Errorf("checkout %s: %w", m.BranchName, err)
		}
		if err := s.git.Pull(); err != nil {
			ew.printf("\n")
			return fmt.Errorf(
				"pull %s: %w\nresolve conflicts, then re-run git-stack pull",
				m.BranchName,
				err,
			)
		}
		ew.printf("done\n")
	}
	if err := s.git.Checkout(current); err != nil {
		return fmt.Errorf("restoring branch %s: %w", current, err)
	}
	return nil
}
