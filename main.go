// Command metabase-omnibus exports a Metabase instance into files built for AI review.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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
  metabase-omnibus [flags]           export everything for the databases you pick
  metabase-omnibus schema [flags]    export only the shape of those databases
  metabase-omnibus content [flags]   export only the questions and dashboards built on them
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
	fs.StringVar(&f.database, "database", "", "databases by ID, name, or pattern, comma-separated; skips the picker")
	fs.StringVar(&f.schema, "schema", "", `only schemas matching these names or patterns, e.g. "sales*"`)
	fs.StringVar(&f.collection, "collection", "", `only collections whose path matches these patterns, e.g. "*Finance*"`)
	fs.BoolVar(&f.all, "all", false, "export every database without asking (the default when there is no terminal)")
	fs.BoolVar(&f.plain, "plain", false, "type your choice instead of using the interactive list")
	fs.BoolVar(&f.hidden, "include-hidden", false, "include tables and columns an admin hid in Metabase")
	fs.BoolVar(&f.audit, "include-audit", false, "include Metabase's own usage-analytics database")
	fs.BoolVar(&f.text, "include-descriptions", false, "include descriptions and dashboard text cards (free text; off by default)")
	fs.BoolVar(&f.personal, "include-personal", false, "include people's personal collections (off by default)")
	fs.BoolVar(&f.instance, "include-instance", false, "name the Metabase host inside the files (off by default)")
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

	// 1. Pick databases. Everything else follows from them: their schemas, and
	// the collections holding questions that query them.
	dbs, err := schema.ListDatabases(ctx, client, f.audit)
	if err != nil {
		return err
	}
	if len(dbs) == 0 {
		return errors.New("these credentials can see no databases")
	}
	chosen, err := pickDatabases(dbs, f, client.Host(), out)
	if err != nil || len(chosen) == 0 {
		if err == nil {
			fmt.Fprintln(out, ui.Notice("cancelled, nothing written"))
		}
		return err
	}

	var cat *content.Catalog
	if doContent {
		if cat, err = content.List(ctx, client, f.personal); err != nil {
			return err
		}
	}
	dbNames := map[int]string{}
	for _, d := range dbs {
		dbNames[d.ID] = d.Name
	}

	root := f.out
	if root == "" {
		root = defaultOut(client.Host(), time.Now())
	}
	var exports []*dbExport

	// 2. One folder per database.
	for _, d := range chosen {
		fmt.Fprintln(out, ui.Bold("\n"+d.Name))
		ex := &dbExport{db: d, folder: safeName(d.Name)}
		dir := filepath.Join(root, ex.folder)

		// The metadata also names the column behind each question's filters.
		meta, err := schema.FetchMetadata(ctx, client, d, f.hidden)
		if err != nil {
			fmt.Fprintln(out, ui.Bad("  skipped: "+err.Error()))
			continue
		}
		if doSchema {
			if err := writeSchema(meta, f, client.Host(), dir, ex, out); err != nil {
				return err
			}
		}
		if doContent {
			if err := writeContent(ctx, client, cat.ForDatabase(d.ID), meta.FieldNames(), dbNames, f, dir, ex, out); err != nil {
				return err
			}
		}
		if ex.empty() {
			continue
		}
		if err := ex.writeIndexes(dir); err != nil {
			return err
		}
		exports = append(exports, ex)
	}

	if len(exports) == 0 {
		fmt.Fprintln(out, ui.Notice("\nnothing written"))
		return nil
	}
	if err := writeText(filepath.Join(root, "index.md"), rootIndex(exports, client.Host(), f.instance)); err != nil {
		return err
	}
	files, toks := 0, 0
	for _, ex := range exports {
		files += ex.files()
		toks += ex.tokens()
	}
	fmt.Fprintln(out, ui.Good("\nWrote "+root))
	fmt.Fprintln(out, ui.Quiet(fmt.Sprintf("%s, about %s tokens in all. Start with index.md.", plural(files, "file"), thousands(toks))))
	return nil
}

func pickDatabases(dbs []schema.Database, f flags, host string, out io.Writer) ([]schema.Database, error) {
	switch {
	case f.database != "":
		return matchDatabases(dbs, f.database)
	case f.all || !ui.Interactive() || len(dbs) == 1:
		return dbs, nil
	}
	items := make([]ui.Item, len(dbs))
	for i, d := range dbs {
		items[i] = ui.Item{Name: d.Name, Detail: d.Engine, Count: "#" + strconv.Itoa(d.ID)}
	}
	fmt.Fprintln(out, ui.Bold(fmt.Sprintf("\n%s: %s", host, plural(len(dbs), "database"))))
	picked, err := ui.Pick(items, "databases", f.plain, out)
	if err != nil {
		return nil, err
	}
	chosen := make([]schema.Database, len(picked))
	for i, j := range picked {
		chosen[i] = dbs[j]
	}
	return chosen, nil
}

// ---------------------------------------------------------------- per database

// dbExport records what was written for one database, for its indexes.
type dbExport struct {
	db       schema.Database
	folder   string
	schemas  []schemaFile
	items    []itemFile
	snippets int // bytes in questions/_snippets.md, 0 when there is none
}

type schemaFile struct {
	name   string
	tables int
	bytes  int
}

type itemFile struct {
	content.Item
	dir   string // folder below questions/, "" for the root collection
	file  string // file name inside dir
	bytes int
}

func (ex *dbExport) empty() bool { return ex.files() == 0 }

func (ex *dbExport) files() int {
	n := len(ex.schemas) + len(ex.items)
	if ex.snippets > 0 {
		n++
	}
	return n
}

func (ex *dbExport) tokens() int {
	n := tokens(ex.snippets)
	for _, s := range ex.schemas {
		n += tokens(s.bytes)
	}
	for _, it := range ex.items {
		n += tokens(it.bytes)
	}
	return n
}

func writeSchema(meta *schema.Metadata, f flags, host, dir string, ex *dbExport, out io.Writer) error {
	opt := schema.Options{IncludeDescriptions: f.text, RowCounts: f.rows}
	if f.instance {
		opt.Instance = host
	}
	for _, s := range meta.Schemas() {
		if f.schema != "" && !matchAny(f.schema, s.Name) {
			continue
		}
		doc := schema.Build(meta, s.Name, opt)
		size, err := writeCounted(filepath.Join(dir, "schema", safeName(s.Name)+".json"),
			func(w io.Writer) error { return schema.WriteDoc(w, doc) })
		if err != nil {
			return err
		}
		ex.schemas = append(ex.schemas, schemaFile{s.Name, len(doc.Tables), size})
	}
	fmt.Fprintf(out, "  %s\n", plural(len(ex.schemas), "schema file"))
	return nil
}

func writeContent(ctx context.Context, client *metabase.Client, cat *content.Catalog, fieldNames, dbNames map[int]string,
	f flags, dir string, ex *dbExport, out io.Writer) error {

	keep := map[int]bool{}
	for _, c := range cat.Collections {
		cards, dashes := cat.Counts(c.ID)
		if cards+dashes > 0 && (f.collection == "" || matchAny(f.collection, c.Path)) {
			keep[c.ID] = true
		}
	}
	if len(keep) == 0 {
		fmt.Fprintln(out, ui.Quiet("  no questions or dashboards"))
		return nil
	}

	items, snippets, err := content.Export(ctx, client, cat, keep, content.Options{
		IncludeText: f.text,
		NoCompile:   f.noCompile,
		FieldNames:  fieldNames,
		DBNames:     dbNames,
		Progress:    func(done, total int, label string) { ui.Progress(done, total, "items", label) },
	})
	if err != nil {
		return err
	}

	for _, it := range items {
		sub := collectionDir(it.Collection)
		name := safeName(it.Label+" "+it.Name) + ".md"
		md := it.Markdown
		size, err := writeCounted(filepath.Join(dir, "questions", sub, name), func(w io.Writer) error { _, err := io.WriteString(w, md); return err })
		if err != nil {
			return err
		}
		ex.items = append(ex.items, itemFile{Item: it, dir: filepath.ToSlash(sub), file: name, bytes: size})
	}
	if snippets != "" {
		size, err := writeCounted(filepath.Join(dir, "questions", "_snippets.md"), func(w io.Writer) error { _, err := io.WriteString(w, snippets); return err })
		if err != nil {
			return err
		}
		ex.snippets = size
	}
	fmt.Fprintf(out, "  %s\n", plural(len(items), "question and dashboard file"))
	return nil
}

// collectionDir maps a collection to its folder below questions/: the root
// collection is questions/ itself, and the rest follow the collection tree.
func collectionDir(c content.Collection) string {
	if len(c.Parts) <= 1 {
		return ""
	}
	safe := make([]string, len(c.Parts)-1)
	for i, p := range c.Parts[1:] {
		safe[i] = safeName(p)
	}
	return filepath.Join(safe...)
}

// ---------------------------------------------------------------- indexes

// Every folder gets an index.md: the export root lists databases, each
// database lists its schemas and every question, and each collection folder
// lists what it holds and the collections below it.

func rootIndex(exports []*dbExport, host string, showHost bool) string {
	var b strings.Builder
	b.WriteString("# Metabase Export\n\n")
	if showHost {
		fmt.Fprintf(&b, "Source: %s\n\n", host)
	}
	fmt.Fprintf(&b, "Generated %s by metabase-omnibus. %s\n\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"), content.FileNote)
	b.WriteString("One folder per database, each with its own index. Give an assistant only the files a question needs. Token counts are estimates.\n\n")
	b.WriteString("| Database | Engine | Schemas | Questions | Dashboards | Tokens |\n| --- | --- | ---: | ---: | ---: | ---: |\n")
	for _, ex := range exports {
		q, d := ex.counts()
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %s |\n",
			link(ex.db.Name, ex.folder+"/index.md"), cell(ex.db.Engine), len(ex.schemas), q, d, thousands(ex.tokens()))
	}
	return b.String()
}

func (ex *dbExport) counts() (questions, dashboards int) {
	for _, it := range ex.items {
		if it.Kind == "dashboard" {
			dashboards++
		} else {
			questions++
		}
	}
	return
}

// folderNode is one collection folder below questions/.
type folderNode struct {
	path     string // "" for questions/ itself
	title    string // the collection's own name
	items    []itemFile
	children map[string]bool
}

func (ex *dbExport) folders() map[string]*folderNode {
	nodes := map[string]*folderNode{"": {path: "", title: "Our analytics", children: map[string]bool{}}}
	var ensure func(path, title string) *folderNode
	ensure = func(path, title string) *folderNode {
		if n, ok := nodes[path]; ok {
			return n
		}
		n := &folderNode{path: path, title: title, children: map[string]bool{}}
		nodes[path] = n
		parent := ""
		if i := strings.LastIndex(path, "/"); i >= 0 {
			parent = path[:i]
		}
		ensure(parent, "").children[path] = true
		return n
	}
	for _, it := range ex.items {
		parts := it.Collection.Parts
		path := ""
		for i := 1; i < len(parts); i++ {
			if path != "" {
				path += "/"
			}
			path += safeName(parts[i])
			if n := ensure(path, parts[i]); n.title == "" {
				n.title = parts[i]
			}
		}
		nodes[it.dir].items = append(nodes[it.dir].items, it)
	}
	return nodes
}

func (ex *dbExport) writeIndexes(dir string) error {
	nodes := ex.folders()
	if len(ex.items) > 0 {
		if err := ex.writeCatalog(filepath.Join(dir, "catalog.csv")); err != nil {
			return err
		}
		for path, n := range nodes {
			if err := writeText(filepath.Join(dir, "questions", filepath.FromSlash(path), "index.md"), ex.folderIndex(n, nodes)); err != nil {
				return err
			}
		}
	}
	return writeText(filepath.Join(dir, "index.md"), ex.dbIndex(nodes))
}

func (ex *dbExport) dbIndex(nodes map[string]*folderNode) string {
	var b strings.Builder
	q, d := ex.counts()
	fmt.Fprintf(&b, "# %s\n\nEngine: %s. %s, %s, %s, about %s tokens.\n\n%s\n\n[Up to all databases](<../index.md>)\n\n",
		ex.db.Name, ex.db.Engine, plural(len(ex.schemas), "schema"), plural(q, "question"), plural(d, "dashboard"),
		thousands(ex.tokens()), content.FileNote)

	if len(ex.schemas) > 0 {
		b.WriteString("## Schemas\n\nThe shape of each schema: tables, columns, types, and keys.\n\n| Schema | Tables | Tokens |\n| --- | ---: | ---: |\n")
		for _, s := range ex.schemas {
			fmt.Fprintf(&b, "| %s | %d | %s |\n", link(s.name, "schema/"+safeName(s.name)+".json"), s.tables, thousands(tokens(s.bytes)))
		}
		b.WriteString("\n")
	}
	if len(ex.items) > 0 {
		b.WriteString("## Collections\n\n")
		ex.writeTree(&b, nodes, "", 0)
		if ex.snippets > 0 {
			fmt.Fprintf(&b, "\nSQL snippets these questions use: %s.\n", link("_snippets.md", "questions/_snippets.md"))
		}
		b.WriteString("\n## Every Question and Dashboard\n\n")
		fmt.Fprintf(&b, "The same list as a spreadsheet, for Excel or a script: %s.\n\n", link("catalog.csv", "catalog.csv"))
		itemTable(&b, ex.items, "questions/", true)
	}
	return b.String()
}

func (ex *dbExport) writeTree(b *strings.Builder, nodes map[string]*folderNode, path string, depth int) {
	n := nodes[path]
	q, d := 0, 0
	for _, it := range n.items {
		if it.Kind == "dashboard" {
			d++
		} else {
			q++
		}
	}
	target := "questions/index.md"
	if path != "" {
		target = "questions/" + path + "/index.md"
	}
	fmt.Fprintf(b, "%s- %s: %s, %s\n", strings.Repeat("  ", depth), link(n.title, target), plural(d, "dashboard"), plural(q, "question"))
	for _, c := range sortedKeys(n.children) {
		ex.writeTree(b, nodes, c, depth+1)
	}
}

func (ex *dbExport) folderIndex(n *folderNode, nodes map[string]*folderNode) string {
	var b strings.Builder
	title := n.title
	if len(n.items) > 0 {
		title = n.items[0].Collection.Path
	}
	// questions/index.md goes up to the database index; any deeper folder
	// goes up to its parent collection's index. Both are ../index.md.
	fmt.Fprintf(&b, "# %s\n\nDatabase: %s.\n\n%s\n\n[Up](<../index.md>)\n\n", title, ex.db.Name, content.FileNote)
	if kids := sortedKeys(n.children); len(kids) > 0 {
		b.WriteString("## Collections Inside\n\n")
		for _, c := range kids {
			name := c[strings.LastIndex(c, "/")+1:]
			fmt.Fprintf(&b, "- %s\n", link(nodes[c].title, name+"/index.md"))
		}
		b.WriteString("\n")
	}
	if len(n.items) > 0 {
		b.WriteString("## In This Collection\n\n")
		itemTable(&b, n.items, "", false)
	}
	if n.path == "" && ex.snippets > 0 {
		fmt.Fprintf(&b, "\nSQL snippets these questions use: %s.\n", link("_snippets.md", "_snippets.md"))
	}
	return b.String()
}

// itemTable lists items with links relative to the index that holds it.
func itemTable(b *strings.Builder, items []itemFile, prefix string, withCollection bool) {
	if withCollection {
		b.WriteString("| ID | Name | Type | Collection | On dashboards | SQL | Updated | Tokens |\n| --- | --- | --- | --- | --- | --- | --- | ---: |\n")
	} else {
		b.WriteString("| ID | Name | Type | On dashboards | SQL | Updated | Tokens |\n| --- | --- | --- | --- | --- | --- | ---: |\n")
	}
	for _, it := range items {
		target := prefix + it.file
		if it.dir != "" {
			target = prefix + it.dir + "/" + it.file
		}
		if !withCollection && it.dir != "" {
			target = it.file // a folder index sits beside its items
		}
		kind := it.Kind
		if it.Display != "" {
			kind += ", " + it.Display
		}
		if it.Kind == "dashboard" {
			kind += fmt.Sprintf(", %s", plural(it.Cards, "card"))
		}
		dash := strings.Join(it.Dashboards, "; ")
		row := []string{it.Label, link(it.Name, target), cell(kind)}
		if withCollection {
			row = append(row, cell(strings.TrimPrefix(it.Collection.Path, "Our analytics > ")))
		}
		row = append(row, cell(dash), cell(it.SQL), cell(it.Updated), thousands(tokens(it.bytes)))
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
	}
}

// catalogHeader names the catalog's columns. collection_id and id are what a
// script needs to act on a row; the rest is for people reading it.
var catalogHeader = []string{
	"database", "database_id", "kind", "id", "label", "name", "collection_id", "collection_path",
	"display", "sql", "updated", "on_dashboards", "file", "tokens",
}

// writeCatalog writes every question and dashboard as one CSV row. It starts
// with a UTF-8 byte-order mark so Excel reads accented names correctly.
func (ex *dbExport) writeCatalog(path string) error {
	_, err := writeCounted(path, func(w io.Writer) error {
		if _, err := io.WriteString(w, "\ufeff"); err != nil {
			return err
		}
		cw := csv.NewWriter(w)
		if err := cw.Write(catalogHeader); err != nil {
			return err
		}
		for _, it := range ex.items {
			file := "questions/" + it.file
			if it.dir != "" {
				file = "questions/" + it.dir + "/" + it.file
			}
			row := []string{
				ex.db.Name, strconv.Itoa(ex.db.ID), it.Kind, strconv.Itoa(it.ID), it.Label, it.Name,
				strconv.Itoa(it.Collection.ID), it.Collection.Path, it.Display, it.SQL, it.Updated,
				strings.Join(it.Dashboards, "; "), file, strconv.Itoa(tokens(it.bytes)),
			}
			for i := range row {
				row[i] = csvSafe(row[i])
			}
			if err := cw.Write(row); err != nil {
				return err
			}
		}
		cw.Flush()
		return cw.Error()
	})
	return err
}

// csvSafe stops a spreadsheet from running a cell as a formula. Names come
// from whoever wrote them in Metabase, and Excel treats a cell starting with
// = + - @ (or a tab or carriage return) as a formula, which is a known way to
// attack whoever opens the file. A leading apostrophe makes Excel show the
// text as written; a script reading the file back strips it.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// tokens estimates how much of an AI assistant's reading capacity a file
// uses: about one token per four characters of English or code.
func tokens(bytes int) int { return (bytes + 3) / 4 }

// link writes a Markdown link whose target may hold spaces.
func link(text, target string) string {
	text = strings.NewReplacer("|", `\|`, "[", `\[`, "]", `\]`).Replace(text)
	return "[" + text + "](<" + target + ">)"
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- helpers

func writeText(path, text string) error {
	_, err := writeCounted(path, func(w io.Writer) error { _, err := io.WriteString(w, text); return err })
	return err
}

// writeCounted writes a file, creating its folders, and reports its size.
func writeCounted(path string, write func(io.Writer) error) (int, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	cw := &countWriter{w: f}
	if err := write(cw); err != nil {
		f.Close()
		return 0, err
	}
	return cw.n, f.Close()
}

type countWriter struct {
	w io.Writer
	n int
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
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

// defaultOut names the export folder after the instance and the time, such as
// metabase-acme-20261120-1925, and puts it in Downloads, or the current folder
// when there is none. A second run in the same minute gets a suffix.
func defaultOut(host string, now time.Time) string {
	name := "metabase-" + shortHost(host) + "-" + now.Format("20060102-1504")
	base := "."
	if home, err := os.UserHomeDir(); err == nil {
		dl := filepath.Join(home, "Downloads")
		if info, err := os.Stat(dl); err == nil && info.IsDir() {
			base = dl
		}
	}
	path := filepath.Join(base, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
		path = filepath.Join(base, fmt.Sprintf("%s-%d", name, i))
	}
}

// shortHost keeps the part of a host name that tells instances apart:
// metabase.acme.com gives acme, and acme.metabaseapp.com gives acme.
func shortHost(host string) string {
	host = strings.ToLower(strings.Split(host, ":")[0])
	for _, label := range strings.Split(host, ".") {
		if label != "" && label != "www" && label != "metabase" && label != "bi" && label != "analytics" {
			return safeName(label)
		}
	}
	return "export"
}

func matchDatabases(dbs []schema.Database, list string) ([]schema.Database, error) {
	var out []schema.Database
	seen := map[int]bool{}
	for _, p := range splitList(list) {
		matched := false
		for _, d := range dbs {
			if strconv.Itoa(d.ID) == p || ui.MatchName(p, d.Name) {
				if !seen[d.ID] {
					out = append(out, d)
					seen[d.ID] = true
				}
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

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
