package content

// Fetching: one request per card and dashboard, spread across a small worker
// pool, with failures recorded as skipped items rather than ending the run.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/hihipy/metabase-omnibus/internal/metabase"
)

// ---------------------------------------------------------------- export

// Options controls what goes into the export beyond the definitions.
type Options struct {
	IncludeText bool           // descriptions and dashboard text cards
	NoCompile   bool           // skip compiling query-builder questions to SQL
	FieldNames  map[int]string // field ID to schema.table.column, from the schema export
	DBNames     map[int]string
	Workers     int // requests at once; 0 means 4
	Progress    func(done, total int, label string)
}

// Skipped is an item that could not be exported, and why. A run records
// these instead of stopping, and the database index lists them.
type Skipped struct {
	Label, Name, Collection, Reason string
}

// Item is one exported question, model, metric, or dashboard: its own file,
// plus the details an index lists about it.
type Item struct {
	Collection Collection
	Kind       string // question, model, metric, or dashboard
	Label      string // Q42, M11, MT7, or D3
	ID         int
	Name       string
	Display    string   // how a question is drawn: table, bar, line...
	Updated    string   // YYYY-MM-DD
	SQL        string   // native, compiled, unavailable, or empty for a dashboard
	Dashboards []string // for a question: the dashboards showing it
	Cards      int      // for a dashboard: how many cards it holds
	Markdown   string
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

// Export fetches the chosen collections' cards and dashboards and renders each
// as its own Markdown item, plus the SQL snippets those cards use.
func Export(ctx context.Context, c *metabase.Client, cat *Catalog, keep map[int]bool, opt Options) ([]Item, string, []Skipped, error) {
	var dashList, cardList []apiListItem
	for _, it := range cat.dashboards {
		if keep[colID(it)] {
			dashList = append(dashList, it)
		}
	}
	for _, it := range cat.cards {
		if keep[colID(it)] {
			cardList = append(cardList, it)
		}
	}
	workers := opt.Workers
	if workers <= 0 {
		workers = 4
	}
	prog := &progress{total: len(dashList) + len(cardList), report: opt.Progress}
	var skipped []Skipped
	skip := func(label string, it apiListItem, err error) {
		skipped = append(skipped, Skipped{label, it.Name, cat.byID[colID(it)].Path, err.Error()})
	}

	// Dashboards first, so each card can list where it appears.
	dashes, errs := fetchAll(ctx, dashList, workers, prog, func(it apiListItem) (apiDashboard, error) {
		var d apiDashboard
		err := c.Get(ctx, fmt.Sprintf("/api/dashboard/%d", it.ID), nil, &d)
		if len(d.Dashcards) == 0 {
			d.Dashcards = d.OrderedCards
		}
		return d, err
	})
	if ctx.Err() != nil {
		return nil, "", nil, ctx.Err()
	}
	var gotDashes []apiDashboard
	onDash := map[int][]string{}
	for i, d := range dashes {
		if errs[i] != nil {
			skip("D"+strconv.Itoa(dashList[i].ID), dashList[i], errs[i])
			continue
		}
		gotDashes = append(gotDashes, d)
		for _, dc := range d.Dashcards {
			if dc.CardID != nil {
				onDash[*dc.CardID] = appendUnique(onDash[*dc.CardID], fmt.Sprintf("[D%d] %s", d.ID, d.Name))
			}
		}
	}
	dashes = gotDashes

	fetched, errs := fetchAll(ctx, cardList, workers, prog, func(it apiListItem) (card, error) {
		var ac apiCard
		// legacy-mbql keeps the query in the classic shape on Metabase 0.57 and
		// later; older versions ignore it.
		if err := c.Get(ctx, fmt.Sprintf("/api/card/%d", it.ID), url.Values{"legacy-mbql": {"true"}}, &ac); err != nil {
			return card{}, err
		}
		cd := card{apiCard: ac, kind: kindOf(ac)}
		cd.fillQuery()
		if cd.sql == "" && !opt.NoCompile && len(ac.DatasetQuery) > 0 {
			cd.compile(ctx, c)
		}
		return cd, nil
	})
	if ctx.Err() != nil {
		return nil, "", nil, ctx.Err()
	}
	var cards []card
	usedSnippets := map[string]bool{}
	for i, cd := range fetched {
		if errs[i] != nil {
			it := cardList[i]
			skip(label(kindOf(apiCard{Type: it.Type, Dataset: it.Dataset}), it.ID), it, errs[i])
			continue
		}
		cd.dashboards = onDash[cd.ID]
		for _, s := range cd.snippets {
			usedSnippets[s] = true
		}
		cards = append(cards, cd)
	}
	prog.finish()

	snippetsMD := ""
	if len(usedSnippets) > 0 {
		var raw json.RawMessage
		if err := c.Get(ctx, "/api/native-query-snippet", nil, &raw); err == nil {
			snips, _ := decodeList[apiSnippet](raw)
			snippetsMD = renderSnippets(snips, usedSnippets, opt)
		}
	}

	cardPath := map[int]string{}
	for _, cd := range cards {
		cardPath[cd.ID] = cat.byID[intOr(cd.CollectionID)].Path
	}

	var items []Item
	for _, d := range dashes {
		var b strings.Builder
		renderDashboard(&b, d, cat, cardPath, opt)
		items = append(items, Item{
			Collection: cat.byID[intOr(d.CollectionID)], Kind: "dashboard", Label: "D" + strconv.Itoa(d.ID),
			ID: d.ID, Name: d.Name, Cards: len(d.Dashcards), Markdown: b.String(),
		})
	}
	for _, cd := range cards {
		var b strings.Builder
		renderCard(&b, cd, cat, opt)
		it := Item{
			Collection: cat.byID[intOr(cd.CollectionID)], Kind: cd.kind, Label: label(cd.kind, cd.ID),
			ID: cd.ID, Name: cd.Name, Display: cd.Display, Dashboards: cd.dashboards, Markdown: b.String(),
		}
		if len(cd.UpdatedAt) >= 10 {
			it.Updated = cd.UpdatedAt[:10]
		}
		switch {
		case cd.sql != "" && cd.compiled:
			it.SQL = "compiled"
		case cd.sql != "":
			it.SQL = "native"
		default:
			it.SQL = "unavailable"
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Collection.Path != items[j].Collection.Path {
			return items[i].Collection.Path < items[j].Collection.Path
		}
		if (items[i].Kind == "dashboard") != (items[j].Kind == "dashboard") {
			return items[i].Kind == "dashboard" // dashboards first in each collection
		}
		return items[i].Name < items[j].Name
	})
	return items, snippetsMD, skipped, nil
}

// progress counts finished requests across workers.
type progress struct {
	mu     sync.Mutex
	done   int
	total  int
	report func(done, total int, label string)
}

func (p *progress) step(label string) {
	if p.report == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done++
	p.report(p.done, p.total, label)
}

func (p *progress) finish() {
	if p.report != nil {
		p.report(p.total, p.total, "done")
	}
}

// fetchAll runs fetch for every item with up to workers at once and returns
// results and errors in the items' order, so the export comes out the same
// however the requests interleave.
func fetchAll[T any](ctx context.Context, items []apiListItem, workers int, prog *progress, fetch func(apiListItem) (T, error)) ([]T, []error) {
	results := make([]T, len(items))
	errs := make([]error, len(items))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, len(items)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i], errs[i] = fetch(items[i])
				prog.step(items[i].Name)
			}
		}()
	}
	for i := range items {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results, errs
}

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
