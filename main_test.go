package main

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"schema/Sample Database/PUBLIC.json",
		"schema/Sample Database/ANALYTICS.json",
		"content/Our analytics/Finance.md",
		"content/Our analytics/Finance/Reports.md",
		"content/_snippets.md",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; wrote %v", want, keys(files))
		}
	}
	for name := range files {
		if strings.Contains(name, "Personal") || strings.Contains(name, "CANARY") {
			t.Errorf("wrote a file for a personal collection: %s", name)
		}
		if name != "index.md" && !strings.Contains(files["index.md"], name) {
			t.Errorf("index.md does not list %s", name)
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

func TestFiltersNameTheirColumns(t *testing.T) {
	_, files := exportAll(t)
	// The schema export feeds field names to the content export in the same run.
	if !strings.Contains(files["content/Our analytics/Finance/Reports.md"], "filters column PUBLIC.PEOPLE.STATE") {
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
