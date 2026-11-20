package ui

import (
	"fmt"
	"io"
	"strings"
)

// The arrow-key picker: raw mode, read escape sequences, redraw in place.
// Raw mode is per platform (term_unix.go, term_windows.go). When it cannot be
// entered, Pick falls back to the typed prompt in pick.go.

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyToggle
	keyAll
	keyClear
	keyAccept
	keyCancel
	keyTop
	keyBottom
)

// decodeKey reads one keypress from the head of buf. It returns the key and
// how many bytes it used, or 0 when buf holds an incomplete escape sequence.
func decodeKey(buf []byte) (key, int) {
	if len(buf) == 0 {
		return keyNone, 0
	}
	switch buf[0] {
	case '\r', '\n':
		return keyAccept, 1
	case ' ':
		return keyToggle, 1
	case 'a', 'A':
		return keyAll, 1
	case 'n', 'N':
		return keyClear, 1
	case 'k':
		return keyUp, 1
	case 'j':
		return keyDown, 1
	case 'g':
		return keyTop, 1
	case 'G':
		return keyBottom, 1
	case 'q', 3: // q or ctrl-c
		return keyCancel, 1
	case 0x1b:
		if len(buf) == 1 {
			return keyCancel, 1 // a lone escape
		}
		if len(buf) < 3 {
			return keyNone, 0 // wait for the rest of the sequence
		}
		if buf[1] == '[' {
			switch buf[2] {
			case 'A':
				return keyUp, 3
			case 'B':
				return keyDown, 3
			case 'H':
				return keyTop, 3
			case 'F':
				return keyBottom, 3
			}
		}
		return keyNone, 3 // another escape sequence, ignored
	}
	return keyNone, 1
}

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

func (s *pickState) apply(k key) (done, accepted bool) {
	switch k {
	case keyUp:
		if s.cursor > 0 {
			s.cursor--
		}
	case keyDown:
		if s.cursor < len(s.items)-1 {
			s.cursor++
		}
	case keyTop:
		s.cursor = 0
	case keyBottom:
		s.cursor = len(s.items) - 1
	case keyToggle:
		s.selected[s.cursor] = !s.selected[s.cursor]
	case keyAll:
		for i := range s.selected {
			s.selected[i] = true
		}
	case keyClear:
		for i := range s.selected {
			s.selected[i] = false
		}
	case keyAccept:
		return true, true
	case keyCancel:
		return true, false
	}
	if s.cursor < s.top {
		s.top = s.cursor
	}
	if s.cursor >= s.top+s.height {
		s.top = s.cursor - s.height + 1
	}
	return false, false
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
	nameW, countW := 0, 0
	for _, it := range s.items {
		nameW = max(nameW, len([]rune(it.Name)))
		countW = max(countW, len(it.Count))
	}
	if limit := s.width/2 - 6; nameW > limit && limit > 12 {
		nameW = limit
	}
	detailW := s.width - 6 - nameW - 2 - countW - 2
	if detailW < 6 {
		detailW = 0
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
		line := fmt.Sprintf("%s[%s] %-*s", point, mark, nameW, truncate(it.Name, nameW))
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

// pickArrows runs the arrow-key picker. ok is false when the terminal cannot
// be driven, so the caller can fall back to typing. A nil result with ok true
// means the user cancelled.
func pickArrows(items []Item, noun string, out io.Writer) (picked []int, ok bool, err error) {
	t, err := openRaw()
	if err != nil {
		fmt.Fprintln(out, Quiet("arrow picker unavailable ("+err.Error()+"), falling back to typing"))
		return nil, false, nil
	}
	restore := func() {
		t.Restore()
		fmt.Fprint(out, "\x1b[?25h") // show the cursor again
	}
	defer restore()
	fmt.Fprint(out, "\x1b[?25l") // hide the cursor while redrawing

	rows, cols := t.Size()
	window := min(rows-6, len(items))
	s := newPickState(items, window, cols)

	drawn := 0
	draw := func() {
		if drawn > 0 {
			fmt.Fprintf(out, "\x1b[%dA", drawn)
		}
		lines := s.view(noun)
		for _, l := range lines {
			fmt.Fprint(out, "\r\x1b[K", l, "\r\n")
		}
		drawn = len(lines)
	}
	draw()

	var buf []byte
	chunk := make([]byte, 64)
	for {
		n, readErr := t.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if readErr != nil && n == 0 {
			return nil, true, fmt.Errorf("reading keys: %w", readErr)
		}
		for len(buf) > 0 {
			k, used := decodeKey(buf)
			if used == 0 {
				break // incomplete escape sequence, wait for more
			}
			buf = buf[used:]
			if done, accepted := s.apply(k); done {
				if !accepted {
					return nil, true, nil
				}
				return s.chosen(), true, nil
			}
		}
		draw()
	}
}
