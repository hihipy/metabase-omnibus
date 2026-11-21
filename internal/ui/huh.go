package ui

import (
	"errors"
	"sort"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// pickList shows a multi-select list built with Charm's huh library. It
// returns the chosen indexes in list order, or nil if the user cancelled.
func pickList(items []Item, noun string) ([]int, error) {
	opts := make([]huh.Option[int], len(items))
	for i, it := range items {
		label := it.Name
		if it.Count != "" {
			label += "  (" + it.Count + ")"
		}
		opts[i] = huh.NewOption(label, i).Selected(true)
	}

	rows, _ := termSize()
	height := len(items) + 4
	if rows > 10 && height > rows-4 {
		height = rows - 4
	}

	var picked []int
	list := huh.NewMultiSelect[int]().
		Title("Which " + noun + "?").
		Description("Everything starts selected. The keys are listed at the bottom.").
		Options(opts...).
		Filterable(true).
		Height(height).
		Value(&picked)

	err := huh.NewForm(huh.NewGroup(list)).WithTheme(pickerTheme()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return nil, nil
	}
	sort.Ints(picked)
	return picked, nil
}

// pickerTheme is readable without telling colours apart. Selection shows as
// [x] against [ ] and as bold against regular weight, so colour only adds to
// what shape already says. Nothing is dimmed, because faint grey is the first
// thing to disappear on a dark background. Colours are the terminal's own
// bright white (15) and bright yellow (11), so they follow the user's palette.
func pickerTheme() *huh.Theme {
	t := huh.ThemeBase()
	plain := lipgloss.NewStyle()
	strong := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))
	cursor := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))

	for _, f := range []*huh.FieldStyles{&t.Focused, &t.Blurred} {
		f.Title = strong
		f.Description = plain
		f.MultiSelectSelector = cursor.SetString("> ")
		f.SelectedOption = strong
		f.SelectedPrefix = strong.SetString("[x] ")
		f.UnselectedOption = plain
		f.UnselectedPrefix = plain.SetString("[ ] ")
	}
	t.Help.ShortKey = strong
	t.Help.ShortDesc = plain
	t.Help.ShortSeparator = plain
	t.Help.FullKey = strong
	t.Help.FullDesc = plain
	t.Help.FullSeparator = plain
	return t
}
