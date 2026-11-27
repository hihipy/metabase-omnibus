//go:build integration

// The integration test runs the whole export against a real Metabase, which
// the Integration workflow starts in Docker. It sets the instance up from
// scratch through the API: the first admin, a SQL question, a dashboard, a
// snippet, and an API key. Then it exports and checks the result.
//
// Run it locally against a throwaway Metabase, never one with real data:
//
//	docker run -d -p 3000:3000 --name mb-it metabase/metabase:latest
//	MB_IT_URL=http://localhost:3000 go test -tags integration -run Integration -v .
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	itEmail    = "it-admin@example.com"
	itPassword = "Omnibus-IT-pass-7319"
	itSQL      = "SELECT STATE, COUNT(*) AS PEOPLE_COUNT FROM PEOPLE GROUP BY STATE"
	itQuestion = "IT people by state"
	itDash     = "IT overview"
	itSnippet  = "it_active_states"
)

// itClient is the bare HTTP the setup needs. It is separate from the tool's
// client on purpose: the test should not trust the code it is testing to set
// up the instance.
type itClient struct {
	base    string
	session string
	t       *testing.T
}

func (c *itClient) call(method, path string, body any, out any) int {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.session != "" {
		req.Header.Set("X-Metabase-Session", c.session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decoding %q: %v", method, path, raw, err)
		}
	}
	if resp.StatusCode >= 300 {
		c.t.Logf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode
}

func (c *itClient) must(method, path string, body any, out any) {
	c.t.Helper()
	if code := c.call(method, path, body, out); code < 200 || code >= 300 {
		c.t.Fatalf("%s %s failed with %d", method, path, code)
	}
}

// until polls f every two seconds until it reports true or the time runs out.
func until(t *testing.T, what string, limit time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s after %s", what, limit)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestIntegration(t *testing.T) {
	base := strings.TrimRight(os.Getenv("MB_IT_URL"), "/")
	if base == "" {
		t.Skip("MB_IT_URL is not set; see the comment at the top of this file")
	}
	c := &itClient{base: base, t: t}

	// Metabase answers 503 while it migrates its own database on first start.
	// The setup token appears in the public session properties once it is up.
	var props struct {
		SetupToken string `json:"setup-token"`
	}
	until(t, "Metabase to start", 5*time.Minute, func() bool {
		return c.call("GET", "/api/session/properties", nil, &props) == 200 && props.SetupToken != ""
	})

	var setup struct {
		ID string `json:"id"`
	}
	c.must("POST", "/api/setup", map[string]any{
		"token": props.SetupToken,
		"user": map[string]any{
			"email": itEmail, "first_name": "Ada", "last_name": "Canary", "password": itPassword,
		},
		"prefs": map[string]any{"site_name": "Omnibus IT"},
	}, &setup)
	c.session = setup.ID

	// The Sample Database is added at startup, sometimes just after setup.
	var dbID int
	until(t, "the Sample Database", 2*time.Minute, func() bool {
		var dbs struct {
			Data []struct {
				ID       int    `json:"id"`
				IsSample bool   `json:"is_sample"`
				Name     string `json:"name"`
			} `json:"data"`
		}
		if c.call("GET", "/api/database", nil, &dbs) != 200 {
			return false
		}
		for _, d := range dbs.Data {
			if d.IsSample || d.Name == "Sample Database" {
				dbID = d.ID
				return true
			}
		}
		return false
	})

	c.must("POST", "/api/native-query-snippet", map[string]any{
		"name": itSnippet, "content": "STATE IN ('FL', 'GA')",
	}, nil)

	var card struct {
		ID int `json:"id"`
	}
	c.must("POST", "/api/card", map[string]any{
		"name":                   itQuestion,
		"display":                "table",
		"visualization_settings": map[string]any{},
		"dataset_query": map[string]any{
			"type": "native", "database": dbID,
			"native": map[string]any{"query": itSQL},
		},
	}, &card)

	var dash struct {
		ID int `json:"id"`
	}
	c.must("POST", "/api/dashboard", map[string]any{"name": itDash}, &dash)
	c.must("PUT", fmt.Sprintf("/api/dashboard/%d", dash.ID), map[string]any{
		"dashcards": []map[string]any{{"id": -1, "card_id": card.ID, "row": 0, "col": 0, "size_x": 12, "size_y": 6}},
	}, nil)

	// The key goes in the Administrators group so the check covers everything
	// on the instance, rather than whatever a default group can see.
	var groups []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	c.must("GET", "/api/permissions/group", nil, &groups)
	adminGroup := 0
	for _, g := range groups {
		if g.Name == "Administrators" {
			adminGroup = g.ID
		}
	}
	if adminGroup == 0 {
		t.Fatalf("no Administrators group in %v", groups)
	}
	var key struct {
		Key string `json:"unmasked_key"`
	}
	c.must("POST", "/api/api-key", map[string]any{"name": "omnibus-it", "group_id": adminGroup}, &key)
	if key.Key == "" {
		t.Fatal("the API key came back empty")
	}

	// Whether the field values check below means anything depends on whether
	// Metabase has scanned them yet, so say which it is.
	var meta json.RawMessage
	c.must("GET", fmt.Sprintf("/api/database/%d/metadata?include_hidden=true", dbID), nil, &meta)
	scanned := bytes.Contains(meta, []byte("Doohickey"))
	t.Logf("Metabase serves PRODUCTS.CATEGORY values in metadata: %v", scanned)

	out := t.TempDir()
	t.Setenv("METABASE_API_KEY", key.Key)
	t.Setenv("METABASE_SESSION", "")
	t.Setenv("METABASE_USERNAME", "")
	t.Setenv("METABASE_PASSWORD", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := run(ctx, []string{"-url", base, "-allow-http", "-all", "-out", out}, true, true); err != nil {
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
	t.Logf("exported %d files", len(files))

	for _, want := range []string{
		"index.md",
		"catalog.csv",
		"Sample Database/index.md",
		"Sample Database/schema/PUBLIC/index.md",
		"Sample Database/schema/PUBLIC/ORDERS.json",
		"Sample Database/schema/PUBLIC/PRODUCTS.json",
		"Sample Database/questions/index.md",
		"Sample Database/questions/_snippets.md",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}

	question := find(files, fmt.Sprintf("Q%d %s.md", card.ID, itQuestion))
	if question == "" {
		t.Errorf("no file for question %d", card.ID)
	} else if !strings.Contains(files[question], itSQL) {
		t.Errorf("%s does not carry the SQL:\n%s", question, files[question])
	}
	dashboard := find(files, fmt.Sprintf("D%d %s.md", dash.ID, itDash))
	if dashboard == "" {
		t.Errorf("no file for dashboard %d", dash.ID)
	} else if !strings.Contains(files[dashboard], itQuestion) {
		t.Errorf("%s does not list its card:\n%s", dashboard, files[dashboard])
	}
	if !strings.Contains(files["Sample Database/questions/_snippets.md"], itSnippet) {
		t.Errorf("_snippets.md does not list %s", itSnippet)
	}

	// Nothing private leaves: no credentials, no creator, no field values or
	// fingerprints in the schema.
	for name, body := range files {
		for _, secret := range []string{key.Key, itPassword, setup.ID, itEmail, "Canary"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s contains %q", name, secret)
			}
		}
		if strings.Contains(name, "/schema/") {
			for _, value := range []string{"Doohickey", "Gizmo", "fingerprint", "has_field_values"} {
				if strings.Contains(body, value) {
					t.Errorf("%s contains %q", name, value)
				}
			}
		}
	}
}

// find returns the exported path that ends in name, in any collection folder.
func find(files map[string]string, name string) string {
	for p := range files {
		if p == name || strings.HasSuffix(p, "/"+name) {
			return p
		}
	}
	return ""
}
