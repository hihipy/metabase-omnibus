package main

import (
	"context"
	"encoding/csv"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hihipy/metabase-omnibus/internal/fakemb"
	"github.com/hihipy/metabase-omnibus/internal/schema"
)

// exportAll runs the whole command against the fake and returns every file it
// wrote, keyed by path relative to the output folder.
func exportAll(t *testing.T, extra ...string) (*fakemb.Server, map[string]string) {
	t.Helper()
	srv := fakemb.New(t)
	return srv, exportFrom(t, srv, extra...)
}

// exportFrom runs the whole command against a given fake, so a test can set
// it up to fail first.
func exportFrom(t *testing.T, srv *fakemb.Server, extra ...string) map[string]string {
	t.Helper()
	out := t.TempDir()
	t.Setenv("METABASE_API_KEY", fakemb.APIKey)
	t.Setenv("METABASE_USERNAME", "")
	t.Setenv("METABASE_PASSWORD", "")

	args := append([]string{"-url", srv.URL, "-allow-http", "-all", "-out", out}, extra...)
	if err := run(context.Background(), args, true, true); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{}
	filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return files
}

func TestExportLayout(t *testing.T) {
	_, files := exportAll(t)
	for _, want := range []string{
		"index.md",
		"Sample Database/index.md",
		"Sample Database/schema/PUBLIC.json",
		"Sample Database/schema/ANALYTICS.json",
		"Sample Database/questions/index.md",
		"Sample Database/questions/Finance/index.md",
		"Sample Database/questions/Finance/D3 Sales overview.md",
		"Sample Database/questions/Finance/M11 Order base.md",
		"Sample Database/questions/Finance/Reports/index.md",
		"Sample Database/questions/Finance/Reports/Q10 Orders by state.md",
		"Sample Database/questions/_snippets.md",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; wrote %v", want, keys(files))
		}
	}
	for name := range files {
		if strings.Contains(name, "Personal") || strings.Contains(name, "CANARY") {
			t.Errorf("wrote a file for a personal collection: %s", name)
		}
	}
}

var linkRe = regexp.MustCompile(`\]\(<([^>]+)>\)`)

// Every file is reachable from an index, and every index link leads somewhere.
func TestIndexesLinkEverything(t *testing.T) {
	_, files := exportAll(t)
	linked := map[string]bool{"index.md": true}
	for name, body := range files {
		if path.Base(name) != "index.md" {
			continue
		}
		for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
			target := path.Clean(path.Join(path.Dir(name), m[1]))
			if _, ok := files[target]; !ok {
				t.Errorf("%s links to %s, which was not written", name, target)
			}
			linked[target] = true
		}
	}
	for name := range files {
		if !linked[name] {
			t.Errorf("no index links to %s", name)
		}
	}
}

func TestDatabaseIndexListsEveryItem(t *testing.T) {
	_, files := exportAll(t)
	idx := files["Sample Database/index.md"]
	for _, want := range []string{"| D3 |", "| M11 |", "| Q10 |", "Finance > Reports", "question, bar", "native", "compiled", "2026-11-10", "[D3] Sales overview"} {
		if !strings.Contains(idx, want) {
			t.Errorf("database index is missing %q", want)
		}
	}
}

func TestSkippedItemsAreListedNotLost(t *testing.T) {
	srv := fakemb.New(t)
	srv.Deny = map[string]int{"/api/card/10": 403}
	files := exportFrom(t, srv)
	idx := files["Sample Database/index.md"]
	if !strings.Contains(idx, "## Not Exported") || !strings.Contains(idx, "| Q10 | Orders by state |") || !strings.Contains(idx, "403") {
		t.Errorf("the database index should list the skipped question and why:\n%s", idx)
	}
	if _, ok := files["Sample Database/questions/Finance/M11 Order base.md"]; !ok {
		t.Error("one denied question stopped the rest of the export")
	}
}

func TestUnreadableDatabaseDoesNotStopTheRun(t *testing.T) {
	srv := fakemb.New(t)
	srv.Deny = map[string]int{"/api/database/1/metadata": 403}
	t.Setenv("METABASE_API_KEY", fakemb.APIKey)
	out := t.TempDir()
	if err := run(context.Background(), []string{"-url", srv.URL, "-allow-http", "-all", "-out", out}, true, true); err != nil {
		t.Fatal(err)
	}
	// With the only database unreadable there is nothing to write, and the
	// run still ends cleanly instead of with an error.
	if _, err := os.Stat(filepath.Join(out, "index.md")); err == nil {
		t.Error("an index was written for an export with nothing in it")
	}
}

func TestRootIndexListsFailedDatabases(t *testing.T) {
	ok := &dbExport{db: schema.Database{ID: 1, Name: "Sales DB", Engine: "postgres"}, folder: "Sales DB",
		schemas: []schemaFile{{name: "public", tables: 3, bytes: 400}}}
	md := rootIndex([]*dbExport{ok}, []failedDB{{"Finance DB", "GET /api/database/2/metadata: HTTP 403"}}, "mb.example.com", false)
	for _, want := range []string{"[Sales DB](<Sales DB/index.md>)", "## Not Exported", "| Finance DB | GET /api/database/2/metadata: HTTP 403 |"} {
		if !strings.Contains(md, want) {
			t.Errorf("root index is missing %q:\n%s", want, md)
		}
	}
}

func TestCatalog(t *testing.T) {
	_, files := exportAll(t)
	raw, ok := files["Sample Database/catalog.csv"]
	if !ok {
		t.Fatal("no catalog.csv")
	}
	if !strings.HasPrefix(raw, "\ufeff") {
		t.Error("catalog.csv should start with a UTF-8 byte-order mark for Excel")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rows[0], ",") != strings.Join(catalogHeader, ",") {
		t.Errorf("header = %v", rows[0])
	}
	if len(rows) != 4 { // header plus D3, M11, Q10
		t.Fatalf("got %d rows, want a header and 3 items:\n%s", len(rows), raw)
	}
	byLabel := map[string][]string{}
	for _, r := range rows[1:] {
		byLabel[r[4]] = r
	}
	q := byLabel["Q10"]
	if q == nil || q[2] != "question" || q[6] != "6" || q[7] != "Our analytics > Finance > Reports" ||
		q[9] != "native" || q[11] != "[D3] Sales overview" || q[12] != "questions/Finance/Reports/Q10 Orders by state.md" {
		t.Errorf("Q10 row = %v", q)
	}
	if _, ok := files["Sample Database/"+q[12]]; !ok {
		t.Errorf("catalog file column %q does not point at a written file", q[12])
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{
		"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")",
		"+1":                "'+1",
		"-draft":            "'-draft",
		"@SUM(A1)":          "'@SUM(A1)",
		"Orders by state":   "Orders by state",
		"":                  "",
	} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultOut(t *testing.T) {
	now := time.Date(2026, 11, 20, 19, 25, 0, 0, time.Local)
	for host, want := range map[string]string{
		"lakeshore.insight.example.io": "metabase-lakeshore-20261120-1925",
		"metabase.acme.com":            "metabase-acme-20261120-1925",
		"www.acme.com:3000":            "metabase-acme-20261120-1925",
		"localhost:3000":               "metabase-localhost-20261120-1925",
	} {
		if got := filepath.Base(defaultOut(host, now)); got != want {
			t.Errorf("defaultOut(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestSchemaAndContentCommands(t *testing.T) {
	srv := fakemb.New(t)
	t.Setenv("METABASE_API_KEY", fakemb.APIKey)
	for _, c := range []struct {
		schema, content bool
		has, lacks      string
	}{
		{true, false, "schema/PUBLIC.json", "questions/"},
		{false, true, "questions/index.md", "schema/"},
	} {
		out := t.TempDir()
		if err := run(context.Background(), []string{"-url", srv.URL, "-allow-http", "-all", "-out", out}, c.schema, c.content); err != nil {
			t.Fatal(err)
		}
		idx, _ := os.ReadFile(filepath.Join(out, "Sample Database", "index.md"))
		if !strings.Contains(string(idx), c.has) || strings.Contains(string(idx), c.lacks) {
			t.Errorf("schema=%v content=%v: index.md should list %s and no %s:\n%s", c.schema, c.content, c.has, c.lacks, idx)
		}
	}
}

func TestSchemaFilter(t *testing.T) {
	_, files := exportAll(t, "-schema", "PUB*")
	if _, ok := files["Sample Database/schema/ANALYTICS.json"]; ok {
		t.Error("-schema PUB* still wrote the ANALYTICS schema")
	}
	if _, ok := files["Sample Database/schema/PUBLIC.json"]; !ok {
		t.Error("-schema PUB* dropped the PUBLIC schema")
	}
}

func TestThousands(t *testing.T) {
	for in, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}

// The whole point of the tool: nothing private leaves, whatever ends up in the
// export. Every canary the fake planted must be absent from every file.
func TestNothingPrivateLeaves(t *testing.T) {
	srv, files := exportAll(t)
	u, _ := url.Parse(srv.URL)
	forbidden := append([]string{u.Host, fakemb.Description, fakemb.TextCard}, fakemb.Canaries...)
	for name, body := range files {
		for _, c := range forbidden {
			if strings.Contains(body, c) {
				t.Errorf("%s contains %q", name, c)
			}
		}
	}
}

func TestOptInsStillProtectCanaries(t *testing.T) {
	_, files := exportAll(t, "-include-descriptions", "-include-instance", "-row-counts")
	for name, body := range files {
		for _, c := range fakemb.Canaries {
			if strings.Contains(body, c) {
				t.Errorf("%s contains %q with every opt-in on", name, c)
			}
		}
	}
}

// Choosing databases for the schema export narrows the questions too.
func TestDatabaseChoiceNarrowsContent(t *testing.T) {
	_, files := exportAll(t)
	for name, body := range files {
		if strings.Contains(body, "Other database question") {
			t.Errorf("%s holds a question on a database that was not chosen", name)
		}
	}
}

func TestFiltersNameTheirColumns(t *testing.T) {
	_, files := exportAll(t)
	// The schema export feeds field names to the content export in the same run.
	if !strings.Contains(files["Sample Database/questions/Finance/Reports/Q10 Orders by state.md"], "filters column PUBLIC.PEOPLE.STATE") {
		t.Error("the question's field filter does not name its column")
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"Sales DB":       "Sales DB",
		`a/b\c:d*e?f"g`:  "a_b_c_d_e_f_g",
		"  trailing. ":   "trailing",
		"":               "_",
		"<CON>|pipe":     "_CON__pipe",
		"s_20200001prod": "s_20200001prod",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
