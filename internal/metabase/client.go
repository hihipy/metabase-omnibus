// Package metabase is a minimal, read-only Metabase API client.
//
// Credential rules this package enforces:
//   - Keys and session tokens never appear in errors or output.
//   - Plain http:// is refused unless the caller opts in.
//   - Redirects are refused. Go forwards custom headers such as x-api-key
//     across redirects, so following one could hand the key to another host.
package metabase

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config holds connection settings. Set APIKey, or Username and Password.
type Config struct {
	URL       string
	APIKey    string
	Username  string
	Password  string
	CABundle  string // optional PEM file, added to the system pool
	AllowHTTP bool
	Timeout   time.Duration
}

// Client talks to one Metabase instance.
type Client struct {
	base    *url.URL
	http    *http.Client
	apiKey  string
	session string
}

// New validates the config, builds the HTTP client, and signs in if needed.
func New(ctx context.Context, cfg Config) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(cfg.URL, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("invalid Metabase URL %q", cfg.URL)
	}
	switch base.Scheme {
	case "https":
	case "http":
		if !cfg.AllowHTTP {
			return nil, errors.New("refusing plain http: the API key would travel unencrypted (use https, or pass -allow-http for a local test server)")
		}
	default:
		return nil, fmt.Errorf("unsupported URL scheme %q", base.Scheme)
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CABundle != "" {
		pem, err := os.ReadFile(cfg.CABundle)
		if err != nil {
			return nil, fmt.Errorf("reading CA bundle: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA bundle contains no PEM certificates")
		}
		tlsCfg.RootCAs = pool
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	c := &Client{
		base:   base,
		apiKey: cfg.APIKey,
		http: &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{TLSClientConfig: tlsCfg, Proxy: http.ProxyFromEnvironment},
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return fmt.Errorf("refusing redirect to %s: check the Metabase URL", req.URL.Redacted())
			},
		},
	}

	if c.apiKey == "" {
		if cfg.Username == "" || cfg.Password == "" {
			return nil, errors.New("no credentials: set METABASE_API_KEY, or METABASE_USERNAME and METABASE_PASSWORD")
		}
		var out struct {
			ID string `json:"id"`
		}
		body := map[string]string{"username": cfg.Username, "password": cfg.Password}
		if err := c.do(ctx, http.MethodPost, "/api/session", nil, body, &out); err != nil {
			return nil, fmt.Errorf("signing in: %w", err)
		}
		if out.ID == "" {
			return nil, errors.New("signing in: Metabase returned no session")
		}
		c.session = out.ID
	}
	return c, nil
}

// Host returns the instance host, for labeling output.
func (c *Client) Host() string { return c.base.Host }

// Get decodes the JSON response of a GET request into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// Post sends body as JSON and decodes the JSON response into out. The tool
// only posts to endpoints that read, such as compiling a question to SQL.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// Close ends a username/password session. API keys need no cleanup.
func (c *Client) Close(ctx context.Context) {
	if c.session != "" {
		_ = c.do(ctx, http.MethodDelete, "/api/session", nil, nil, nil)
		c.session = ""
	}
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	u.RawQuery = query.Encode()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}
	if c.session != "" {
		req.Header.Set("X-Metabase-Session", c.session)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &HTTPError{Method: method, Path: path, Status: resp.StatusCode, Body: c.scrub(string(snippet))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
	}
	return nil
}

// scrub removes credentials from text that may be shown to the user.
func (c *Client) scrub(s string) string {
	for _, secret := range []string{c.apiKey, c.session} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return strings.TrimSpace(s)
}

// HTTPError is a non-2xx response. It never carries request headers.
type HTTPError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *HTTPError) Error() string {
	hint := ""
	switch e.Status {
	case http.StatusUnauthorized:
		hint = " (the key or password was rejected)"
	case http.StatusForbidden:
		hint = " (your group lacks permission for this)"
	}
	return fmt.Sprintf("%s %s: HTTP %d%s: %s", e.Method, e.Path, e.Status, hint, e.Body)
}
