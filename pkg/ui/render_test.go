package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatEntry(t *testing.T) {
	p := plainPalette()
	tests := []struct {
		name  string
		entry *TreeEntry
		want  string
	}{
		{"plain", &TreeEntry{BranchName: "main"}, "main"},
		{"ahead count", &TreeEntry{BranchName: "feat-1", AheadCount: 3}, "feat-1 (+3)"},
		{
			"ahead and behind",
			&TreeEntry{BranchName: "feat-1", AheadCount: 3, BehindCount: 2},
			"feat-1 (+3 -2)",
		},
		{
			"behind only",
			&TreeEntry{BranchName: "feat-1", BehindCount: 2},
			"feat-1 (-2)",
		},
		{
			"drift marker",
			&TreeEntry{BranchName: "feat-1", Drifted: true},
			"feat-1 [drift]",
		},
		{
			"counts and drift",
			&TreeEntry{
				BranchName:  "feat-1",
				AheadCount:  3,
				BehindCount: 2,
				Drifted:     true,
			},
			"feat-1 (+3 -2) [drift]",
		},
		{"current branch", &TreeEntry{BranchName: "feat-1", IsCurrent: true}, "feat-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.formatEntry(tt.entry); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderTree_LinearStack(t *testing.T) {
	feat1 := &TreeEntry{BranchName: "feat-1", AheadCount: 2}
	feat2 := &TreeEntry{BranchName: "feat-2", AheadCount: 1}
	feat1.Children = []*TreeEntry{feat2}
	root := &TreeEntry{BranchName: "main", Children: []*TreeEntry{feat1}}

	var buf bytes.Buffer
	RenderTree(root, &buf)
	got := buf.String()

	for _, want := range []string{"main", "feat-1", "+2", "feat-2", "+1"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderTree_DriftedBranch(t *testing.T) {
	root := &TreeEntry{
		BranchName: "main",
		Children: []*TreeEntry{
			{BranchName: "feat-1", Drifted: true},
		},
	}

	var buf bytes.Buffer
	RenderTree(root, &buf)
	if got := buf.String(); !strings.Contains(got, "[drift]") {
		t.Fatalf("output missing drift marker:\n%s", got)
	}
}

func TestRenderTree_MultipleChildren(t *testing.T) {
	root := &TreeEntry{
		BranchName: "main",
		Children: []*TreeEntry{
			{BranchName: "feat-1a"},
			{BranchName: "feat-1b"},
		},
	}

	var buf bytes.Buffer
	RenderTree(root, &buf)
	got := buf.String()

	if !strings.Contains(got, treeTee) {
		t.Errorf("non-last child missing tee connector %q:\n%s", treeTee, got)
	}
	if !strings.Contains(got, treeElbow) {
		t.Errorf("last child missing elbow connector %q:\n%s", treeElbow, got)
	}
}
