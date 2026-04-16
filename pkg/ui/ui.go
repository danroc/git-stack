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
	pal palette
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
	pal := plainPalette()
	if f, ok := w.(*os.File); ok {
		//nolint:gosec // Fd() fits in int on all supported platforms
		if term.IsTerminal(int(f.Fd())) {
			pal = colorPalette()
		}
	}
	ew := &writer{w: w, pal: pal}
	ew.printf("%s\n", formatEntry(root, pal))
	renderChildren(root.Children, "", ew)
}

func renderChildren(children []*TreeEntry, prefix string, w *writer) {
	p := w.pal
	for i, child := range children {
		isLast := i == len(children)-1
		connector := "├─ "
		childPrefix := "│  "
		if isLast {
			connector = "└─ "
			childPrefix = "   "
		}
		w.printf("%s%s│%s\n", p.dimPrefix(prefix), p.connector, p.reset)
		w.printf(
			"%s%s%s%s%s\n",
			p.dimPrefix(prefix), p.connector, connector, p.reset, formatEntry(child, p),
		)
		if len(child.Children) > 0 {
			renderChildren(child.Children, prefix+childPrefix, w)
		}
	}
}

func formatEntry(e *TreeEntry, p palette) string {
	s := e.BranchName
	if e.IsCurrent {
		s = p.branch + s + p.reset
	}
	if e.AheadCount > 0 {
		s += fmt.Sprintf(" (%s+%d%s)", p.ahead, e.AheadCount, p.reset)
	}
	if e.IsCurrent {
		s += fmt.Sprintf(" - %sCURRENT%s", p.current, p.reset)
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
