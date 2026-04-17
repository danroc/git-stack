// Package ui handles terminal rendering and interactive prompts.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// TreeEntry is the display-layer input for RenderTree.
type TreeEntry struct {
	BranchName string
	AheadCount int // relative to immediate parent; 0 for the root/base node
	IsCurrent  bool
	Children   []*TreeEntry
}

// writer wraps an io.Writer and absorbs write errors after the first failure.
type writer struct {
	w   io.Writer
	p   palette
	err error
}

func (ew *writer) printf(format string, args ...any) {
	if ew.err == nil {
		_, ew.err = fmt.Fprintf(ew.w, format, args...)
	}
}

// RenderTree prints the full stack tree to w.
func RenderTree(root *TreeEntry, w io.Writer) {
	palette := plainPalette()
	if f, ok := w.(*os.File); ok {
		if term.IsTerminal(int(f.Fd())) { //nolint:gosec
			palette = colorPalette()
		}
	}

	ew := &writer{w: w, p: palette}
	ew.printf("%s\n", palette.formatEntry(root))
	renderChildren(root.Children, "", ew)
}

const (
	treeBar       = "│"
	treeTee       = "├─ "
	treeBarIndent = "│  "
	treeElbow     = "└─ "
	treeIndent    = "   "
)

func renderChildren(children []*TreeEntry, prefix string, w *writer) {
	for i, child := range children {
		isLast := i == len(children)-1
		connector := treeTee
		childPrefix := treeBarIndent
		if isLast {
			connector = treeElbow
			childPrefix = treeIndent
		}

		w.printf("%s%s\n", w.p.formatPrefix(prefix), w.p.formatConnector(treeBar))
		w.printf(
			"%s%s%s\n",
			w.p.formatPrefix(prefix),
			w.p.formatConnector(connector),
			w.p.formatEntry(child),
		)

		if len(child.Children) > 0 {
			renderChildren(child.Children, prefix+childPrefix, w)
		}
	}
}

func (p palette) formatEntry(e *TreeEntry) string {
	s := e.BranchName
	if e.IsCurrent {
		s = p.branch + s + p.reset
	}
	if e.AheadCount > 0 {
		s += fmt.Sprintf(" (%s+%d%s)", p.ahead, e.AheadCount, p.reset)
	}
	return s
}

// Disambiguate prompts the user to choose from multiple candidate branches
// when a bifurcation is detected. It satisfies discovery.ChooseBranchFn.
func Disambiguate(action string, choices []string) (string, error) {
	fmt.Printf("Multiple branches detected. Which branch do you want to %s?\n", action)
	for i, c := range choices {
		fmt.Printf("  (%d) %s\n", i+1, c)
	}
	fmt.Print("Enter choice: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("reading input: %w", err)
	}
	line = strings.TrimSpace(line)

	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(choices) {
		return "", fmt.Errorf("invalid choice %q", line)
	}

	return choices[n-1], nil
}
