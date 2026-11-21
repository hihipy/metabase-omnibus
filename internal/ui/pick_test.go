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

func TestPickStateNeverWraps(t *testing.T) {
	s := newPickState(items(20), 20, 50)
	for _, line := range s.view("schemas") {
		if n := len([]rune(line)); n > 50 {
			t.Errorf("line is %d columns, wider than the 50-column window: %q", n, line)
		}
	}
}

func TestTruncateStartKeepsTheEnd(t *testing.T) {
	got := truncateStart("Study A(PROD) - Standard Queries > Adverse Events", 20)
	if got != "\u2026es > Adverse Events" || len([]rune(got)) != 20 {
		t.Errorf("truncateStart = %q", got)
	}
	if truncateStart("short", 20) != "short" {
		t.Error("a short name should be left alone")
	}
}

// Collection paths share a long prefix. With no detail column, they get the
// whole row, and what is cut is the shared front, not the distinguishing end.
func TestLongNamesStayDistinguishable(t *testing.T) {
	long := "Study A(PROD) - Standard Queries > "
	s := newPickState([]Item{
		{Name: long + "Adverse Events", Count: "1 dashboard, 3 questions"},
		{Name: long + "Enrollment", Count: "0 dashboards, 6 questions"},
	}, 10, 70)
	lines := s.view("collections")
	if !strings.Contains(lines[0], "Adverse Events") || !strings.Contains(lines[1], "Enrollment") {
		t.Errorf("the distinguishing end of each name was cut:\n%s\n%s", lines[0], lines[1])
	}
}
