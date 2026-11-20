package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Item is one row in the picker.
type Item struct {
	Name   string // matched by typed names and globs
	Detail string // shown after the name, such as an engine
	Count  string // right-aligned, such as "12 tables"
}

// Pick asks which items to keep and returns their indexes in list order, or
// nil if the user cancelled. noun names the items in prompts ("databases").
//
// It prefers the arrow-key picker and falls back to typing when the terminal
// cannot be driven, or always types when plain is set. The typed prompt takes
// an empty line for everything, or numbers, ranges, names, and globs. A minus
// on every entry drops instead of takes.
func Pick(items []Item, noun string, plain bool, out io.Writer) ([]int, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if !plain {
		if picked, ok, err := pickArrows(items, noun, out); ok {
			if err != nil {
				return nil, err
			}
			if picked != nil {
				fmt.Fprintf(out, "selected %d of %d %s\n", len(picked), len(items), noun)
			}
			return picked, nil
		}
	}
	return pickTyped(items, noun, os.Stdin, out)
}

func pickTyped(items []Item, noun string, in io.Reader, out io.Writer) ([]int, error) {
	_, width := termSize()
	st := newPickState(items, len(items), width)
	numWidth := len(strconv.Itoa(len(items)))

	fmt.Fprintln(out)
	for i, line := range st.view(noun)[:len(items)] {
		// Swap the checkbox for the number the prompt asks for.
		fmt.Fprintf(out, "  %*d %s\n", numWidth, i+1, strings.TrimPrefix(line, "  [x] "))
	}
	fmt.Fprintln(out, "\nEnter for all. Take with numbers, names, or globs (1,4,7-9 or sales*).")
	fmt.Fprintln(out, "Put a minus on every entry to drop instead (-12 or -*_test). Type q to cancel.")

	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "> ")
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return nil, fmt.Errorf("reading selection: %w", err)
		}
		if strings.EqualFold(strings.TrimSpace(line), "q") {
			return nil, nil
		}
		chosen, perr := ParseSelection(line, names)
		if perr != nil {
			fmt.Fprintln(out, Bad(perr.Error()))
			continue
		}
		if len(chosen) == 0 {
			fmt.Fprintln(out, Notice("that leaves nothing to export"))
			continue
		}
		idx := make([]int, len(chosen))
		for i, c := range chosen {
			idx[i] = c - 1
		}
		fmt.Fprintf(out, "selected %d of %d %s\n", len(idx), len(items), noun)
		return idx, nil
	}
}

// ParseSelection turns a line of input into a sorted list of 1-based indexes
// into names. An empty line means everything. Taking and dropping cannot be
// mixed, because "1,-3" reads as either "1, not 3" or "1 through 3".
func ParseSelection(line string, names []string) ([]int, error) {
	n := len(names)
	line = strings.TrimSpace(line)
	fields := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	if len(fields) == 0 {
		return seq(n), nil
	}

	dropping := strings.HasPrefix(fields[0], "-")
	picked := map[int]bool{}
	for _, f := range fields {
		if strings.HasPrefix(f, "-") != dropping {
			return nil, fmt.Errorf("mixing take and drop is ambiguous: %q", line)
		}
		f = strings.TrimPrefix(f, "-")

		if lo, hi, err := parseRange(f, n); err == nil {
			for i := lo; i <= hi; i++ {
				picked[i] = true
			}
			continue
		} else if isNumeric(f) {
			return nil, err
		}

		matched := false
		for i, name := range names {
			if MatchName(f, name) {
				picked[i+1] = true
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("nothing matches %q", f)
		}
	}

	var out []int
	for i := 1; i <= n; i++ {
		if picked[i] != dropping {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out, nil
}

// MatchName reports whether pattern names s, by exact name or glob, ignoring case.
func MatchName(pattern, s string) bool {
	if strings.EqualFold(pattern, s) {
		return true
	}
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(s))
	return err == nil && ok
}

func parseRange(f string, n int) (int, int, error) {
	lo, hi := f, f
	if i := strings.Index(f, "-"); i > 0 {
		lo, hi = f[:i], f[i+1:]
	}
	a, err := strconv.Atoi(strings.TrimSpace(lo))
	if err != nil {
		return 0, 0, fmt.Errorf("not a number: %q", f)
	}
	b, err := strconv.Atoi(strings.TrimSpace(hi))
	if err != nil {
		return 0, 0, fmt.Errorf("not a number: %q", f)
	}
	if a > b {
		a, b = b, a
	}
	if a < 1 || b > n {
		return 0, 0, fmt.Errorf("out of range 1 to %d: %q", n, f)
	}
	return a, b, nil
}

// isNumeric separates a mistyped index from a name.
func isNumeric(f string) bool {
	for _, r := range f {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return f != ""
}

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

// Interactive reports whether stdin is a terminal, so a run in a pipeline
// takes everything instead of hanging on a prompt nobody will answer.
func Interactive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// Progress draws a one-line bar on stderr, so it never mixes into the export
// when the export goes to stdout.
func Progress(done, total int, noun, label string) {
	const width = 24
	filled := 0
	if total > 0 {
		filled = done * width / total
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
	fmt.Fprintf(os.Stderr, "\r[%s] %d/%d %s  %-40s", bar, done, total, noun, truncate(label, 40))
	if done == total {
		fmt.Fprintln(os.Stderr)
	}
}
