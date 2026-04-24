package ui

import (
	"bytes"
	"fmt"
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

func TestFormatConnector(t *testing.T) {
	t.Parallel()

	p := plainPalette()
	if got := p.formatConnector(treeBar); got != treeBar {
		t.Errorf("plainPalette.formatConnector = %q, want %q", got, treeBar)
	}

	p = colorPalette()
	got := p.formatConnector(treeBar)
	if got == treeBar {
		t.Error("colorPalette.formatConnector should wrap with ANSI codes")
	}
	if !strings.Contains(got, reset) {
		t.Error("colorPalette.formatConnector should include reset")
	}
}

func TestFormatPrefix(t *testing.T) {
	t.Parallel()

	p := plainPalette()
	prefix := treeBar + treeBarIndent
	if got := p.formatPrefix(prefix); got != prefix {
		t.Errorf("plainPalette.formatPrefix = %q, want %q", got, prefix)
	}

	p = colorPalette()
	got := p.formatPrefix(prefix)
	if got == prefix {
		t.Error("colorPalette.formatPrefix should wrap with ANSI codes")
	}
}

func TestRenderTree_NilChildren(t *testing.T) {
	root := &TreeEntry{
		BranchName: "main",
		Children:   nil,
	}

	var buf bytes.Buffer
	RenderTree(root, &buf)
	got := buf.String()
	if !strings.Contains(got, "main") {
		t.Errorf("output missing main:\n%s", got)
	}
}

func TestRenderTree_DeeplyNested(t *testing.T) {
	// Build a 4-level deep tree: main → feat-1 → feat-2 → feat-3
	feat3 := &TreeEntry{BranchName: "feat-3"}
	feat2 := &TreeEntry{BranchName: "feat-2", Children: []*TreeEntry{feat3}}
	feat1 := &TreeEntry{BranchName: "feat-1", Children: []*TreeEntry{feat2}}
	root := &TreeEntry{BranchName: "main", Children: []*TreeEntry{feat1}}

	var buf bytes.Buffer
	RenderTree(root, &buf)
	got := buf.String()

	for _, want := range []string{"main", "feat-1", "feat-2", "feat-3"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderTree_WriterError(_ *testing.T) {
	root := &TreeEntry{BranchName: "main"}

	// errWriter fails on first write.
	ew := &errWriter{fail: true}
	RenderTree(root, ew)
	// The writer should absorb the error and subsequent calls should be no-ops.
}

// errWriter is an io.Writer that fails on the first call.
type errWriter struct {
	fail bool
	n    int
}

func (w *errWriter) Write(p []byte) (int, error) {
	w.n++
	if w.fail && w.n == 1 {
		return 0, fmt.Errorf("write error")
	}
	return len(p), nil
}

func TestColorPalette(t *testing.T) {
	t.Parallel()

	p := colorPalette()
	if p.branch == "" {
		t.Error("colorPalette.branch should not be empty")
	}
	if p.ahead == "" {
		t.Error("colorPalette.ahead should not be empty")
	}
	if p.behind == "" {
		t.Error("colorPalette.behind should not be empty")
	}
	if p.connector == "" {
		t.Error("colorPalette.connector should not be empty")
	}
	if p.reset == "" {
		t.Error("colorPalette.reset should not be empty")
	}
}

func TestPlainPalette(t *testing.T) {
	t.Parallel()

	p := plainPalette()
	if p.branch != "" || p.ahead != "" || p.behind != "" || p.connector != "" ||
		p.reset != "" {
		t.Error("plainPalette should have all empty fields")
	}
}
