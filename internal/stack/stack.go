// Package stack orchestrates bulk operations across a branch stack.
package stack

import (
	"fmt"
	"path/filepath"

	"github.com/danroc/git-stack/internal/discovery"
	"github.com/danroc/git-stack/internal/git"
	"github.com/danroc/git-stack/internal/ui"
)

// Repository abstracts the git operations that stack commands need. Satisfied by
// *git.Client in production and by fakes in tests.
type Repository interface {
	CurrentBranch() (string, error)
	Checkout(branch string) error
	Push(branch string) error
	Pull() error
	Rebase(onto string) error
	RebaseOnto(newBase, upstream, branch string) error
}

// Discoverer abstracts the stack-discovery operations that Stack needs. Satisfied by
// *discovery.Engine in production and by fakes in tests.
type Discoverer interface {
	BaseBranch() string
	DiscoverStack(
		currentBranch string,
		chooseBranch discovery.ChooseBranchFn,
	) ([]discovery.Branch, error)
	IsChildOf(child, parent string) bool
	Parent(branch string) (string, error)
	SubtreeChildren(branchName string) []discovery.BranchWithParent
	SetParent(branch, parent string) error
}

// Step describes one unit of work within a bulk operation.
type Step struct {
	Branch string
	Parent string // Rebase target or old parent (branch below); empty for Push and Pull
	To     string // New parent (branch below); set only for the initial step of a Move
}

// NotifyFn is called before (done=false) and after (done=true) each step to report
// incremental progress. nil is valid and produces no output.
type NotifyFn func(step Step, done bool)

// Stack orchestrates push/pull/rebase across every branch in a discovered stack.
type Stack struct {
	git          Repository
	disc         Discoverer
	chooseBranch discovery.ChooseBranchFn
}

// New constructs a Stack using the provided Git adapter and base branch, wiring the
// interactive disambiguation prompt. If other worktrees are detected, git operations
// are wrapped to handle branches checked out there.
func New(g *git.Client, base string) (*Stack, error) {
	var ops Repository = g
	if worktrees, err := g.WorktreeList(); err == nil && len(worktrees) > 1 {
		cwd, _ := filepath.Abs(".")
		ops = newWorktreeGitOps(g, worktrees, cwd, func(dir string) Repository {
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

// branchAction is called for each non-base branch in the stack. It receives the branch
// name and its parent (the branch below it in the stack).
type branchAction func(branch, parent string) error

// forEachBranch discovers the stack, iterates non-base members bottom-to-top, and calls
// action for each. If restoreBranch is true, the original branch is checked out after
// all actions succeed.
func (s *Stack) forEachBranch(restoreBranch bool, action branchAction) error {
	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}

	members, err := s.disc.DiscoverStack(current, s.chooseBranch)
	if err != nil {
		return err
	}

	parent := s.disc.BaseBranch()
	for _, member := range members {
		if member.Name == parent {
			continue
		}
		if err := action(member.Name, parent); err != nil {
			return err
		}
		parent = member.Name
	}

	if restoreBranch {
		return s.restoreBranch(current)
	}
	return nil
}

// orNoop returns fn if non-nil, otherwise a no-op NotifyFn.
func orNoop(fn NotifyFn) NotifyFn {
	if fn != nil {
		return fn
	}
	return func(Step, bool) {}
}

// runStep emits start/done notifications around a step and only emits done on success.
func runStep(notify NotifyFn, step Step, fn func() error) error {
	notify(step, false)
	if err := fn(); err != nil {
		return err
	}
	notify(step, true)
	return nil
}

func (s *Stack) restoreBranch(branch string) error {
	if err := s.git.Checkout(branch); err != nil {
		return fmt.Errorf("restore branch %s: %w", branch, err)
	}
	return nil
}

// Push pushes every non-base branch in the stack to its upstream, bottom-to-top.
func (s *Stack) Push(fn NotifyFn) error {
	notify := orNoop(fn)
	return s.forEachBranch(false, func(branch, _ string) error {
		return runStep(notify, Step{Branch: branch}, func() error {
			if err := s.git.Push(branch); err != nil {
				return fmt.Errorf("push %s: %w", branch, err)
			}
			return nil
		})
	})
}

// Rebase rebases each non-base branch onto the current tip of its immediate parent,
// bottom-to-top. On conflict it halts and leaves the repository in the in-progress
// rebase state. On full success it restores the original branch.
func (s *Stack) Rebase(fn NotifyFn) error {
	notify := orNoop(fn)
	return s.forEachBranch(true, func(branch, parent string) error {
		return runStep(notify, Step{Branch: branch, Parent: parent}, func() error {
			return s.checkoutAndRebase(branch, parent)
		})
	})
}

// rebaseAndSetParent checks out branch, runs the supplied rebase operation, and records
// parent in stack metadata.
func (s *Stack) rebaseAndSetParent(branch, parent string, rebase func() error) error {
	if err := s.git.Checkout(branch); err != nil {
		return fmt.Errorf("checkout %s: %w", branch, err)
	}
	if err := rebase(); err != nil {
		return err
	}
	if err := s.disc.SetParent(branch, parent); err != nil {
		return fmt.Errorf("update stack metadata for %s: %w", branch, err)
	}
	return nil
}

// checkoutAndRebase checks out branch, rebases it onto parent, and records the parent
// in stack metadata.
func (s *Stack) checkoutAndRebase(branch, parent string) error {
	return s.rebaseAndSetParent(branch, parent, func() error {
		if err := s.git.Rebase(parent); err != nil {
			return fmt.Errorf("rebase %s onto %s: %w", branch, parent, err)
		}
		return nil
	})
}

// checkoutAndRebaseOnto checks out branch, rebases it from oldParent onto newParent,
// and records the new parent in stack metadata.
func (s *Stack) checkoutAndRebaseOnto(branch, oldParent, newParent string) error {
	return s.rebaseAndSetParent(branch, newParent, func() error {
		if err := s.git.RebaseOnto(newParent, oldParent, branch); err != nil {
			return fmt.Errorf(
				"move %s from %s to %s: %w", branch, oldParent, newParent, err,
			)
		}
		return nil
	})
}

// Pull checks out and pulls (--rebase) every non-base branch in order. On failure it
// halts and leaves the repo on the failing branch so the user can resolve conflicts and
// re-run. On full success it restores the original branch.
func (s *Stack) Pull(fn NotifyFn) error {
	notify := orNoop(fn)
	return s.forEachBranch(true, func(branch, _ string) error {
		return runStep(notify, Step{Branch: branch}, func() error {
			if err := s.git.Checkout(branch); err != nil {
				return fmt.Errorf("checkout %s: %w", branch, err)
			}
			if err := s.git.Pull(); err != nil {
				return fmt.Errorf("pull %s: %w", branch, err)
			}
			return nil
		})
	})
}

// Move rebases branch from its current parent onto newParent, then cascades the rebase
// to all of branch's children. On conflict it halts leaving the repo in the in-progress
// rebase state. On full success it restores the original branch.
func (s *Stack) Move(branch, newParent string, fn NotifyFn) error {
	notify := orNoop(fn)

	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}

	if branch == newParent {
		return fmt.Errorf("cannot move %s onto itself", branch)
	}
	if s.disc.IsChildOf(newParent, branch) {
		return fmt.Errorf(
			"cannot move %s onto %s: would create a cycle",
			branch,
			newParent,
		)
	}

	oldParent, err := s.disc.Parent(branch)
	if err != nil {
		return fmt.Errorf("find parent of %s: %w", branch, err)
	}
	if oldParent == newParent {
		return fmt.Errorf("%s is already a child of %s", branch, newParent)
	}

	// Snapshot the subtree before the move; commit hashes change after rebase.
	children := s.disc.SubtreeChildren(branch)

	if err := runStep(
		notify,
		Step{Branch: branch, Parent: oldParent, To: newParent},
		func() error { return s.checkoutAndRebaseOnto(branch, oldParent, newParent) },
	); err != nil {
		return err
	}

	for _, dep := range children {
		if err := runStep(
			notify,
			Step{Branch: dep.Branch.Name, Parent: dep.Parent},
			func() error { return s.checkoutAndRebase(dep.Branch.Name, dep.Parent) },
		); err != nil {
			return err
		}
	}

	return s.restoreBranch(current)
}
