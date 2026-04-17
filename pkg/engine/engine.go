// Package engine implements stack discovery using Git primitives.
package engine

import (
	"fmt"

	"git-stack/pkg/gitutils"
)

// StackMember is a branch name paired with its HEAD hash.
type StackMember struct {
	BranchName string
	CommitHash string
}

// DisambiguateFn is called when multiple direct-child branches are found at a
// bifurcation point.
//
// It receives the action being performed and the candidate branch names, and returns
// the chosen branch name.
type DisambiguateFn func(action string, choices []string) (string, error)

// DiscoveryEngine identifies stack lineage using a commit graph loaded once from git
// and then queried in-process.
type DiscoveryEngine struct {
	git        *gitutils.Git
	baseBranch string
	graph      *gitutils.Graph
}

// NewDiscoveryEngine creates an engine that discovers stacks relative to baseBranch.
func NewDiscoveryEngine(
	git *gitutils.Git,
	baseBranch string,
) (*DiscoveryEngine, error) {
	graph, err := git.LoadGraph(baseBranch)
	if err != nil {
		return nil, fmt.Errorf("loading commit graph: %w", err)
	}
	return &DiscoveryEngine{git: git, baseBranch: baseBranch, graph: graph}, nil
}

// BaseBranch returns the base branch that anchors the bottom of every stack.
func (e *DiscoveryEngine) BaseBranch() string {
	return e.baseBranch
}

// DetectBaseBranch returns the base branch by checking for well-known defaults.
//
// It looks for "main" then "master" among local branches. If neither exists, it returns
// an error asking the user to specify --base explicitly.
func DetectBaseBranch(git *gitutils.Git) (string, error) {
	branches, err := git.ListBranches()
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
	Member       StackMember
	CommitsAhead int
	Children     []*TreeNode
}

// DiscoverStack identifies the full linear stack that contains currentBranch.
//
// The upward trace (base → currentBranch) walks the first-parent chain in the commit
// graph, collecting commits that are branch heads. The downward trace (branches above
// currentBranch) uses graph ancestry queries. disambiguate is called if a bifurcation
// is found.
func (e *DiscoveryEngine) DiscoverStack(
	currentBranch string,
	disambiguate DisambiguateFn,
) ([]StackMember, error) {
	currentHead, ok := e.graph.HeadOf(currentBranch)
	if !ok {
		return nil, fmt.Errorf("branch %q not found in graph", currentBranch)
	}

	baseHead, ok := e.graph.HeadOf(e.baseBranch)
	if !ok {
		return nil, fmt.Errorf("base branch %q not found in graph", e.baseBranch)
	}

	// --- Upward trace: walk first-parent from currentHead toward base ---

	// Collect branch-head commits newest-to-oldest, then reverse.
	ancestors := []StackMember{{BranchName: e.baseBranch, CommitHash: baseHead}}
	if currentBranch != e.baseBranch {
		var chain []StackMember
		h := currentHead
		for e.graph.Contains(h) {
			if branch, ok := e.graph.BranchAt(h); ok {
				chain = append(chain, StackMember{BranchName: branch, CommitHash: h})
			}
			p, ok := e.graph.FirstParent(h)
			if !ok {
				break
			}
			h = p
		}
		for i := len(chain) - 1; i >= 0; i-- {
			ancestors = append(ancestors, chain[i])
		}

		// Config fallback: if graph walk didn't reach currentBranch, follow
		// stackParent config upward to fill in diverged branches.
		last := ancestors[len(ancestors)-1].BranchName
		if last != currentBranch {
			var configChain []StackMember
			b := currentBranch
			for b != last && b != e.baseBranch && b != "" {
				bHead, _ := e.graph.HeadOf(b)
				configChain = append(configChain, StackMember{
					BranchName: b,
					CommitHash: bHead,
				})
				parent, ok := e.git.GetStackParent(b)
				if !ok {
					break
				}
				b = parent
			}
			// Reverse to get base-to-tip order and append.
			for i := len(configChain) - 1; i >= 0; i-- {
				ancestors = append(ancestors, configChain[i])
			}
		}
	}

	// --- Downward trace: branches built on top of currentBranch ---
	descendants, err := e.traceDescendants(currentBranch, disambiguate)
	if err != nil {
		return nil, err
	}

	return append(ancestors, descendants...), nil
}

func (e *DiscoveryEngine) traceDescendants(
	branch string,
	disambiguate DisambiguateFn,
) ([]StackMember, error) {
	var result []StackMember
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
			chosen, err = disambiguate("traverse", children)
			if err != nil {
				return nil, err
			}
		}

		chosenHash, _ := e.graph.HeadOf(chosen)
		result = append(result, StackMember{BranchName: chosen, CommitHash: chosenHash})
		branch = chosen
	}
}

// BuildTree constructs the full branch tree rooted at the base branch. Unlike
// DiscoverStack, it never prompts, all descendants are included.
func (e *DiscoveryEngine) BuildTree() *TreeNode {
	baseHead, _ := e.graph.HeadOf(e.baseBranch)
	root := &TreeNode{
		Member: StackMember{BranchName: e.baseBranch, CommitHash: baseHead},
	}
	e.buildChildren(root)
	return root
}

func (e *DiscoveryEngine) buildChildren(node *TreeNode) {
	for _, child := range e.directChildren(node.Member.BranchName) {
		childHash, _ := e.graph.HeadOf(child)
		childNode := &TreeNode{
			Member:       StackMember{BranchName: child, CommitHash: childHash},
			CommitsAhead: e.graph.CommitsAhead(node.Member.CommitHash, childHash),
		}
		node.Children = append(node.Children, childNode)
		e.buildChildren(childNode)
	}
}

// directChildren returns branches above parent, with no intermediate branch between
// them. Also checks git config for diverged branches and persists discoveries.
func (e *DiscoveryEngine) directChildren(parent string) []string {
	parentHead, _ := e.graph.HeadOf(parent)

	// Collect all branches above parent via graph ancestry.
	aboveSet := make(map[string]bool)
	for _, branch := range e.graph.Branches() {
		if branch == parent || branch == e.baseBranch {
			continue
		}
		head, ok := e.graph.HeadOf(branch)
		if !ok {
			continue
		}
		if parent == e.baseBranch {
			if e.graph.Contains(head) {
				// Skip branches that have a non-base config parent —
				// they belong under that parent, not directly under base.
				if cp, ok := e.git.GetStackParent(branch); ok && cp != e.baseBranch {
					continue
				}
				aboveSet[branch] = true
			}
		} else {
			if head != parentHead && e.graph.IsAncestor(parentHead, head) {
				aboveSet[branch] = true
			}
		}
	}

	// Config fallback: add branches whose stackParent is parent but
	// weren't found via graph ancestry (diverged branches).
	for _, branch := range e.graph.Branches() {
		if aboveSet[branch] || branch == parent || branch == e.baseBranch {
			continue
		}
		configParent, ok := e.git.GetStackParent(branch)
		if ok && configParent == parent {
			aboveSet[branch] = true
		}
	}

	above := make([]string, 0, len(aboveSet))
	for branch := range aboveSet {
		above = append(above, branch)
	}

	// Filter to only direct children (no intermediate branch between parent and child).
	var direct []string
	for _, c := range above {
		cHead, _ := e.graph.HeadOf(c)
		isDirect := true
		for _, other := range above {
			if other == c {
				continue
			}
			otherHead, _ := e.graph.HeadOf(other)
			if e.graph.IsAncestor(otherHead, cHead) {
				isDirect = false
				break
			}
		}
		if isDirect {
			direct = append(direct, c)
		}
	}

	// Persist discovered relationships, but don't overwrite existing config.
	for _, child := range direct {
		if _, ok := e.git.GetStackParent(child); !ok {
			_ = e.git.SetStackParent(child, parent)
		}
	}

	return direct
}
