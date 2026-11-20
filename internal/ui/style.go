// Package ui holds the terminal pieces: colour, the picker, and the progress bar.
package ui

import (
	"os"
	"strings"
)

// Colour marks the lines a reader skims past: a choice that was skipped, a
// file that was written. It stays off unless stderr, where prompts go, is a terminal that can
// show it, and it honours NO_COLOR.

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

var styled = colourSupported()

func colourSupported() bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	return ansiOK() // per platform, in term_unix.go and term_windows.go
}

func paint(codes, s string) string {
	if !styled || s == "" {
		return s
	}
	// Reset before any newline so a highlighted line does not colour the
	// rest of the screen when it wraps.
	return codes + strings.ReplaceAll(s, "\n", ansiReset+"\n"+codes) + ansiReset
}

// Bold marks a heading.
func Bold(s string) string { return paint(ansiBold, s) }

// Notice marks something the reader needs to see and might skim past.
func Notice(s string) string { return paint(ansiBold+ansiYellow, s) }

// Good marks a finished piece of work, such as a file written.
func Good(s string) string { return paint(ansiBold+ansiGreen, s) }

// Bad marks a failure that did not stop the run.
func Bad(s string) string { return paint(ansiRed, s) }

// Quiet marks detail worth printing but not worth reading twice.
func Quiet(s string) string { return paint(ansiDim, s) }

// SetStyled turns styling on or off, for a test that needs a known answer.
func SetStyled(on bool) { styled = on }
