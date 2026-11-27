package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hihipy/metabase-omnibus/internal/content"
	"github.com/hihipy/metabase-omnibus/internal/metabase"
	"github.com/hihipy/metabase-omnibus/internal/schema"
	"github.com/hihipy/metabase-omnibus/internal/ui"
)

// Per-database export: schema files, one file per question and dashboard,
// and the record the indexes are built from.

// dbExport records what was written for one database, for its indexes.
type dbExport struct {
	db       schema.Database
	folder   string
	root     string // the root collection's name, from Metabase
	schemas  []schemaFile
	items    []itemFile
	skipped  []content.Skipped
	snippets int // bytes in questions/_snippets.md, 0 when there is none
}

// schemaFile is one schema's export: a single file, or a folder with one
// file per table and an index. bytes covers everything written for it.
type schemaFile struct {
	name   string
	tables int
	bytes  int
	path   string // what the database index links to, relative to the database folder
	files  int    // files written for this schema
}

type itemFile struct {
	content.Item
	dir   string // folder below questions/, "" for the root collection
	file  string // file name inside dir
	bytes int
}

func (ex *dbExport) empty() bool { return ex.files() == 0 }

func (ex *dbExport) files() int {
	n := len(ex.items)
	for _, s := range ex.schemas {
		n += s.files
	}
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
		if f.schemaLayout == "single" {
			rel := "schema/" + safeName(s.Name) + ".json"
			size, err := writeCounted(filepath.Join(dir, filepath.FromSlash(rel)), func(w io.Writer) error { return schema.WriteDoc(w, doc) })
			if err != nil {
				return err
			}
			ex.schemas = append(ex.schemas, schemaFile{name: s.Name, tables: len(doc.Tables), bytes: size, path: rel, files: 1})
			continue
		}
		sf, err := writeTables(doc, filepath.Join(dir, "schema", safeName(s.Name)), ex.db.Name)
		if err != nil {
			return err
		}
		sf.path = "schema/" + safeName(s.Name) + "/index.md"
		ex.schemas = append(ex.schemas, sf)
	}
	tables := 0
	for _, s := range ex.schemas {
		tables += s.tables
	}
	fmt.Fprintf(out, "  %s, %s\n", plural(len(ex.schemas), "schema"), plural(tables, "table"))
	return nil
}

// tableEntry is one table file, for its schema's index.
type tableEntry struct {
	t     schema.Table
	file  string
	bytes int
}

// writeTables writes one file per table and an index, so an assistant can
// read the index and then load only the tables a question touches.
func writeTables(doc schema.Doc, dir, dbName string) (schemaFile, error) {
	sf := schemaFile{name: doc.Metadata.Schema, tables: len(doc.Tables)}
	var entries []tableEntry
	used := map[string]bool{} // lowercased: Windows and macOS treat Orders and orders as one file
	for _, t := range doc.Tables {
		base := safeName(t.Name)
		name := base + ".json"
		for i := 2; used[strings.ToLower(name)]; i++ {
			name = fmt.Sprintf("%s (%d).json", base, i)
		}
		used[strings.ToLower(name)] = true
		one := doc.ForTable(t)
		size, err := writeCounted(filepath.Join(dir, name), func(w io.Writer) error { return schema.WriteDoc(w, one) })
		if err != nil {
			return sf, err
		}
		entries = append(entries, tableEntry{t, name, size})
		sf.bytes += size
		sf.files++
	}
	size, err := writeCounted(filepath.Join(dir, "index.md"), func(w io.Writer) error {
		_, err := io.WriteString(w, schemaIndex(doc, dbName, entries))
		return err
	})
	if err != nil {
		return sf, err
	}
	sf.bytes += size
	sf.files++
	return sf, nil
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

	items, snippets, skipped, err := content.Export(ctx, client, cat, keep, content.Options{
		IncludeText: f.text,
		NoCompile:   f.noCompile,
		FieldNames:  fieldNames,
		DBNames:     dbNames,
		Workers:     f.workers,
		Progress:    func(done, total int, label string) { ui.Progress(done, total, "items", label) },
	})
	if err != nil {
		return err
	}
	ex.root = cat.RootName()
	ex.skipped = skipped

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
	if len(skipped) > 0 {
		fmt.Fprintln(out, ui.Notice(fmt.Sprintf("  %s could not be exported; the database index lists them and why", plural(len(skipped), "item"))))
	}
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
