package gitutils

import (
	"bufio"
	"maps"
	"slices"
	"strings"
)

// Graph is an in-memory commit DAG covering all commits between local branch heads and
// the base branch. All ancestry and distance queries run in-process after an initial
// two-command load.
type Graph struct {
	parents  map[string][]string // commit_hash → parent_hashes
	heads    map[string]string   // branch_name → commit_hash
	branchAt map[string]string   // commit_hash → branch_name
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
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		// Each line is "<branch-name> <commit-hash>"
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			continue
		}
		heads[parts[0]] = parts[1]
	}
	return heads, nil
}

func (g *Git) buildGraph(baseBranch string, heads map[string]string) (*Graph, error) {
	graph := &Graph{
		parents:  make(map[string][]string),
		heads:    heads,
		branchAt: make(map[string]string),
	}
	if len(heads) == 0 {
		return graph, nil
	}

	for branch, hash := range heads {
		graph.branchAt[hash] = branch
	}

	hashes := slices.Sorted(maps.Values(heads))
	args := []string{"log", "--format=%H %P"}
	args = append(args, hashes...)
	args = append(args, "^"+baseBranch)

	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}
		graph.parents[parts[0]] = parts[1:]
	}
	return graph, nil
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

// Branches returns all local branch names known to the graph, sorted alphabetically.
func (g *Graph) Branches() []string {
	return slices.Sorted(maps.Keys(g.heads))
}

// FirstParent returns the first parent of hash.
func (g *Graph) FirstParent(hash string) (string, bool) {
	parents, ok := g.parents[hash]
	if !ok || len(parents) == 0 {
		return "", false
	}
	return parents[0], true
}

// IsAncestor reports whether ancestorHash is reachable from descendantHash.
func (g *Graph) IsAncestor(ancestorHash, descendantHash string) bool {
	if ancestorHash == descendantHash {
		return true
	}

	visited := make(map[string]bool)
	queue := []string{descendantHash}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if visited[next] {
			continue
		}
		if next == ancestorHash {
			return true
		}
		visited[next] = true
		for _, parent := range g.parents[next] {
			if g.Contains(parent) {
				queue = append(queue, parent)
			}
		}
	}
	return false
}

// CommitsAhead returns the number of commits in branchHash's first-parent chain that
// are above parentHash.
func (g *Graph) CommitsAhead(parentHash, branchHash string) int {
	count := 0
	hash := branchHash
	for g.Contains(hash) && hash != parentHash {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		count++
		hash = parent
	}
	return count
}
