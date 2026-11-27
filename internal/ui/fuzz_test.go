package ui

import (
	"sort"
	"testing"
)

// FuzzParseSelection feeds the picker's typed input arbitrary lines. It must
// never panic, and whatever it accepts must be a sorted list of distinct
// indexes that exist.
func FuzzParseSelection(f *testing.F) {
	names := []string{"Sample Database", "Finance", "Finance/Reports", "Marketing", "Orders (2)"}
	for _, s := range []string{"", "*", "1", "1,3", "1-3", "-2", "-1-2", "2-1", "0", "6", "99999999999999999999", "fin", "-fin", "1,-3", "-", "--", ",,", "1 - 3", "*,1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		got, err := ParseSelection(line, names)
		if err != nil {
			if got != nil {
				t.Fatalf("ParseSelection(%q) returned %v with error %v", line, got, err)
			}
			return
		}
		if !sort.IntsAreSorted(got) {
			t.Fatalf("ParseSelection(%q) = %v is not sorted", line, got)
		}
		for i, n := range got {
			if n < 1 || n > len(names) {
				t.Fatalf("ParseSelection(%q) = %v holds %d, outside 1 to %d", line, got, n, len(names))
			}
			if i > 0 && got[i-1] == n {
				t.Fatalf("ParseSelection(%q) = %v repeats %d", line, got, n)
			}
		}
	})
}
