package main

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hihipy/metabase-omnibus/internal/fakemb"
)

// exportAll runs the whole command against the fake and returns every file it
// wrote, keyed by path relative to the output folder.
func exportAll(t *testing.T, extra ...string) (*fakemb.Server, map[string]string) {
	t.Helper()
	srv := fakemb.New(t)
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
	return srv, files
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
