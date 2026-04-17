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
	return &Engine{git: initTestRepo(t), baseBranch: baseBranch, graph: g}
}

// linearTestGraph: main(c0) ← feat-1(c1) ← feat-2(c2)
// c0 is NOT in parents (base boundary).
func linearTestGraph() *git.Graph {
	return git.NewGraph(
		map[string][]string{
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

func TestTraceAncestors(t *testing.T) {
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
			got, err := e.traceAncestors(tt.current)
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
