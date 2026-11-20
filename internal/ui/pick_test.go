package ui

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSelection(t *testing.T) {
	names := []string{"PUBLIC", "ANALYTICS", "s_20200001prod", "s_20200001test", "s_20210001prod"}
	cases := []struct {
		in      string
		want    []int
		wantErr string
	}{
		{"", []int{1, 2, 3, 4, 5}, ""},
		{"  ", []int{1, 2, 3, 4, 5}, ""},
		{"2", []int{2}, ""},
		{"1,3", []int{1, 3}, ""},
		{"4-2", []int{2, 3, 4}, ""}, // a reversed range still reads as a range
		{"public", []int{1}, ""},    // names ignore case
		{"s_20200001*", []int{3, 4}, ""},
		{"*prod", []int{3, 5}, ""},
		{"-*test", []int{1, 2, 3, 5}, ""},
		{"-1 -2", []int{3, 4, 5}, ""},
		{"1,-2", nil, "ambiguous"},
		{"9", nil, "out of range"},
		{"nope", nil, "nothing matches"},
	}
	for _, c := range cases {
		got, err := ParseSelection(c.in, names)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("ParseSelection(%q) error = %v, want one containing %q", c.in, err, c.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseSelection(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
}

func TestDecodeKey(t *testing.T) {
	cases := []struct {
		in       string
		want     key
		consumed int
	}{
		{"\x1b[A", keyUp, 3},
		{"\x1b[B", keyDown, 3},
		{"\x1b[H", keyTop, 3},
		{"\x1b[F", keyBottom, 3},
		{"k", keyUp, 1},
		{"j", keyDown, 1},
		{" ", keyToggle, 1},
		{"a", keyAll, 1},
		{"n", keyClear, 1},
		{"\r", keyAccept, 1},
		{"\n", keyAccept, 1},
		{"q", keyCancel, 1},
		{"\x03", keyCancel, 1}, // ctrl-c
		{"\x1b", keyCancel, 1}, // a lone escape
		{"\x1b[", keyNone, 0},  // incomplete, wait for more bytes
		{"", keyNone, 0},
		{"z", keyNone, 1},
	}
	for _, c := range cases {
		got, n := decodeKey([]byte(c.in))
		if got != c.want || n != c.consumed {
			t.Errorf("decodeKey(%q) = (%v, %d), want (%v, %d)", c.in, got, n, c.want, c.consumed)
		}
	}
}

func items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{Name: string(rune('a' + i)), Count: "1 table"}
	}
	return out
}

func TestPickStateStartsFullySelected(t *testing.T) {
	s := newPickState(items(4), 10, 200)
	if got := len(s.chosen()); got != 4 {
		t.Errorf("chosen = %d, want all 4 selected at the start", got)
	}
}

func TestPickStateKeys(t *testing.T) {
	s := newPickState(items(3), 10, 200)
	s.apply(keyUp) // already at the top
	if s.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", s.cursor)
	}
	s.apply(keyToggle) // drop the first
	s.apply(keyDown)
	s.apply(keyDown)
	s.apply(keyDown) // already at the bottom
	if s.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", s.cursor)
	}
	if got := s.chosen(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("chosen = %v, want [1 2]", got)
	}
	s.apply(keyClear)
	if got := s.chosen(); len(got) != 0 {
		t.Errorf("after none, chosen = %v", got)
	}
	s.apply(keyAll)
	if got := s.chosen(); len(got) != 3 {
		t.Errorf("after all, chosen = %v", got)
	}
	if done, ok := s.apply(keyAccept); !done || !ok {
		t.Error("enter should finish and accept")
	}
	if done, ok := s.apply(keyCancel); !done || ok {
		t.Error("q should finish and cancel")
	}
}

func TestPickStateScrollsAndNeverWraps(t *testing.T) {
	s := newPickState(items(20), 5, 50)
	for i := 0; i < 12; i++ {
		s.apply(keyDown)
	}
	if s.cursor < s.top || s.cursor >= s.top+s.height {
		t.Errorf("cursor %d outside window starting %d of height %d", s.cursor, s.top, s.height)
	}
	for _, line := range s.view("schemas") {
		if n := len([]rune(line)); n > 50 {
			t.Errorf("line is %d columns, wider than the 50-column window: %q", n, line)
		}
	}
}
