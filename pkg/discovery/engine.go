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

type candidateScore struct {
	name string
	dist int
}

// DiscoverStack identifies the full linear stack that contains currentBranch.
//
// Two passes build the result. The first (base → currentBranch) walks the first-parent
// chain collecting branch heads from the bottom up. The second (currentBranch → tip)
// follows graph ancestry to find branches stacked above currentBranch, calling
// chooseBranch at any bifurcation.
func (e *Engine) DiscoverStack(
	currentBranch string,
	chooseBranch ChooseBranchFn,
) ([]Branch, error) {
	ancestors, err := e.traceChainTo(currentBranch)
	if err != nil {
		return nil, err
	}

	descendants, err := e.traceDescendants(currentBranch, chooseBranch)
	if err != nil {
		return nil, err
	}

	return append(ancestors, descendants...), nil
}

// traceChainTo returns the chain from baseBranch (inclusive) up to target (inclusive),
// bottom-to-top.
//
// Parent resolution is config-first and side-effect-free:
// 1. use branch.<name>.stackParent when present
// 2. otherwise infer a parent from the graph
// 3. otherwise fall back to the base branch
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

func (e *Engine) traceDescendants(
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

// directChildren returns branches whose resolved parent is parent.
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

// IsChildOf reports whether child is a direct or transitive child of parent
// in the resolved stack tree (i.e., child is strictly below parent).
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

// SubtreeMembers returns all branches in the subtree rooted at branchName (excluding
// the root itself), each paired with their immediate parent, in pre-order (parents
// before children).
func (e *Engine) SubtreeMembers(branchName string) []BranchWithParent {
	root := e.BuildTree()
	node := findTreeNode(root, branchName)
	if node == nil {
		return nil
	}

	var result []BranchWithParent
	for _, child := range node.Children {
		collectSubtreeMembers(child, branchName, &result)
	}
	return result
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

func collectSubtreeMembers(node *TreeNode, parent string, result *[]BranchWithParent) {
	*result = append(
		*result,
		BranchWithParent{
			Branch: node.Branch,
			Parent: parent,
		},
	)

	for _, child := range node.Children {
		collectSubtreeMembers(child, node.Branch.Name, result)
	}
}

var errStopWalk = fmt.Errorf("stop walk")

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

func (e *Engine) mustHeadOf(branch string) (string, error) {
	head, ok := e.graph.HeadOf(branch)
	if !ok {
		return "", fmt.Errorf("branch %q not found in graph", branch)
	}
	return head, nil
}

func (e *Engine) resolveParent(branch string) (string, error) {
	if _, err := e.mustHeadOf(branch); err != nil {
		return "", err
	}
	if branch == e.base {
		return "", fmt.Errorf("branch %q has no parent in the stack", branch)
	}
	if parent, ok := e.git.StackParent(branch); ok {
		return parent, nil
	}
	if parent, ok := e.inferParent(branch); ok {
		return parent, nil
	}
	return e.base, nil
}

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
		if cfgParent, ok := e.git.StackParent(candidate); ok && cfgParent == branch {
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

func isBetterCandidate(a, b candidateScore) bool {
	if a.dist != b.dist {
		return a.dist < b.dist
	}
	return a.name < b.name
}

func (e *Engine) hasDrift(branch string) bool {
	parent, ok := e.git.StackParent(branch)
	if !ok {
		return false
	}
	storedBase, ok := e.git.StackMergeBase(branch)
	if !ok || storedBase == "" {
		return false
	}
	branchHead, ok := e.graph.HeadOf(branch)
	if !ok {
		return false
	}
	parentHead, ok := e.graph.HeadOf(parent)
	if !ok {
		return false
	}
	base, ok := e.graph.MergeBase(branchHead, parentHead)
	if !ok {
		return false
	}
	return base != storedBase
}
