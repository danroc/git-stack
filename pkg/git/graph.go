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

// LoadGraph builds the commit graph for all local branches. The graph floor is the
// octopus merge-base of every branch head — commits at and above the floor are loaded.
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
		name, hash, ok := strings.Cut(scanner.Text(), " ")
		if !ok {
			continue
		}
		heads[name] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, err
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

	// Determine whether the floor has a parent to anchor ^<floor>^. A root commit has
	// no parents, in which case we drop the exclusion.
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
	graph.parents[floor] = nil

	return graph, nil
}

// NewGraph constructs a Graph from raw commit data. When two branches share a HEAD,
// both are retained in branchAt at that commit, sorted alphabetically.
func NewGraph(parents map[string][]string, heads map[string]string) *Graph {
	branchAt := make(map[string][]string, len(heads))
	for branch, hash := range heads {
		branchAt[hash] = append(branchAt[hash], branch)
	}

	for _, names := range branchAt {
		slices.Sort(names)
	}

	return &Graph{
		parents:  parents,
		heads:    heads,
		branchAt: branchAt,
	}
}

// CommitsBetweenResult holds the count of commits ahead and behind two branches,
// relative to their closest common ancestor.
type CommitsBetweenResult struct {
	Ahead  int
	Behind int
}

// Contains reports whether hash is in the loaded graph (at or above the floor commit —
// the octopus merge-base of all branch heads).
func (g *Graph) Contains(hash string) bool {
	_, ok := g.parents[hash]
	return ok
}

// HeadOf returns the commit hash that branch points to.
func (g *Graph) HeadOf(branch string) (string, bool) {
	h, ok := g.heads[branch]
	return h, ok
}

// BranchAt returns all branches whose HEAD is at hash, sorted alphabetically. Returns
// (nil, false) when no branch points at hash. The returned slice is a copy; callers may
// modify it freely.
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

	var (
		visited = map[string]bool{descendantHash: true}
		queue   = []string{descendantHash}
	)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == ancestorHash {
			return true
		}
		for _, p := range g.parents[next] {
			// Mark visited before enqueuing, duplicates never enter the queue.
			if !visited[p] {
				visited[p] = true
				queue = append(queue, p)
			}
		}
	}
	return false
}

// CommitsBetween returns the number of commits between a and b relative to their
// closest common ancestor in the graph, as measured along first-parent chains only.
//
// Ahead is the first-parent chain distance from the common ancestor to a. Behind is the
// first-parent chain distance from the common ancestor to b.
//
// If no common ancestor exists on the first-parent chains, the result has both counts
// set to zero.
func (g *Graph) CommitsBetween(a, b string) CommitsBetweenResult {
	commonAncestor, ok := g.closestCommonAncestor(a, b)
	if !ok {
		return CommitsBetweenResult{}
	}

	return CommitsBetweenResult{
		Ahead:  g.countStepsToAncestor(a, commonAncestor),
		Behind: g.countStepsToAncestor(b, commonAncestor),
	}
}

// countStepsToAncestor counts the number of first-parent steps from hash up to (but not
// including) target along the first-parent chain.
func (g *Graph) countStepsToAncestor(hash, target string) int {
	var count int
	for hash != target {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		count++
		hash = parent
	}
	return count
}

// closestCommonAncestor returns the closest commit reachable from both a and b along
// their first-parent chains, or "" if the chains share no common commit. It first
// collects all commits on a's chain into a set, then walks b's chain until it finds a
// hit.
//
// Because it follows only first-parent chains, it may miss a common ancestor that is
// only reachable via a non-first parent (e.g. the second parent of a merge commit).
func (g *Graph) closestCommonAncestor(a, b string) (string, bool) {
	// Collect all first-parent ancestors of a (including a itself).
	aAncestors := map[string]bool{a: true}
	for hash := a; ; {
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		aAncestors[parent] = true
		hash = parent
	}

	// Walk first-parent chain from b; the first node found in aAncestors is the closest
	// common ancestor.
	for hash := b; ; {
		if aAncestors[hash] {
			return hash, true
		}
		parent, ok := g.FirstParent(hash)
		if !ok {
			break
		}
		hash = parent
	}

	return "", false
}
