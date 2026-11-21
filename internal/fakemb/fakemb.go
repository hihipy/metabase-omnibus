// Package fakemb is a fake Metabase for tests. It serves the endpoints the
// tool reads, shaped like Metabase's Sample Database.
//
// Every place a real Metabase could hand back something private holds a
// canary string. Tests scan the tool's output for every canary in Canaries:
// finding one means a privacy rule broke.
//
// The fake also fails the test if anything asks it to run a query.
package fakemb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Credentials the fake accepts.
const (
	APIKey   = "test-api-key-CANARY_KEY"
	Username = "analyst@example.com"
	Password = "CANARY_PASSWORD"
	Session  = "CANARY_SESSION"
)

// Canaries must never appear in any export, whatever the flags.
var Canaries = []string{
	"CANARY_KEY", "CANARY_PASSWORD", "CANARY_SESSION", // credentials
	"CANARY_HOST", "CANARY_DBUSER", // connection details
	"CANARY_FINGERPRINT", "CANARY_VALUE", "CANARY_SEMANTIC", // derived from data
	"CANARY_DEFAULT",  // filter defaults
	"CANARY_CREATOR",  // people
	"CANARY_PERSONAL", // personal collections
}

// OptIn canaries appear only when their flag is set.
const (
	Description = "CANARY_DESCRIPTION" // -include-descriptions
	TextCard    = "CANARY_TEXTCARD"    // -include-descriptions
)

// Server is a running fake.
type Server struct {
	*httptest.Server
	mu        sync.Mutex
	Requests  []string // "METHOD /path?query", in order
	LoggedOut bool
}

// New starts a fake and stops it when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.Requests = append(s.Requests, r.Method+" "+r.URL.RequestURI())
		s.mu.Unlock()
		s.handle(t, w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// Saw reports whether any request started with prefix, such as "GET /api/card/12".
func (s *Server) Saw(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.Requests {
		if strings.HasPrefix(r, prefix) {
			return true
		}
	}
	return false
}

func (s *Server) handle(t *testing.T, w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method == http.MethodPost && path == "/api/session" {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != Username || body["password"] != Password {
			http.Error(w, `{"errors":{"password":"did not match"}}`, http.StatusUnauthorized)
			return
		}
		write(w, map[string]string{"id": Session})
		return
	}
	if r.Header.Get("X-Api-Key") != APIKey && r.Header.Get("X-Metabase-Session") != Session {
		http.Error(w, "Unauthenticated", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodDelete && path == "/api/session":
		s.mu.Lock()
		s.LoggedOut = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodPost && (path == "/api/dataset" || strings.HasPrefix(path, "/api/card/") && strings.Contains(path, "/query")):
		t.Errorf("the tool asked Metabase to run a query: %s %s", r.Method, path)
		http.Error(w, "queries are not allowed in tests", http.StatusTeapot)

	case r.Method == http.MethodPost && path == "/api/dataset/native":
		write(w, map[string]any{"query": "SELECT \"PUBLIC\".\"ORDERS\".\"ID\", \"PUBLIC\".\"ORDERS\".\"TOTAL\"\nFROM \"PUBLIC\".\"ORDERS\"", "params": nil})

	case path == "/api/database":
		raw(w, databases)
	case path == "/api/database/1/metadata":
		raw(w, sampleMetadata)
	case path == "/api/collection":
		raw(w, collections)
	case path == "/api/card":
		raw(w, cardList)
	case path == "/api/dashboard/":
		raw(w, dashboardList)
	case path == "/api/native-query-snippet":
		raw(w, snippets)
	case path == "/api/dashboard/3":
		raw(w, dashboard3)
	case path == "/api/dashboard/4":
		raw(w, dashboard4)

	case strings.HasPrefix(path, "/api/card/"):
		if r.URL.Query().Get("legacy-mbql") != "true" {
			http.Error(w, "test fake requires legacy-mbql=true", http.StatusBadRequest)
			return
		}
		switch path {
		case "/api/card/10":
			raw(w, card10)
		case "/api/card/11":
			raw(w, card11)
		case "/api/card/12":
			raw(w, card12)
		case "/api/card/14":
			raw(w, card14)
		default:
			http.NotFound(w, r)
		}

	default:
		http.NotFound(w, r)
	}
}

func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func raw(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(s))
}

// ---------------------------------------------------------------- fixtures

const databases = `{"data":[
 {"id":1,"name":"Sample Database","engine":"h2","is_audit":false,
  "details":{"host":"CANARY_HOST","user":"CANARY_DBUSER","password":"**MetabasePass**"}},
 {"id":13371337,"name":"Internal Metabase Database","engine":"postgres","is_audit":true}
]}`

// Field IDs: ORDERS 100-103, PEOPLE 200-203.
const sampleMetadata = `{"id":1,"name":"Sample Database","details":{"host":"CANARY_HOST"},"tables":[
 {"id":2,"schema":"PUBLIC","name":"ORDERS","display_name":"Orders","description":"CANARY_DESCRIPTION","visibility_type":null,
  "estimated_row_count":18760,"fields":[
  {"id":100,"name":"ID","display_name":"ID","database_type":"BIGINT","base_type":"type/BigInteger","semantic_type":"type/PK","database_position":0,"visibility_type":"normal"},
  {"id":101,"name":"USER_ID","database_type":"INTEGER","base_type":"type/Integer","semantic_type":"type/FK","fk_target_field_id":200,"database_position":1,"visibility_type":"normal"},
  {"id":102,"name":"TOTAL","database_type":"DOUBLE PRECISION","base_type":"type/Float","semantic_type":"type/CANARY_SEMANTIC","database_position":2,"visibility_type":"normal",
   "description":"CANARY_DESCRIPTION","fingerprint":{"type":{"type/Number":{"min":"CANARY_FINGERPRINT","max":"CANARY_FINGERPRINT"}}}},
  {"id":103,"name":"CREATED_AT","database_type":"TIMESTAMP","base_type":"type/DateTime","database_position":3,"visibility_type":"normal"}]},
 {"id":3,"schema":"PUBLIC","name":"PEOPLE","display_name":"People","visibility_type":null,"estimated_row_count":2500,"fields":[
  {"id":200,"name":"ID","database_type":"BIGINT","base_type":"type/BigInteger","semantic_type":"type/PK","database_position":0,"visibility_type":"normal"},
  {"id":201,"name":"EMAIL","database_type":"CHARACTER VARYING","base_type":"type/Text","semantic_type":"type/Email","database_position":1,"visibility_type":"sensitive",
   "values":["CANARY_VALUE"],"fingerprint":{"global":{"distinct-count":2500},"type":{"type/Text":{"top":"CANARY_FINGERPRINT"}}}},
  {"id":202,"name":"STATE","database_type":"CHARACTER","base_type":"type/Text","database_position":2,"visibility_type":"normal","has_field_values":"list","values":["CANARY_VALUE"]},
  {"id":203,"name":"CITY","database_type":"CHARACTER VARYING","base_type":"type/Text","database_position":3,"visibility_type":"normal"}]},
 {"id":4,"schema":"ANALYTICS","name":"MONTHLY","display_name":"Monthly","visibility_type":null,"fields":[
  {"id":300,"name":"MONTH","database_type":"DATE","base_type":"type/Date","database_position":0,"visibility_type":"normal"}]}
]}`

const collections = `[
 {"id":"root","name":"Our analytics"},
 {"id":5,"name":"Finance","location":"/","archived":false},
 {"id":6,"name":"Reports","location":"/5/","archived":false},
 {"id":9,"name":"CANARY_PERSONAL's Personal Collection","location":"/","personal_owner_id":3,"archived":false},
 {"id":10,"name":"Scratch","location":"/9/","archived":false},
 {"id":11,"name":"Old stuff","location":"/","archived":true}
]`

const cardList = `[
 {"id":10,"name":"Orders by state","type":"question","collection_id":6,"database_id":1,"archived":false,"creator":{"common_name":"CANARY_CREATOR"}},
 {"id":11,"name":"Order base","type":"model","collection_id":5,"database_id":1,"archived":false},
 {"id":14,"name":"Other database question","type":"question","collection_id":5,"database_id":2,"archived":false},
 {"id":12,"name":"CANARY_PERSONAL draft","type":"question","collection_id":10,"database_id":1,"archived":false},
 {"id":13,"name":"Archived question","type":"question","collection_id":5,"archived":true}
]`

const card10 = `{"id":10,"name":"Orders by state","type":"question","display":"bar","collection_id":6,"database_id":1,
 "updated_at":"2026-11-10T12:00:00Z","archived":false,"description":"CANARY_DESCRIPTION",
 "creator":{"common_name":"CANARY_CREATOR","email":"CANARY_CREATOR@example.com"},
 "dataset_query":{"type":"native","database":1,"native":{
  "query":"SELECT STATE, COUNT(*) FROM {{#11}} WHERE {{snippet: completed}} [[AND STATE = {{state}}]] GROUP BY STATE",
  "template-tags":{
   "state":{"id":"a","name":"state","display-name":"State","type":"dimension","widget-type":"string/=","required":false,
    "default":["CANARY_DEFAULT"],"dimension":["field",202,null]},
   "#11":{"id":"b","name":"#11","display-name":"#11","type":"card","card-id":11},
   "snippet: completed":{"id":"c","name":"snippet: completed","display-name":"Snippet","type":"snippet","snippet-name":"completed","snippet-id":1}}}},
 "result_metadata":[
  {"name":"STATE","display_name":"State","base_type":"type/Text","fingerprint":{"type":{"type/Text":{"top":"CANARY_FINGERPRINT"}}}},
  {"name":"count","display_name":"Count","base_type":"type/BigInteger","fingerprint":{"type":{"type/Number":{"max":"CANARY_FINGERPRINT"}}}}]}`

const card11 = `{"id":11,"name":"Order base","type":"model","display":"table","collection_id":5,"database_id":1,
 "updated_at":"2026-10-01T12:00:00Z","archived":false,"creator":{"common_name":"CANARY_CREATOR"},
 "dataset_query":{"type":"query","database":1,"query":{"source-table":2}},
 "result_metadata":[{"name":"ID","base_type":"type/BigInteger"},{"name":"TOTAL","base_type":"type/Float"}]}`

const card12 = `{"id":12,"name":"CANARY_PERSONAL draft","type":"question","display":"table","collection_id":10,"database_id":1,
 "dataset_query":{"type":"native","database":1,"native":{"query":"SELECT 'CANARY_PERSONAL'"}}}`

// card14 queries a database other than the sample, so choosing only the
// sample database must leave it out.
const card14 = `{"id":14,"name":"Other database question","type":"question","display":"table","collection_id":5,"database_id":2,
 "dataset_query":{"type":"native","database":2,"native":{"query":"SELECT 1"}}}`

const dashboardList = `[
 {"id":3,"name":"Sales overview","collection_id":5,"archived":false},
 {"id":4,"name":"CANARY_PERSONAL board","collection_id":9,"archived":false}
]`

const dashboard3 = `{"id":3,"name":"Sales overview","collection_id":5,"description":"CANARY_DESCRIPTION",
 "creator":{"common_name":"CANARY_CREATOR"},
 "parameters":[{"id":"p1","name":"State","slug":"state","type":"string/=","default":["CANARY_DEFAULT"]}],
 "tabs":[{"id":1,"name":"Main"}],
 "dashcards":[
  {"id":1,"card_id":10,"dashboard_tab_id":1,"row":0,"col":0,"size_x":12,"size_y":6,
   "parameter_mappings":[{"parameter_id":"p1","card_id":10,"target":["dimension",["template-tag","state"]]}],
   "visualization_settings":{"card.title":"Orders, by state"}},
  {"id":2,"card_id":null,"dashboard_tab_id":1,"row":6,"col":0,"size_x":24,"size_y":2,
   "visualization_settings":{"text":"CANARY_TEXTCARD","virtual_card":{"display":"text"}}}]}`

const dashboard4 = `{"id":4,"name":"CANARY_PERSONAL board","collection_id":9,"parameters":[],"dashcards":[]}`

const snippets = `[
 {"id":1,"name":"completed","description":"CANARY_DESCRIPTION","content":"STATUS = 'complete'","creator":{"common_name":"CANARY_CREATOR"}},
 {"id":2,"name":"unused","content":"1 = 1"}
]`
