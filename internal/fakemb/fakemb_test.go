package fakemb

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func get(t *testing.T, s *Server, path string, withKey bool) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.URL+path, nil)
	if withKey {
		req.Header.Set("X-Api-Key", APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// The privacy tests elsewhere pass when no canary shows up in the output.
// That only means something if the fake really serves every canary, so a
// fixture edit that drops one must fail here.
func TestEveryCanaryIsServed(t *testing.T) {
	s := New(t)
	var served strings.Builder
	for _, p := range []string{
		"/api/database", "/api/database/1/metadata", "/api/collection", "/api/card",
		"/api/card/10?legacy-mbql=true", "/api/card/12?legacy-mbql=true",
		"/api/dashboard/", "/api/dashboard/3", "/api/native-query-snippet",
	} {
		code, body := get(t, s, p, true)
		if code != http.StatusOK {
			t.Fatalf("%s: HTTP %d", p, code)
		}
		served.WriteString(body)
	}
	for _, c := range []string{
		"CANARY_HOST", "CANARY_DBUSER", "CANARY_FINGERPRINT", "CANARY_VALUE", "CANARY_SEMANTIC",
		"CANARY_DEFAULT", "CANARY_CREATOR", "CANARY_PERSONAL", Description, TextCard,
	} {
		if !strings.Contains(served.String(), c) {
			t.Errorf("the fake never serves %s, so the privacy tests cannot catch it leaking", c)
		}
	}
}

func TestRequiresCredentials(t *testing.T) {
	if code, _ := get(t, New(t), "/api/database", false); code != http.StatusUnauthorized {
		t.Errorf("no key: HTTP %d, want 401", code)
	}
}

// Metabase 0.57 and later return a different query shape unless asked for
// the classic one, so the fake refuses card requests that do not ask.
func TestRequiresLegacyMBQL(t *testing.T) {
	if code, _ := get(t, New(t), "/api/card/10", true); code != http.StatusBadRequest {
		t.Errorf("card without legacy-mbql: HTTP %d, want 400", code)
	}
}
