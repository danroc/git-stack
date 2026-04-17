// Package stack orchestrates bulk operations across a branch stack.
package stack

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"git-stack/pkg/discovery"
	"git-stack/pkg/git"
	"git-stack/pkg/ui"
)

// Repository abstracts the git operations that stack commands need. Satisfied by
// *git.Client in production and by fakes in tests.
type Repository interface {
	CurrentBranch() (string, error)
	Checkout(branch string) error
	Push(branch string) error
	Pull() error
	Rebase(onto string) error
}

// Stack orchestrates push/pull/rebase across every branch in a discovered stack.
type Stack struct {
	git          Repository
	disc         *discovery.Engine
	chooseBranch discovery.ChooseBranchFn
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
func New(g *git.Client, base string) (*Stack, error) {
	var ops Repository = g
	if wts, err := g.WorktreeList(); err == nil && len(wts) > 1 {
		cwd, _ := filepath.Abs(".")
		ops = newWorktreeGitOps(g, wts, cwd, func(dir string) Repository {
			return git.NewClient(dir)
		})
	}
	disc, err := discovery.NewEngine(g, base)
	if err != nil {
		return nil, err
	}
	return &Stack{
		git:          ops,
		disc:         disc,
		chooseBranch: ui.Disambiguate,
	}, nil
}

// writeGitErr prints the stderr from a git.Error to w with surrounding blank lines.
func writeGitErr(w *writer, err error) {
	var gitErr *git.Error
	if errors.As(err, &gitErr) && gitErr.Stderr != "" {
		w.printf("\n\n%s\n\n", gitErr.Stderr)
	} else {
		w.printf("\n")
	}
}

// branchAction is called for each non-base branch in the stack. It receives the
// branch name, its parent's name, and the index in the member list.
type branchAction func(branch, parent string) error

// forEachBranch discovers the stack, iterates non-base members bottom-to-top,
// and calls action for each. If restoreBranch is true, the original branch is
// checked out after all actions succeed.
func (s *Stack) forEachBranch(
	w io.Writer,
	restoreBranch bool,
	action branchAction,
) error {
	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}

	members, err := s.disc.DiscoverStack(current, s.chooseBranch)
	if err != nil {
		return err
	}

	ew := &writer{w: w}
	for i, m := range members {
		if m.BranchName == s.disc.BaseBranch() {
			continue
		}
		parent := members[i-1].BranchName
		if err := action(m.BranchName, parent); err != nil {
			writeGitErr(ew, err)
			return err
		}
	}

	if restoreBranch {
		if err := s.git.Checkout(current); err != nil {
			return fmt.Errorf("restoring branch %s: %w", current, err)
		}
	}
	return nil
}

// Push pushes every non-base branch in the stack to its upstream, bottom-to-top.
func (s *Stack) Push(w io.Writer) error {
	ew := &writer{w: w}
	return s.forEachBranch(w, false, func(branch, _ string) error {
		ew.printf("Pushing %s... ", branch)
		if err := s.git.Push(branch); err != nil {
			return fmt.Errorf("push %s failed: %w", branch, err)
		}
		ew.printf("done\n")
		return nil
	})
}

// Rebase rebases each non-base branch onto the current tip of its immediate
// parent, bottom-to-top. On conflict it halts and leaves the repository in the
// in-progress rebase state. On full success it restores the original branch.
func (s *Stack) Rebase(w io.Writer) error {
	ew := &writer{w: w}
	return s.forEachBranch(w, true, func(branch, parent string) error {
		ew.printf("Rebasing %s onto %s... ", branch, parent)
		if err := s.git.Checkout(branch); err != nil {
			return fmt.Errorf("checkout %s failed: %w", branch, err)
		}
		if err := s.git.Rebase(parent); err != nil {
			return fmt.Errorf("rebase %s onto %s failed: %w", branch, parent, err)
		}
		ew.printf("done\n")
		return nil
	})
}

// Pull checks out and pulls (--rebase) every non-base branch in order.
// On failure it halts and leaves the repo on the failing branch so the user
// can resolve conflicts and re-run. On full success it restores the original branch.
func (s *Stack) Pull(w io.Writer) error {
	ew := &writer{w: w}
	return s.forEachBranch(w, true, func(branch, _ string) error {
		ew.printf("Pulling %s... ", branch)
		if err := s.git.Checkout(branch); err != nil {
			return fmt.Errorf("checkout %s failed: %w", branch, err)
		}
		if err := s.git.Pull(); err != nil {
			return fmt.Errorf("pull %s failed: %w", branch, err)
		}
		ew.printf("done\n")
		return nil
	})
}
