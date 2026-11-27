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
	"unicode/utf8"

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
  METABASE_SESSION    a browser session token, only if you sign in through single sign-on

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
	url, out, database, schema, collection, caBundle, schemaLayout             string
	all, plain, hidden, audit, text, personal, instance, rows, noCompile, http bool
	workers                                                                    int
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
	fs.StringVar(&f.schemaLayout, "schema-layout", "tables", `"tables" writes one file per table with an index; "single" writes one file per schema`)
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
	fs.IntVar(&f.workers, "workers", 4, "requests to Metabase at once")
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
	if f.schemaLayout != "tables" && f.schemaLayout != "single" {
		return fmt.Errorf("-schema-layout must be tables or single, not %q", f.schemaLayout)
	}
	out := os.Stderr // prompts and progress

	client, err := metabase.New(ctx, metabase.Config{
		URL:       f.url,
		APIKey:    os.Getenv("METABASE_API_KEY"),
		Session:   os.Getenv("METABASE_SESSION"),
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
	var failed []failedDB

	// 2. One folder per database.
	for _, d := range chosen {
		fmt.Fprintln(out, ui.Bold("\n"+d.Name))
		ex := &dbExport{db: d, folder: safeName(d.Name)}
		dir := filepath.Join(root, ex.folder)

		// The metadata also names the column behind each question's filters.
		meta, err := schema.FetchMetadata(ctx, client, d, f.hidden)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintln(out, ui.Bad("  could not read this database, so it was skipped: "+err.Error()))
			failed = append(failed, failedDB{d.Name, err.Error()})
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
	if err := writeText(filepath.Join(root, "index.md"), rootIndex(exports, failed, client.Host(), f.instance)); err != nil {
		return err
	}
	files, toks, skipped := 0, 0, 0
	for _, ex := range exports {
		files += ex.files()
		toks += ex.tokens()
		skipped += len(ex.skipped)
	}
	if skipped+len(failed) > 0 {
		fmt.Fprintln(out, ui.Notice(fmt.Sprintf("\n%s and %s could not be exported. The indexes list each one and why.",
			plural(len(failed), "database"), plural(skipped, "item"))))
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

// maxName caps a folder or file name in bytes. macOS and Linux refuse names
// over 255 bytes, and an item's file name adds an ID and an extension to it.
const maxName = 200

// safeName turns a name into a folder or file name that works on Windows,
// macOS, and Linux: no characters any of them refuse, no leading or trailing
// space or dot, at most maxName bytes, and never a name Windows reserves for
// a device, such as CON or NUL.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if len(s) > maxName {
		cut := maxName
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = strings.Trim(s[:cut], " .")
	}
	if s == "" {
		return "_"
	}
	if reservedName(s) {
		// CON.json becomes CON_.json, keeping any extension.
		i := strings.IndexByte(s, '.')
		if i < 0 {
			i = len(s)
		}
		s = s[:i] + "_" + s[i:]
	}
	return s
}

// reservedName reports whether Windows treats a name as a device. The test
// is on the part before the first dot, so CON.json is reserved too.
func reservedName(s string) bool {
	base, _, _ := strings.Cut(s, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
		base[3] >= '1' && base[3] <= '9'
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
