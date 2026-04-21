// Package main defines the Cobra commands for the git-stack CLI.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"git-stack/pkg/discovery"
	"git-stack/pkg/git"
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

	root.AddCommand(
		cmdAdd(),
		cmdMove(),
		cmdView(),
		cmdPush(),
		cmdPull(),
		cmdRebase(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// resolveBase returns a Git client and the base branch name (from --base flag or
// auto-detection).
func resolveBase() (*git.Client, string, error) {
	g := git.NewClient(".")
	base := baseBranch
	if base == "" {
		var err error
		base, err = discovery.DetectBase(g)
		if err != nil {
			return nil, "", err
		}
	}
	return g, base, nil
}

// runStackCmd is a RunE handler that resolves the base, builds a Stack, and calls fn.
func runStackCmd(fn func(*stack.Stack) error) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, _ []string) error {
		g, base, err := resolveBase()
		if err != nil {
			return err
		}
		s, err := stack.New(g, base)
		if err != nil {
			return err
		}
		return fn(s)
	}
}

// stepPrinter returns a NotifyFn that writes incremental progress to w. verb is used
// for rebase-type steps ("Rebasing", "Pulling", "Pushing"). Steps with To set are
// formatted as move operations.
func stepPrinter(w io.Writer, verb string) stack.NotifyFn {
	return func(s stack.Step, done bool) {
		if done {
			_, _ = fmt.Fprintln(w, "done")
			return
		}
		switch {
		case s.To != "":
			_, _ = fmt.Fprintf(
				w,
				"Moving %s from %s to %s... ",
				s.Branch, s.Parent, s.To,
			)
		case s.Parent != "":
			_, _ = fmt.Fprintf(w, "%s %s onto %s... ", verb, s.Branch, s.Parent)
		default:
			_, _ = fmt.Fprintf(w, "%s %s... ", verb, s.Branch)
		}
	}
}

// printGitStderr prints git's stderr to os.Stderr when a git operation fails, so the
// user sees the same output they'd get from running git directly.
func printGitStderr(err error) {
	var gitErr *git.Error
	if errors.As(err, &gitErr) && gitErr.Stderr != "" {
		_, _ = fmt.Fprintf(os.Stderr, "\n%s\n\n", gitErr.Stderr)
	}
}

func cmdAdd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <branch-name>",
		Short: "Create a new branch from the current HEAD, extending the stack",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			g := git.NewClient(".")
			current, err := g.CurrentBranch()
			if err != nil {
				return err
			}
			if err := g.CreateBranch(args[0]); err != nil {
				return err
			}
			return g.RecordStackParent(args[0], current)
		},
	}
}

func cmdView() *cobra.Command {
	return &cobra.Command{
		Use:   "view",
		Short: "Show the full stack tree",
		RunE: func(_ *cobra.Command, _ []string) error {
			g, base, err := resolveBase()
			if err != nil {
				return err
			}
			disc, err := discovery.NewEngine(g, base)
			if err != nil {
				return err
			}
			current, err := g.CurrentBranch()
			if err != nil {
				return err
			}
			root := disc.BuildTree()
			ui.RenderTree(buildDisplayTree(root, current), os.Stdout)
			return nil
		},
	}
}

// buildDisplayTree converts a discovery tree to a ui tree for rendering.
func buildDisplayTree(node *discovery.TreeNode, current string) *ui.TreeEntry {
	entry := &ui.TreeEntry{
		BranchName:  node.Branch.Name,
		AheadCount:  node.AheadCount,
		BehindCount: node.BehindCount,
		Drifted:     node.Drifted,
		IsCurrent:   node.Branch.Name == current,
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
		RunE: runStackCmd(func(s *stack.Stack) error {
			err := s.Push(stepPrinter(os.Stdout, "Pushing"))
			if err != nil {
				printGitStderr(err)
			}
			return err
		}),
	}
}

func cmdPull() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Pull all branches in the stack from their upstreams",
		RunE: runStackCmd(func(s *stack.Stack) error {
			err := s.Pull(stepPrinter(os.Stdout, "Pulling"))
			if err != nil {
				printGitStderr(err)
			}
			return err
		}),
	}
}

func cmdRebase() *cobra.Command {
	return &cobra.Command{
		Use:   "rebase",
		Short: "Rebase each branch in the stack onto the tip of its parent, bottom-to-top",
		RunE: runStackCmd(func(s *stack.Stack) error {
			err := s.Rebase(stepPrinter(os.Stdout, "Rebasing"))
			if err != nil {
				printGitStderr(err)
			}
			return err
		}),
	}
}

func cmdMove() *cobra.Command {
	return &cobra.Command{
		Use:   "move [branch] <new-parent>",
		Short: "Move a branch to a different parent, rebasing it and its descendants",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			g, base, err := resolveBase()
			if err != nil {
				return err
			}
			s, err := stack.New(g, base)
			if err != nil {
				return err
			}
			var branch, newParent string
			if len(args) == 1 {
				branch, err = g.CurrentBranch()
				if err != nil {
					return err
				}
				newParent = args[0]
			} else {
				branch, newParent = args[0], args[1]
			}
			err = s.Move(branch, newParent, stepPrinter(os.Stdout, "Rebasing"))
			if err != nil {
				printGitStderr(err)
			}
			return err
		},
	}
}
