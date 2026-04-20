package git

import (
	"slices"
	"testing"
)

// linearGraph: main(c0) ← feat-1(c1) ← feat-2(c2)
// c0 is NOT in parents, simulating the base-branch boundary.
func linearGraph() *Graph {
	return &Graph{
		parents: map[string][]string{
			"c1": {"c0"},
			"c2": {"c1"},
		},
		heads: map[string]string{
			"main":   "c0",
			"feat-1": "c1",
			"feat-2": "c2",
		},
		branchAt: map[string][]string{
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
		{"c0", false},  // base boundary — not in parents map
		{"c99", false}, // missing
	}
	for _, tt := range tests {
		if got := g.Contains(tt.hash); got != tt.want {
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
		ok   bool
	}{
		{"c1", []string{"feat-1"}, true},
		{"c2", []string{"feat-2"}, true},
		{"c99", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.hash, func(t *testing.T) {
			got, ok := g.BranchAt(tt.hash)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
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
		{"c0", "", false}, // not in graph
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
		{"below base boundary", "c0", "c2", false},
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

func TestGraph_CommitsAhead(t *testing.T) {
	g := linearGraph()
	tests := []struct {
		name   string
		parent string
		branch string
		want   int
	}{
		{"one above main", "c0", "c1", 1},
		{"one above feat-1", "c1", "c2", 1},
		{"two above main", "c0", "c2", 2},
		{"same commit", "c2", "c2", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := g.CommitsAhead(tt.parent, tt.branch); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
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
	branches, ok := g.BranchAt("c1")
	if !ok {
		t.Fatal("expected c1 to have branches")
	}
	want := []string{"feat-a", "feat-b"}
	if !slices.Equal(branches, want) {
		t.Errorf("got %v, want %v (sorted alphabetically)", branches, want)
	}
}
