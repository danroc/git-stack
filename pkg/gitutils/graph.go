package gitutils

import (
	"sort"
	"strings"
)

// Graph is an in-memory commit DAG covering all commits between local branch heads and
// the base branch. All ancestry and distance queries run in-process after an initial
// two-command load.
type Graph struct {
	// parents maps each commit hash to its parent hashes, first-parent first.
	parents map[string][]string
	// heads maps branch name → HEAD commit hash.
	heads map[string]string
	// branchAt is the reverse of heads: HEAD hash → branch name. When two branches
	// share a commit, the last one inserted wins; callers that need precise per-branch
	// identity should use HeadOf instead.
	branchAt map[string]string
}

// LoadGraph builds the commit graph for all local branches relative to baseBranch. It
// issues exactly two git commands:
//
// 1. git for-each-ref to collect all branch heads.
// 2. git log to load the commit DAG between those heads and baseBranch.
func (g *Git) LoadGraph(baseBranch string) (*Graph, error) {
	heads, err := g.listBranchHeads()
	if err != nil {
		return nil, err
	}
	return g.buildGraph(baseBranch, heads)
}

func (g *Git) listBranchHeads() (map[string]string, error) {
	out, err := g.run(
		"for-each-ref",
		"--format=%(refname:short) %(objectname)",
		"refs/heads/",
	)
	if err != nil {
		return nil, err
	}
	heads := make(map[string]string)
	if out == "" {
		return heads, nil
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		heads[parts[0]] = parts[1]
	}
	return heads, nil
}

func (g *Git) buildGraph(baseBranch string, heads map[string]string) (*Graph, error) {
	gr := &Graph{
		parents:  make(map[string][]string),
		heads:    heads,
		branchAt: make(map[string]string),
	}
	for branch, hash := range heads {
		gr.branchAt[hash] = branch
	}

	if len(heads) == 0 {
		return gr, nil
	}

	args := []string{"log", "--format=%H %P"}
	hashes := make([]string, 0, len(heads))
	for _, h := range heads {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	args = append(args, hashes...)
	args = append(args, "^"+baseBranch)

	out, err := g.run(args...)
	if err != nil {
		// No commits above base is not an error.
		return gr, nil
	}
	if out == "" {
		return gr, nil
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		// parts[0] = commit hash, parts[1:] = parent hashes (first-parent first).
		gr.parents[parts[0]] = parts[1:]
	}
	return gr, nil
}

// Contains reports whether hash is in the loaded graph. Commits below the base branch
// boundary are not loaded, so Contains returns false for them.
func (g *Graph) Contains(hash string) bool {
	_, ok := g.parents[hash]
	return ok
}

// HeadOf returns the commit hash that branch points to.
func (g *Graph) HeadOf(branch string) (string, bool) {
	h, ok := g.heads[branch]
	return h, ok
}

// BranchAt returns the branch whose HEAD is hash. When two branches share the same
// HEAD, one is returned non-deterministically.
func (g *Graph) BranchAt(hash string) (string, bool) {
	b, ok := g.branchAt[hash]
	return b, ok
}

// Branches returns all local branch names known to the graph, sorted alphabetically to
// guarantee a deterministic traversal order.
func (g *Graph) Branches() []string {
	out := make([]string, 0, len(g.heads))
	for b := range g.heads {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

// FirstParent returns the first parent of hash. The first parent is the one followed by
// --first-parent walks, which traces the "main line" of a branch.
func (g *Graph) FirstParent(hash string) (string, bool) {
	parents, ok := g.parents[hash]
	if !ok || len(parents) == 0 {
		return "", false
	}
	return parents[0], true
}

// IsAncestor reports whether ancestorHash is reachable from descendantHash by following
// parent edges within the loaded graph.
func (g *Graph) IsAncestor(ancestorHash, descendantHash string) bool {
	if ancestorHash == descendantHash {
		return true
	}
	visited := make(map[string]bool)
	queue := []string{descendantHash}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if visited[h] {
			continue
		}
		if h == ancestorHash {
			return true
		}
		visited[h] = true
		for _, p := range g.parents[h] {
			if g.Contains(p) {
				queue = append(queue, p)
			}
		}
	}
	return false
}

// CommitsAhead returns the number of commits in branchHash's first-parent chain that
// are above parentHash.
//
// For feature-to-feature pairs, the walk stops when it reaches parentHash (which is in
// the graph). For base-to-feature pairs, parentHash is the base branch HEAD and is not
// in the graph; the walk stops at the graph boundary. Both cases are handled by the
// combined loop condition.
func (g *Graph) CommitsAhead(parentHash, branchHash string) int {
	count := 0
	h := branchHash
	for g.Contains(h) && h != parentHash {
		p, ok := g.FirstParent(h)
		if !ok {
			break
		}
		count++
		h = p
	}
	return count
}
