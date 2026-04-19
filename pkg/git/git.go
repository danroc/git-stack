// Package git wraps git subprocesses so the rest of the codebase never touches
// os/exec directly. Every instance is scoped to a directory via cmd.Dir.
package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Client executes git commands in a fixed working directory.
type Client struct {
	dir string
}

// NewClient returns a Client that runs all commands with cmd.Dir set to dir.
func NewClient(dir string) *Client {
	return &Client{dir: dir}
}

// Error is returned when a git subprocess exits with a non-zero status.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func (g *Client) run(args ...string) (string, error) {
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
		return "", &Error{
			Args:   args,
			Stderr: strings.TrimSpace(stderr.String()),
			Err:    err,
		}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// isExitCode reports whether err wraps an exec.ExitError with the given code.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// CurrentBranch returns the short name of HEAD (e.g. "main"), or "HEAD" if detached.
func (g *Client) CurrentBranch() (string, error) {
	return g.run("rev-parse", "--abbrev-ref", "HEAD")
}

// ListBranches returns all local branch names (refs/heads/*).
func (g *Client) ListBranches() ([]string, error) {
	out, err := g.run("for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Checkout switches to the specified branch.
//
// Will fail if the branch is checked out in another worktree, see worktreeGitOps for
// that case.
func (g *Client) Checkout(branch string) error {
	_, err := g.run("checkout", branch)
	return err
}

// CreateBranch creates a new branch at HEAD and switches to it.
func (g *Client) CreateBranch(name string) error {
	_, err := g.run("checkout", "-b", name)
	return err
}

// SetStackParent records parent as the stack parent of branch in local git config.
func (g *Client) SetStackParent(branch, parent string) error {
	_, err := g.run("config", "--local", "branch."+branch+".stackParent", parent)
	return err
}

// GetStackParent returns the configured stack parent, or ("", false) if unset.
func (g *Client) GetStackParent(branch string) (string, bool) {
	out, err := g.run("config", "--get", "branch."+branch+".stackParent")
	if err != nil {
		return "", false
	}
	return out, true
}

// getUpstream returns the remote and remote-tracking branch for a local branch. Returns
// ("", "", nil) if no upstream is configured.
func (g *Client) getUpstream(branch string) (string, string, error) {
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
func (g *Client) Push(branch string) error {
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

// Pull runs `git pull --rebase` on the currently checked-out branch.
func (g *Client) Pull() error {
	_, err := g.run("pull", "--rebase")
	return err
}

// Rebase rebases the current branch onto the given target.
func (g *Client) Rebase(onto string) error {
	_, err := g.run("rebase", onto)
	return err
}

// RebaseOnto replays commits reachable from branch but not from upstream onto newBase.
// Equivalent to: git rebase --onto newBase upstream branch
func (g *Client) RebaseOnto(newBase, upstream, branch string) error {
	_, err := g.run("rebase", "--onto", newBase, upstream, branch)
	return err
}

// WorktreeList returns branch → absolute worktree path for every worktree that has a
// branch checked out.
func (g *Client) WorktreeList() (map[string]string, error) {
	out, err := g.run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktreeList(out), nil
}

// ParseWorktreeList parses the porcelain output of `git worktree list --porcelain`.
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

// CommitsAhead returns the number of commits reachable from branch but not from parent.
func (g *Client) CommitsAhead(parent, branch string) (int, error) {
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
