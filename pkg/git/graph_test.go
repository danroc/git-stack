package git

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// linearGraph: main(c0) ← feat-1(c1) ← feat-2(c2)
// c0 is present as a root (the floor) with no parents, matching the
// post-Task-3 graph shape where buildGraph loads commits down to the
// octopus merge-base inclusive.
func linearGraph() *Graph {
	return &Graph{
		parents: map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
		},
		heads: map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c2",
		},
		branchesAt: map[string][]string{
			"c0": {"main"},
			"c1": {"feat-1"},
			"c2": {"feat-2"},
		},
	}
}

func TestGraph_Contains(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		hash string
		want bool
	}{
		{"c1", true},
		{"c2", true},
		{"c0", true},   // floor commit is in parents under the new graph
		{"c99", false}, // missing
	}
	for _, tt := range tests {
		if got := g.HasHash(tt.hash); got != tt.want {
			t.Errorf("Contains(%q) = %v, want %v", tt.hash, got, tt.want)
		}
	}
}

func TestGraph_HeadOf(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		branch string
		want   string
		ok     bool
	}{
		{"feat-1", "c1", true},
		{"feat-2", "c2", true},
		{"missing", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got, ok := g.HeadOf(tt.branch)
			if ok != tt.ok || got != tt.want {
				t.Errorf("got %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestGraph_BranchAt(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		hash string
		want []string
	}{
		{"c1", []string{"feat-1"}},
		{"c2", []string{"feat-2"}},
		{"c99", nil},
	}
	for _, tt := range tests {
		t.Run(tt.hash, func(t *testing.T) {
			got := g.BranchesAt(tt.hash)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, b := range tt.want {
				if got[i] != b {
					t.Errorf("[%d] = %q, want %q", i, got[i], b)
				}
			}
		})
	}
}

func TestGraph_Branches(t *testing.T) {
	g := linearGraph()
	got := g.Branches()
	want := []string{"feat-1", "feat-2", "main"}
	if len(got) != len(want) {
		t.Fatalf("Branches() = %v, want %v", got, want)
	}
	for i, b := range want {
		if got[i] != b {
			t.Errorf("[%d] = %q, want %q", i, got[i], b)
		}
	}
}

func TestGraph_FirstParent(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		hash string
		want string
		ok   bool
	}{
		{"c2", "c1", true},
		{"c1", "c0", true},
		{"c0", "", false}, // root commit — in graph but has no parents
	}
	for _, tt := range tests {
		t.Run(tt.hash, func(t *testing.T) {
			got, ok := g.FirstParent(tt.hash)
			if ok != tt.ok || got != tt.want {
				t.Errorf("got %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestGraph_IsAncestor(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		name string
		anc  string
		desc string
		want bool
	}{
		{"forward", "c1", "c2", true},
		{"self", "c1", "c1", true},
		{"reverse", "c2", "c1", false},
		{"floor reaches descendant", "c0", "c2", true},
		{"missing", "c99", "c2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := g.IsAncestor(tt.anc, tt.desc); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGraph_IsAncestor_UsesAllParents(t *testing.T) {
	g := NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"m1": {"c2", "c0"},
		},
		map[string]string{
			"main": "m1",
			"base": "c0",
		},
	)

	if !g.IsAncestor("c0", "m1") {
		t.Fatal("expected c0 to be an ancestor of merge commit m1 via second parent")
	}
}

func TestGraph_Traverse_ReportsBFSDepth(t *testing.T) {
	g := NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c1"},
			"m1": {"c2", "c3"},
		},
		map[string]string{"main": "m1"},
	)

	got := map[string]int{}
	g.Traverse("m1", func(hash string, depth int) bool {
		got[hash] = depth
		return true
	})

	want := map[string]int{
		"m1": 0,
		"c2": 1,
		"c3": 1,
		"c1": 2,
		"c0": 3,
	}
	if len(got) != len(want) {
		t.Fatalf("Traverse visited %v, want %v", got, want)
	}
	for hash, depth := range want {
		if got[hash] != depth {
			t.Fatalf("depth[%s] = %d, want %d", hash, got[hash], depth)
		}
	}
}

func TestGraph_CommitsBetween(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		name   string
		branch string
		parent string
		want   CommitsBetweenResult
	}{
		{
			"one ahead, zero behind",
			"c1",
			"c0",
			CommitsBetweenResult{Ahead: 1, Behind: 0},
		},
		{
			"one behind, zero ahead",
			"c0",
			"c1",
			CommitsBetweenResult{Ahead: 0, Behind: 1},
		},
		{
			"two ahead, zero behind",
			"c2",
			"c0",
			CommitsBetweenResult{Ahead: 2, Behind: 0},
		},
		{"same commit", "c2", "c2", CommitsBetweenResult{Ahead: 0, Behind: 0}},
		{
			"c1 vs c2 (c1 ancestor of c2)",
			"c1",
			"c2",
			CommitsBetweenResult{Ahead: 0, Behind: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.CommitsBetween(tt.branch, tt.parent)
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGraph_CommitsBetween_Divergent(t *testing.T) {
	g := NewGraph(
		map[string][]string{
			"c0": {},
			"c1": {"c0"},
			"c2": {"c1"},
			"c3": {"c0"},
			"c4": {"c3"},
		},
		map[string]string{
			"main":     "c0",
			"main-dev": "c2",
			"feat":     "c4",
		},
	)
	tests := []struct {
		name string
		a    string
		b    string
		want CommitsBetweenResult
	}{
		{"same branch", "c0", "c0", CommitsBetweenResult{Ahead: 0, Behind: 0}},
		{
			"main-dev vs main: 2 ahead, 0 behind",
			"c2",
			"c0",
			CommitsBetweenResult{Ahead: 2, Behind: 0},
		},
		{
			"feat vs main: 2 ahead, 0 behind",
			"c4",
			"c0",
			CommitsBetweenResult{Ahead: 2, Behind: 0},
		},
		{
			"main-dev vs feat: 2 ahead, 2 behind",
			"c2",
			"c4",
			CommitsBetweenResult{Ahead: 2, Behind: 2},
		},
		{
			"feat vs main-dev: 2 ahead, 2 behind",
			"c4",
			"c2",
			CommitsBetweenResult{Ahead: 2, Behind: 2},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.CommitsBetween(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGraph_MergeBase_UsesAllParents(t *testing.T) {
	g := NewGraph(
		map[string][]string{
			"c0":    {},
			"c1":    {"c0"},
			"c2":    {"c1"},
			"side":  {"c0"},
			"merge": {"c2", "side"},
		},
		map[string]string{
			"main": "merge",
			"side": "side",
		},
	)

	got, ok := g.MergeBase("merge", "side")
	if !ok {
		t.Fatal("MergeBase should find a common ancestor through the second parent")
	}
	if got != "side" {
		t.Fatalf("MergeBase(merge, side) = %q, want side", got)
	}
}

func TestGraph_BranchAt_MultipleBranches(t *testing.T) {
	g := NewGraph(
		map[string][]string{"c1": {"c0"}},
		map[string]string{
			"main":   "c0",
			"feat-a": "c1",
			"feat-b": "c1", // same HEAD as feat-a
		},
	)
	branches := g.BranchesAt("c1")
	want := []string{"feat-a", "feat-b"}
	if !slices.Equal(branches, want) {
		t.Errorf("got %v, want %v (sorted alphabetically)", branches, want)
	}
}

// initGraphRepo initializes a temp repo identical to initRepo but scoped to
// graph tests. Duplicated to keep git package test helpers non-exported.
func initGraphRepo(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		full := append([]string{"-C", dir}, args...)
		cmd := exec.Command("git", full...) //nolint:gosec
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("commit", "--allow-empty", "-m", "c0")
	return NewClient(dir), dir
}

func rev(t *testing.T, dir, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", ref) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s: %v\n%s", ref, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestLoadGraph_IncludesBaseDownToFloor verifies the loaded graph contains
// base-branch commits down to the octopus merge-base (inclusive).
func TestLoadGraph_IncludesBaseDownToFloor(t *testing.T) {
	c, dir := initGraphRepo(t)
	gi := func(args ...string) {
		full := append([]string{"-C", dir}, args...)
		cmd := exec.Command("git", full...) //nolint:gosec
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Layout: c0 (main@fork) ─ m1 ─ m2 (main@tip)
	//                       └ f1 (feat-1@tip)
	fork := rev(t, dir, "HEAD") // c0
	gi("checkout", "-q", "-b", "feat-1")
	gi("commit", "--allow-empty", "-m", "f1")
	featTip := rev(t, dir, "HEAD")
	gi("checkout", "-q", "main")
	gi("commit", "--allow-empty", "-m", "m1")
	gi("commit", "--allow-empty", "-m", "m2")
	mainTip := rev(t, dir, "HEAD")

	graph, err := c.LoadGraph()
	if err != nil {
		t.Fatal(err)
	}

	// Floor commit (c0) must now be contained.
	if !graph.HasHash(fork) {
		t.Errorf("graph must contain floor commit %s (fork point)", fork)
	}
	// Base-above-floor commits (m1, m2) must be contained.
	if !graph.HasHash(mainTip) {
		t.Errorf("graph must contain main tip %s", mainTip)
	}
	// Feature tip must be contained.
	if !graph.HasHash(featTip) {
		t.Errorf("graph must contain feat-1 tip %s", featTip)
	}
}
