// Package stack orchestrates bulk operations across a branch stack.
package stack

import (
	"fmt"
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
	IsBranchDescendant(ancestor, descendant string) bool
	Parent(branch string) (string, error)
	SubtreeMembers(branchName string) []discovery.BranchWithParent
	SetParent(branch, parent string) error
}

// Step describes one unit of work within a bulk operation.
type Step struct {
	Branch string
	Parent string // rebase target or old parent; empty for Push and Pull
	To     string // new parent; set only for the initial step of a Move
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
// name and its immediate parent's name.
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

	for i, member := range members {
		if member.Name == s.disc.BaseBranch() {
			continue
		}
		parent := members[i-1].Name
		if err := action(member.Name, parent); err != nil {
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

// orNoop returns fn if non-nil, otherwise a no-op NotifyFn.
func orNoop(fn NotifyFn) NotifyFn {
	if fn != nil {
		return fn
	}
	return func(Step, bool) {}
}

// Push pushes every non-base branch in the stack to its upstream, bottom-to-top.
func (s *Stack) Push(notify NotifyFn) error {
	n := orNoop(notify)
	return s.forEachBranch(false, func(branch, _ string) error {
		n(Step{Branch: branch}, false)
		if err := s.git.Push(branch); err != nil {
			return fmt.Errorf("push %s failed: %w", branch, err)
		}
		n(Step{Branch: branch}, true)
		return nil
	})
}

// Rebase rebases each non-base branch onto the current tip of its immediate parent,
// bottom-to-top. On conflict it halts and leaves the repository in the in-progress
// rebase state. On full success it restores the original branch.
func (s *Stack) Rebase(notify NotifyFn) error {
	n := orNoop(notify)
	return s.forEachBranch(true, func(branch, parent string) error {
		n(Step{Branch: branch, Parent: parent}, false)
		if err := s.git.Checkout(branch); err != nil {
			return fmt.Errorf("checkout %s failed: %w", branch, err)
		}
		if err := s.git.Rebase(parent); err != nil {
			return fmt.Errorf("rebase %s onto %s failed: %w", branch, parent, err)
		}
		n(Step{Branch: branch, Parent: parent}, true)
		return nil
	})
}

// Pull checks out and pulls (--rebase) every non-base branch in order. On failure it
// halts and leaves the repo on the failing branch so the user can resolve conflicts and
// re-run. On full success it restores the original branch.
func (s *Stack) Pull(notify NotifyFn) error {
	n := orNoop(notify)
	return s.forEachBranch(true, func(branch, _ string) error {
		n(Step{Branch: branch}, false)
		if err := s.git.Checkout(branch); err != nil {
			return fmt.Errorf("checkout %s failed: %w", branch, err)
		}
		if err := s.git.Pull(); err != nil {
			return fmt.Errorf("pull %s failed: %w", branch, err)
		}
		n(Step{Branch: branch}, true)
		return nil
	})
}

// Move rebases branch from its current parent onto newParent, then cascades the rebase
// to all of branch's descendants. On conflict it halts leaving the repo in the
// in-progress rebase state. On full success it restores the original branch.
func (s *Stack) Move(branch, newParent string, notify NotifyFn) error {
	n := orNoop(notify)

	current, err := s.git.CurrentBranch()
	if err != nil {
		return err
	}

	if branch == newParent {
		return fmt.Errorf("cannot move %s onto itself", branch)
	}
	if s.disc.IsBranchDescendant(branch, newParent) {
		return fmt.Errorf(
			"cannot move %s onto %s: would create a cycle",
			branch,
			newParent,
		)
	}

	oldParent, err := s.disc.Parent(branch)
	if err != nil {
		return fmt.Errorf("cannot determine current parent of %s: %w", branch, err)
	}
	if oldParent == newParent {
		return fmt.Errorf("%s is already a child of %s", branch, newParent)
	}

	// Snapshot the subtree before the move; commit hashes change after rebase.
	descendants := s.disc.SubtreeMembers(branch)

	moveStep := Step{Branch: branch, Parent: oldParent, To: newParent}
	n(moveStep, false)
	if err := s.git.RebaseOnto(newParent, oldParent, branch); err != nil {
		return fmt.Errorf(
			"rebase --onto %s %s %s: %w",
			newParent,
			oldParent,
			branch,
			err,
		)
	}
	if err := s.disc.SetParent(branch, newParent); err != nil {
		return err
	}
	n(moveStep, true)

	for _, dep := range descendants {
		step := Step{Branch: dep.Branch.Name, Parent: dep.Parent}
		n(step, false)
		if err := s.git.Checkout(dep.Branch.Name); err != nil {
			return fmt.Errorf("checkout %s failed: %w", dep.Branch.Name, err)
		}
		if err := s.git.Rebase(dep.Parent); err != nil {
			return fmt.Errorf(
				"rebase %s onto %s failed: %w",
				dep.Branch.Name,
				dep.Parent,
				err,
			)
		}
		n(step, true)
	}

	if err := s.git.Checkout(current); err != nil {
		return fmt.Errorf("restoring branch %s: %w", current, err)
	}
	return nil
}
