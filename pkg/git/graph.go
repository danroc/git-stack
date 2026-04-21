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
	parents    map[string][]string // commit_hash → parent_hashes
	heads      map[string]string   // branch_name → commit_hash
	branchesAt map[string][]string // commit_hash → branch_names (sorted)
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
		if ok {
			heads[name] = hash
		}
	}

	return heads, scanner.Err()
}

func (g *Client) buildGraph(heads map[string]string) (*Graph, error) {
	graph := &Graph{
		parents:    make(map[string][]string),
		heads:      heads,
		branchesAt: make(map[string][]string),
	}
	if len(heads) == 0 {
		return graph, nil
	}

	// Map commits → branches pointing to them.
	for branch, hash := range heads {
		graph.branchesAt[hash] = append(graph.branchesAt[hash], branch)
	}
	for _, branches := range graph.branchesAt {
		slices.Sort(branches)
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

	// Build git log arguments.
	args := []string{"log", "--format=%H %P"}
	args = append(args, refs...)
	if hasParent {
		args = append(args, "^"+floor+"^")
	}

	out, err := g.run(args...)
	if err != nil {
		return nil, err
	}

	parents, err := parseParentLines(out)
	if err != nil {
		return nil, err
	}

	// Ensure floor is included with no parents.
	parents[floor] = nil
	graph.parents = parents

	return graph, nil
}

func parseParentLines(out string) (map[string][]string, error) {
	parents := make(map[string][]string)
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 {
			parents[fields[0]] = fields[1:]
		}
	}

	return parents, scanner.Err()
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
		parents:    parents,
		heads:      heads,
		branchesAt: branchAt,
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

// BranchesAt returns all branches whose HEAD is at hash, sorted alphabetically. The
// returned slice is a copy; callers may modify it freely.
func (g *Graph) BranchesAt(hash string) []string {
	branches := g.branchesAt[hash]
	return slices.Clone(branches)
}

// Branches returns all local branch names known to the graph, sorted alphabetically.
func (g *Graph) Branches() []string {
	return slices.Sorted(maps.Keys(g.heads))
}

// FirstParent returns the first parent of hash.
func (g *Graph) FirstParent(hash string) (string, bool) {
	ps := g.parents[hash]
	if len(ps) == 0 {
		return "", false
	}
	return ps[0], true
}

// IsAncestor reports whether ancestor is reachable from descendant.
func (g *Graph) IsAncestor(ancestor, descendant string) bool {
	if ancestor == descendant {
		return true
	}

	visited := map[string]bool{descendant: true}
	queue := []string{descendant}

	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]

		for _, p := range g.parents[c] {
			if p == ancestor {
				return true
			}
			if !visited[p] {
				visited[p] = true
				queue = append(queue, p)
			}
		}
	}

	return false
}

// Traverse visits all commits reachable from start, including start itself, in BFS
// order. If visit returns false, the traversal is aborted.
func (g *Graph) Traverse(start string, visit func(hash string) bool) {
	if !g.Contains(start) {
		return
	}

	visited := map[string]bool{start: true}
	queue := []string{start}

	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]

		if !visit(c) {
			return
		}

		for _, p := range g.parents[c] {
			if !visited[p] {
				visited[p] = true
				queue = append(queue, p)
			}
		}
	}
}

// AncestorsOf returns all commits reachable from hash, including hash itself, in BFS
// order.
func (g *Graph) AncestorsOf(hash string) []string {
	var ancestors []string
	g.Traverse(hash, func(h string) bool {
		ancestors = append(ancestors, h)
		return true
	})
	return ancestors
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
	mb := g.MergeBase(a, b)
	if mb == "" {
		return CommitsBetweenResult{}
	}

	return CommitsBetweenResult{
		Ahead:  g.countStepsToAncestor(a, mb),
		Behind: g.countStepsToAncestor(b, mb),
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

// MergeBase returns the closest commit reachable from both a and b along
// their first-parent chains, or "" if the chains share no common commit.
func (g *Graph) MergeBase(a, b string) string {
	ancestors := make(map[string]bool)
	for _, c := range g.AncestorsOf(a) {
		ancestors[c] = true
	}

	var base string
	g.Traverse(b, func(c string) bool {
		if ancestors[c] {
			base = c
			return false
		}
		return true
	})

	return base
}
