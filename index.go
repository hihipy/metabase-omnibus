package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hihipy/metabase-omnibus/internal/content"
	"github.com/hihipy/metabase-omnibus/internal/schema"
)

// Indexes: every folder gets an index.md, and each database a catalog.csv.

// failedDB is a database whose metadata could not be read at all.
type failedDB struct{ name, reason string }

// Every folder gets an index.md: the export root lists databases, each
// database lists its schemas and every question, and each collection folder
// lists what it holds and the collections below it.

func rootIndex(exports []*dbExport, failed []failedDB, host string, showHost bool) string {
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
	if len(failed) > 0 {
		fmt.Fprintf(&b, "\n## Not Exported\n\n%s could not be read, even after retries.\n\n| Database | Why |\n| --- | --- |\n", plural(len(failed), "database"))
		for _, fd := range failed {
			fmt.Fprintf(&b, "| %s | %s |\n", cell(fd.name), cell(fd.reason))
		}
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
	nodes := map[string]*folderNode{"": {path: "", title: ex.rootTitle(), children: map[string]bool{}}}
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
		// The table below links each schema to its index of tables, or to its single file.
		b.WriteString("## Schemas\n\nThe shape of each schema: tables, columns, types, and keys.\n\n| Schema | Tables | Tokens |\n| --- | ---: | ---: |\n")
		for _, s := range ex.schemas {
			fmt.Fprintf(&b, "| %s | %d | %s |\n", link(s.name, s.path), s.tables, thousands(tokens(s.bytes)))
		}
		b.WriteString("\n")
	}
	if len(ex.items) > 0 {
		b.WriteString("## Collections\n\n")
		ex.writeTree(&b, nodes, "", 0)
		if ex.snippets > 0 {
			fmt.Fprintf(&b, "\nSQL snippets these questions use: %s.\n", link("_snippets.md", "questions/_snippets.md"))
		}
		ex.writeSkipped(&b)
		b.WriteString("\n## Every Question and Dashboard\n\n")
		fmt.Fprintf(&b, "The same list as a spreadsheet, for Excel or a script: %s.\n\n", link("catalog.csv", "catalog.csv"))
		itemTable(&b, ex.items, "questions/", true)
	}
	return b.String()
}

// schemaIndex lists a schema's tables, one file each, with what an assistant
// needs to pick the right ones: size, keys, and what each table points to.
func schemaIndex(doc schema.Doc, dbName string, entries []tableEntry) string {
	var b strings.Builder
	total := 0
	for _, e := range entries {
		total += tokens(e.bytes)
	}
	fmt.Fprintf(&b, "# Schema %s\n\nDatabase: %s (%s). %s, about %s tokens in all.\n\n%s %s\n\n[Up](<../../index.md>)\n\n",
		doc.Metadata.Schema, dbName, doc.Metadata.Engine, plural(len(entries), "table"), thousands(total),
		schema.PrivacyNote, schema.Limitations)
	b.WriteString("Each table is its own file. Read this list, then load only the tables a question touches.\n\n")
	b.WriteString("| Table | Columns | Primary key | Points to | Tokens |\n| --- | ---: | --- | --- | ---: |\n")
	for _, e := range entries {
		pk := ""
		if e.t.PrimaryKey != nil {
			pk = strings.Join(e.t.PrimaryKey.Columns, ", ")
		}
		var to []string
		seen := map[string]bool{}
		for _, fk := range e.t.ForeignKeys {
			target := fk.ToTable
			if fk.ToSchema != doc.Metadata.Schema {
				target = fk.ToSchema + "." + fk.ToTable
			}
			if !seen[target] {
				seen[target] = true
				to = append(to, target)
			}
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s |\n", link(e.t.Name, e.file), len(e.t.Columns), cell(pk), cell(strings.Join(to, ", ")), thousands(tokens(e.bytes)))
	}
	return b.String()
}

// writeSkipped lists what could not be exported, so nothing goes missing quietly.
func (ex *dbExport) writeSkipped(b *strings.Builder) {
	if len(ex.skipped) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Not Exported\n\n%s could not be fetched, even after retries. Everything else was exported.\n\n| ID | Name | Collection | Why |\n| --- | --- | --- | --- |\n",
		plural(len(ex.skipped), "item"))
	for _, sk := range ex.skipped {
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", cell(sk.Label), cell(sk.Name), cell(sk.Collection), cell(sk.Reason))
	}
}

func (ex *dbExport) rootTitle() string {
	if ex.root != "" {
		return ex.root
	}
	return "Our analytics"
}

// collectionLabel is a collection's path without the root, which every path shares.
func collectionLabel(c content.Collection) string {
	if len(c.Parts) <= 1 {
		return c.Path
	}
	return strings.Join(c.Parts[1:], " > ")
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
			row = append(row, cell(collectionLabel(it.Collection)))
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
