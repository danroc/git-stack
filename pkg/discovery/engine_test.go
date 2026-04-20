package discovery

import (
	"os/exec"
	"testing"

	"git-stack/pkg/git"
)

// initTestRepo creates a temporary git repository for config operations.
func initTestRepo(t *testing.T) *git.Client {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir) //nolint:gosec
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return git.NewClient(dir)
}

func newTestEngine(t *testing.T, g *git.Graph, baseBranch string) *Engine {
	t.Helper()
	baseHead, _ := g.HeadOf(baseBranch)
	return &Engine{
		git:        initTestRepo(t),
		baseBranch: baseBranch,
		baseHead:   baseHead,
		graph:      g,
	}
}

// linearTestGraph: main(c0) ← feat-1(c1) ← feat-2(c2)
// c0 is present as a root (the floor) with no parents, matching the
// post-Task-3 graph shape where the loaded graph includes commits down to
// the octopus merge-base inclusive.
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

// branchingTestGraph: two branches off main — feat-1a(c1) and feat-1b(c2).
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

func TestTraceChainTo(t *testing.T) {
	tests := []struct {
		name    string
		current string
		want    []string
	}{
		{
			"linear from top",
			"feat-2",
			[]string{"main", "feat-1", "feat-2"},
		},
		{
			"from base",
			"main",
			[]string{"main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEngine(t, linearTestGraph(), "main")
			got, err := e.traceChainTo(tt.current)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, b := range tt.want {
				if got[i].Name != b {
					t.Errorf("[%d] = %q, want %q", i, got[i].Name, b)
				}
			}
		})
	}
}

func TestTraceDescendants_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	got, err := e.traceDescendants(
		"feat-1",
		func(_ string, _ []string) (string, error) {
			t.Fatal("chooseBranch should not be called for single-child chain")
			return "", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "feat-2" {
		t.Errorf("got %v, want [{feat-2}]", got)
	}
}

func TestTraceDescendants_Bifurcation(t *testing.T) {
	e := newTestEngine(t, branchingTestGraph(), "main")
	chooseCalled := false
	_, err := e.traceDescendants(
		"main",
		func(action string, choices []string) (string, error) {
			chooseCalled = true
			if action != "traverse" {
				t.Errorf("action = %q, want traverse", action)
			}
			if len(choices) != 2 {
				t.Errorf("got %d choices, want 2", len(choices))
			}
			return choices[0], nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !chooseCalled {
		t.Error("chooseBranch should have been called at bifurcation")
	}
}

func TestDirectChildren(t *testing.T) {
	tests := []struct {
		branch string
		want   []string
	}{
		{"main", []string{"feat-1"}},
		{"feat-1", []string{"feat-2"}},
		{"feat-2", nil},
	}

	e := newTestEngine(t, linearTestGraph(), "main")
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got := e.directChildren(tt.branch)
			if len(got) != len(tt.want) {
				t.Fatalf("directChildren(%q) = %v, want %v", tt.branch, got, tt.want)
			}
			for i, b := range tt.want {
				if got[i] != b {
					t.Errorf("[%d] = %q, want %q", i, got[i], b)
				}
			}
		})
	}
}

// coEqualTestGraph: main(c0) ← feat-1(c1) ← feat-2a(c2)
//
//	└─ feat-2b(c2)  // same commit as feat-2a
func coEqualTestGraph() *git.Graph {
	return git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":    "c0",
			"feat-1":  "c1",
			"feat-2a": "c2",
			"feat-2b": "c2",
		},
	)
}

// configParentCoEqualGraph: main(c0) ← test-1(c1) ← test-3(c2)
//
//	└─ test-4(c2)  // co-equal with test-3, declared child of test-3 via config
func configParentCoEqualGraph() *git.Graph {
	return git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"test-1": "c1",
			"test-3": "c2",
			"test-4": "c2",
		},
	)
}

// TestDirectChildren_ConfigParentOverridesTopology verifies that a branch with an
// explicit stackParent config is placed under its declared parent, not its topological
// ancestor. test-4 is co-equal with test-3 (same commit) but declares test-3 as its
// parent, so it must appear under test-3 and not under test-1.
func TestDirectChildren_ConfigParentOverridesTopology(t *testing.T) {
	e := newTestEngine(t, configParentCoEqualGraph(), "main")
	if err := e.git.SetStackParent("test-4", "test-3"); err != nil {
		t.Fatal(err)
	}

	got1 := e.directChildren("test-1")
	want1 := []string{"test-3"}
	if len(got1) != len(want1) || (len(got1) > 0 && got1[0] != want1[0]) {
		t.Errorf("directChildren(test-1) = %v, want %v", got1, want1)
	}

	got3 := e.directChildren("test-3")
	want3 := []string{"test-4"}
	if len(got3) != len(want3) || (len(got3) > 0 && got3[0] != want3[0]) {
		t.Errorf("directChildren(test-3) = %v, want %v", got3, want3)
	}
}

// TestDirectChildren_ConfigExcludedIntermediateBlocks verifies that a branch
// excluded from the candidate set via configParent still acts as a topological
// intermediate and blocks candidates above it.
func TestDirectChildren_ConfigExcludedIntermediateBlocks(t *testing.T) {
	// main(c0) ← feat-A(c1) ← feat-B(c2) ← feat-C(c3)
	// feat-B's configParent points away from feat-A, so it is excluded from
	// directChildren("feat-A")'s candidate set. But feat-B sits topologically
	// between feat-A and feat-C, so feat-C must not be returned as a direct child.
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c2"},
		},
		map[string]string{
			"main":   "c0",
			"feat-A": "c1",
			"feat-B": "c2",
			"feat-C": "c3",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-B", "some-other-parent"); err != nil {
		t.Fatal(err)
	}

	got := e.directChildren("feat-A")
	if len(got) != 0 {
		t.Errorf("directChildren(feat-A) = %v, want []", got)
	}
}

func TestDirectChildren_CoEqualBranches(t *testing.T) {
	// Two branches sharing the same HEAD commit must both appear as direct
	// children of their common parent; neither should block the other.
	e := newTestEngine(t, coEqualTestGraph(), "main")
	got := e.directChildren("feat-1")
	want := []string{"feat-2a", "feat-2b"}
	if len(got) != len(want) {
		t.Fatalf("directChildren(\"feat-1\") = %v, want %v", got, want)
	}
	for i, b := range want {
		if got[i] != b {
			t.Errorf("[%d] = %q, want %q", i, got[i], b)
		}
	}
}

// virtualStackTestGraph: main(c0) ─┬─ feat-inter(c1)  [sibling of feat-1 from c0]
//
//	└─ feat-1(c2) ← feat-2a(c3)
//	              └─ feat-2b(c3)
//
// feat-1's config parent is set to "feat-inter" (virtual stack via config,
// not topology). feat-2a and feat-2b share the same commit.
func virtualStackTestGraph() *git.Graph {
	return git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c0"},
			"c3": {"c2"},
		},
		map[string]string{
			"main":       "c0",
			"feat-inter": "c1",
			"feat-1":     "c2",
			"feat-2a":    "c3",
			"feat-2b":    "c3",
		},
	)
}

// TestBuildTree_CoEqualBranches_VirtualStack verifies that co-equal branches
// (feat-2a, feat-2b) do not appear as direct children of main when a
// topological intermediate (feat-1) has a non-base config parent.
func TestBuildTree_CoEqualBranches_VirtualStack(t *testing.T) {
	e := newTestEngine(t, virtualStackTestGraph(), "main")
	if err := e.git.SetStackParent("feat-1", "feat-inter"); err != nil {
		t.Fatal(err)
	}

	root := e.BuildTree()

	// main should have exactly one direct child: feat-inter.
	if len(root.Children) != 1 || root.Children[0].Branch.Name != "feat-inter" {
		names := make([]string, len(root.Children))
		for i, c := range root.Children {
			names[i] = c.Branch.Name
		}
		t.Fatalf("root.Children = %v, want [feat-inter]", names)
	}

	featInter := root.Children[0]
	if len(featInter.Children) != 1 || featInter.Children[0].Branch.Name != "feat-1" {
		t.Fatalf("feat-inter.Children = %v, want [feat-1]", featInter.Children)
	}

	feat1 := featInter.Children[0]
	if len(feat1.Children) != 2 {
		t.Fatalf(
			"feat-1 has %d children, want 2: %v",
			len(feat1.Children),
			feat1.Children,
		)
	}
	names := []string{feat1.Children[0].Branch.Name, feat1.Children[1].Branch.Name}
	want := []string{"feat-2a", "feat-2b"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("feat-1.Children[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestBuildTree_CoEqualBranches(t *testing.T) {
	e := newTestEngine(t, coEqualTestGraph(), "main")
	root := e.BuildTree()

	if len(root.Children) != 1 {
		t.Fatalf("root has %d children, want 1", len(root.Children))
	}
	feat1 := root.Children[0]
	if feat1.Branch.Name != "feat-1" {
		t.Fatalf("root.Children[0] = %q, want feat-1", feat1.Branch.Name)
	}
	if len(feat1.Children) != 2 {
		t.Fatalf(
			"feat-1 has %d children, want 2: %v",
			len(feat1.Children),
			feat1.Children,
		)
	}
	names := []string{feat1.Children[0].Branch.Name, feat1.Children[1].Branch.Name}
	want := []string{"feat-2a", "feat-2b"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("feat-1.Children[%d] = %q, want %q", i, names[i], w)
		}
	}
}

func TestBuildTree_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	root := e.BuildTree()

	if root.Branch.Name != "main" {
		t.Fatalf("root = %q, want main", root.Branch.Name)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root has %d children, want 1", len(root.Children))
	}
	feat1 := root.Children[0]
	if feat1.Branch.Name != "feat-1" {
		t.Fatalf("root.Children[0] = %q, want feat-1", feat1.Branch.Name)
	}
	if len(feat1.Children) != 1 {
		t.Fatalf("feat-1 has %d children, want 1", len(feat1.Children))
	}
	if feat1.Children[0].Branch.Name != "feat-2" {
		t.Errorf("feat-1.Children[0] = %q, want feat-2", feat1.Children[0].Branch.Name)
	}
}

func TestBuildTree_CommitsAhead(t *testing.T) {
	// feat-1 is 2 commits above main; feat-2 is 1 commit above feat-1.
	// c1 is an intermediate commit with no branch.
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c2"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c2",
			"feat-2": "c3",
		},
	)
	e := newTestEngine(t, g, "main")
	root := e.BuildTree()

	if len(root.Children) != 1 {
		t.Fatalf("root has %d children, want 1", len(root.Children))
	}
	feat1 := root.Children[0]
	if feat1.Branch.Name != "feat-1" {
		t.Fatalf("root.Children[0] = %q, want feat-1", feat1.Branch.Name)
	}
	if feat1.CommitsAhead != 2 {
		t.Errorf("feat-1 CommitsAhead = %d, want 2", feat1.CommitsAhead)
	}
	if len(feat1.Children) != 1 {
		t.Fatalf("feat-1 has %d children, want 1", len(feat1.Children))
	}
	if feat1.Children[0].CommitsAhead != 1 {
		t.Errorf("feat-2 CommitsAhead = %d, want 1", feat1.Children[0].CommitsAhead)
	}
}

func TestParent_FromConfig(t *testing.T) {
	g := initTestRepo(t)
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := g.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}
	e.git = g

	parent, err := e.Parent("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "feat-1" {
		t.Errorf("got %q, want feat-1", parent)
	}
}

func TestParent_FromGraph(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	parent, err := e.Parent("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "feat-1" {
		t.Errorf("got %q, want feat-1", parent)
	}
}

func TestIsBranchDescendant(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")

	if !e.IsBranchDescendant("main", "feat-1") {
		t.Error("feat-1 should be a descendant of main")
	}
	if !e.IsBranchDescendant("feat-1", "feat-2") {
		t.Error("feat-2 should be a descendant of feat-1")
	}
	if e.IsBranchDescendant("feat-2", "main") {
		t.Error("main should not be a descendant of feat-2")
	}
	if e.IsBranchDescendant("feat-1", "feat-1") {
		t.Error("a branch should not be a descendant of itself")
	}
}

func TestSubtreeMembers_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	members := e.SubtreeMembers("feat-1")
	if len(members) != 1 {
		t.Fatalf("got %d members, want 1", len(members))
	}
	if members[0].Branch.Name != "feat-2" || members[0].Parent != "feat-1" {
		t.Errorf(
			"got {%q, %q}, want {feat-2, feat-1}",
			members[0].Branch.Name,
			members[0].Parent,
		)
	}
}

func TestSubtreeMembers_Branching(t *testing.T) {
	// main(c0) ← feat-1(c1) ← feat-2(c2)
	//                        ← feat-3(c3)
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c2",
			"feat-3": "c3",
		},
	)
	e := newTestEngine(t, g, "main")
	members := e.SubtreeMembers("feat-1")

	if len(members) != 2 {
		t.Fatalf("got %d members, want 2: %v", len(members), members)
	}
	for _, m := range members {
		if m.Parent != "feat-1" {
			t.Errorf("member %q has parent %q, want feat-1", m.Branch.Name, m.Parent)
		}
	}
}

func TestSubtreeMembers_Empty(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	members := e.SubtreeMembers("feat-2")
	if len(members) != 0 {
		t.Errorf("got %d members, want 0", len(members))
	}
}

// TestTraceChainTo_CoLocatedConfiguredSibling verifies that when two branches
// share a HEAD and config says one is the stack parent of the other, the
// configured parent appears in the traced chain immediately below the target.
func TestTraceChainTo_CoLocatedConfiguredSibling(t *testing.T) {
	// main(c0) ← feat-1(c1)
	//                  feat-2(c1)  // same commit as feat-1
	g := git.NewGraph(
		map[string][]string{"c0": {}, "c1": {"c0"}},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c1",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(chain))
	for i, b := range chain {
		got[i] = b.Name
	}
	want := []string{"main", "feat-1", "feat-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestDirectChildren_StaleConfigLosesToUnambiguousGraph verifies that when
// config declares a branch's parent incorrectly and the graph unambiguously
// shows a different parent, the graph wins and config gets rewritten.
func TestDirectChildren_StaleConfigLosesToUnambiguousGraph(t *testing.T) {
	// Linear: main(c0) ← feat-1(c1) ← feat-2(c2)
	// feat-2.stackParent is set to "main" (wrong); graph shows feat-2 above feat-1.
	g := linearTestGraph()
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}

	children := e.directChildren("feat-1")
	if len(children) != 1 || children[0] != "feat-2" {
		t.Errorf("directChildren(feat-1) = %v, want [feat-2]", children)
	}
	// Config should have been repaired.
	got, ok := e.git.GetStackParent("feat-2")
	if !ok || got != "feat-1" {
		t.Errorf("feat-2 stackParent = %q (ok=%v), want feat-1", got, ok)
	}
}

// TestDirectChildren_Step2OnlyDemotesTrulyCoLocated ensures a branch is not
// demoted when its stackParent config names an unrelated branch that happens
// to be in the above-set but is not co-located.
func TestDirectChildren_Step2OnlyDemotesTrulyCoLocated(t *testing.T) {
	// main(c0) ← feat-1(c1)
	//                  feat-2(c1)      (co-located with feat-1)
	//                              ← feat-3(c2)  (unrelated, above main)
	// feat-2.stackParent = feat-3  (bogus config — feat-3 is NOT co-located with
	// feat-2)
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c1",
			"feat-3": "c2",
		},
	)
	e := newTestEngine(t, g, "main")
	if err := e.git.SetStackParent("feat-2", "feat-3"); err != nil {
		t.Fatal(err)
	}
	// feat-2 should NOT be demoted from main's above-set: feat-3 is not
	// a co-located sibling of feat-2.
	children := e.directChildren("main")
	hasFeat2 := false
	for _, c := range children {
		if c == "feat-2" {
			hasFeat2 = true
		}
	}
	if !hasFeat2 {
		t.Errorf("feat-2 must remain a child of main; got %v", children)
	}
}

// TestTraceChainTo_CoLocatedSiblingsByDefault verifies that without a config
// hint, a co-located sibling does NOT appear in the target's chain (they are
// siblings, not parent/child).
func TestTraceChainTo_CoLocatedSiblingsByDefault(t *testing.T) {
	g := git.NewGraph(
		map[string][]string{"c0": {}, "c1": {"c0"}},
		map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c1",
		},
	)
	e := newTestEngine(t, g, "main")

	chain, err := e.traceChainTo("feat-2")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range chain {
		if b.Name == "feat-1" {
			t.Errorf("feat-1 should not appear in feat-2's chain (siblings)")
		}
	}
	// The chain should be [main, feat-2].
	if len(chain) != 2 || chain[0].Name != "main" || chain[1].Name != "feat-2" {
		names := make([]string, len(chain))
		for i, b := range chain {
			names[i] = b.Name
		}
		t.Errorf("got %v, want [main feat-2]", names)
	}
}
