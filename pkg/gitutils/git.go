// Package gitutils wraps git subprocesses so the rest of the codebase never touches
// os/exec directly. Every instance is scoped to a directory via cmd.Dir.
package gitutils

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Git executes git commands in a fixed working directory.
type Git struct {
	dir string
}

// NewGit returns a Git instance that runs all commands with cmd.Dir set to dir.
func NewGit(dir string) *Git {
	return &Git{dir: dir}
}

func (g *Git) run(args ...string) (string, error) {
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = g.dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf(
			"git %s: %w (stderr: %q)",
			strings.Join(args, " "),
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// isExitCode reports whether err wraps an exec.ExitError with the given code.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// RunRaw exposes the low-level run method for one-off commands that don't warrant a
// dedicated method (e.g. symbolic-ref lookups in DetectBaseBranch).
func (g *Git) RunRaw(args ...string) (string, error) {
	return g.run(args...)
}

// CurrentBranch returns the short name of HEAD (e.g. "main"), or "HEAD" if detached.
func (g *Git) CurrentBranch() (string, error) {
	return g.run("rev-parse", "--abbrev-ref", "HEAD")
}

// ListBranches returns all local branch names (refs/heads/*).
func (g *Git) ListBranches() ([]string, error) {
	out, err := g.run("for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// IsAncestor reports whether ancestor is reachable from descendant.
func (g *Git) IsAncestor(ancestor, descendant string) (bool, error) {
	_, err := g.run("merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if isExitCode(err, 1) {
		return false, nil
	}
	return false, err
}

// Checkout switches to the specified branch. Will fail if the branch is checked out in
// another worktree; see worktreeGitOps for that case.
func (g *Git) Checkout(branch string) error {
	_, err := g.run("checkout", branch)
	return err
}

// CreateBranch creates a new branch at HEAD and switches to it.
func (g *Git) CreateBranch(name string) error {
	_, err := g.run("checkout", "-b", name)
	return err
}

// getUpstream returns the remote and remote-tracking branch for a local branch. Returns
// ("", "", nil) if no upstream is configured.
func (g *Git) getUpstream(branch string) (string, string, error) {
	remote, err := g.run("config", "--get", "branch."+branch+".remote")
	if err != nil {
		if isExitCode(err, 1) {
			return "", "", nil
		}
		return "", "", err
	}
	merge, err := g.run("config", "--get", "branch."+branch+".merge")
	if err != nil {
		if isExitCode(err, 1) {
			return "", "", nil
		}
		return "", "", err
	}
	return remote, strings.TrimPrefix(merge, "refs/heads/"), nil
}

// Push pushes branch to its configured upstream. If no upstream is configured, pushes
// to origin/<branch> and sets it as the upstream.
func (g *Git) Push(branch string) error {
	remote, remoteBranch, err := g.getUpstream(branch)
	if err != nil {
		return err
	}
	if remote == "" {
		_, err = g.run("push", "-u", "origin", branch)
		return err
	}
	_, err = g.run("push", remote, branch+":"+remoteBranch)
	return err
}

// Pull runs pull --rebase on the currently checked-out branch. On conflict it leaves
// the rebase in-progress so the caller can guide the user to resolve it.
func (g *Git) Pull() error {
	_, err := g.run("pull", "--rebase")
	return err
}

// Rebase rebases the current branch onto the given target. Like Pull, a conflict leaves
// the rebase in-progress for manual resolution.
func (g *Git) Rebase(onto string) error {
	_, err := g.run("rebase", onto)
	return err
}

// WorktreeList returns branch → absolute worktree path for every worktree that has a
// branch checked out. Detached-HEAD and bare worktrees are excluded.
func (g *Git) WorktreeList() (map[string]string, error) {
	out, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktreeList(out), nil
}

// ParseWorktreeList parses the porcelain output of `git worktree list --porcelain`.
// Format: newline-separated stanzas, each containing "worktree <path>", "HEAD <hash>",
// and either "branch refs/heads/<name>" or "detached".
//
// See: https://git-scm.com/docs/git-worktree#_porcelain_format
func ParseWorktreeList(output string) map[string]string {
	const (
		worktreePrefix = "worktree "
		branchPrefix   = "branch refs/heads/"
	)

	result := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(output))

	var currentPath string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			currentPath = ""
		} else if path, ok := strings.CutPrefix(line, worktreePrefix); ok {
			currentPath = path
		} else if branch, ok := strings.CutPrefix(line, branchPrefix); ok {
			if currentPath != "" {
				result[branch] = currentPath
			}
		}
	}
	return result
}

// CommitsAhead returns the number of commits reachable from branch but not from parent
// (i.e. how far branch is ahead of parent).
func (g *Git) CommitsAhead(parent, branch string) (int, error) {
	out, err := g.run("rev-list", "--count", parent+".."+branch)
	if err != nil {
		return 0, err
	}
	var n int
	if _, err := fmt.Sscan(out, &n); err != nil {
		return 0, fmt.Errorf("parsing commit count %q: %w", out, err)
	}
	return n, nil
}
