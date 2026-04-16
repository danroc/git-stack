package ui

import "strings"

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

// palette holds ANSI escape sequences for each semantic element.
// When color is disabled, all fields are empty strings.
type palette struct {
	branch    string // current branch name
	ahead     string // "+N" count
	current   string // "*current" marker
	connector string // tree-drawing chars
	reset     string
}

func colorPalette() palette {
	return palette{
		branch:    bold + cyan,
		ahead:     yellow,
		current:   green,
		connector: dim,
		reset:     reset,
	}
}

func plainPalette() palette {
	return palette{}
}

// dimPrefix wraps any "│" characters in the accumulated prefix with dim styling.
func (p palette) dimPrefix(prefix string) string {
	if p.connector == "" {
		return prefix
	}
	return strings.ReplaceAll(prefix, "│", p.connector+"│"+p.reset)
}
