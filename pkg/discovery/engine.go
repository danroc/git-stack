// Package discovery implements branch stack discovery.
package discovery

import (
	"fmt"
	"slices"

	"git-stack/pkg/git"
)

// Branch is a branch name paired with its head.
type Branch struct {
	Name string
	Head string
}

// ChooseBranchFn is called when multiple direct-child branches are found at a
// bifurcation point.
//
// It receives the action being performed and the candidate branch names, and returns
// the chosen branch name.
type ChooseBranchFn func(action string, choices []string) (string, error)

// Engine identifies stack lineage using a commit graph loaded once from git and then
// queried in-process.
type Engine struct {
	base  string
	git   *git.Client
	graph *git.Graph
}

// NewEngine creates an engine that discovers stacks relative to baseBranch.
func NewEngine(g *git.Client, base string) (*Engine, error) {
	graph, err := g.LoadGraph()
	if err != nil {
		return nil, fmt.Errorf("loading commit graph: %w", err)
	}
	return &Engine{
		base:  base,
		git:   g,
		graph: graph,
	}, nil
}

// BaseBranch returns the base branch that anchors the bottom of every stack.
func (e *Engine) BaseBranch() string {
	return e.base
}

// DetectBase returns the base branch by checking for well-known defaults.
//
// It looks for "main" then "master" among local branches. If neither exists, it returns
// an error asking the user to specify --base explicitly.
func DetectBase(g *git.Client) (string, error) {
	branches, err := g.ListBranches()
	if err != nil {
		return "", err
	}

	for _, branch := range branches {
		if branch == "main" || branch == "master" {
			return branch, nil
		}
	}

	return "", fmt.Errorf("unable to detect base branch; use --base to specify")
}

// TreeNode is a node in the full branch tree built by BuildTree.
type TreeNode struct {
	Branch      Branch
	AheadCount  int
	BehindCount int
	Drifted     bool
	Children    []*TreeNode
}

// BranchWithParent is a branch paired with the name of its immediate stack parent.
type BranchWithParent struct {
	Branch Branch
	Parent string
}

// candidateScore holds a candidate branch name and its distance from the target branch
// for parent inference.
type candidateScore struct {
	name string
	dist int
}

// DiscoverStack identifies the full linear stack that contains currentBranch.
//
// Two passes build the result. The first (base → currentBranch) walks the first-parent
// chain collecting branch heads from the bottom up. The second (currentBranch → tip)
// follows the parent/child tree to find child branches above currentBranch, calling
// chooseBranch at any bifurcation.
func (e *Engine) DiscoverStack(
	currentBranch string,
	chooseBranch ChooseBranchFn,
) ([]Branch, error) {
	ancestors, err := e.traceChainTo(currentBranch)
	if err != nil {
		return nil, err
	}

	children, err := e.traceChildren(currentBranch, chooseBranch)
	if err != nil {
		return nil, err
	}

	return append(ancestors, children...), nil
}

// traceChainTo returns the chain from baseBranch (inclusive) up to target (inclusive),
// bottom-to-top (base → ... → target).
//
// Parent resolution is config-first and side-effect-free:
//
//  1. use branch.<name>.stackParent when present
//  2. otherwise infer a parent from the graph
//  3. otherwise fall back to the base branch
func (e *Engine) traceChainTo(target string) ([]Branch, error) {
	if target == e.base {
		head, err := e.mustHeadOf(e.base)
		if err != nil {
			return nil, err
		}
		return []Branch{{Name: e.base, Head: head}}, nil
	}

	head, err := e.mustHeadOf(target)
	if err != nil {
		return nil, err
	}
	chain := []Branch{{Name: target, Head: head}}
	if err := e.walkResolvedParents(target, func(parent string) error {
		head, err := e.mustHeadOf(parent)
		if err != nil {
			return err
		}
		chain = append(chain, Branch{Name: parent, Head: head})
		return nil
	}); err != nil {
		return nil, err
	}

	slices.Reverse(chain)
	return chain, nil
}

// traceChildren walks up the stack from branch, following the first available child at
// each level. At bifurcation points it calls chooseBranch to disambiguate.
//
// Returns the list of child branches found, excluding branch itself.
func (e *Engine) traceChildren(
	branch string,
	chooseBranch ChooseBranchFn,
) ([]Branch, error) {
	var result []Branch
	for {
		children := e.directChildren(branch)
		if len(children) == 0 {
			return result, nil
		}

		var chosen string
		if len(children) == 1 {
			chosen = children[0]
		} else {
			var err error
			chosen, err = chooseBranch("traverse", children)
			if err != nil {
				return nil, err
			}
		}

		chosenHash, _ := e.graph.HeadOf(chosen)
		result = append(result, Branch{Name: chosen, Head: chosenHash})
		branch = chosen
	}
}

// BuildTree constructs the full branch tree rooted at the base branch. Unlike
// DiscoverStack, it never prompts, all descendants are included.
func (e *Engine) BuildTree() *TreeNode {
	baseHead, _ := e.graph.HeadOf(e.base)
	root := &TreeNode{
		Branch: Branch{Name: e.base, Head: baseHead},
	}
	e.buildChildren(root)
	return root
}

// buildChildren recursively populates node.Children with direct child branches,
// computing ahead/behind counts and drift status for each.
func (e *Engine) buildChildren(node *TreeNode) {
	for _, child := range e.directChildren(node.Branch.Name) {
		childHash, _ := e.graph.HeadOf(child)
		result := e.graph.CommitsBetween(childHash, node.Branch.Head)
		childNode := &TreeNode{
			Branch:      Branch{Name: child, Head: childHash},
			AheadCount:  result.Ahead,
			BehindCount: result.Behind,
			Drifted:     e.hasDrift(child),
		}
		node.Children = append(node.Children, childNode)
		e.buildChildren(childNode)
	}
}

// directChildren returns branches whose resolved parent is parent (i.e., branches
// directly above parent in the stack).
//
// Stored relationships are authoritative. The graph is only used to fill in branches
// that do not have a stored parent, using a deterministic merge-base-based inference.
func (e *Engine) directChildren(parent string) []string {
	var children []string
	for _, branch := range e.graph.Branches() {
		if branch == parent || branch == e.base {
			continue
		}
		resolvedParent, err := e.resolveParent(branch)
		if err == nil && resolvedParent == parent {
			children = append(children, branch)
		}
	}
	slices.Sort(children)
	return children
}

// Parent returns the immediate stack parent of branch.
func (e *Engine) Parent(branch string) (string, error) {
	if branch == e.base {
		return "", fmt.Errorf("branch %q has no parent in the stack", branch)
	}
	return e.resolveParent(branch)
}

// IsChildOf reports whether child is a transitive child of parent in the resolved stack
// tree (i.e., child is strictly below parent).
func (e *Engine) IsChildOf(child, parent string) bool {
	if child == parent {
		return false
	}
	found := false
	err := e.walkResolvedParents(child, func(p string) error {
		if p == parent {
			found = true
			return errStopWalk
		}
		return nil
	})
	return found && err == errStopWalk
}

// SubtreeChildren returns all branches in the subtree rooted at branch (excluding the
// root itself), each paired with their immediate parent, in pre-order (parents before
// children).
func (e *Engine) SubtreeChildren(branch string) []BranchWithParent {
	root := e.BuildTree()
	node := findTreeNode(root, branch)
	if node == nil {
		return nil
	}
	return flattenSubtree(node)
}

// SetParent sets branch's stack parent in git config.
//
// The merge-base is computed from the live branch heads (not the cached graph) so that
// it remains correct even after a rebase has moved the branch within the same session.
func (e *Engine) SetParent(branch, parent string) error {
	if err := e.git.SetStackParent(branch, parent); err != nil {
		return err
	}
	base, err := e.git.ComputeMergeBase(branch, parent)
	if err != nil {
		return nil
	}
	return e.git.SetStackParentMergeBase(branch, base)
}

// findTreeNode searches the tree rooted at root for a node with the given name,
// returning it or nil if not found.
func findTreeNode(root *TreeNode, name string) *TreeNode {
	if root.Branch.Name == name {
		return root
	}
	for _, child := range root.Children {
		if found := findTreeNode(child, name); found != nil {
			return found
		}
	}
	return nil
}

// flattenSubtree returns all descendants of node in pre-order, each paired with their
// immediate parent name. The node itself is not included.
func flattenSubtree(node *TreeNode) []BranchWithParent {
	var result []BranchWithParent
	for _, child := range node.Children {
		result = append(
			result,
			BranchWithParent{
				Branch: child.Branch,
				Parent: node.Branch.Name,
			},
		)
		result = append(result, flattenSubtree(child)...)
	}
	return result
}

var errStopWalk = fmt.Errorf("stop walk")

// walkResolvedParents walks the parent chain from branch up to (but not including)
// e.base, calling visit for each resolved parent. Returns an error if a cycle is
// detected or a parent cannot be resolved. If visit returns a non-nil error, that error
// is returned immediately.
func (e *Engine) walkResolvedParents(
	branch string,
	visit func(parent string) error,
) error {
	seen := map[string]bool{branch: true}
	for current := branch; current != e.base; {
		parent, err := e.resolveParent(current)
		if err != nil {
			return err
		}
		if parent == "" {
			parent = e.base
		}
		if seen[parent] {
			return fmt.Errorf("cycle detected while resolving parent of %q", current)
		}
		if err := visit(parent); err != nil {
			return err
		}
		seen[parent] = true
		current = parent
	}
	return nil
}

// mustHeadOf returns the commit hash for branch, returning an error if the branch is
// not present in the graph.
func (e *Engine) mustHeadOf(branch string) (string, error) {
	head, ok := e.graph.HeadOf(branch)
	if !ok {
		return "", fmt.Errorf("branch %q not found in graph", branch)
	}
	return head, nil
}

// resolveParent returns the immediate stack parent of branch using a three-tier
// fallback:
//
//  1. Stored config (branch.<name>.stackParent), which remains authoritative while the
//     configured parent branch still exists, even if the graph has drifted.
//
//  2. graph-based inference via inferParent when no config is present or the configured
//     parent no longer exists.
//
//  3. e.base as the ultimate fallback.
func (e *Engine) resolveParent(branch string) (string, error) {
	if _, err := e.mustHeadOf(branch); err != nil {
		return "", err
	}
	if branch == e.base {
		return "", fmt.Errorf("branch %q has no parent in the stack", branch)
	}
	if parent, ok := e.git.StackParent(branch); ok {
		if e.graph.HasBranch(parent) {
			return parent, nil
		}
		// If the stored parent doesn't exist (anymore) in the graph, ignore it and fall
		// back to inference.
	}
	if parent, ok := e.inferParent(branch); ok {
		return parent, nil
	}
	return e.base, nil
}

// inferParent finds the best candidate parent for branch among all known branches by
// checking which branch head is a merge-base ancestor of branch's head. Among ties, the
// closest (fewest first-parent steps) is chosen; further ties broken alphabetically.
func (e *Engine) inferParent(branch string) (string, bool) {
	branchHead, ok := e.graph.HeadOf(branch)
	if !ok {
		return "", false
	}

	var best *candidateScore
	for _, candidate := range e.graph.Branches() {
		if candidate == branch {
			continue
		}

		// Config is authoritative, so a branch explicitly configured as a child of
		// branch cannot also be inferred as branch's parent. We intentionally do not
		// add drift checks here: drift is surfaced separately, but it does not weaken
		// configured parent/child relationships while the configured parent exists.
		cfgParent, ok := e.git.StackParent(candidate)
		if ok && cfgParent == branch {
			continue
		}

		candidateHead, ok := e.graph.HeadOf(candidate)
		if !ok || candidateHead == branchHead {
			continue
		}
		base, ok := e.graph.MergeBase(branchHead, candidateHead)
		if !ok || base != candidateHead {
			continue
		}
		dist, ok := e.graph.DistanceToAncestor(branchHead, base)
		if !ok {
			continue
		}

		score := candidateScore{
			name: candidate,
			dist: dist,
		}
		if best == nil || isBetterCandidate(score, *best) {
			best = &score
		}
	}
	if best == nil {
		return "", false
	}
	return best.name, true
}

// isBetterCandidate reports whether a is a better candidate than b, preferring smaller
// distance (closer to branch) than alphabetically earlier name as a tiebreaker.
func isBetterCandidate(a, b candidateScore) bool {
	if a.dist != b.dist {
		return a.dist < b.dist
	}
	return a.name < b.name
}

// hasDrift reports whether branch has been modified outside the stack workflow since
// its parent was last recorded. It returns true when the current merge-base of branch
// and its parent differs from the stored merge-base.
func (e *Engine) hasDrift(branch string) bool {
	parent, ok := e.git.StackParent(branch)
	if !ok {
		return false
	}
	parentHead, ok := e.graph.HeadOf(parent)
	if !ok {
		return false
	}
	branchHead, ok := e.graph.HeadOf(branch)
	if !ok {
		return false
	}
	base, ok := e.graph.MergeBase(branchHead, parentHead)
	if !ok {
		return false
	}
	storedBase, ok := e.git.StackMergeBase(branch)
	if !ok {
		return false
	}
	return base != storedBase
}
