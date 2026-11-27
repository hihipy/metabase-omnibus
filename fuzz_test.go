package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzSafeName checks the promises a file name has to keep on every platform
// the tool runs on: no control characters, none of the characters Windows
// refuses, no leading or trailing space or dot, no Windows device name, at
// most maxName bytes, never empty, and stable when applied twice.
func FuzzSafeName(f *testing.F) {
	for _, s := range []string{"", " ", ".", "..", "Orders by state", "a/b\\c", "Q1: <draft>?", " .hidden. ", "tab\there", "\x00", "über", "\xff\xfe", "CON", "lpt1.json", "nul .md", "CON." + strings.Repeat("a", 196)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := safeName(s)
		if got == "" {
			t.Fatalf("safeName(%q) is empty", s)
		}
		for _, r := range got {
			if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
				t.Fatalf("safeName(%q) = %q keeps %q", s, got, r)
			}
		}
		if got != "_" && (strings.Trim(got, " .") != got) {
			t.Fatalf("safeName(%q) = %q starts or ends with a space or dot", s, got)
		}
		if len(got) > maxName {
			t.Fatalf("safeName(%q) is %d bytes", s, len(got))
		}
		if reservedName(got) {
			t.Fatalf("safeName(%q) = %q is a Windows device name", s, got)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("safeName(%q) = %q is not valid UTF-8", s, got)
		}
		if again := safeName(got); again != got {
			t.Fatalf("safeName is not stable: %q, then %q", got, again)
		}
	})
}

// FuzzCSVSafe checks that no cell in catalog.csv can start with a character a
// spreadsheet reads as a formula, and that a safe cell passes through as is.
func FuzzCSVSafe(f *testing.F) {
	for _, s := range []string{"", "=SUM(A1)", "+1", "-1", "@cmd", "\tx", "\rx", "Orders", "'quoted"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := csvSafe(s)
		if got != s && got != "'"+s {
			t.Fatalf("csvSafe(%q) = %q changes more than a leading quote", s, got)
		}
		if got != "" && strings.ContainsRune("=+-@\t\r", rune(got[0])) {
			t.Fatalf("csvSafe(%q) = %q starts with a formula character", s, got)
		}
	})
}
