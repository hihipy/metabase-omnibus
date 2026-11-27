// Package content exports what people built in Metabase: collections,
// questions, models, metrics, dashboards, and the SQL behind them.
//
// Privacy rules, matching the schema export:
//   - The API structs in api.go are an allowlist; unnamed fields are dropped.
//   - No query is ever run. Query-builder questions are compiled to SQL by
//     POST /api/dataset/native, which compiles without executing.
//   - No column fingerprints, filter default values (a default can be a real
//     value, such as an ID), creator names, or personal collections.
//   - Free text (descriptions, dashboard text cards) only on opt-in.
//
// SQL is exported as written, so a literal typed into a query (WHERE id = 42)
// is included. That is stated in every file.
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/hihipy/metabase-omnibus/internal/metabase"
)

// ---------------------------------------------------------------- collections

// Collection is one folder in Metabase.
type Collection struct {
	ID       int    // 0 is the root collection
	Name     string // own name
	Path     string // root name > Team > Study
	Parts    []string
	Personal bool
}

// defaultRootName is what Metabase calls its root collection when the API
// does not say. Instances can rename it, and translations differ.
const defaultRootName = "Our analytics"

// Catalog is every collection, card, and dashboard the credentials can list.
type Catalog struct {
	Collections []Collection
	byID        map[int]Collection
	cards       []apiListItem
	dashboards  []apiListItem
	cardNames   map[int]string
}

// Restrict keeps only the questions that query the given databases, and the
// dashboards in collections that still hold one, so choosing databases also
// narrows the collections on offer.
func (c *Catalog) Restrict(dbIDs map[int]bool) {
	var cards []apiListItem
	live := map[int]bool{}
	for _, it := range c.cards {
		if it.DatabaseID != nil && dbIDs[*it.DatabaseID] {
			cards = append(cards, it)
			live[colID(it)] = true
		}
	}
	var dashes []apiListItem
	for _, it := range c.dashboards {
		if live[colID(it)] {
			dashes = append(dashes, it)
		}
	}
	c.cards, c.dashboards = cards, dashes
}

// ForDatabase returns a copy of the catalog holding only one database's
// questions, and the dashboards in the collections that hold them.
func (c *Catalog) ForDatabase(id int) *Catalog {
	cp := *c
	cp.Restrict(map[int]bool{id: true})
	return &cp
}

// RootName is the root collection's name, as Metabase calls it.
func (c *Catalog) RootName() string { return c.byID[0].Name }

// Counts reports how many cards and dashboards sit directly in a collection.
func (c *Catalog) Counts(id int) (cards, dashboards int) {
	for _, it := range c.cards {
		if colID(it) == id {
			cards++
		}
	}
	for _, it := range c.dashboards {
		if colID(it) == id {
			dashboards++
		}
	}
	return
}

// List reads collections, cards, and dashboards. Personal collections are
// skipped unless includePersonal is set.
func List(ctx context.Context, c *metabase.Client, includePersonal bool) (*Catalog, error) {
	var raw json.RawMessage
	if err := c.Get(ctx, "/api/collection", nil, &raw); err != nil {
		return nil, err
	}
	cols, err := decodeList[apiCollection](raw)
	if err != nil {
		return nil, fmt.Errorf("listing collections: %w", err)
	}
	names := map[int]string{}
	personalRoots := map[int]bool{}
	for _, col := range cols {
		if id, ok := intID(col.ID); ok {
			names[id] = col.Name
			if col.PersonalOwnerID != nil {
				personalRoots[id] = true
			}
		}
	}

	rootName := defaultRootName
	for _, col := range cols {
		if strings.Trim(string(col.ID), `"`) == "root" && col.Name != "" {
			rootName = col.Name
		}
	}
	cat := &Catalog{byID: map[int]Collection{}, cardNames: map[int]string{}}
	root := Collection{ID: 0, Name: rootName, Path: rootName, Parts: []string{rootName}}
	cat.Collections = append(cat.Collections, root)
	cat.byID[0] = root
	for _, col := range cols {
		id, ok := intID(col.ID)
		if !ok || col.Archived {
			continue
		}
		parts := []string{rootName}
		personal := personalRoots[id]
		for _, p := range strings.Split(strings.Trim(col.Location, "/"), "/") {
			if pid, err := strconv.Atoi(p); err == nil {
				parts = append(parts, names[pid])
				personal = personal || personalRoots[pid]
			}
		}
		parts = append(parts, col.Name)
		if personal && !includePersonal {
			continue
		}
		cc := Collection{ID: id, Name: col.Name, Path: strings.Join(parts, " > "), Parts: parts, Personal: personal}
		cat.Collections = append(cat.Collections, cc)
		cat.byID[id] = cc
	}
	sort.Slice(cat.Collections, func(i, j int) bool { return cat.Collections[i].Path < cat.Collections[j].Path })

	if cat.cards, err = listItems(ctx, c, "/api/card", []string{"card", "dataset", "metric"}); err != nil {
		return nil, err
	}
	if cat.dashboards, err = listItems(ctx, c, "/api/dashboard/", []string{"dashboard"}); err != nil {
		return nil, err
	}
	for _, it := range cat.cards {
		cat.cardNames[it.ID] = it.Name
	}
	return cat, nil
}

func listItems(ctx context.Context, c *metabase.Client, endpoint string, models []string) ([]apiListItem, error) {
	var raw json.RawMessage
	err := c.Get(ctx, endpoint, url.Values{"f": {"all"}}, &raw)
	var httpErr *metabase.HTTPError
	if errors.As(err, &httpErr) && httpErr.Status == 404 {
		// The list endpoint is gone in this version; search finds the same items.
		err = c.Get(ctx, "/api/search", url.Values{"models": models}, &raw)
	}
	if err != nil {
		return nil, err
	}
	items, err := decodeList[apiListItem](raw)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", endpoint, err)
	}
	var out []apiListItem
	for _, it := range items {
		if it.Archived || (it.Model != "" && !contains(models, it.Model)) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func colID(it apiListItem) int {
	if it.CollectionID != nil {
		return *it.CollectionID
	}
	if it.Collection != nil {
		if id, ok := intID(it.Collection.ID); ok {
			return id
		}
	}
	return 0
}
