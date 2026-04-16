// Package ui handles terminal rendering and interactive prompts.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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
	err error
}

func (ew *writer) printf(format string, args ...any) {
	if ew.err == nil {
		_, ew.err = fmt.Fprintf(ew.w, format, args...)
	}
}

// RenderTree prints the full stack tree to w. Example output:
//
//	main
//	│
//	├─ feat-auth          +3
//	│  │
//	│  ├─ feat-auth-ui    +1   *current
//	│  │
//	│  └─ feat-auth-mobile  +2
//	│
//	└─ feat-payment       +2
//	   │
//	   └─ feat-payment-ui  +1
func RenderTree(root *TreeEntry, w io.Writer) {
	ew := &writer{w: w}
	ew.printf("%s\n", formatEntry(root))
	renderChildren(root.Children, "", ew)
}

func renderChildren(children []*TreeEntry, prefix string, w *writer) {
	for i, child := range children {
		isLast := i == len(children)-1
		connector := "├─ "
		childPrefix := "│  "
		if isLast {
			connector = "└─ "
			childPrefix = "   "
		}
		w.printf("%s│\n", prefix)
		w.printf("%s%s%s\n", prefix, connector, formatEntry(child))
		if len(child.Children) > 0 {
			renderChildren(child.Children, prefix+childPrefix, w)
		}
	}
}

func formatEntry(e *TreeEntry) string {
	s := e.BranchName
	if e.AheadCount > 0 {
		s += fmt.Sprintf("  +%d", e.AheadCount)
	}
	if e.IsCurrent {
		s += "  *current"
	}
	return s
}

// Disambiguate prompts the user to choose from multiple candidate branches
// when a bifurcation is detected. It satisfies engine.DisambiguateFn.
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
