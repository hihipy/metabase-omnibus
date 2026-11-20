// Command metabase-omnibus exports a Metabase instance into files built for AI review.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hihipy/metabase-omnibus/internal/content"
	"github.com/hihipy/metabase-omnibus/internal/metabase"
	"github.com/hihipy/metabase-omnibus/internal/schema"
	"github.com/hihipy/metabase-omnibus/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "dev"

const usage = `metabase-omnibus exports a Metabase instance into files built for AI review.

Usage:
  metabase-omnibus [flags]           export everything: database shape, then questions and dashboards
  metabase-omnibus schema [flags]    export only the shape of your databases
  metabase-omnibus content [flags]   export only questions, models, metrics, and dashboards
  metabase-omnibus version           print the version

Credentials come from environment variables, never from flags:
  METABASE_URL        https://your-metabase.example.com
  METABASE_API_KEY    preferred
  METABASE_USERNAME   with METABASE_PASSWORD, if you have no API key

Run "metabase-omnibus -h" for flags.
`

func main() {
	cmd, args := "all", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch cmd {
	case "all":
		err = run(ctx, args, true, true)
	case "schema":
		err = run(ctx, args, true, false)
	case "content":
		err = run(ctx, args, false, true)
	case "version":
		fmt.Println(version)
	case "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Bad("error: "+err.Error()))
		os.Exit(1)
	}
}

type flags struct {
	url, out, database, schema, collection, caBundle                           string
	all, plain, hidden, audit, text, personal, instance, rows, noCompile, http bool
	timeout                                                                    time.Duration
}

func run(ctx context.Context, args []string, doSchema, doContent bool) error {
	var f flags
	fs := flag.NewFlagSet("metabase-omnibus", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage+"\nFlags:\n"); fs.PrintDefaults() }
	fs.StringVar(&f.url, "url", os.Getenv("METABASE_URL"), "Metabase URL (default: $METABASE_URL)")
	fs.StringVar(&f.out, "out", "", "output folder (default: Downloads/metabase-omnibus-<host>)")
	fs.StringVar(&f.database, "database", "", "databases by ID, name, or glob, comma-separated; skips the database picker")
	fs.StringVar(&f.schema, "schema", "", `schemas by name or glob, comma-separated, e.g. "sales*"; skips the schema picker`)
	fs.StringVar(&f.collection, "collection", "", `collections by path glob, comma-separated, e.g. "*Finance*"; skips the collection picker`)
	fs.BoolVar(&f.all, "all", false, "export everything without asking (the default when there is no terminal)")
	fs.BoolVar(&f.plain, "plain", false, "use the typed picker instead of arrow keys")
	fs.BoolVar(&f.hidden, "include-hidden", false, "include tables and columns an admin hid in Metabase")
	fs.BoolVar(&f.audit, "include-audit", false, "include Metabase's own usage-analytics database")
	fs.BoolVar(&f.text, "include-descriptions", false, "include descriptions and dashboard text cards (free text; off by default)")
	fs.BoolVar(&f.personal, "include-personal", false, "include people's personal collections (off by default)")
	fs.BoolVar(&f.instance, "include-instance", false, "name the Metabase host inside each schema file (off by default)")
	fs.BoolVar(&f.rows, "row-counts", false, "include estimated row counts (a small count can identify a small group; off by default)")
	fs.BoolVar(&f.noCompile, "no-compile", false, "skip asking Metabase for the SQL behind query-builder questions")
	fs.StringVar(&f.caBundle, "ca-bundle", "", "PEM file with your organization's CA certificate")
	fs.BoolVar(&f.http, "allow-http", false, "allow plain http:// (local test servers only)")
	fs.DurationVar(&f.timeout, "timeout", 60*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if f.url == "" {
		return errors.New("set METABASE_URL or pass -url")
	}
	ask := !f.all && ui.Interactive()
	out := os.Stderr // prompts and progress

	client, err := metabase.New(ctx, metabase.Config{
		URL:       f.url,
		APIKey:    os.Getenv("METABASE_API_KEY"),
		Username:  os.Getenv("METABASE_USERNAME"),
		Password:  os.Getenv("METABASE_PASSWORD"),
		CABundle:  f.caBundle,
		AllowHTTP: f.http,
		Timeout:   f.timeout,
	})
	if err != nil {
		return err
	}
	defer client.Close(context.Background())

	root := f.out
	if root == "" {
		root = defaultOut(client.Host())
	}
	idx := &index{}

	// Database names label questions, and the schema export's field map lets
	// the content export name the column each filter points at.
	dbs, err := schema.ListDatabases(ctx, client, f.audit)
	if err != nil {
		return err
	}
	dbNames := map[int]string{}
	for _, d := range dbs {
		dbNames[d.ID] = d.Name
	}
	fieldNames := map[int]string{}

	if doSchema {
		metas, cancelled, err := exportSchema(ctx, client, f, ask, dbs, filepath.Join(root, "schema"), idx, out)
		if err != nil {
			return err
		}
		if cancelled {
			fmt.Fprintln(out, ui.Notice("schema export cancelled"))
		}
		for _, m := range metas {
			for id, name := range m.FieldNames() {
				fieldNames[id] = name
			}
		}
	}
	if doContent {
		cancelled, err := exportContent(ctx, client, f, ask, dbNames, fieldNames, filepath.Join(root, "content"), idx, out)
		if err != nil {
			return err
		}
		if cancelled {
			fmt.Fprintln(out, ui.Notice("content export cancelled"))
		}
	}

	if idx.empty() {
		fmt.Fprintln(out, ui.Notice("nothing written"))
		return nil
	}
	if err := writeFile(filepath.Join(root, "index.md"), func(w io.Writer) error {
		_, err := io.WriteString(w, idx.markdown(client.Host(), f.instance))
		return err
	}); err != nil {
		return err
	}
	fmt.Fprintln(out, ui.Good("Wrote "+root))
	fmt.Fprintln(out, ui.Quiet("Start with index.md: it lists every file and what it holds."))
	return nil
}

// ---------------------------------------------------------------- schema

func exportSchema(ctx context.Context, client *metabase.Client, f flags, ask bool, dbs []schema.Database,
	dir string, idx *index, out io.Writer) ([]*schema.Metadata, bool, error) {

	if len(dbs) == 0 {
		return nil, false, errors.New("these credentials can see no databases")
	}
	fmt.Fprintln(out, ui.Bold("\nDatabase shape"))
	switch {
	case f.database != "":
		var err error
		if dbs, err = matchDatabases(dbs, f.database); err != nil {
			return nil, false, err
		}
	case ask && len(dbs) > 1:
		items := make([]ui.Item, len(dbs))
		for i, d := range dbs {
			items[i] = ui.Item{Name: d.Name, Detail: d.Engine, Count: "#" + strconv.Itoa(d.ID)}
		}
		picked, err := ui.Pick(items, "databases", f.plain, out)
		if err != nil || len(picked) == 0 {
			return nil, err == nil, err
		}
		chosen := make([]schema.Database, len(picked))
		for i, j := range picked {
			chosen[i] = dbs[j]
		}
		dbs = chosen
	}

	metas := make([]*schema.Metadata, 0, len(dbs))
	for i, d := range dbs {
		ui.Progress(i, len(dbs), "databases", d.Name)
		m, err := schema.FetchMetadata(ctx, client, d, f.hidden)
		if err != nil {
			fmt.Fprintln(out)
			return nil, false, err
		}
		metas = append(metas, m)
	}
	ui.Progress(len(dbs), len(dbs), "databases", "done")

	type choice struct {
		meta   int
		schema string
	}
	var choices []choice
	var items []ui.Item
	for mi, m := range metas {
		for _, s := range m.Schemas() {
			name := s.Name
			if len(metas) > 1 {
				name = m.DB.Name + "/" + s.Name
			}
			choices = append(choices, choice{mi, s.Name})
			items = append(items, ui.Item{Name: name, Count: plural(s.Tables, "table")})
		}
	}
	if len(choices) == 0 {
		return metas, false, errors.New("the chosen databases have no tables these credentials can see")
	}
	chosen := make([]int, len(choices))
	for i := range chosen {
		chosen[i] = i
	}
	switch {
	case f.schema != "":
		chosen = chosen[:0]
		for i, c := range choices {
			if matchAny(f.schema, c.schema) {
				chosen = append(chosen, i)
			}
		}
		if len(chosen) == 0 {
			return metas, false, fmt.Errorf("no schema matches %q", f.schema)
		}
	case ask && len(choices) > 1:
		var err error
		if chosen, err = ui.Pick(items, "schemas", f.plain, out); err != nil || len(chosen) == 0 {
			return metas, err == nil, err
		}
	}

	opt := schema.Options{IncludeDescriptions: f.text, RowCounts: f.rows}
	if f.instance {
		opt.Instance = client.Host()
	}
	for _, i := range chosen {
		c := choices[i]
		m := metas[c.meta]
		doc := schema.Build(m, c.schema, opt)
		rel := filepath.Join(safeName(m.DB.Name), safeName(c.schema)+".json")
		if err := os.MkdirAll(filepath.Join(dir, safeName(m.DB.Name)), 0o755); err != nil {
			return metas, false, err
		}
		if err := writeFile(filepath.Join(dir, rel), func(w io.Writer) error { return schema.WriteDoc(w, doc) }); err != nil {
			return metas, false, err
		}
		idx.schemas = append(idx.schemas, indexRow{
			file: filepath.ToSlash(filepath.Join("schema", rel)),
			what: fmt.Sprintf("%s, schema %s: %s", m.DB.Name, c.schema, plural(len(doc.Tables), "table")),
		})
	}
	fmt.Fprintf(out, "  %s\n", plural(len(chosen), "schema file"))
	return metas, false, nil
}

// ---------------------------------------------------------------- content

func exportContent(ctx context.Context, client *metabase.Client, f flags, ask bool, dbNames, fieldNames map[int]string,
	dir string, idx *index, out io.Writer) (bool, error) {

	fmt.Fprintln(out, ui.Bold("\nQuestions and dashboards"))
	cat, err := content.List(ctx, client, f.personal)
	if err != nil {
		return false, err
	}

	var cols []content.Collection
	var items []ui.Item
	for _, c := range cat.Collections {
		cards, dashes := cat.Counts(c.ID)
		if cards+dashes == 0 {
			continue
		}
		cols = append(cols, c)
		items = append(items, ui.Item{Name: c.Path, Count: plural(dashes, "dashboard") + ", " + plural(cards, "question")})
	}
	if len(cols) == 0 {
		fmt.Fprintln(out, ui.Notice("  no questions or dashboards visible to these credentials"))
		return false, nil
	}

	chosen := make([]int, len(cols))
	for i := range chosen {
		chosen[i] = i
	}
	switch {
	case f.collection != "":
		chosen = chosen[:0]
		for i, c := range cols {
			if matchAny(f.collection, c.Path) {
				chosen = append(chosen, i)
			}
		}
		if len(chosen) == 0 {
			return false, fmt.Errorf("no collection matches %q", f.collection)
		}
	case ask && len(cols) > 1:
		if chosen, err = ui.Pick(items, "collections", f.plain, out); err != nil || len(chosen) == 0 {
			return err == nil, err
		}
	}
	keep := map[int]bool{}
	for _, i := range chosen {
		keep[cols[i].ID] = true
	}

	files, snippets, err := content.Export(ctx, client, cat, keep, content.Options{
		IncludeText: f.text,
		NoCompile:   f.noCompile,
		FieldNames:  fieldNames,
		DBNames:     dbNames,
		Progress:    func(done, total int, label string) { ui.Progress(done, total, "items", label) },
	})
	if err != nil {
		fmt.Fprintln(out)
		return false, err
	}

	used := map[string]bool{}
	for _, file := range files {
		parts := make([]string, len(file.Collection.Parts))
		for i, p := range file.Collection.Parts {
			parts[i] = safeName(p)
		}
		rel := filepath.Join(parts...) + ".md"
		if used[rel] { // two sibling collections with the same name
			rel = strings.TrimSuffix(rel, ".md") + fmt.Sprintf(" (#%d).md", file.Collection.ID)
		}
		used[rel] = true
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			return false, err
		}
		md := file.Markdown
		if err := writeFile(filepath.Join(dir, rel), func(w io.Writer) error { _, err := io.WriteString(w, md); return err }); err != nil {
			return false, err
		}
		idx.content = append(idx.content, indexRow{
			file: filepath.ToSlash(filepath.Join("content", rel)),
			what: fmt.Sprintf("%s: %s, %s", file.Collection.Path, plural(file.Dashboards, "dashboard"), plural(file.Cards, "question")),
		})
	}
	if snippets != "" {
		if err := writeFile(filepath.Join(dir, "_snippets.md"), func(w io.Writer) error { _, err := io.WriteString(w, snippets); return err }); err != nil {
			return false, err
		}
		idx.content = append(idx.content, indexRow{file: "content/_snippets.md", what: "SQL snippets the exported questions use"})
	}
	fmt.Fprintf(out, "  %s\n", plural(len(files), "collection file"))
	return false, nil
}

// ---------------------------------------------------------------- index

type indexRow struct{ file, what string }

type index struct{ schemas, content []indexRow }

func (x *index) empty() bool { return len(x.schemas)+len(x.content) == 0 }

func (x *index) markdown(host string, showHost bool) string {
	var b strings.Builder
	b.WriteString("# Metabase Export\n\n")
	if showHost {
		fmt.Fprintf(&b, "Source: %s\n\n", host)
	}
	fmt.Fprintf(&b, "Generated %s by metabase-omnibus. Definitions and structure only: no data values and no query results.\n\n",
		time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("Give an assistant only the files a question needs. Schema files describe tables and columns; content files hold questions, their SQL, and dashboards.\n\n")
	section := func(title string, rows []indexRow) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n| File | Holds |\n| --- | --- |\n", title)
		for _, r := range rows {
			fmt.Fprintf(&b, "| %s | %s |\n", strings.ReplaceAll(r.file, "|", `\|`), strings.ReplaceAll(r.what, "|", `\|`))
		}
		b.WriteString("\n")
	}
	section("Database Shape", x.schemas)
	section("Questions and Dashboards", x.content)
	return b.String()
}

// ---------------------------------------------------------------- helpers

func writeFile(path string, write func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// safeName turns a name into a folder or file name that works on Windows,
// macOS, and Linux.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if s == "" {
		return "_"
	}
	return s
}

// defaultOut puts the export folder in Downloads, or the current folder when
// there is none.
func defaultOut(host string) string {
	name := "metabase-omnibus-" + safeName(host)
	if home, err := os.UserHomeDir(); err == nil {
		dl := filepath.Join(home, "Downloads")
		if info, err := os.Stat(dl); err == nil && info.IsDir() {
			return filepath.Join(dl, name)
		}
	}
	return name
}

func matchDatabases(dbs []schema.Database, list string) ([]schema.Database, error) {
	var out []schema.Database
	for _, p := range splitList(list) {
		matched := false
		for _, d := range dbs {
			if strconv.Itoa(d.ID) == p || ui.MatchName(p, d.Name) {
				out = append(out, d)
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("no database matches %q: check the name or ID and your group's access", p)
		}
	}
	return out, nil
}

func matchAny(list, s string) bool {
	for _, p := range splitList(list) {
		if ui.MatchName(p, s) {
			return true
		}
	}
	return false
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
