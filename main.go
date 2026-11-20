// Command metabase-omnibus exports a Metabase instance into files built for AI review.
package main

import (
	"context"
	"encoding/json"
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

	"github.com/hihipy/metabase-omnibus/internal/metabase"
	"github.com/hihipy/metabase-omnibus/internal/schema"
	"github.com/hihipy/metabase-omnibus/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "dev"

const usage = `metabase-omnibus exports a Metabase instance into files built for AI review.

Usage:
  metabase-omnibus schema [flags]   export the shape of your databases, privacy-safe
  metabase-omnibus version          print the version

Credentials come from environment variables, never from flags:
  METABASE_URL        https://your-metabase.example.com
  METABASE_API_KEY    preferred
  METABASE_USERNAME   with METABASE_PASSWORD, if you have no API key

Run "metabase-omnibus schema -h" for schema flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch os.Args[1] {
	case "schema":
		err = runSchema(ctx, os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, ui.Bad("error: "+err.Error()))
		os.Exit(1)
	}
}

func runSchema(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("schema", flag.ContinueOnError)
	urlFlag := fs.String("url", os.Getenv("METABASE_URL"), "Metabase URL (default: $METABASE_URL)")
	outPath := fs.String("out", "", `output file, or "-" for the terminal (default: Downloads/metabase-schema-<host>.json)`)
	dbFlag := fs.String("database", "", "databases to export by ID, name, or glob, comma-separated; skips the database picker")
	schemaFlag := fs.String("schema", "", `schemas to export by name or glob, comma-separated, e.g. "sales*"; skips the schema picker`)
	all := fs.Bool("all", false, "export everything without asking (the default when there is no terminal)")
	plain := fs.Bool("plain", false, "use the typed picker instead of arrow keys")
	hidden := fs.Bool("include-hidden", false, "include tables and columns an admin hid in Metabase")
	audit := fs.Bool("include-audit", false, "include Metabase's own usage-analytics database")
	descriptions := fs.Bool("include-descriptions", false, "include table and column descriptions (free text; off by default)")
	rows := fs.Bool("row-counts", false, "include estimated row counts (a small count can identify a small group; off by default)")
	caBundle := fs.String("ca-bundle", "", "PEM file with your organization's CA certificate")
	allowHTTP := fs.Bool("allow-http", false, "allow plain http:// (local test servers only)")
	timeout := fs.Duration("timeout", 60*time.Second, "per-request timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *urlFlag == "" {
		return errors.New("set METABASE_URL or pass -url")
	}
	ask := !*all && ui.Interactive()
	out := os.Stderr // prompts and progress, kept off stdout so "-out -" stays clean

	client, err := metabase.New(ctx, metabase.Config{
		URL:       *urlFlag,
		APIKey:    os.Getenv("METABASE_API_KEY"),
		Username:  os.Getenv("METABASE_USERNAME"),
		Password:  os.Getenv("METABASE_PASSWORD"),
		CABundle:  *caBundle,
		AllowHTTP: *allowHTTP,
		Timeout:   *timeout,
	})
	if err != nil {
		return err
	}
	defer client.Close(context.Background())

	// 1. Databases.
	dbs, err := schema.ListDatabases(ctx, client, *audit)
	if err != nil {
		return err
	}
	if len(dbs) == 0 {
		return errors.New("these credentials can see no databases")
	}
	switch {
	case *dbFlag != "":
		dbs, err = matchDatabases(dbs, *dbFlag)
		if err != nil {
			return err
		}
	case ask && len(dbs) > 1:
		fmt.Fprintln(out, ui.Bold(fmt.Sprintf("\n%s: %d databases", client.Host(), len(dbs))))
		items := make([]ui.Item, len(dbs))
		for i, d := range dbs {
			items[i] = ui.Item{Name: d.Name, Detail: d.Engine, Count: "#" + strconv.Itoa(d.ID)}
		}
		idx, err := ui.Pick(items, "databases", *plain, out)
		if err != nil {
			return err
		}
		if len(idx) == 0 {
			fmt.Fprintln(out, ui.Notice("cancelled, nothing written"))
			return nil
		}
		dbs = pickDBs(dbs, idx)
	}

	// 2. Metadata for each chosen database.
	metas := make([]*schema.Metadata, 0, len(dbs))
	for i, d := range dbs {
		ui.Progress(i, len(dbs), "databases", d.Name)
		m, err := schema.FetchMetadata(ctx, client, d, *hidden)
		if err != nil {
			fmt.Fprintln(out)
			return err
		}
		metas = append(metas, m)
	}
	ui.Progress(len(dbs), len(dbs), "databases", "done")

	// 3. Schemas.
	type pick struct {
		meta   int
		schema string
	}
	var choices []pick
	var items []ui.Item
	for mi, m := range metas {
		for _, s := range m.Schemas() {
			name := s.Name
			if len(metas) > 1 {
				name = m.DB.Name + "/" + s.Name
			}
			choices = append(choices, pick{mi, s.Name})
			items = append(items, ui.Item{Name: name, Count: plural(s.Tables, "table")})
		}
	}
	if len(choices) == 0 {
		return errors.New("the chosen databases have no tables these credentials can see")
	}
	chosen := make([]int, len(choices))
	for i := range chosen {
		chosen[i] = i
	}
	switch {
	case *schemaFlag != "":
		chosen = chosen[:0]
		patterns := splitList(*schemaFlag)
		for i, c := range choices {
			for _, p := range patterns {
				if ui.MatchName(p, c.schema) {
					chosen = append(chosen, i)
					break
				}
			}
		}
		if len(chosen) == 0 {
			return fmt.Errorf("no schema matches %q", *schemaFlag)
		}
	case ask && len(choices) > 1:
		fmt.Fprintln(out, ui.Bold(fmt.Sprintf("\n%d schemas", len(choices))))
		chosen, err = ui.Pick(items, "schemas", *plain, out)
		if err != nil {
			return err
		}
		if len(chosen) == 0 {
			fmt.Fprintln(out, ui.Notice("cancelled, nothing written"))
			return nil
		}
	}

	keep := make([]map[string]bool, len(metas))
	for i := range keep {
		keep[i] = map[string]bool{}
	}
	for _, i := range chosen {
		keep[choices[i].meta][choices[i].schema] = true
	}

	// 4. Build and write.
	opt := schema.Options{Instance: client.Host(), IncludeDescriptions: *descriptions, RowCounts: *rows}
	var docs []schema.Doc
	for i, m := range metas {
		if len(keep[i]) > 0 {
			docs = append(docs, schema.Build(m, keep[i], opt))
		}
	}

	path := *outPath
	if path == "" {
		path = defaultOut(client.Host())
	}
	if err := writeJSON(path, docs); err != nil {
		return err
	}

	fmt.Fprintln(out)
	for _, d := range docs {
		fmt.Fprintf(out, "  %s: %s, %s\n", d.Metadata.Database,
			plural(len(d.Metadata.Schemas), "schema"), plural(len(d.Tables), "table"))
	}
	if path != "-" {
		fmt.Fprintln(out, ui.Good("Wrote "+path))
	}
	return nil
}

// writeJSON writes one database as a single sql-x-ray document, or several as an array.
func writeJSON(path string, docs []schema.Doc) error {
	var w io.Writer = os.Stdout
	if path != "-" {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if len(docs) == 1 {
		return enc.Encode(docs[0])
	}
	return enc.Encode(docs)
}

// defaultOut puts the export in the Downloads folder, or the current folder
// when there is none.
func defaultOut(host string) string {
	name := "metabase-schema-" + strings.NewReplacer(":", "-", "/", "-").Replace(host) + ".json"
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

func pickDBs(dbs []schema.Database, idx []int) []schema.Database {
	out := make([]schema.Database, len(idx))
	for i, j := range idx {
		out[i] = dbs[j]
	}
	return out
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
