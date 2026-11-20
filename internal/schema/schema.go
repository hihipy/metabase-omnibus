// Package schema exports the shape of the databases connected to Metabase, in
// the same JSON layout sql-x-ray produces, so an LLM reads both the same way.
//
// It reads only the metadata Metabase already stores from its syncs. It runs
// no SQL against any database.
//
// Privacy rules, matching sql-x-ray:
//   - The API structs below are an allowlist. Decoding keeps only the fields
//     named here, so fields a future Metabase adds are dropped by default.
//   - Nothing derived from data values: no fingerprints (min, max, top
//     values), no field values, no sample rows, and no semantic types, which
//     Metabase partly infers by scanning values.
//   - No connection details.
//   - Free text (table and column descriptions) and row counts are left out
//     unless the caller opts in.
package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/hihipy/metabase-omnibus/internal/metabase"
)

// ---------------------------------------------------------------- API allowlist

type apiDatabase struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	IsAudit bool   `json:"is_audit"`
}

type apiMetadata struct {
	Tables []apiTable `json:"tables"`
}

type apiTable struct {
	Schema            string     `json:"schema"`
	Name              string     `json:"name"`
	Description       *string    `json:"description"`
	VisibilityType    *string    `json:"visibility_type"`
	EstimatedRowCount *int64     `json:"estimated_row_count"`
	Fields            []apiField `json:"fields"`
}

type apiField struct {
	ID               int     `json:"id"`
	Name             string  `json:"name"`
	DatabaseType     string  `json:"database_type"`
	SemanticType     *string `json:"semantic_type"` // read only to find primary keys; never written
	Description      *string `json:"description"`
	FKTargetFieldID  *int    `json:"fk_target_field_id"`
	VisibilityType   string  `json:"visibility_type"`
	DatabasePosition int     `json:"database_position"`
}

// ---------------------------------------------------------------- listing

// Database is one database connected to Metabase.
type Database struct {
	ID     int
	Name   string
	Engine string
}

// ListDatabases returns the databases the credentials can see, skipping
// Metabase's own usage-analytics database unless includeAudit is set.
func ListDatabases(ctx context.Context, c *metabase.Client, includeAudit bool) ([]Database, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/database", nil, &raw); err != nil {
		return nil, err
	}
	dbs, err := decodeList[apiDatabase](raw)
	if err != nil {
		return nil, fmt.Errorf("listing databases: %w", err)
	}
	var out []Database
	for _, d := range dbs {
		if d.ID <= 0 || (d.IsAudit && !includeAudit) {
			continue
		}
		out = append(out, Database{ID: d.ID, Name: d.Name, Engine: d.Engine})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Metadata is one database's stored metadata, before any filtering.
type Metadata struct {
	DB     Database
	tables []apiTable
}

// FetchMetadata reads one database's tables and columns from Metabase.
func FetchMetadata(ctx context.Context, c *metabase.Client, db Database, includeHidden bool) (*Metadata, error) {
	q := url.Values{"include_hidden": {strconv.FormatBool(includeHidden)}}
	var meta apiMetadata
	if err := c.Get(ctx, fmt.Sprintf("/api/database/%d/metadata", db.ID), q, &meta); err != nil {
		return nil, err
	}
	return &Metadata{DB: db, tables: meta.Tables}, nil
}

// Schemas returns each schema name with its table count, sorted by name.
func (m *Metadata) Schemas() []SchemaCount {
	counts := map[string]int{}
	for _, t := range m.tables {
		counts[t.Schema]++
	}
	out := make([]SchemaCount, 0, len(counts))
	for name, n := range counts {
		out = append(out, SchemaCount{Name: name, Tables: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SchemaCount is a schema and how many tables it holds.
type SchemaCount struct {
	Name   string
	Tables int
}

// ---------------------------------------------------------------- export, in sql-x-ray's layout

// Doc is one database's dump.
type Doc struct {
	Metadata DocMetadata `json:"metadata"`
	Tables   []Table     `json:"tables"`
}

type DocMetadata struct {
	ToolName     string         `json:"tool_name"`
	Source       string         `json:"source"`
	Instance     string         `json:"instance"`
	Engine       string         `json:"engine"`
	Database     string         `json:"database"`
	GeneratedAt  string         `json:"generated_at"`
	Schemas      []string       `json:"schemas"`
	ObjectCounts map[string]int `json:"object_counts"`
	PrivacyNote  string         `json:"privacy_note"`
	Limitations  string         `json:"limitations"`
}

type Table struct {
	Schema           string       `json:"schema"`
	Name             string       `json:"name"`
	RowCountEstimate *int64       `json:"row_count_estimate,omitempty"`
	Hidden           bool         `json:"hidden_in_metabase,omitempty"`
	Description      string       `json:"description,omitempty"`
	PrimaryKey       *PrimaryKey  `json:"primary_key"`
	ForeignKeys      []ForeignKey `json:"foreign_keys"`
	Columns          []Column     `json:"columns"`
}

type PrimaryKey struct {
	Columns []string `json:"columns"`
}

type ForeignKey struct {
	FromColumns []string `json:"from_columns"`
	ToSchema    string   `json:"to_schema"`
	ToTable     string   `json:"to_table"`
	ToColumns   []string `json:"to_columns"`
}

type Column struct {
	Name        string `json:"name"`
	Position    int    `json:"position"`
	DataType    string `json:"data_type"`
	Hidden      bool   `json:"hidden_in_metabase,omitempty"`
	Description string `json:"description,omitempty"`
}

// Options controls what goes into a dump beyond the structure.
type Options struct {
	Instance            string
	IncludeDescriptions bool // free text an admin typed; could say anything
	RowCounts           bool // aggregate, but a small count can identify a small group
}

const privacyNote = "This document contains only structural metadata: schema, table, and column names, column types, and key relationships. " +
	"It contains no data values, value statistics, sample rows, or connection details."

const limitations = "Built from the metadata Metabase stores, not from the database catalog. " +
	"Views appear as tables, and indexes, nullability, routines, and multi-column keys are not available."

// Build turns one database's metadata into a dump, keeping only the named schemas.
func Build(m *Metadata, keep map[string]bool, opt Options) Doc {
	type ref struct{ schema, table, column string }
	byID := map[int]ref{}
	for _, t := range m.tables {
		for _, f := range t.Fields {
			byID[f.ID] = ref{t.Schema, t.Name, f.Name}
		}
	}

	doc := Doc{Metadata: DocMetadata{
		ToolName:    "metabase-omnibus",
		Source:      "metabase",
		Instance:    opt.Instance,
		Engine:      m.DB.Engine,
		Database:    m.DB.Name,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		PrivacyNote: privacyNote,
		Limitations: limitations,
	}}
	if opt.IncludeDescriptions {
		doc.Metadata.PrivacyNote += " Table and column descriptions written in Metabase are included and were not reviewed."
	}

	seen := map[string]bool{}
	for _, t := range m.tables {
		if !keep[t.Schema] {
			continue
		}
		seen[t.Schema] = true
		tbl := Table{
			Schema:      t.Schema,
			Name:        t.Name,
			Hidden:      t.VisibilityType != nil && *t.VisibilityType != "",
			ForeignKeys: []ForeignKey{},
		}
		if opt.RowCounts {
			tbl.RowCountEstimate = t.EstimatedRowCount
		}
		if opt.IncludeDescriptions && t.Description != nil {
			tbl.Description = *t.Description
		}

		fields := append([]apiField(nil), t.Fields...)
		sort.SliceStable(fields, func(i, j int) bool { return fields[i].DatabasePosition < fields[j].DatabasePosition })
		var pk []string
		for i, f := range fields {
			col := Column{
				Name:     f.Name,
				Position: i + 1, // ordinal in database order, counted from 1 as sql-x-ray does
				DataType: f.DatabaseType,
				Hidden:   f.VisibilityType != "" && f.VisibilityType != "normal" && f.VisibilityType != "details-only",
			}
			if opt.IncludeDescriptions && f.Description != nil {
				col.Description = *f.Description
			}
			tbl.Columns = append(tbl.Columns, col)

			if f.SemanticType != nil && *f.SemanticType == "type/PK" {
				pk = append(pk, f.Name)
			}
			if f.FKTargetFieldID != nil {
				if target, ok := byID[*f.FKTargetFieldID]; ok {
					tbl.ForeignKeys = append(tbl.ForeignKeys, ForeignKey{
						FromColumns: []string{f.Name},
						ToSchema:    target.schema,
						ToTable:     target.table,
						ToColumns:   []string{target.column},
					})
				}
			}
		}
		if len(pk) > 0 {
			tbl.PrimaryKey = &PrimaryKey{Columns: pk}
		}
		doc.Tables = append(doc.Tables, tbl)
	}

	sort.Slice(doc.Tables, func(i, j int) bool {
		if doc.Tables[i].Schema != doc.Tables[j].Schema {
			return doc.Tables[i].Schema < doc.Tables[j].Schema
		}
		return doc.Tables[i].Name < doc.Tables[j].Name
	})
	for s := range seen {
		doc.Metadata.Schemas = append(doc.Metadata.Schemas, s)
	}
	sort.Strings(doc.Metadata.Schemas)
	doc.Metadata.ObjectCounts = map[string]int{"tables": len(doc.Tables)}
	return doc
}

// decodeList accepts a bare JSON array or Metabase's {"data": [...]} wrapper.
func decodeList[T any](raw json.RawMessage) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		var wrapped struct {
			Data []T `json:"data"`
		}
		err := json.Unmarshal(raw, &wrapped)
		return wrapped.Data, err
	}
	var list []T
	err := json.Unmarshal(raw, &list)
	return list, err
}
