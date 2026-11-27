package metabase

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// flaky answers with the given statuses in order, then 200.
func flaky(t *testing.T, statuses []int, retryAfter string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= len(statuses) {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(statuses[calls-1])
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testClient(t *testing.T, url string) (*Client, *[]time.Duration) {
	t.Helper()
	c, err := New(context.Background(), Config{URL: url, APIKey: key, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	c.Sleep = func(d time.Duration) { waits = append(waits, d) }
	return c, &waits
}

func TestRetriesTemporaryFailures(t *testing.T) {
	srv, calls := flaky(t, []int{503, 502}, "")
	c, waits := testClient(t, srv.URL)
	var out map[string]bool
	if err := c.Get(context.Background(), "/api/card/1", nil, &out); err != nil || !out["ok"] {
		t.Fatalf("err = %v, out = %v; want success after two retries", err, out)
	}
	if *calls != 3 || len(*waits) != 2 || (*waits)[0] != time.Second || (*waits)[1] != 2*time.Second {
		t.Errorf("calls = %d, waits = %v; want 3 calls with 1s then 2s backoff", *calls, *waits)
	}
}

func TestHonoursRetryAfterWithACap(t *testing.T) {
	srv, _ := flaky(t, []int{429}, "120")
	c, waits := testClient(t, srv.URL)
	if err := c.Get(context.Background(), "/api/card/1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(*waits) != 1 || (*waits)[0] != 30*time.Second {
		t.Errorf("waits = %v, want Retry-After 120 capped to 30s", *waits)
	}
}

func TestGivesUpAfterRetries(t *testing.T) {
	srv, calls := flaky(t, []int{503, 503, 503, 503, 503}, "")
	c, _ := testClient(t, srv.URL)
	err := c.Get(context.Background(), "/api/card/1", nil, nil)
	if !Temporary(err) || *calls != 4 {
		t.Errorf("err = %v, calls = %d; want a temporary error after 1 try and 3 retries", err, *calls)
	}
}

func TestPermanentFailuresAreNotRetried(t *testing.T) {
	srv, calls := flaky(t, []int{403}, "")
	c, waits := testClient(t, srv.URL)
	err := c.Get(context.Background(), "/api/card/1", nil, nil)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 403 || Temporary(err) || *calls != 1 || len(*waits) != 0 {
		t.Errorf("err = %v, calls = %d, waits = %v; want one 403 and no retry", err, *calls, *waits)
	}
}

func TestRedirectsAreNotRetried(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "https://elsewhere.example.com/", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := testClient(t, srv.URL)
	if err := c.Get(context.Background(), "/api/database", nil, nil); err == nil || calls != 1 {
		t.Errorf("err = %v, calls = %d; want a refused redirect on the first try", err, calls)
	}
}

func TestBrowserSessionIsUsedAndNotSignedOut(t *testing.T) {
	var gotSession string
	var signedOut bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			signedOut = true
		}
		gotSession = r.Header.Get("X-Metabase-Session")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := New(context.Background(), Config{URL: srv.URL, Session: "browser-token", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), "/api/database", nil, nil); err != nil {
		t.Fatal(err)
	}
	c.Close(context.Background())
	if gotSession != "browser-token" || signedOut {
		t.Errorf("session header = %q, signed out = %v; want the token used and the browser left signed in", gotSession, signedOut)
	}
}
