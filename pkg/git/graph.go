package git

import (
	"bufio"
	"fmt"
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
	branchAt map[string][]string // commit_hash → branch_names (sorted)
}

// LoadGraph builds the commit graph for all local branches. The graph floor is
// the octopus merge-base of every branch head — commits at and above the floor
// are loaded.
func (g *Client) LoadGraph() (*Graph, error) {
	heads, err := g.listBranchHeads()
	if err != nil {
		return nil, err
	}
	return g.buildGraph(heads)
}

func (g *Client) listBranchHeads() (map[string]string, error) {
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

func (g *Client) buildGraph(heads map[string]string) (*Graph, error) {
	graph := &Graph{
		parents:  make(map[string][]string),
		heads:    heads,
		branchAt: make(map[string][]string),
	}
	if len(heads) == 0 {
		return graph, nil
	}

	for branch, hash := range heads {
		graph.branchAt[hash] = append(graph.branchAt[hash], branch)
	}
	for _, names := range graph.branchAt {
		slices.Sort(names)
	}

	// Compute the floor: the merge-base of every branch head (including the base
	// branch). Commits at and above the floor are loaded into the graph.
	refs := slices.Sorted(maps.Values(heads))
	floor, err := g.MergeBaseOctopus(refs...)
	if err != nil {
		return nil, fmt.Errorf("computing graph floor: %w", err)
	}

	// Determine whether the floor has a parent to anchor ^<floor>^. A root commit
	// has no parents, in which case we drop the exclusion.
	hasParent, err := g.commitHasParent(floor)
	if err != nil {
		return nil, fmt.Errorf("inspecting floor parent: %w", err)
	}

	args := []string{"log", "--format=%H %P"}
	args = append(args, refs...)
	if hasParent {
		args = append(args, "^"+floor+"^")
	}

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

// NewGraph constructs a Graph from raw commit data. When two branches share a
// HEAD, both are retained in branchAt at that commit, sorted alphabetically.
func NewGraph(parents map[string][]string, heads map[string]string) *Graph {
	branchAt := make(map[string][]string, len(heads))
	for branch, hash := range heads {
		branchAt[hash] = append(branchAt[hash], branch)
	}
	for _, names := range branchAt {
		slices.Sort(names)
	}
	return &Graph{parents: parents, heads: heads, branchAt: branchAt}
}

// CommitsBetweenResult holds the count of commits ahead and behind
// two branches, relative to their closest common ancestor.
type CommitsBetweenResult struct {
	Ahead  int
	Behind int
}

// Contains reports whether hash is in the loaded graph (at or above the floor
// commit — the octopus merge-base of all branch heads).
func (g *Graph) Contains(hash string) bool {
	_, ok := g.parents[hash]
	return ok
}

// HeadOf returns the commit hash that branch points to.
func (g *Graph) HeadOf(branch string) (string, bool) {
	h, ok := g.heads[branch]
	return h, ok
}

// BranchAt returns all branches whose HEAD is at hash, sorted alphabetically.
// Returns (nil, false) when no branch points at hash. The returned slice is a
// copy; callers may modify it freely.
func (g *Graph) BranchAt(hash string) ([]string, bool) {
	branches, ok := g.branchAt[hash]
	if !ok {
		return nil, false
	}
	return slices.Clone(branches), true
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

// CommitsBetween returns the number of commits between branchHash and
// parentHash relative to their closest common ancestor in the graph.
//
// Ahead is the first-parent chain distance from the common ancestor to
// branchHash. Behind is the first-parent chain distance from the common
// ancestor to parentHash.
func (g *Graph) CommitsBetween(branchHash, parentHash string) CommitsBetweenResult {
	// Find the closest common ancestor via bidirectional BFS.
	commonAncestor := g.closestCommonAncestor(branchHash, parentHash)
	if commonAncestor == "" {
		return CommitsBetweenResult{}
	}

	var ahead, behind int

	// Count ahead: first-parent chain from branchHash to common ancestor.
	hash := branchHash
	for g.Contains(hash) && hash != commonAncestor {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		ahead++
		hash = parent
	}

	// Count behind: first-parent chain from parentHash to common ancestor.
	hash = parentHash
	for g.Contains(hash) && hash != commonAncestor {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		behind++
		hash = parent
	}

	return CommitsBetweenResult{Ahead: ahead, Behind: behind}
}

// closestCommonAncestor finds the nearest commit reachable from both
// branchHash and parentHash by walking parent chains. It uses bidirectional
// BFS to minimize the number of graph nodes visited.
func (g *Graph) closestCommonAncestor(a, b string) string {
	if a == b {
		return a
	}

	aVisited := map[string]bool{a: true}
	bVisited := map[string]bool{b: true}
	aQueue := []string{a}
	bQueue := []string{b}

	for len(aQueue) > 0 && len(bQueue) > 0 {
		// Expand the shorter frontier first for efficiency.
		if len(bQueue) < len(aQueue) {
			aQueue, bQueue = bQueue, aQueue
			aVisited, bVisited = bVisited, aVisited
		}

		nextAQueue := make([]string, 0, len(aQueue))
		for _, commit := range aQueue {
			// Check intersection before expanding.
			if bVisited[commit] {
				return commit
			}
			parent, ok := g.FirstParent(commit)
			if !ok {
				continue
			}
			if !g.Contains(parent) {
				continue
			}
			if aVisited[parent] {
				continue
			}
			aVisited[parent] = true
			// Check intersection on the parent.
			if bVisited[parent] {
				return parent
			}
			nextAQueue = append(nextAQueue, parent)
		}
		aQueue = nextAQueue
	}

	return ""
}
