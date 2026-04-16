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
		Use:   "git-stack",
		Short: "Manage stacks of interdependent Git branches",
	}
	root.PersistentFlags().
		StringVar(&baseBranch, "base", "", "base branch (default: auto-detect)")

	root.AddCommand(cmdAdd(), cmdView(), cmdPush(), cmdPull(), cmdRebase())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// setup resolves the base branch (from --base flag or auto-detection) and returns the
// shared Git adapter, base name, and a DiscoveryEngine ready to use.
func setup() (*gitutils.Git, string, *engine.DiscoveryEngine, error) {
	git := gitutils.NewGit(".")
	base := baseBranch
	if base == "" {
		var err error
		base, err = engine.DetectBaseBranch(git)
		if err != nil {
			return nil, "", nil, err
		}
	}
	disc, err := engine.NewDiscoveryEngine(git, base)
	if err != nil {
		return nil, "", nil, err
	}
	return git, base, disc, nil
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
			git, _, disc, err := setup()
			if err != nil {
				return err
			}
			current, err := git.CurrentBranch()
			if err != nil {
				return err
			}
			root, err := disc.BuildTree()
			if err != nil {
				return err
			}
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
		RunE: func(_ *cobra.Command, _ []string) error {
			git, base, _, err := setup()
			if err != nil {
				return err
			}
			s, err := stack.New(git, base)
			if err != nil {
				return err
			}
			return s.Push(os.Stdout)
		},
	}
}

func cmdPull() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull all branches in the stack from their upstreams",
		RunE: func(_ *cobra.Command, _ []string) error {
			git, base, _, err := setup()
			if err != nil {
				return err
			}
			s, err := stack.New(git, base)
			if err != nil {
				return err
			}
			return s.Pull(os.Stdout)
		},
	}
}

func cmdRebase() *cobra.Command {
	return &cobra.Command{
		Use:   "rebase",
		Short: "Rebase each branch in the stack onto the tip of its parent, bottom-to-top",
		RunE: func(_ *cobra.Command, _ []string) error {
			git, base, _, err := setup()
			if err != nil {
				return err
			}
			s, err := stack.New(git, base)
			if err != nil {
				return err
			}
			return s.Rebase(os.Stdout)
		},
	}
}
