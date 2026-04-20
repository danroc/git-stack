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

// Engine identifies stack lineage using a commit graph loaded once from git
// and then queried in-process.
type Engine struct {
	baseBranch string
	baseHead   string
	git        *git.Client
	graph      *git.Graph
}

// NewEngine creates an engine that discovers stacks relative to baseBranch.
func NewEngine(
	g *git.Client,
	baseBranch string,
) (*Engine, error) {
	graph, err := g.LoadGraph()
	if err != nil {
		return nil, fmt.Errorf("loading commit graph: %w", err)
	}
	baseHead, _ := graph.HeadOf(baseBranch)
	return &Engine{
		git:        g,
		baseBranch: baseBranch,
		baseHead:   baseHead,
		graph:      graph,
	}, nil
}

// BaseBranch returns the base branch that anchors the bottom of every stack.
func (e *Engine) BaseBranch() string {
	return e.baseBranch
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
	Branch       Branch
	CommitsAhead int
	Children     []*TreeNode
}

// BranchWithParent is a branch paired with the name of its immediate stack parent.
type BranchWithParent struct {
	Branch Branch
	Parent string
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

// traceChainTo returns the chain from baseBranch (inclusive) up to target
// (inclusive), bottom-to-top.
//
// The graph is the primary source of truth: the first-parent chain is walked
// from target downward, and at each commit we enumerate every branch pinned
// there. target itself is always included; co-located siblings of target are
// included only when target.stackParent names them (otherwise they are
// considered siblings, not ancestors). Branches encountered strictly below
// target's head are included unconditionally — they lie on target's chain.
//
// When the graph walk cannot reach baseBranch (base has diverged, or an
// intermediate branch has diverged so the walk stops early), the chain is
// completed by following each missing branch's stackParent config upward.
//
// Every adjacent (child, parent) pair in the final chain is persisted via
// persistParent.
func (e *Engine) traceChainTo(target string) ([]Branch, error) {
	baseHead, ok := e.graph.HeadOf(e.baseBranch)
	if !ok {
		return nil, fmt.Errorf("base branch %q not found in graph", e.baseBranch)
	}
	targetHead, ok := e.graph.HeadOf(target)
	if !ok {
		return nil, fmt.Errorf("branch %q not found in graph", target)
	}

	if target == e.baseBranch {
		return []Branch{{Name: e.baseBranch, Head: baseHead}}, nil
	}

	// Primary graph walk: first-parent from target downward, collecting branches
	// at each commit. target's co-located siblings are filtered per config.
	targetConfigParent, _ := e.git.GetStackParent(target)

	var chain []Branch
	for commit := targetHead; e.graph.Contains(commit); {
		if branches, ok := e.graph.BranchAt(commit); ok {
			if commit == targetHead {
				// At the target commit: add target first (top of local pair),
				// then the configured co-located parent (one level below). All
				// other co-located names are siblings and are skipped.
				chain = append(chain, Branch{Name: target, Head: commit})
				if targetConfigParent != "" {
					for _, name := range branches {
						if name != target && name == targetConfigParent {
							chain = append(chain, Branch{Name: name, Head: commit})
						}
					}
				}
			} else {
				// Below target's head: include all branches at this commit.
				for _, name := range branches {
					chain = append(chain, Branch{Name: name, Head: commit})
				}
			}
		}
		parent, ok := e.graph.FirstParent(commit)
		if !ok {
			break
		}
		commit = parent
	}

	// chain is top-to-bottom; reverse to bottom-to-top.
	slices.Reverse(chain)

	// Divergence recovery: if the bottom of the chain isn't baseBranch, walk
	// the stackParent config chain upward from the bottom-most collected branch
	// until we reach baseBranch or give up.
	bottomName := target
	if len(chain) > 0 {
		bottomName = chain[0].Name
	}
	if bottomName != e.baseBranch {
		recovered := e.recoverViaConfig(bottomName)
		chain = append(recovered, chain...)
	}

	// Ensure baseBranch sits at the bottom. If the recovery did not produce it,
	// prepend explicitly.
	if len(chain) == 0 || chain[0].Name != e.baseBranch {
		chain = append([]Branch{{Name: e.baseBranch, Head: baseHead}}, chain...)
	}

	// Persist every adjacent (child, parent) pair.
	for i := 1; i < len(chain); i++ {
		e.persistParent(chain[i].Name, chain[i-1].Name)
	}

	return chain, nil
}

// recoverViaConfig walks branch's stackParent config chain upward until it
// reaches the base branch or can go no further. Returned slice is bottom-to-top
// and excludes branch itself.
func (e *Engine) recoverViaConfig(branch string) []Branch {
	var chain []Branch
	seen := map[string]bool{branch: true}
	for current := branch; ; {
		parent, ok := e.git.GetStackParent(current)
		if !ok || seen[parent] {
			return chain
		}
		seen[parent] = true
		parentHead, _ := e.graph.HeadOf(parent)
		chain = append([]Branch{{Name: parent, Head: parentHead}}, chain...)
		if parent == e.baseBranch {
			return chain
		}
		current = parent
	}
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
	baseHead, _ := e.graph.HeadOf(e.baseBranch)
	root := &TreeNode{
		Branch: Branch{Name: e.baseBranch, Head: baseHead},
	}
	e.buildChildren(root)
	return root
}

func (e *Engine) buildChildren(node *TreeNode) {
	for _, child := range e.directChildren(node.Branch.Name) {
		childHash, _ := e.graph.HeadOf(child)
		childNode := &TreeNode{
			Branch:       Branch{Name: child, Head: childHash},
			CommitsAhead: e.graph.CommitsAhead(node.Branch.Head, childHash),
		}
		node.Children = append(node.Children, childNode)
		e.buildChildren(childNode)
	}
}

// isAbove reports whether candidateHead is strictly above parentHead in the
// commit graph (ancestor-of, and not equal).
func (e *Engine) isAbove(parentHead, candidateHead string) bool {
	if candidateHead == parentHead {
		return false
	}
	return e.graph.IsAncestor(parentHead, candidateHead)
}

// directChildren returns branches stacked directly on top of parent. The graph
// is authoritative: a branch is a direct child if it is above parent in the
// graph and no other branch sits strictly between them. Config is consulted
// only for two ambiguities:
//
//  1. Co-located branches: when two branches share a HEAD and one's
//     stackParent names the other, the named one stays as direct child of
//     parent; the other demotes to a child of its config-parent.
//  2. Divergence: when a branch's stackParent config names parent but the
//     branch is not in the graph-above set (parent has moved past it).
//
// If a branch above parent sits at a commit shared by multiple branches, the
// edge case "child above co-located pair" applies: the child is assigned to
// its configured parent if named, else to the alphabetically-first co-located
// branch, and that choice is persisted to config.
//
// Every branch in the final set has its stackParent written to config.
//
// Precondition: parent must be a branch present in the loaded graph; passing
// an unknown name returns an empty slice silently.
func (e *Engine) directChildren(parent string) []string {
	parentHead, _ := e.graph.HeadOf(parent)

	// Step 1 — graph-above set.
	above := make(map[string]bool)
	for _, branch := range e.graph.Branches() {
		if branch == parent || branch == e.baseBranch {
			continue
		}
		head, ok := e.graph.HeadOf(branch)
		if !ok {
			continue
		}
		if e.isAbove(parentHead, head) {
			above[branch] = true
		}
	}

	// Step 2 — co-location handling among branches already in the set.
	// If X and Y share a HEAD and one's stackParent names the other, demote
	// the configured-child.
	for _, b := range e.graph.Branches() {
		if !above[b] {
			continue
		}
		bHead, _ := e.graph.HeadOf(b)
		coLocated, _ := e.graph.BranchAt(bHead)
		if len(coLocated) < 2 {
			continue
		}
		configParent, ok := e.git.GetStackParent(b)
		if !ok {
			continue
		}
		if !slices.Contains(coLocated, configParent) {
			continue
		}
		if above[configParent] && configParent != b {
			delete(above, b)
		}
	}

	// Step 3 — graph-direct filter. Drop C if some D sits strictly between
	// parent and C. Intermediates are checked against every branch, not just
	// members of `above`, so a non-candidate still blocks.
	direct := make(map[string]bool)
	for candidate := range above {
		cHead, _ := e.graph.HeadOf(candidate)
		isDirect := true
		for _, other := range e.graph.Branches() {
			if other == candidate || other == e.baseBranch || other == parent {
				continue
			}
			oHead, _ := e.graph.HeadOf(other)
			if oHead == parentHead || oHead == cHead {
				continue
			}
			if e.isAbove(parentHead, oHead) && e.graph.IsAncestor(oHead, cHead) {
				isDirect = false
				break
			}
		}
		if isDirect {
			direct[candidate] = true
		}
	}

	// Step 3.5 — "child above co-located pair" edge case.
	// If candidate C's first-parent chain lands on a commit shared by multiple
	// branches, C would be reported as a direct child of each of them. Assign
	// C to its configured parent if named; otherwise to the alphabetically-
	// first co-located branch (and ensure C is a direct child only of the
	// owner).
	for candidate := range direct {
		cHead, _ := e.graph.HeadOf(candidate)
		owner := e.coLocatedOwnerFor(candidate, cHead)
		if owner != "" && owner != parent {
			ownerHead, _ := e.graph.HeadOf(owner)
			if parentHead == ownerHead {
				// `parent` and `owner` share a commit; only `owner` should
				// claim this candidate.
				delete(direct, candidate)
			}
		}
	}

	// Step 4 — divergence recovery. Branches whose stackParent config names
	// parent but that aren't in the direct set.
	for _, branch := range e.graph.Branches() {
		if direct[branch] || branch == parent || branch == e.baseBranch {
			continue
		}
		configParent, ok := e.git.GetStackParent(branch)
		if ok && configParent == parent {
			direct[branch] = true
		}
	}

	// Finalize and sort.
	result := make([]string, 0, len(direct))
	for b := range direct {
		result = append(result, b)
	}
	slices.Sort(result)

	// Step 5 — persist (always-write).
	for _, child := range result {
		e.persistParent(child, parent)
	}

	return result
}

// coLocatedOwnerFor determines which co-located branch (if any) "owns"
// candidate when candidate sits above multiple branches sharing the same HEAD
// commit in candidate's first-parent chain.
//
// Returns "" if candidate's first-parent chain encounters a commit with a
// single branch before it encounters a co-located multi-branch commit (no
// ambiguity at the nearest branch level). Otherwise returns the owning
// branch: the one named by candidate.stackParent if it is among the
// co-located set, else the alphabetically-first co-located branch.
//
// A self-referential config (stackParent == candidate) is harmless here
// because candidate is never in the first-parent chain being walked — the
// walk starts from candidate's parent commit.
func (e *Engine) coLocatedOwnerFor(candidate, candidateHead string) string {
	configParent, hasConfig := e.git.GetStackParent(candidate)
	for commit, ok := e.graph.FirstParent(candidateHead); ok; commit, ok = e.graph.FirstParent(commit) {
		branches, haveBranches := e.graph.BranchAt(commit)
		if !haveBranches {
			continue
		}
		if len(branches) < 2 {
			return ""
		}
		if hasConfig {
			for _, b := range branches {
				if b == configParent {
					return b
				}
			}
		}
		return branches[0]
	}
	return ""
}

// Parent returns the immediate stack parent of branch by walking the full chain
// from base to branch via traceChainTo. Under graph-first semantics, this is
// the correct way to resolve the parent: the graph walk plus config recovery
// handles divergence and co-location ambiguities uniformly.
func (e *Engine) Parent(branch string) (string, error) {
	chain, err := e.traceChainTo(branch)
	if err != nil {
		return "", err
	}
	if len(chain) < 2 {
		return "", fmt.Errorf("branch %q has no parent in the stack", branch)
	}
	return chain[len(chain)-2].Name, nil
}

// IsBranchDescendant reports whether descendant is strictly below ancestor in
// the stack tree. This is true if the commit graph shows descendant above
// ancestor, OR descendant's stackParent config chain reaches ancestor.
//
// The config-chain case covers divergence: when a parent branch advances past
// a child, graph ancestry alone loses the relationship, but the stack tree
// still considers the child a descendant. Callers like stack.Move rely on this
// for cycle detection.
func (e *Engine) IsBranchDescendant(ancestor, descendant string) bool {
	ancestorHead, ok := e.graph.HeadOf(ancestor)
	if !ok {
		return false
	}
	descHead, ok := e.graph.HeadOf(descendant)
	if !ok {
		return false
	}
	if ancestorHead == descHead {
		return false
	}
	if e.graph.IsAncestor(ancestorHead, descHead) {
		return true
	}

	// Config-chain fallback: walk descendant's stackParent upward looking for
	// ancestor. Guarded against cycles by a visited set.
	visited := map[string]bool{descendant: true}
	for current := descendant; ; {
		parent, ok := e.git.GetStackParent(current)
		if !ok || visited[parent] {
			return false
		}
		if parent == ancestor {
			return true
		}
		visited[parent] = true
		current = parent
	}
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
func (e *Engine) SetParent(branch, parent string) error {
	return e.git.SetStackParent(branch, parent)
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

// persistParent records child's immediate stack parent in git config. Errors
// are silently swallowed: config is a hint, never a hard dependency.
func (e *Engine) persistParent(child, parent string) {
	_ = e.git.SetStackParent(child, parent)
}
