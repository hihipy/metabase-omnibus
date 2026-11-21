package ui

import (
	"fmt"
	"strings"
)

// pickState lays out the typed picker's list. The interactive picker is huh,
// in huh.go.

// pickState is the picker's model, kept apart from the terminal so the
// behaviour can be tested without one.
type pickState struct {
	items    []Item
	selected []bool
	cursor   int
	top      int // first visible row
	height   int // visible rows
	width    int // visible columns
}

func newPickState(items []Item, height, width int) *pickState {
	sel := make([]bool, len(items))
	for i := range sel {
		sel[i] = true // everything starts selected, so the common case is Enter
	}
	if height < 3 {
		height = len(items)
	}
	if width < 40 {
		width = 100
	}
	return &pickState{items: items, selected: sel, height: height, width: width}
}

func (s *pickState) chosen() []int {
	var out []int
	for i := range s.items {
		if s.selected[i] {
			out = append(out, i)
		}
	}
	return out
}

// view renders the frame as lines. Columns are cut rather than wrapped: a
// wrapped row would break the redraw, which moves up one line per row.
func (s *pickState) view(noun string) []string {
	nameW, countW, hasDetail := 0, 0, false
	for _, it := range s.items {
		nameW = max(nameW, len([]rune(it.Name)))
		countW = max(countW, len(it.Count))
		hasDetail = hasDetail || it.Detail != ""
	}
	// A list with no detail column gives the names the whole row.
	limit := s.width - 6 - countW - 2
	if hasDetail {
		limit = s.width/2 - 6
	}
	if nameW > limit && limit > 12 {
		nameW = limit
	}
	detailW := 0
	if hasDetail {
		if detailW = s.width - 6 - nameW - 2 - countW - 2; detailW < 6 {
			detailW = 0
		}
	}

	count := 0
	for _, on := range s.selected {
		if on {
			count++
		}
	}

	var lines []string
	end := min(s.top+s.height, len(s.items))
	for i := s.top; i < end; i++ {
		it := s.items[i]
		mark, point := " ", "  "
		if s.selected[i] {
			mark = "x"
		}
		if i == s.cursor {
			point = "> "
		}
		line := fmt.Sprintf("%s[%s] %-*s", point, mark, nameW, truncateStart(it.Name, nameW))
		if detailW > 0 {
			line += fmt.Sprintf("  %-*s", detailW, truncate(it.Detail, detailW))
		}
		if countW > 0 {
			line += fmt.Sprintf("  %*s", countW, it.Count)
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	if s.top > 0 || end < len(s.items) {
		lines = append(lines, fmt.Sprintf("      showing %d to %d of %d", s.top+1, end, len(s.items)))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("  %d of %d %s selected", count, len(s.items), noun))
	lines = append(lines, truncate(
		"  up and down to move, space to toggle, a all, n none, enter to confirm, q to cancel", s.width))
	return lines
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// truncateStart cuts from the front. Names such as collection paths share a
// long prefix and differ at the end, so the end is the part worth keeping.
func truncateStart(s string, n int) string {
	r := []rune(s)
	if n <= 0 {
		return ""
	}
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "\u2026"
	}
	return "\u2026" + string(r[len(r)-n+1:])
}
