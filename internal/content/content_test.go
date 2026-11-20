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
	files    []File
	snippets string
	all      string // every file and the snippets, joined, for scanning
}

func export(t *testing.T, opt Options, personal bool) run {
	t.Helper()
	srv := fakemb.New(t)
	c, err := metabase.New(context.Background(), metabase.Config{URL: srv.URL, APIKey: fakemb.APIKey, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := List(context.Background(), c, personal)
	if err != nil {
		t.Fatal(err)
	}
	keep := map[int]bool{}
	for _, col := range cat.Collections {
		keep[col.ID] = true
	}
	files, snippets, err := Export(context.Background(), c, cat, keep, opt)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.Markdown)
	}
	b.WriteString(snippets)
	return run{srv, cat, files, snippets, b.String()}
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
		"## [Q10] Orders by state",
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
		"## [D3] Sales overview",
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
