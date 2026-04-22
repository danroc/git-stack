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
	dir                 string
	stackParentCache    map[string]string // nil = not yet loaded
	stackMergeBaseCache map[string]string // nil = not yet loaded
}

// NewClient returns a Client that runs all commands with cmd.Dir set to dir.
func NewClient(dir string) *Client {
	return &Client{dir: dir}
}

// Dir returns the working directory this client operates in.
func (g *Client) Dir() string { return g.dir }

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

// run executes a git command with the given args in the client's working directory,
// returning trimmed stdout. On non-zero exit it returns a trimmed stderr and an Error.
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
	if err != nil {
		return err
	}
	if g.stackParentCache != nil {
		g.stackParentCache[branch] = parent
	}
	return nil
}

// SetStackParentMergeBase records the last known merge-base for branch's configured
// stack parent in local git config.
func (g *Client) SetStackParentMergeBase(branch, mergeBase string) error {
	_, err := g.run(
		"config",
		"--local",
		"branch."+branch+".stackParentMergeBase",
		mergeBase,
	)
	if err != nil {
		return err
	}
	if g.stackMergeBaseCache != nil {
		g.stackMergeBaseCache[branch] = mergeBase
	}
	return nil
}

// StackParent returns the configured stack parent, or ("", false) if unset. All values
// are loaded in a single git config call on first use and cached.
func (g *Client) StackParent(branch string) (string, bool) {
	if g.stackParentCache == nil {
		g.loadStackCaches()
	}
	parent, ok := g.stackParentCache[branch]
	return parent, ok
}

// StackMergeBase returns the stored last known merge-base for branch's stack parent, or
// ("", false) if unset.
func (g *Client) StackMergeBase(branch string) (string, bool) {
	if g.stackMergeBaseCache == nil {
		g.loadStackCaches()
	}
	base, ok := g.stackMergeBaseCache[branch]
	return base, ok
}

// RecordStackParent updates the configured parent relationship and snapshots the
// current merge-base at the same time. This is only for explicit user-driven mutations.
func (g *Client) RecordStackParent(branch, parent string) error {
	if err := g.SetStackParent(branch, parent); err != nil {
		return err
	}
	mergeBase, err := g.ComputeMergeBase(branch, parent)
	if err != nil {
		return err
	}
	return g.SetStackParentMergeBase(branch, mergeBase)
}

// loadStackCaches loads all branch.*.stackParent and branch.*.stackParentMergeBase
// entries from local git config in a single subprocess call, caching them for fast
// lookup by StackParent and StackMergeBase.
func (g *Client) loadStackCaches() {
	g.stackParentCache = make(map[string]string)
	g.stackMergeBaseCache = make(map[string]string)
	out, err := g.run("config", "--local", "--list")
	if err != nil {
		return
	}

	for line := range strings.SplitSeq(out, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		// 1. git config key format: section.subsection.variable
		// 2. Section and variable are case-insensitive (lowercased in output).
		// 3. Subsection is the branch name and is case-sensitive — preserve its case.
		section, rest, ok := strings.Cut(key, ".")
		if !ok || !strings.EqualFold(section, "branch") {
			continue
		}

		lastDot := strings.LastIndexByte(rest, '.')
		if lastDot < 0 {
			continue
		}

		branch := rest[:lastDot]
		switch variable := rest[lastDot+1:]; {
		case strings.EqualFold(variable, "stackparent"):
			g.stackParentCache[branch] = value
		case strings.EqualFold(variable, "stackparentmergebase"):
			g.stackMergeBaseCache[branch] = value
		}
	}
}

// ComputeMergeBase returns git's merge-base for two refs.
func (g *Client) ComputeMergeBase(a, b string) (string, error) {
	return g.run("merge-base", a, b)
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

// MergeBaseOctopus returns the best common ancestor of two or more refs, using the
// octopus algorithm (same semantics as `git merge-base --octopus`). Returns an error if
// any two refs have disjoint histories.
func (g *Client) MergeBaseOctopus(refs ...string) (string, error) {
	if len(refs) == 0 {
		return "", fmt.Errorf("MergeBaseOctopus: no refs provided")
	}
	args := append([]string{"merge-base", "--octopus"}, refs...)
	return g.run(args...)
}

// commitHasParent reports whether hash has at least one parent commit. A root commit
// has no parents.
func (g *Client) commitHasParent(hash string) (bool, error) {
	out, err := g.run("rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return false, err
	}
	// Output is "<hash> <parent> [<parent>...]" or "<hash>" for a root commit.
	return len(strings.Fields(out)) > 1, nil
}
