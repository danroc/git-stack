// Package main defines the Cobra commands for the git-stack CLI.
package main

import (
	"os"

	"git-stack/pkg/engine"
	"git-stack/pkg/gitutils"
	"git-stack/pkg/stack"
	"git-stack/pkg/ui"

	"github.com/spf13/cobra"
)

var baseBranch string

func main() {
	root := &cobra.Command{
		Use:          "git-stack",
		Short:        "Manage stacks of interdependent Git branches",
		SilenceUsage: true,
	}
	root.PersistentFlags().
		StringVar(&baseBranch, "base", "", "base branch (default: auto-detect)")

	root.AddCommand(cmdAdd(), cmdView(), cmdPush(), cmdPull(), cmdRebase())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// resolveBase returns a Git adapter and the base branch name (from --base flag or
// auto-detection).
func resolveBase() (*gitutils.Git, string, error) {
	git := gitutils.NewGit(".")
	base := baseBranch
	if base == "" {
		var err error
		base, err = engine.DetectBaseBranch(git)
		if err != nil {
			return nil, "", err
		}
	}
	return git, base, nil
}

// runStackCmd is a RunE handler that resolves the base, builds a Stack, and calls fn.
func runStackCmd(fn func(*stack.Stack) error) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, _ []string) error {
		git, base, err := resolveBase()
		if err != nil {
			return err
		}
		s, err := stack.New(git, base)
		if err != nil {
			return err
		}
		return fn(s)
	}
}

func cmdAdd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <branch-name>",
		Short: "Create a new branch from the current HEAD, extending the stack",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return gitutils.NewGit(".").CreateBranch(args[0])
		},
	}
}

func cmdView() *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Show the full stack tree",
		RunE: func(_ *cobra.Command, _ []string) error {
			git, base, err := resolveBase()
			if err != nil {
				return err
			}
			disc, err := engine.NewDiscoveryEngine(git, base)
			if err != nil {
				return err
			}
			current, err := git.CurrentBranch()
			if err != nil {
				return err
			}
			root := disc.BuildTree()
			ui.RenderTree(buildDisplayTree(root, current), os.Stdout)
			return nil
		},
	}
}

// buildDisplayTree maps engine nodes to ui nodes. CommitsAhead is already computed by
// the engine so this is a pure structural conversion.
func buildDisplayTree(node *engine.TreeNode, current string) *ui.TreeEntry {
	entry := &ui.TreeEntry{
		BranchName: node.Member.BranchName,
		AheadCount: node.CommitsAhead,
		IsCurrent:  node.Member.BranchName == current,
	}
	for _, child := range node.Children {
		entry.Children = append(entry.Children, buildDisplayTree(child, current))
	}
	return entry
}

func cmdPush() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Push all branches in the stack to their upstreams",
		RunE:  runStackCmd(func(s *stack.Stack) error { return s.Push(os.Stdout) }),
	}
}

func cmdPull() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull all branches in the stack from their upstreams",
		RunE:  runStackCmd(func(s *stack.Stack) error { return s.Pull(os.Stdout) }),
	}
}

func cmdRebase() *cobra.Command {
	return &cobra.Command{
		Use:   "rebase",
		Short: "Rebase each branch in the stack onto the tip of its parent, bottom-to-top",
		RunE:  runStackCmd(func(s *stack.Stack) error { return s.Rebase(os.Stdout) }),
	}
}
