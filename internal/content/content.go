// Package content exports what people built in Metabase: collections,
// questions, models, metrics, dashboards, and the SQL behind them.
//
// Privacy rules, matching the schema export:
//   - The API structs below are an allowlist; unnamed fields are dropped.
//   - No query is ever run. Query-builder questions are compiled to SQL by
//     POST /api/dataset/native, which compiles without executing.
//   - No column fingerprints, filter default values (a default can be a real
//     value, such as an ID), creator names, or personal collections.
//   - Free text (descriptions, dashboard text cards) only on opt-in.
//
// SQL is exported as written, so a literal typed into a query (WHERE id = 42)
// is included. That is stated in every file.
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hihipy/metabase-omnibus/internal/metabase"
)

// ---------------------------------------------------------------- API allowlist

type apiCollection struct {
	ID              json.RawMessage `json:"id"` // the root collection's ID is the string "root"
	Name            string          `json:"name"`
	Location        string          `json:"location"`
	PersonalOwnerID *int            `json:"personal_owner_id"`
	Archived        bool            `json:"archived"`
}

type apiListItem struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Model        string `json:"model"` // only in /api/search results
	CollectionID *int   `json:"collection_id"`
	Collection   *struct {
		ID json.RawMessage `json:"id"`
	} `json:"collection"` // /api/search nests it
	Archived bool `json:"archived"`
}

type apiCard struct {
	ID             int             `json:"id"`
	Name           string          `json:"name"`
	Description    *string         `json:"description"`
	Type           string          `json:"type"`
	Dataset        bool            `json:"dataset"` // older versions mark models this way
	Display        string          `json:"display"`
	CollectionID   *int            `json:"collection_id"`
	DatabaseID     *int            `json:"database_id"`
	UpdatedAt      string          `json:"updated_at"`
	Archived       bool            `json:"archived"`
	DatasetQuery   json.RawMessage `json:"dataset_query"`
	ResultMetadata []struct {
		Name     string `json:"name"`
		BaseType string `json:"base_type"`
	} `json:"result_metadata"`
}

type apiTag struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"display-name"`
	Type        string          `json:"type"`
	CardID      *int            `json:"card-id"`
	SnippetName string          `json:"snippet-name"`
	WidgetType  string          `json:"widget-type"`
	Required    bool            `json:"required"`
	Dimension   json.RawMessage `json:"dimension"`
}

type apiNative struct {
	Query        string            `json:"query"`
	TemplateTags map[string]apiTag `json:"template-tags"`
}

type apiQuery struct {
	Type     string            `json:"type"`
	Database int               `json:"database"`
	Native   *apiNative        `json:"native"`
	Stages   []json.RawMessage `json:"stages"` // MBQL 5, Metabase 0.57 and later
}

type apiStage struct {
	LibType      string            `json:"lib/type"`
	Native       string            `json:"native"`
	TemplateTags map[string]apiTag `json:"template-tags"`
}

type apiDashboard struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	CollectionID *int    `json:"collection_id"`
	Archived     bool    `json:"archived"`
	Parameters   []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
		Type string `json:"type"`
	} `json:"parameters"`
	Tabs []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"tabs"`
	Dashcards    []apiDashcard `json:"dashcards"`
	OrderedCards []apiDashcard `json:"ordered_cards"` // older versions
}

type apiDashcard struct {
	CardID            *int `json:"card_id"`
	TabID             *int `json:"dashboard_tab_id"`
	Row               int  `json:"row"`
	Col               int  `json:"col"`
	SizeX             int  `json:"size_x"`
	SizeY             int  `json:"size_y"`
	ParameterMappings []struct {
		ParameterID string          `json:"parameter_id"`
		Target      json.RawMessage `json:"target"`
	} `json:"parameter_mappings"`
	Viz struct {
		Text        *string `json:"text"`
		CardTitle   string  `json:"card.title"`
		VirtualCard *struct {
			Display string `json:"display"`
		} `json:"virtual_card"`
	} `json:"visualization_settings"`
}

type apiSnippet struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Content     string  `json:"content"`
}

// ---------------------------------------------------------------- collections

// Collection is one folder in Metabase.
type Collection struct {
	ID       int    // 0 is the root, "Our analytics"
	Name     string // own name
	Path     string // "Our analytics > Team > Study"
	Parts    []string
	Personal bool
}

const rootName = "Our analytics"

// Catalog is every collection, card, and dashboard the credentials can list.
type Catalog struct {
	Collections []Collection
	byID        map[int]Collection
	cards       []apiListItem
	dashboards  []apiListItem
	cardNames   map[int]string
}

// Counts reports how many cards and dashboards sit directly in a collection.
func (c *Catalog) Counts(id int) (cards, dashboards int) {
	for _, it := range c.cards {
		if colID(it) == id {
			cards++
		}
	}
	for _, it := range c.dashboards {
		if colID(it) == id {
			dashboards++
		}
	}
	return
}

// List reads collections, cards, and dashboards. Personal collections are
// skipped unless includePersonal is set.
func List(ctx context.Context, c *metabase.Client, includePersonal bool) (*Catalog, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/collection", nil, &raw); err != nil {
		return nil, err
	}
	cols, err := decodeList[apiCollection](raw)
	if err != nil {
		return nil, fmt.Errorf("listing collections: %w", err)
	}
	names := map[int]string{}
	personalRoots := map[int]bool{}
	for _, col := range cols {
		if id, ok := intID(col.ID); ok {
			names[id] = col.Name
			if col.PersonalOwnerID != nil {
				personalRoots[id] = true
			}
		}
	}

	cat := &Catalog{byID: map[int]Collection{}, cardNames: map[int]string{}}
	root := Collection{ID: 0, Name: rootName, Path: rootName, Parts: []string{rootName}}
	cat.Collections = append(cat.Collections, root)
	cat.byID[0] = root
	for _, col := range cols {
		id, ok := intID(col.ID)
		if !ok || col.Archived {
			continue
		}
		parts := []string{rootName}
		personal := personalRoots[id]
		for _, p := range strings.Split(strings.Trim(col.Location, "/"), "/") {
			if pid, err := strconv.Atoi(p); err == nil {
				parts = append(parts, names[pid])
				personal = personal || personalRoots[pid]
			}
		}
		parts = append(parts, col.Name)
		if personal && !includePersonal {
			continue
		}
		cc := Collection{ID: id, Name: col.Name, Path: strings.Join(parts, " > "), Parts: parts, Personal: personal}
		cat.Collections = append(cat.Collections, cc)
		cat.byID[id] = cc
	}
	sort.Slice(cat.Collections, func(i, j int) bool { return cat.Collections[i].Path < cat.Collections[j].Path })

	if cat.cards, err = listItems(ctx, c, "/api/card", []string{"card", "dataset", "metric"}); err != nil {
		return nil, err
	}
	if cat.dashboards, err = listItems(ctx, c, "/api/dashboard/", []string{"dashboard"}); err != nil {
		return nil, err
	}
	for _, it := range cat.cards {
		cat.cardNames[it.ID] = it.Name
	}
	return cat, nil
}

func listItems(ctx context.Context, c *metabase.Client, endpoint string, models []string) ([]apiListItem, error) {
	var raw json.RawMessage
	err := c.Get(ctx, endpoint, url.Values{"f": {"all"}}, &raw)
	var httpErr *metabase.HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == 404 {
		// The list endpoint is gone in this version; search finds the same items.
		err = c.Get(ctx, "/api/search", url.Values{"models": models}, &raw)
	}
	if err != nil {
		return nil, err
	}
	items, err := decodeList[apiListItem](raw)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", endpoint, err)
	}
	var out []apiListItem
	for _, it := range items {
		if it.Archived || (it.Model != "" && !contains(models, it.Model)) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func colID(it apiListItem) int {
	if it.CollectionID != nil {
		return *it.CollectionID
	}
	if it.Collection != nil {
		if id, ok := intID(it.Collection.ID); ok {
			return id
		}
	}
	return 0
}

// ---------------------------------------------------------------- export

// Options controls what goes into the export beyond the definitions.
type Options struct {
	IncludeText bool           // descriptions and dashboard text cards
	NoCompile   bool           // skip compiling query-builder questions to SQL
	FieldNames  map[int]string // field ID to schema.table.column, from the schema export
	DBNames     map[int]string
	Progress    func(done, total int, label string)
}

// File is one collection's export, ready to write.
type File struct {
	Collection Collection
	Markdown   string
	Cards      int
	Dashboards int
}

type card struct {
	apiCard
	kind       string
	sql        string
	compiled   bool
	sqlError   string
	mbql       string
	tags       []apiTag
	cardDeps   []int
	snippets   []string
	dashboards []string
}

// Export fetches the chosen collections' cards and dashboards and renders one
// Markdown file per collection, plus the SQL snippets those cards use.
func Export(ctx context.Context, c *metabase.Client, cat *Catalog, keep map[int]bool, opt Options) ([]File, string, error) {
	var dashIDs, cardIDs []int
	for _, it := range cat.dashboards {
		if keep[colID(it)] {
			dashIDs = append(dashIDs, it.ID)
		}
	}
	for _, it := range cat.cards {
		if keep[colID(it)] {
			cardIDs = append(cardIDs, it.ID)
		}
	}
	total := len(dashIDs) + len(cardIDs)
	done := 0
	step := func(label string) {
		if opt.Progress != nil {
			opt.Progress(done, total, label)
		}
		done++
	}

	// Dashboards first, so each card can list where it appears.
	var dashes []apiDashboard
	onDash := map[int][]string{}
	for _, id := range dashIDs {
		step(fmt.Sprintf("dashboard %d", id))
		var d apiDashboard
		if err := c.Get(ctx, fmt.Sprintf("/api/dashboard/%d", id), nil, &d); err != nil {
			return nil, "", err
		}
		if len(d.Dashcards) == 0 {
			d.Dashcards = d.OrderedCards
		}
		dashes = append(dashes, d)
		for _, dc := range d.Dashcards {
			if dc.CardID != nil {
				onDash[*dc.CardID] = appendUnique(onDash[*dc.CardID], fmt.Sprintf("[D%d] %s", d.ID, d.Name))
			}
		}
	}

	var cards []card
	usedSnippets := map[string]bool{}
	for _, id := range cardIDs {
		step(fmt.Sprintf("question %d", id))
		var ac apiCard
		// legacy-mbql keeps the query in the classic shape on Metabase 0.57 and
		// later; older versions ignore it.
		if err := c.Get(ctx, fmt.Sprintf("/api/card/%d", id), url.Values{"legacy-mbql": {"true"}}, &ac); err != nil {
			return nil, "", err
		}
		cd := card{apiCard: ac, kind: kindOf(ac), dashboards: onDash[ac.ID]}
		cd.fillQuery()
		if cd.sql == "" && !opt.NoCompile && len(ac.DatasetQuery) > 0 {
			cd.compile(ctx, c)
		}
		for _, s := range cd.snippets {
			usedSnippets[s] = true
		}
		cards = append(cards, cd)
	}
	if opt.Progress != nil {
		opt.Progress(total, total, "done")
	}

	snippetsMD := ""
	if len(usedSnippets) > 0 {
		var raw json.RawMessage
		if err := c.Get(ctx, "/api/native-query-snippet", nil, &raw); err == nil {
			snips, _ := decodeList[apiSnippet](raw)
			snippetsMD = renderSnippets(snips, usedSnippets, opt)
		}
	}

	// Group into one file per collection.
	byCol := map[int]*File{}
	cardFile := map[int]string{}
	for _, cd := range cards {
		cardFile[cd.ID] = cat.byID[intOr(cd.CollectionID)].Path
	}
	get := func(id int) *File {
		if f, ok := byCol[id]; ok {
			return f
		}
		f := &File{Collection: cat.byID[id]}
		byCol[id] = f
		return f
	}
	var bodies = map[int]*strings.Builder{}
	body := func(id int) *strings.Builder {
		if b, ok := bodies[id]; ok {
			return b
		}
		b := &strings.Builder{}
		bodies[id] = b
		return b
	}
	sort.Slice(dashes, func(i, j int) bool { return dashes[i].Name < dashes[j].Name })
	for _, d := range dashes {
		id := intOr(d.CollectionID)
		get(id).Dashboards++
		renderDashboard(body(id), d, cat, cardFile, opt)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
	for _, cd := range cards {
		id := intOr(cd.CollectionID)
		get(id).Cards++
		renderCard(body(id), cd, cat, opt)
	}

	var files []File
	for id, f := range byCol {
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", f.Collection.Path)
		fmt.Fprintf(&b, "%s, %s. %s\n\n", plural(f.Dashboards, "dashboard"), plural(f.Cards, "question"), fileNote)
		b.WriteString(bodies[id].String())
		f.Markdown = b.String()
		files = append(files, *f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Collection.Path < files[j].Collection.Path })
	return files, snippetsMD, nil
}

const fileNote = "Definitions only: no query results. SQL is shown as written, so any literal typed into a query is included. " +
	"IDs: Q question, M model, MT metric, D dashboard. SQL uses Metabase syntax: {{variable}}, [[optional clause]], {{#id}} for a saved question."

func kindOf(c apiCard) string {
	switch {
	case c.Type != "":
		return c.Type
	case c.Dataset:
		return "model"
	}
	return "question"
}

func label(kind string, id int) string {
	switch kind {
	case "model":
		return "M" + strconv.Itoa(id)
	case "metric":
		return "MT" + strconv.Itoa(id)
	}
	return "Q" + strconv.Itoa(id)
}

var cardRefRe = regexp.MustCompile(`card__(\d+)`)

// fillQuery reads native SQL and template tags from either query shape, and
// finds the saved questions and snippets the card builds on.
func (cd *card) fillQuery() {
	var q apiQuery
	if json.Unmarshal(cd.DatasetQuery, &q) != nil {
		return
	}
	var tags map[string]apiTag
	switch {
	case q.Native != nil:
		cd.sql, tags = q.Native.Query, q.Native.TemplateTags
	case len(q.Stages) > 0:
		var st apiStage
		if json.Unmarshal(q.Stages[len(q.Stages)-1], &st) == nil && strings.HasSuffix(st.LibType, "native") {
			cd.sql, tags = st.Native, st.TemplateTags
		}
	}
	for _, t := range tags {
		switch t.Type {
		case "card":
			if t.CardID != nil {
				cd.cardDeps = appendUniqueInt(cd.cardDeps, *t.CardID)
			}
		case "snippet":
			cd.snippets = appendUnique(cd.snippets, t.SnippetName)
		default:
			cd.tags = append(cd.tags, t)
		}
	}
	sort.Slice(cd.tags, func(i, j int) bool { return cd.tags[i].Name < cd.tags[j].Name })
	for _, m := range cardRefRe.FindAllStringSubmatch(string(cd.DatasetQuery), -1) {
		if id, err := strconv.Atoi(m[1]); err == nil && id != cd.ID {
			cd.cardDeps = appendUniqueInt(cd.cardDeps, id)
		}
	}
	sort.Ints(cd.cardDeps)
	sort.Strings(cd.snippets)
}

// compile asks Metabase for the SQL behind a query-builder question. This
// compiles the query; it does not run it.
func (cd *card) compile(ctx context.Context, c *metabase.Client) {
	var body map[string]any
	if json.Unmarshal(cd.DatasetQuery, &body) != nil {
		return
	}
	if db, ok := body["database"].(float64); (!ok || db <= 0) && cd.DatabaseID != nil {
		body["database"] = *cd.DatabaseID
	}
	body["pretty"] = true
	var out struct {
		Query string `json:"query"`
	}
	if err := c.Post(ctx, "/api/dataset/native", body, &out); err != nil {
		cd.sqlError = err.Error()
		cd.mbql = compactJSON(cd.DatasetQuery)
		return
	}
	cd.sql, cd.compiled = out.Query, true
}

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
	fmt.Fprintf(b, "## [D%d] %s\n\n", d.ID, d.Name)
	fmt.Fprintf(b, "Dashboard.")
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
	fmt.Fprintf(b, "## [%s] %s\n\n", label(cd.kind, cd.ID), cd.Name)
	meta := []string{cd.kind, "display " + cd.Display}
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

func decodeList[T any](raw json.RawMessage) ([]T, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "{") {
		var wrapped struct {
			Data []T `json:"data"`
		}
		err := json.Unmarshal(raw, &wrapped)
		return wrapped.Data, err
	}
	var list []T
	err := json.Unmarshal(raw, &list)
	return list, err
}

func intID(raw json.RawMessage) (int, bool) {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	return 0, false
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func compactJSON(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

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
