package content

// Rendering: one markdown file per card and dashboard, written for a reader
// who sees that file alone.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// FileNote explains the export; the indexes carry it in full.
const FileNote = "Definitions only: no query results. SQL is shown as written, so any literal typed into a query is included. " +
	"IDs: Q question, M model, MT metric, D dashboard. SQL uses Metabase syntax: {{variable}}, [[optional clause]], {{#id}} for a saved question."

// itemNote travels in every item file, so a file read on its own still says what it is.
const itemNote = "_Definition only, no query results. SQL is shown as written, so a literal typed into a query is included._"

// ---------------------------------------------------------------- render

var (
	tagTargetRe   = regexp.MustCompile(`\["template-tag",\s*"([^"]+)"\]`)
	fieldTargetRe = regexp.MustCompile(`\["field",\s*(\d+)`)
)

// target describes what a filter feeds: a SQL variable or a column.
func target(raw []byte, fields map[int]string) string {
	s := string(raw)
	if m := tagTargetRe.FindStringSubmatch(s); m != nil {
		return "variable {{" + m[1] + "}}"
	}
	if m := fieldTargetRe.FindStringSubmatch(s); m != nil {
		id, _ := strconv.Atoi(m[1])
		if name, ok := fields[id]; ok {
			return "column " + name
		}
		return "field #" + m[1]
	}
	return compactJSON(raw)
}

func renderDashboard(b *strings.Builder, d apiDashboard, cat *Catalog, cardFile map[int]string, opt Options) {
	fmt.Fprintf(b, "# [D%d] %s\n\n%s\n\n", d.ID, d.Name, itemNote)
	fmt.Fprintf(b, "Dashboard in %s.", cat.byID[intOr(d.CollectionID)].Path)
	if opt.IncludeText && d.Description != nil && *d.Description != "" {
		fmt.Fprintf(b, " Description: %s", oneLine(*d.Description))
	}
	b.WriteString("\n\n")

	pname := map[string]string{}
	if len(d.Parameters) > 0 {
		b.WriteString("Filters: ")
		for i, p := range d.Parameters {
			pname[p.ID] = p.Name
			if i > 0 {
				b.WriteString("; ")
			}
			fmt.Fprintf(b, "%s (%s)", p.Name, p.Type)
		}
		b.WriteString("\n\n")
	}

	tabs := map[int]string{}
	for _, t := range d.Tabs {
		tabs[t.ID] = t.Name
	}
	dcs := append([]apiDashcard(nil), d.Dashcards...)
	sort.SliceStable(dcs, func(i, j int) bool {
		ti, tj := intOr(dcs[i].TabID), intOr(dcs[j].TabID)
		if ti != tj {
			return ti < tj
		}
		if dcs[i].Row != dcs[j].Row {
			return dcs[i].Row < dcs[j].Row
		}
		return dcs[i].Col < dcs[j].Col
	})
	if len(dcs) > 0 {
		b.WriteString("Cards, in layout order (row, column, width x height):\n\n")
	}
	for _, dc := range dcs {
		pos := fmt.Sprintf("%d,%d %dx%d", dc.Row, dc.Col, dc.SizeX, dc.SizeY)
		if tab, ok := tabs[intOr(dc.TabID)]; ok && len(tabs) > 1 {
			pos = "tab " + tab + ", " + pos
		}
		if dc.CardID == nil {
			kind := "text"
			if dc.Viz.VirtualCard != nil && dc.Viz.VirtualCard.Display != "" {
				kind = dc.Viz.VirtualCard.Display
			}
			text := "(text left out; use -include-descriptions)"
			if opt.IncludeText && dc.Viz.Text != nil {
				text = oneLine(*dc.Viz.Text)
			}
			fmt.Fprintf(b, "- %s: %s card: %s\n", pos, kind, text)
			continue
		}
		id := *dc.CardID
		name, ok := cat.cardNames[id]
		if !ok {
			name = "(not visible to these credentials)"
		}
		line := fmt.Sprintf("- %s: [%d] %s", pos, id, name)
		if dc.Viz.CardTitle != "" && dc.Viz.CardTitle != name {
			line += fmt.Sprintf(" (shown as %q)", dc.Viz.CardTitle)
		}
		if f, ok := cardFile[id]; ok && f != "" {
			line += " in " + f
		}
		var wires []string
		for _, m := range dc.ParameterMappings {
			wires = append(wires, fmt.Sprintf("%s to %s", pname[m.ParameterID], target(m.Target, opt.FieldNames)))
		}
		if len(wires) > 0 {
			line += ". Filters: " + strings.Join(wires, "; ")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
}

func renderCard(b *strings.Builder, cd card, cat *Catalog, opt Options) {
	fmt.Fprintf(b, "# [%s] %s\n\n%s\n\n", label(cd.kind, cd.ID), cd.Name, itemNote)
	meta := []string{cd.kind + " in " + cat.byID[intOr(cd.CollectionID)].Path, "display " + cd.Display}
	if cd.DatabaseID != nil {
		if name, ok := opt.DBNames[*cd.DatabaseID]; ok {
			meta = append(meta, "database "+name)
		}
	}
	if len(cd.UpdatedAt) >= 10 {
		meta = append(meta, "updated "+cd.UpdatedAt[:10])
	}
	b.WriteString(strings.Join(meta, ", ") + "\n")
	if opt.IncludeText && cd.Description != nil && *cd.Description != "" {
		fmt.Fprintf(b, "Description: %s\n", oneLine(*cd.Description))
	}
	if len(cd.cardDeps) > 0 {
		var refs []string
		for _, id := range cd.cardDeps {
			name, ok := cat.cardNames[id]
			if !ok {
				name = "(not visible to these credentials)"
			}
			refs = append(refs, fmt.Sprintf("[%d] %s", id, name))
		}
		fmt.Fprintf(b, "Built on: %s\n", strings.Join(refs, "; "))
	}
	if len(cd.snippets) > 0 {
		fmt.Fprintf(b, "Snippets: %s\n", strings.Join(cd.snippets, ", "))
	}
	if len(cd.dashboards) > 0 {
		fmt.Fprintf(b, "On dashboards: %s\n", strings.Join(cd.dashboards, "; "))
	}
	if len(cd.tags) > 0 {
		var ps []string
		for _, t := range cd.tags {
			kind := t.WidgetType
			if kind == "" {
				kind = t.Type
			}
			p := fmt.Sprintf("{{%s}} %s", t.Name, kind)
			if t.Required {
				p += ", required"
			}
			if m := fieldTargetRe.FindStringSubmatch(string(t.Dimension)); m != nil {
				p += ", filters " + target(t.Dimension, opt.FieldNames)
			}
			ps = append(ps, p)
		}
		fmt.Fprintf(b, "Variables: %s\n", strings.Join(ps, "; "))
	}

	switch {
	case cd.sql != "" && cd.compiled:
		fmt.Fprintf(b, "\nSQL, compiled from the query builder:\n\n```sql\n%s\n```\n", strings.TrimRight(cd.sql, "\n "))
	case cd.sql != "":
		fmt.Fprintf(b, "\n```sql\n%s\n```\n", strings.TrimRight(cd.sql, "\n "))
	case cd.sqlError != "":
		fmt.Fprintf(b, "\nSQL not available (%s). Query builder definition:\n\n```json\n%s\n```\n", cd.sqlError, cd.mbql)
	}

	if len(cd.ResultMetadata) > 0 {
		var cols []string
		for _, col := range cd.ResultMetadata {
			cols = append(cols, col.Name+" "+strings.TrimPrefix(col.BaseType, "type/"))
		}
		fmt.Fprintf(b, "\nColumns: %s\n", strings.Join(cols, ", "))
	}
	b.WriteString("\n")
}

func renderSnippets(snips []apiSnippet, used map[string]bool, opt Options) string {
	var b strings.Builder
	b.WriteString("# SQL Snippets\n\nSnippets referenced as {{snippet: name}} by the exported questions.\n\n")
	sort.Slice(snips, func(i, j int) bool { return snips[i].Name < snips[j].Name })
	for _, s := range snips {
		if !used[s.Name] {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", s.Name)
		if opt.IncludeText && s.Description != nil && *s.Description != "" {
			fmt.Fprintf(&b, "Description: %s\n\n", oneLine(*s.Description))
		}
		fmt.Fprintf(&b, "```sql\n%s\n```\n\n", strings.TrimRight(s.Content, "\n "))
	}
	return b.String()
}

// ---------------------------------------------------------------- helpers

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	if contains(list, s) {
		return list
	}
	return append(list, s)
}

func appendUniqueInt(list []int, n int) []int {
	for _, x := range list {
		if x == n {
			return list
		}
	}
	return append(list, n)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
