package metabase

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const key = "secret-key-123"

func TestRefusesPlainHTTP(t *testing.T) {
	_, err := New(context.Background(), Config{URL: "http://metabase.example.com", APIKey: key})
	if err == nil || !strings.Contains(err.Error(), "refusing plain http") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestSendsKeyHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Api-Key")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(context.Background(), Config{URL: srv.URL, APIKey: key, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := c.Get(context.Background(), "/api/user/current", nil, &out); err != nil {
		t.Fatal(err)
	}
	if got != key {
		t.Errorf("X-Api-Key = %q, want the key", got)
	}
}

// Go forwards custom headers such as X-Api-Key across redirects, so a
// redirect to another host would hand it the key. The client must refuse.
func TestRefusesRedirectsAndNeverLeaksKey(t *testing.T) {
	var leaked bool
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Api-Key") != ""
		w.Write([]byte(`{}`))
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusFound)
	}))
	defer srv.Close()

	c, _ := New(context.Background(), Config{URL: srv.URL, APIKey: key, AllowHTTP: true})
	err := c.Get(context.Background(), "/api/database", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("err = %v, want a refused redirect", err)
	}
	if leaked {
		t.Error("the key reached the redirect target")
	}
}

func TestErrorsNeverEchoTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad key: "+r.Header.Get("X-Api-Key"), http.StatusUnauthorized)
	}))
	defer srv.Close()

	c, _ := New(context.Background(), Config{URL: srv.URL, APIKey: key, AllowHTTP: true})
	err := c.Get(context.Background(), "/api/database", nil, nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusUnauthorized {
		t.Fatalf("err = %v, want a 401 HTTPError", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("error text contains the key: %v", err)
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error should explain a 401: %v", err)
	}
}

func TestSessionLoginAndLogout(t *testing.T) {
	var loggedOut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/session":
			w.Write([]byte(`{"id":"sess-1"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/session":
			loggedOut = r.Header.Get("X-Metabase-Session") == "sess-1"
		default:
			if r.Header.Get("X-Metabase-Session") != "sess-1" {
				w.WriteHeader(http.StatusUnauthorized)
			}
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	c, err := New(context.Background(), Config{URL: srv.URL, Username: "u", Password: "p", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), "/api/database", nil, nil); err != nil {
		t.Fatal(err)
	}
	c.Close(context.Background())
	if !loggedOut {
		t.Error("Close did not end the session")
	}
}

func TestNeedsCredentials(t *testing.T) {
	_, err := New(context.Background(), Config{URL: "https://metabase.example.com"})
	if err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Errorf("err = %v, want a missing-credentials error", err)
	}
}
