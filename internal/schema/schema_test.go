package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hihipy/metabase-omnibus/internal/fakemb"
	"github.com/hihipy/metabase-omnibus/internal/metabase"
)

func fetch(t *testing.T) *Metadata {
	t.Helper()
	srv := fakemb.New(t)
	c, err := metabase.New(context.Background(), metabase.Config{URL: srv.URL, APIKey: fakemb.APIKey, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	dbs, err := ListDatabases(context.Background(), c, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].Name != "Sample Database" {
		t.Fatalf("databases = %+v, want only the sample database (the audit database is skipped)", dbs)
	}
	m, err := FetchMetadata(context.Background(), c, dbs[0], false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func render(t *testing.T, d Doc) string {
	t.Helper()
	var b bytes.Buffer
	if err := WriteDoc(&b, d); err != nil {
		t.Fatal(err)
	}
	var check map[string]any
	if err := json.Unmarshal(b.Bytes(), &check); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	return b.String()
}

func TestSchemasAndCounts(t *testing.T) {
	got := fetch(t).Schemas()
	if len(got) != 2 || got[0].Name != "ANALYTICS" || got[1].Name != "PUBLIC" || got[1].Tables != 2 {
		t.Errorf("schemas = %+v", got)
	}
}

func TestKeysAndOrder(t *testing.T) {
	d := Build(fetch(t), "PUBLIC", Options{})
	if len(d.Tables) != 2 || d.Tables[0].Name != "ORDERS" {
		t.Fatalf("tables = %+v", d.Tables)
	}
	orders := d.Tables[0]
	if orders.PrimaryKey == nil || orders.PrimaryKey.Columns[0] != "ID" {
		t.Errorf("primary key = %+v", orders.PrimaryKey)
	}
	if len(orders.ForeignKeys) != 1 || orders.ForeignKeys[0].ToTable != "PEOPLE" || orders.ForeignKeys[0].ToColumns[0] != "ID" {
		t.Errorf("foreign keys = %+v", orders.ForeignKeys)
	}
	var names []string
	for _, c := range orders.Columns {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "ID,USER_ID,TOTAL,CREATED_AT" {
		t.Errorf("columns out of database order: %v", names)
	}
}

func TestPrivacyDefaults(t *testing.T) {
	out := render(t, Build(fetch(t), "PUBLIC", Options{}))
	for _, c := range append(fakemb.Canaries, fakemb.Description) {
		if strings.Contains(out, c) {
			t.Errorf("default export contains %s", c)
		}
	}
	for _, s := range []string{"row_count_estimate", "instance", "type/"} {
		if strings.Contains(out, s) {
			t.Errorf("default export contains %q", s)
		}
	}
}

func TestOptIns(t *testing.T) {
	out := render(t, Build(fetch(t), "PUBLIC", Options{IncludeDescriptions: true, RowCounts: true, Instance: "mb.example.com"}))
	for _, s := range []string{fakemb.Description, `"row_count_estimate":18760`, `"instance":"mb.example.com"`} {
		if !strings.Contains(out, s) {
			t.Errorf("opt-in export is missing %s", s)
		}
	}
	for _, c := range fakemb.Canaries {
		if strings.Contains(out, c) {
			t.Errorf("opt-ins must never unlock %s", c)
		}
	}
}

func TestOneTablePerLine(t *testing.T) {
	out := render(t, Build(fetch(t), "PUBLIC", Options{}))
	if got := strings.Count(out, "\n"); got != 5 { // metadata, "tables":[, two tables, close
		t.Errorf("got %d lines, want metadata, one per table, and the close:\n%s", got, out)
	}
}

func TestFieldNames(t *testing.T) {
	if got := fetch(t).FieldNames()[202]; got != "PUBLIC.PEOPLE.STATE" {
		t.Errorf("field 202 = %q", got)
	}
}

func TestForTable(t *testing.T) {
	d := Build(fetch(t), "PUBLIC", Options{Instance: "mb.example.com"})
	one := d.ForTable(d.Tables[0])
	out := render(t, one)
	if len(one.Tables) != 1 || one.Metadata.Table != "ORDERS" || one.Metadata.Schema != "PUBLIC" {
		t.Errorf("one-table doc = %+v", one.Metadata)
	}
	for _, want := range []string{`"table":"ORDERS"`, `"privacy_note":"Structure only`, `"instance":"mb.example.com"`, `"foreign_keys"`} {
		if !strings.Contains(out, want) {
			t.Errorf("one-table file is missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "limitations") || strings.Contains(out, "object_counts") {
		t.Errorf("one-table file should leave the long notes to the index:\n%s", out)
	}
	if !strings.Contains(render(t, d), `"limitations"`) {
		t.Error("the whole-schema file lost its limitations note")
	}
}
