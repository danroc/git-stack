package ui

import "strings"

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	yellow = "\033[33m"
)

// palette holds ANSI escape sequences for each semantic element.
// When color is disabled, all fields are empty strings.
type palette struct {
	branch    string // current branch name
	ahead     string // "+N" count
	connector string // tree-drawing chars
	reset     string
}

func colorPalette() palette {
	return palette{
		branch:    bold + green,
		ahead:     yellow,
		connector: dim,
		reset:     reset,
	}
}

func plainPalette() palette {
	return palette{}
}

// formatConnector wraps s with the connector color.
func (p palette) formatConnector(s string) string {
	return p.connector + s + p.reset
}

// formatPrefix wraps "│" characters in the accumulated prefix with connector styling.
func (p palette) formatPrefix(prefix string) string {
	if p.connector == "" {
		return prefix
	}
	return strings.ReplaceAll(prefix, treeBar, p.formatConnector(treeBar))
}
