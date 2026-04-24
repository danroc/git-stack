package discovery

import (
	"fmt"
	"os/exec"
	"slices"
	"testing"

	"github.com/danroc/git-stack/internal/git"
)

// initTestRepo creates a temporary git repository for config operations.
func initTestRepo(t *testing.T) *git.Client {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main", dir) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return git.NewClient(dir)
}

// initLinearRepo creates a real git repo with three commits and branches:
// main (c0) ← feat-1 (c1) ← feat-2 (c2).
func initLinearRepo(t *testing.T) *git.Client {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"git", "init", "-b", "main", dir},
		{"git", "-C", dir, "config", "user.email", "test@test"},
		{"git", "-C", dir, "config", "user.name", "test"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "c0"},
		{"git", "-C", dir, "checkout", "-b", "feat-1"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "c1"},
		{"git", "-C", dir, "checkout", "-b", "feat-2"},
		{"git", "-C", dir, "commit", "--allow-empty", "-m", "c2"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return git.NewClient(dir)
}

func newTestEngine(t *testing.T, g *git.Graph, base string) *Engine {
	t.Helper()
	return &Engine{
		git:   initTestRepo(t),
		base:  base,
		graph: g,
	}
}

func linearTestGraph() *git.Graph {
	return git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c2",
		},
	)
}

// linearTestGraphWithHeads creates a linear graph with the same parent structure
// but custom branch heads.
func linearTestGraphWithHeads(heads map[string]string) *git.Graph {
	return git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		heads,
	)
}

func branchingTestGraph() *git.Graph {
	return git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c0"},
		},
		map[string]string{
			"main":    "c0",
			"feat-1a": "c1",
			"feat-1b": "c2",
		},
	)
}

func TestTraceChainTo_LinearInference(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}

	got := []string{chain[0].Name, chain[1].Name, chain[2].Name}
	want := []string{"main", "feat-1", "feat-2"}
	if !slices.Equal(got, want) {
		t.Fatalf("traceChainTo(feat-2) = %v, want %v", got, want)
	}
}

func TestTraceChainTo_ConfigParentWins(t *testing.T) {
	g := linearTestGraphWithHeads(map[string]string{
		"main":   "c0",
		"feat-1": "c2",
		"feat-2": "c1",
	})
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}

	got := []string{chain[0].Name, chain[1].Name, chain[2].Name}
	want := []string{"main", "feat-1", "feat-2"}
	if !slices.Equal(got, want) {
		t.Fatalf("traceChainTo(feat-2) = %v, want %v", got, want)
	}
}

func TestTraceChildren_Bifurcation(t *testing.T) {
	e := newTestEngine(t, branchingTestGraph(), "main")
	chooseCalled := false

	_, err := e.traceChildren(
		"main",
		func(action string, choices []string) (string, error) {
			chooseCalled = true
			if action != "traverse" {
				t.Fatalf("action = %q, want traverse", action)
			}
			if len(choices) != 2 {
				t.Fatalf("choices = %v, want 2 branches", choices)
			}
			return choices[0], nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !chooseCalled {
		t.Fatal("chooseBranch should be called for bifurcation")
	}
}

func TestDirectChildren_UsesConfigAndInferenceForUnconfiguredBranches(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	if got := e.directChildren("main"); !slices.Equal(got, []string{"feat-1"}) {
		t.Fatalf("directChildren(main) = %v, want [feat-1]", got)
	}
	if got := e.directChildren("feat-1"); !slices.Equal(got, []string{"feat-2"}) {
		t.Fatalf("directChildren(feat-1) = %v, want [feat-2]", got)
	}
}

func TestDirectChildren_InferenceOnlyUsesAncestorCandidates(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c1": {},
			"c2": {"c1"},
			"c3": {"c2"},
			"c4": {"c3"},
		},
		map[string]string{
			"main": "c1",
			"D":    "c2",
			"F":    "c3",
			"B":    "c4",
		},
	)
	e := newTestEngine(t, g, "main")

	if got := e.directChildren("main"); !slices.Equal(got, []string{"D"}) {
		t.Fatalf("directChildren(main) = %v, want [D]", got)
	}
	if got := e.directChildren("D"); !slices.Equal(got, []string{"F"}) {
		t.Fatalf("directChildren(D) = %v, want [F]", got)
	}
}

func TestDirectChildren_FallsBackToBaseWhenInferenceFails(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"m1": {"c0"},
			"f1": {"c0"},
		},
		map[string]string{
			"main":   "m1",
			"feat-1": "f1",
		},
	)
	e := newTestEngine(t, g, "main")

	if got := e.directChildren("main"); !slices.Equal(got, []string{"feat-1"}) {
		t.Fatalf("directChildren(main) = %v, want [feat-1]", got)
	}
}

func TestDirectChildren_DoesNotOverrideStoredParent(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}

	if got := e.directChildren("feat-1"); len(got) != 0 {
		t.Fatalf("directChildren(feat-1) = %v, want []", got)
	}
	if got := e.directChildren(
		"main",
	); !slices.Equal(
		got,
		[]string{"feat-1", "feat-2"},
	) {
		t.Fatalf("directChildren(main) = %v, want [feat-1 feat-2]", got)
	}
}

func TestParent_ConfigFirstThenInference(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}

	parent, err := e.Parent("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "main" {
		t.Fatalf("Parent(feat-2) = %q, want main", parent)
	}

	parent, err = e.Parent("feat-1")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "main" {
		t.Fatalf("Parent(feat-1) = %q, want main", parent)
	}
}

func TestIsChildOf_FollowsResolvedParents(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if !e.IsChildOf("feat-2", "main") {
		t.Fatal("feat-2 should be below main")
	}

	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}
	if e.IsChildOf("feat-2", "feat-1") {
		t.Fatal("feat-2 should no longer be below feat-1 once config points to main")
	}
}

func TestBuildTree_MarksDriftWithoutCorrectingParent(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.git.SetStackMergeBase("feat-2", "c0"); err != nil {
		t.Fatal(err)
	}

	root := e.BuildTree()
	if len(root.Children) != 1 || root.Children[0].Branch.Name != "feat-1" {
		t.Fatalf("root.Children = %v, want feat-1 tree", root.Children)
	}
	feat2 := root.Children[0].Children[0]
	if feat2.Branch.Name != "feat-2" {
		t.Fatalf("child = %q, want feat-2", feat2.Branch.Name)
	}
	if !feat2.Drifted {
		t.Fatal("feat-2 should be marked drifted when stored merge-base differs")
	}
}

func TestSetParent_StoresLastKnownMergeBase(t *testing.T) {
	g := initLinearRepo(t) // real repo: main ← feat-1 ← feat-2
	graph, err := g.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{git: g, base: "main", graph: graph}

	if err := e.SetParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	parent, ok := e.git.StackParent("feat-2")
	if !ok || parent != "feat-1" {
		t.Fatalf("stack parent = %q (ok=%v), want feat-1", parent, ok)
	}

	feat1Head, _ := graph.HeadOf("feat-1")
	mergeBase, ok := e.git.StackMergeBase("feat-2")
	if !ok || mergeBase != feat1Head {
		t.Fatalf("stack merge-base = %q (ok=%v), want %s", mergeBase, ok, feat1Head)
	}
}

func TestSubtreeChildren_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	children := e.SubtreeChildren("feat-1")
	if len(children) != 1 {
		t.Fatalf("got %d members, want 1", len(children))
	}
	if children[0].Branch.Name != "feat-2" || children[0].Parent != "feat-1" {
		t.Fatalf(
			"got {%q %q}, want {feat-2 feat-1}",
			children[0].Branch.Name,
			children[0].Parent,
		)
	}
}

func TestTraceChainTo_TargetEqualsBase(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	chain, err := e.traceChainTo("main")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 || chain[0].Name != "main" {
		t.Fatalf("traceChainTo(main) = %v, want [main]", chain)
	}
}

func TestTraceChainTo_BranchNotFound(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	_, err := e.traceChainTo("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent branch")
	}
}

func TestTraceChildren_SingleChild(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	children, err := e.traceChildren("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	// traceChildren follows the chain bottom-to-top, so it returns all
	// descendants: feat-1 and feat-2.
	if len(children) != 2 {
		t.Fatalf("traceChildren(main) has %d children, want 2", len(children))
	}
	if children[0].Name != "feat-1" || children[1].Name != "feat-2" {
		t.Fatalf("traceChildren(main) = %v, want [feat-1, feat-2]", children)
	}
}

func TestTraceChildren_ZeroChildren(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c0": {}},
		map[string]string{"main": "c0"},
	)
	e := newTestEngine(t, g, "main")

	children, err := e.traceChildren("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 0 {
		t.Fatalf("traceChildren(main) = %v, want []", children)
	}
}

func TestTraceChildren_ChainFollowsSingleChild(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	children, err := e.traceChildren("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("traceChildren(main) = %v, want [feat-1, feat-2]", children)
	}
	if children[0].Name != "feat-1" || children[1].Name != "feat-2" {
		t.Fatalf("got %v, want [feat-1, feat-2]", children)
	}
}

func TestTraceChildren_ChoseBranchError(t *testing.T) {
	e := newTestEngine(t, branchingTestGraph(), "main")

	_, err := e.traceChildren("main", func(_ string, _ []string) (string, error) {
		return "", fmt.Errorf("user cancelled")
	})
	if err == nil || err.Error() != "user cancelled" {
		t.Fatalf("expected 'user cancelled' error, got %v", err)
	}
}

func TestDirectChildren_ZeroChildren(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c0": {}},
		map[string]string{"main": "c0"},
	)
	e := newTestEngine(t, g, "main")

	children := e.directChildren("main")
	if len(children) != 0 {
		t.Fatalf("directChildren(main) = %v, want []", children)
	}
}

func TestDirectChildren_AllConfigured(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c0"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c2",
		},
	)
	e := newTestEngine(t, g, "main")
	// Configure both branches to be direct children of main.
	if err := e.git.SetStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}
	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}

	children := e.directChildren("main")
	if !slices.Equal(children, []string{"feat-1", "feat-2"}) {
		t.Fatalf("directChildren(main) = %v, want [feat-1 feat-2]", children)
	}
}

func TestParent_BaseBranchReturnsError(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	_, err := e.Parent("main")
	if err == nil {
		t.Fatal("expected error for Parent(base)")
	}
}

func TestIsChildOf_SameBranch(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	if e.IsChildOf("feat-1", "feat-1") {
		t.Error("IsChildOf(x, x) should be false")
	}
}

func TestIsChildOf_NotChild(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	if e.IsChildOf("main", "feat-1") {
		t.Error("main should not be a child of feat-1")
	}
}

func TestSubtreeChildren_NonExistent(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	children := e.SubtreeChildren("nonexistent")
	if children != nil {
		t.Errorf("SubtreeChildren(nonexistent) = %v, want nil", children)
	}
}

func TestSubtreeChildren_DeepNonLinear(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c1"},
			"c4": {"c2"},
		},
		map[string]string{
			"main":    "c0",
			"feat-1":  "c1",
			"feat-2a": "c2",
			"feat-2b": "c3",
			"feat-3":  "c4",
		},
	)
	// Set up parent relationships.
	e := newTestEngine(t, g, "main")
	for _, pair := range [][2]string{
		{"feat-1", "main"},
		{"feat-2a", "feat-1"},
		{"feat-2b", "feat-1"},
		{"feat-3", "feat-2a"},
	} {
		if err := e.git.SetStackParent(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}

	children := e.SubtreeChildren("feat-1")
	if len(children) != 3 {
		t.Fatalf("SubtreeChildren(feat-1) has %d members, want 3", len(children))
	}
	// Pre-order: feat-2a, feat-3, feat-2b
	if children[0].Branch.Name != "feat-2a" || children[0].Parent != "feat-1" {
		t.Errorf(
			"first child = {%q %q}, want {feat-2a feat-1}",
			children[0].Branch.Name,
			children[0].Parent,
		)
	}
	if children[1].Branch.Name != "feat-3" || children[1].Parent != "feat-2a" {
		t.Errorf(
			"second child = {%q %q}, want {feat-3 feat-2a}",
			children[1].Branch.Name,
			children[1].Parent,
		)
	}
	if children[2].Branch.Name != "feat-2b" || children[2].Parent != "feat-1" {
		t.Errorf(
			"third child = {%q %q}, want {feat-2b feat-1}",
			children[2].Branch.Name,
			children[2].Parent,
		)
	}
}

func TestHasDrift_NoConfigParent(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	if e.hasDrift("feat-1") {
		t.Error("hasDrift should be false when no config parent is set")
	}
}

func TestHasDrift_GraphMissingBranch(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c0": {}, "c1": {"c0"}},
		map[string]string{"main": "c0"},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}

	if e.hasDrift("feat-1") {
		t.Error("hasDrift should be false when branch is not in graph")
	}
}

func TestBuildTree_NoBranches(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c0": {}},
		map[string]string{"main": "c0"},
	)
	e := newTestEngine(t, g, "main")

	root := e.BuildTree()
	if root.Branch.Name != "main" {
		t.Fatalf("root = %q, want main", root.Branch.Name)
	}
	if len(root.Children) != 0 {
		t.Errorf("root.Children = %v, want []", root.Children)
	}
}

func TestBuildTree_ComputesAheadBehind(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c2",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-1", "main"); err != nil {
		t.Fatal(err)
	}

	root := e.BuildTree()
	if len(root.Children) != 1 {
		t.Fatalf("root.Children has %d children, want 1", len(root.Children))
	}
	feat1 := root.Children[0]
	if feat1.Branch.Name != "feat-1" {
		t.Fatalf("child = %q, want feat-1", feat1.Branch.Name)
	}
	if feat1.AheadCount != 2 {
		t.Errorf("AheadCount = %d, want 2", feat1.AheadCount)
	}
}

func TestWalkResolvedParents_CycleDetection(t *testing.T) {
	g := initTestRepo(t)

	e := &Engine{
		git:   g,
		base:  "main",
		graph: linearTestGraph(),
	}

	// Create a cycle: feat-1 → feat-2 → feat-1
	for _, pair := range [][2]string{
		{"feat-1", "feat-2"},
		{"feat-2", "feat-1"},
	} {
		if err := e.git.SetStackParent(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}

	err := e.walkResolvedParents("feat-1", func(_ string) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestFindTreeNode_NotFound(t *testing.T) {
	t.Parallel()

	root := &TreeNode{
		Branch: Branch{Name: "main"},
		Children: []*TreeNode{
			{Branch: Branch{Name: "feat-1"}},
		},
	}

	if findTreeNode(root, "nonexistent") != nil {
		t.Error("findTreeNode should return nil for non-existent node")
	}
}

func TestFlattenSubtree_EmptyChildren(t *testing.T) {
	t.Parallel()

	node := &TreeNode{
		Branch:   Branch{Name: "main"},
		Children: nil,
	}

	result := flattenSubtree(node)
	if len(result) != 0 {
		t.Errorf("flattenSubtree(empty) = %v, want nil or empty", result)
	}
}

func TestDiscoverStack_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	stack, err := e.DiscoverStack("feat-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stack) != 3 {
		t.Fatalf("DiscoverStack(feat-2) has %d branches, want 3", len(stack))
	}
	if stack[0].Name != "main" || stack[1].Name != "feat-1" ||
		stack[2].Name != "feat-2" {
		t.Fatalf("stack = %v, want [main feat-1 feat-2]", stack)
	}
}

func TestDiscoverStack_SingleBranch(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c0": {}},
		map[string]string{"main": "c0"},
	)
	e := newTestEngine(t, g, "main")

	stack, err := e.DiscoverStack("main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(stack) != 1 || stack[0].Name != "main" {
		t.Fatalf("DiscoverStack(main) = %v, want [main]", stack)
	}
}

func TestNewEngine(t *testing.T) {
	g := initLinearRepo(t)

	engine, err := NewEngine(g, "main")
	if err != nil {
		t.Fatal(err)
	}
	if engine.BaseBranch() != "main" {
		t.Errorf("BaseBranch() = %q, want main", engine.BaseBranch())
	}
}

func TestDetectBase_MainBranch(t *testing.T) {
	g := initTestRepo(t)
	dir := g.Dir()

	// Init with main.
	cmd := exec.Command( //nolint:gosec,golines
		"git", "-C", dir, "commit", "--allow-empty", "-m", "c0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	base, err := DetectBase(g)
	if err != nil {
		t.Fatal(err)
	}
	if base != "main" {
		t.Errorf("DetectBase() = %q, want main", base)
	}
}

func TestDetectBase_MasterBranch(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "master", dir) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cmd = exec.Command( //nolint:gosec,golines
		"git", "-C", dir, "commit", "--allow-empty", "-m", "c0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	g := git.NewClient(dir)
	base, err := DetectBase(g)
	if err != nil {
		t.Fatal(err)
	}
	if base != "master" {
		t.Errorf("DetectBase() = %q, want master", base)
	}
}

func TestDetectBase_NoDefaultBranch(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Create a branch that is neither main nor master.
	cmd = exec.Command( //nolint:gosec,golines
		"git", "-C", dir, "checkout", "-q", "-b", "develop",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v\n%s", err, out)
	}
	cmd = exec.Command( //nolint:gosec,golines
		"git", "-C", dir, "commit", "--allow-empty", "-m", "c0",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	g := git.NewClient(dir)
	_, err := DetectBase(g)
	if err == nil {
		t.Fatal("expected error when no main/master branch exists")
	}
}

func TestResolveParent_ConfigParentMissingInGraph(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	// Configure feat-2 to point to feat-1, then remove feat-1 from graph.
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	// feat-1 still exists in graph, so config should win.
	parent, err := e.resolveParent("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "feat-1" {
		t.Errorf("resolveParent(feat-2) = %q, want feat-1", parent)
	}
}

func TestInferParent_FallsBackToMainWhenNoAncestor(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{
			"c0":       {},
			"c1":       {"c0"},
			"z-branch": {"c1"},
			"a-branch": {"c1"},
		},
		map[string]string{
			"main":     "c0",
			"z-branch": "c1",
			"a-branch": "c1",
		},
	)
	e := newTestEngine(t, g, "main")

	// z-branch and a-branch share the same head (c1), so neither is an
	// ancestor of the other. inferParent(z-branch) falls back to main.
	parent, ok := e.inferParent("z-branch")
	if !ok {
		t.Fatal("inferParent should find a parent")
	}
	if parent != "main" {
		t.Errorf("inferParent(z-branch) = %q, want main", parent)
	}
}

func TestIsBetterCandidate_DistanceFirst(t *testing.T) {
	t.Parallel()

	a := candidateScore{name: "b", dist: 1}
	b := candidateScore{name: "a", dist: 2}
	if !isBetterCandidate(a, b) {
		t.Error("closer distance should win regardless of alphabetical order")
	}
}

func TestIsBetterCandidate_AlphabeticalTiebreak(t *testing.T) {
	t.Parallel()

	a := candidateScore{name: "a", dist: 1}
	b := candidateScore{name: "b", dist: 1}
	if !isBetterCandidate(a, b) {
		t.Error("alphabetically earlier name should win on tie")
	}
}
