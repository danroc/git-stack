package discovery

import (
	"os/exec"
	"slices"
	"testing"

	"git-stack/pkg/git"
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
	g := git.NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		map[string]string{
			"main":   "c0",
			"feat-1": "c2",
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

	got := []string{chain[0].Name, chain[1].Name, chain[2].Name}
	want := []string{"main", "feat-1", "feat-2"}
	if !slices.Equal(got, want) {
		t.Fatalf("traceChainTo(feat-2) = %v, want %v", got, want)
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

func TestIsBranchDescendant_FollowsResolvedParents(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if !e.IsBranchDescendant("main", "feat-2") {
		t.Fatal("feat-2 should be below main")
	}

	if err := e.git.SetStackParent("feat-2", "main"); err != nil {
		t.Fatal(err)
	}
	if e.IsBranchDescendant("feat-1", "feat-2") {
		t.Fatal("feat-2 should no longer be below feat-1 once config points to main")
	}
}

func TestBuildTree_MarksDriftWithoutCorrectingParent(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := e.git.SetStackParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.git.SetStackParentMergeBase("feat-2", "c0"); err != nil {
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
	e := newTestEngine(t, linearTestGraph(), "main")
	if err := e.SetParent("feat-2", "feat-1"); err != nil {
		t.Fatal(err)
	}

	parent, ok := e.git.GetStackParent("feat-2")
	if !ok || parent != "feat-1" {
		t.Fatalf("stack parent = %q (ok=%v), want feat-1", parent, ok)
	}
	mergeBase, ok := e.git.GetStackParentMergeBase("feat-2")
	if !ok || mergeBase != "c1" {
		t.Fatalf("stack merge-base = %q (ok=%v), want c1", mergeBase, ok)
	}
}

func TestSubtreeMembers_Linear(t *testing.T) {
	e := newTestEngine(t, linearTestGraph(), "main")
	members := e.SubtreeMembers("feat-1")
	if len(members) != 1 {
		t.Fatalf("got %d members, want 1", len(members))
	}
	if members[0].Branch.Name != "feat-2" || members[0].Parent != "feat-1" {
		t.Fatalf(
			"got {%q %q}, want {feat-2 feat-1}",
			members[0].Branch.Name,
			members[0].Parent,
		)
	}
}
