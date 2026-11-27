package content

import (
	"context"
	"strings"
	"testing"

	"github.com/hihipy/metabase-omnibus/internal/fakemb"
	"github.com/hihipy/metabase-omnibus/internal/metabase"
)

type run struct {
	srv      *fakemb.Server
	cat      *Catalog
	items    []Item
	skipped  []Skipped
	snippets string
	all      string // every file and the snippets, joined, for scanning
}

func export(t *testing.T, opt Options, personal bool) run {
	return exportFor(t, opt, personal, nil)
}

func exportFor(t *testing.T, opt Options, personal bool, dbs map[int]bool) run {
	return exportWith(t, fakemb.New(t), opt, personal, dbs)
}

func exportWith(t *testing.T, srv *fakemb.Server, opt Options, personal bool, dbs map[int]bool) run {
	t.Helper()
	c, err := metabase.New(context.Background(), metabase.Config{URL: srv.URL, APIKey: fakemb.APIKey, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := List(context.Background(), c, personal)
	if err != nil {
		t.Fatal(err)
	}
	if dbs != nil {
		cat.Restrict(dbs)
	}
	keep := map[int]bool{}
	for _, col := range cat.Collections {
		keep[col.ID] = true
	}
	items, snippets, skipped, err := Export(context.Background(), c, cat, keep, opt)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Markdown)
	}
	b.WriteString(snippets)
	return run{srv, cat, items, skipped, snippets, b.String()}
}

func TestCollectionsSkipPersonalAndArchived(t *testing.T) {
	r := export(t, Options{}, false)
	var paths []string
	for _, c := range r.cat.Collections {
		paths = append(paths, c.Path)
	}
	want := "Our analytics|Our analytics > Finance|Our analytics > Finance > Reports"
	if got := strings.Join(paths, "|"); got != want {
		t.Errorf("collections = %s\nwant %s", got, want)
	}
	if r.srv.Saw("GET /api/card/12") || r.srv.Saw("GET /api/dashboard/4") {
		t.Error("fetched an item from a personal collection")
	}
}

func TestPrivacyDefaults(t *testing.T) {
	r := export(t, Options{}, false)
	for _, c := range append(fakemb.Canaries, fakemb.Description, fakemb.TextCard) {
		if strings.Contains(r.all, c) {
			t.Errorf("default export contains %s", c)
		}
	}
}

func TestOptInText(t *testing.T) {
	r := export(t, Options{IncludeText: true}, false)
	for _, s := range []string{fakemb.Description, fakemb.TextCard} {
		if !strings.Contains(r.all, s) {
			t.Errorf("-include-descriptions export is missing %s", s)
		}
	}
	for _, c := range fakemb.Canaries {
		if strings.Contains(r.all, c) {
			t.Errorf("an opt-in must never unlock %s", c)
		}
	}
}

func TestNativeQuestion(t *testing.T) {
	r := export(t, Options{FieldNames: map[int]string{202: "PUBLIC.PEOPLE.STATE"}, DBNames: map[int]string{1: "Sample Database"}}, false)
	for _, s := range []string{
		"# [Q10] Orders by state",
		"database Sample Database",
		"[[AND STATE = {{state}}]]",
		"Built on: [11] Order base",
		"Snippets: completed",
		"On dashboards: [D3] Sales overview",
		"{{state}} string/=, filters column PUBLIC.PEOPLE.STATE",
		"Columns: STATE Text, count BigInteger",
	} {
		if !strings.Contains(r.all, s) {
			t.Errorf("export is missing %q", s)
		}
	}
}

func TestQueryBuilderIsCompiledNotRun(t *testing.T) {
	r := export(t, Options{}, false)
	if !r.srv.Saw("POST /api/dataset/native") {
		t.Error("the model was not compiled to SQL")
	}
	if !strings.Contains(r.all, "SQL, compiled from the query builder") || !strings.Contains(r.all, `FROM "PUBLIC"."ORDERS"`) {
		t.Error("compiled SQL is missing")
	}
	// fakemb fails the test by itself if anything asked it to run a query.
}

func TestNoCompile(t *testing.T) {
	r := export(t, Options{NoCompile: true}, false)
	if r.srv.Saw("POST /api/dataset/native") {
		t.Error("-no-compile still compiled")
	}
}

func TestDashboard(t *testing.T) {
	r := export(t, Options{}, false)
	for _, s := range []string{
		"# [D3] Sales overview",
		"Filters: State (string/=)",
		`[10] Orders by state (shown as "Orders, by state") in Our analytics > Finance > Reports`,
		"State to variable {{state}}",
		"text card: (text left out",
	} {
		if !strings.Contains(r.all, s) {
			t.Errorf("dashboard is missing %q", s)
		}
	}
}

func TestOnlyUsedSnippets(t *testing.T) {
	r := export(t, Options{}, false)
	if !strings.Contains(r.snippets, "STATUS = 'complete'") || strings.Contains(r.snippets, "## unused") {
		t.Errorf("snippets file:\n%s", r.snippets)
	}
}

func TestPersonalOptIn(t *testing.T) {
	r := export(t, Options{}, true)
	found := false
	for _, c := range r.cat.Collections {
		found = found || c.Personal
	}
	if !found {
		t.Error("-include-personal did not include the personal collection")
	}
}

func TestRestrictToDatabases(t *testing.T) {
	all := export(t, Options{}, false)
	if !strings.Contains(all.all, "Other database question") {
		t.Fatal("with no database chosen, every question should be exported")
	}
	one := exportFor(t, Options{}, false, map[int]bool{1: true})
	if strings.Contains(one.all, "Other database question") || one.srv.Saw("GET /api/card/14") {
		t.Error("a question on an unchosen database was exported or fetched")
	}
	if !strings.Contains(one.all, "Orders by state") || !strings.Contains(one.all, "Sales overview") {
		t.Error("questions and dashboards on the chosen database went missing")
	}
	none := exportFor(t, Options{}, false, map[int]bool{99: true})
	if len(none.items) != 0 {
		t.Errorf("no question uses database 99, but %d items were exported", len(none.items))
	}
}

func TestItemDetails(t *testing.T) {
	r := export(t, Options{}, false)
	byLabel := map[string]Item{}
	for _, it := range r.items {
		byLabel[it.Label] = it
		if !strings.HasPrefix(it.Markdown, "# ["+it.Label+"] ") {
			t.Errorf("%s does not open with its own heading", it.Label)
		}
		if !strings.Contains(it.Markdown, "no query results") {
			t.Errorf("%s does not say what it is", it.Label)
		}
	}
	q, m, d := byLabel["Q10"], byLabel["M11"], byLabel["D3"]
	if q.Kind != "question" || q.SQL != "native" || q.Display != "bar" || q.Updated != "2026-11-10" ||
		q.Collection.Path != "Our analytics > Finance > Reports" || len(q.Dashboards) != 1 {
		t.Errorf("Q10 = %+v", q)
	}
	if m.Kind != "model" || m.SQL != "compiled" {
		t.Errorf("M11 = %+v", m)
	}
	if d.Kind != "dashboard" || d.Cards != 2 {
		t.Errorf("D3 = %+v", d)
	}
	if r.items[0].Label != "D3" {
		t.Errorf("dashboards should come first in their collection; first item is %s", r.items[0].Label)
	}
}

func TestOneFailureSkipsOneItem(t *testing.T) {
	srv := fakemb.New(t)
	srv.Deny = map[string]int{"/api/card/11": 403, "/api/dashboard/3": 500}
	r := exportWith(t, srv, Options{}, false, nil)
	if len(r.skipped) != 2 {
		t.Fatalf("skipped = %+v, want the model and the dashboard", r.skipped)
	}
	got := r.skipped[0].Label + " " + r.skipped[1].Label
	if got != "D3 M11" {
		t.Errorf("skipped labels = %q, want D3 then M11", got)
	}
	for _, sk := range r.skipped {
		if sk.Reason == "" || sk.Collection == "" || sk.Name == "" {
			t.Errorf("skipped entry is missing details: %+v", sk)
		}
	}
	if !strings.Contains(r.all, "# [Q10] Orders by state") {
		t.Error("the question that could be fetched was not exported")
	}
}

func TestParallelExportKeepsItsOrder(t *testing.T) {
	one := export(t, Options{Workers: 1}, false)
	many := export(t, Options{Workers: 8}, false)
	if one.all != many.all {
		t.Error("the export changed with the number of workers")
	}
}

func TestRootNameComesFromMetabase(t *testing.T) {
	srv := fakemb.New(t)
	srv.RootName = "Unsere Analysen"
	r := exportWith(t, srv, Options{}, false, nil)
	if r.cat.Collections[0].Path != "Unsere Analysen" || !strings.Contains(r.all, "Unsere Analysen > Finance > Reports") {
		t.Errorf("root = %q; the root collection's own name should be used", r.cat.Collections[0].Path)
	}
}
