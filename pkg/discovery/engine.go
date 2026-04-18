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
	git        *git.Client
	graph      *git.Graph
}

// NewEngine creates an engine that discovers stacks relative to baseBranch.
func NewEngine(
	g *git.Client,
	baseBranch string,
) (*Engine, error) {
	graph, err := g.LoadGraph(baseBranch)
	if err != nil {
		return nil, fmt.Errorf("loading commit graph: %w", err)
	}
	return &Engine{git: g, baseBranch: baseBranch, graph: graph}, nil
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
	ancestors, err := e.traceAncestors(currentBranch)
	if err != nil {
		return nil, err
	}

	descendants, err := e.traceDescendants(currentBranch, chooseBranch)
	if err != nil {
		return nil, err
	}

	return append(ancestors, descendants...), nil
}

// traceAncestors returns the chain from base (inclusive) up to currentBranch
// (inclusive) in bottom-to-top order. It walks the first-parent commit chain for the
// primary trace, then falls back to stackParent config for any diverged branches the
// graph walk missed.
func (e *Engine) traceAncestors(currentBranch string) ([]Branch, error) {
	baseHead, ok := e.graph.HeadOf(e.baseBranch)
	if !ok {
		return nil, fmt.Errorf("base branch %q not found in graph", e.baseBranch)
	}
	currentHead, ok := e.graph.HeadOf(currentBranch)
	if !ok {
		return nil, fmt.Errorf("branch %q not found in graph", currentBranch)
	}

	ancestors := []Branch{{Name: e.baseBranch, Head: baseHead}}
	if currentBranch == e.baseBranch {
		return ancestors, nil
	}

	var chain []Branch
	for commit := currentHead; e.graph.Contains(commit); {
		if branch, ok := e.graph.BranchAt(commit); ok {
			chain = append(chain, Branch{Name: branch, Head: commit})
		}
		parent, ok := e.graph.FirstParent(commit)
		if !ok {
			break
		}
		commit = parent
	}
	slices.Reverse(chain)
	ancestors = append(ancestors, chain...)

	reached := ancestors[len(ancestors)-1].Name
	if reached == currentBranch {
		return ancestors, nil
	}

	var fallback []Branch
	for branch := currentBranch; branch != reached && branch != e.baseBranch && branch != ""; {
		head, _ := e.graph.HeadOf(branch)
		fallback = append(fallback, Branch{Name: branch, Head: head})
		parent, ok := e.git.GetStackParent(branch)
		if !ok {
			break
		}
		branch = parent
	}
	slices.Reverse(fallback)
	return append(ancestors, fallback...), nil
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

// directChildren returns branches stacked directly on top of parent: one level above
// it with no intermediate branch between them. Also checks git config for diverged
// branches and persists discoveries.
func (e *Engine) directChildren(parent string) []string {
	parentHead, _ := e.graph.HeadOf(parent)

	// Collect all branches stacked on top of parent (further from base).
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
			// The base head is not in the graph, so IsAncestor can't be used. Any
			// branch whose head is in the graph is above the base by definition.
			//
			// Branches with a non-base configured parent are skipped: without this,
			// two branches at the same HEAD would mutually exclude each other in the
			// directness filter (IsAncestor returns true for equal commits).
			if e.graph.Contains(head) {
				if configParent, ok := e.git.GetStackParent(
					branch,
				); ok && configParent != e.baseBranch {
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

	// Config fallback: add branches whose stackParent is parent but weren't found via
	// graph ancestry (diverged branches).
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
	slices.Sort(above)

	// Filter to only direct children (no intermediate branch between parent and child).
	var direct []string
	for _, candidate := range above {
		candidateHead, _ := e.graph.HeadOf(candidate)
		isDirect := true
		for _, other := range above {
			if other == candidate {
				continue
			}
			otherHead, _ := e.graph.HeadOf(other)
			if otherHead != parentHead && e.graph.IsAncestor(otherHead, candidateHead) {
				isDirect = false
				break
			}
		}
		if isDirect {
			direct = append(direct, candidate)
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
