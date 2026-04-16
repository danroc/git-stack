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
	return &DiscoveryEngine{baseBranch: baseBranch, graph: graph}, nil
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
		// Ensure currentBranch is the last element (handles the case where its HEAD is
		// below the graph boundary, i.e. no commits above base).
		last := ancestors[len(ancestors)-1].BranchName
		if last != currentBranch {
			ancestors = append(ancestors, StackMember{
				BranchName: currentBranch,
				CommitHash: currentHead,
			})
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
	above := e.branchesAbove(branch)
	if len(above) == 0 {
		return nil, nil
	}

	direct := e.filterDirectChildren(above)
	chosen, err := selectOne(direct, disambiguate)
	if err != nil {
		return nil, err
	}

	chosenHash, _ := e.graph.HeadOf(chosen)
	rest, err := e.traceDescendants(chosen, disambiguate)
	if err != nil {
		return nil, err
	}

	head := []StackMember{{BranchName: chosen, CommitHash: chosenHash}}
	return append(head, rest...), nil
}

// BuildTree constructs the full branch tree rooted at the base branch. Unlike
// DiscoverStack, it never prompts, all descendants are included.
func (e *DiscoveryEngine) BuildTree() (*TreeNode, error) {
	baseHead, _ := e.graph.HeadOf(e.baseBranch)
	root := &TreeNode{
		Member: StackMember{BranchName: e.baseBranch, CommitHash: baseHead},
	}
	if err := e.buildChildren(root); err != nil {
		return nil, err
	}
	return root, nil
}

func (e *DiscoveryEngine) buildChildren(node *TreeNode) error {
	for _, child := range e.filterDirectChildren(
		e.branchesAbove(node.Member.BranchName),
	) {
		childHash, _ := e.graph.HeadOf(child)
		childNode := &TreeNode{
			Member:       StackMember{BranchName: child, CommitHash: childHash},
			CommitsAhead: e.graph.CommitsAhead(node.Member.CommitHash, childHash),
		}
		node.Children = append(node.Children, childNode)
		if err := e.buildChildren(childNode); err != nil {
			return err
		}
	}
	return nil
}

// branchesAbove returns all branches (excluding parent and baseBranch itself) whose
// HEAD is above parent in the commit graph.
func (e *DiscoveryEngine) branchesAbove(parent string) []string {
	parentHead, _ := e.graph.HeadOf(parent)
	var result []string
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
				result = append(result, branch)
			}
		} else {
			if e.graph.IsAncestor(parentHead, head) {
				result = append(result, branch)
			}
		}
	}
	return result
}

func selectOne(choices []string, disambiguate DisambiguateFn) (string, error) {
	if len(choices) == 1 {
		return choices[0], nil
	}
	return disambiguate("traverse", choices)
}

// filterDirectChildren returns the subset of candidates that have no other candidate
// sitting between them and their common ancestor.
func (e *DiscoveryEngine) filterDirectChildren(candidates []string) []string {
	var direct []string
	for _, c := range candidates {
		cHead, _ := e.graph.HeadOf(c)
		isDirect := true
		for _, other := range candidates {
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
	return direct
}
