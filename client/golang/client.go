package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one `ladym serve --http` data-plane.
type Client struct {
	baseURL  string // scheme://host:port, no trailing slash
	user     string // Basic-auth username; "" = no-auth deployment
	password string // Basic-auth password (may be "" for passwordless users)
	hc       *http.Client
}

// Option customizes a Client.
type Option func(*Client)

// WithAuth sets the Basic-auth credentials. A username with an empty
// password is valid — it matches a passwordless server account. With no
// WithAuth at all, no Authorization header is sent (no-auth deployments).
func WithAuth(username, password string) Option {
	return func(c *Client) { c.user, c.password = username, password }
}

// WithHTTPClient swaps the underlying http.Client (proxies, TLS, tracing).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.hc = hc }
}

// WithTimeout sets http.Client.Timeout. Prefer a per-call context deadline;
// this is a blunt instrument for simple callers.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) { c.hc.Timeout = d }
}

// New returns a Client for baseURL (e.g. "http://127.0.0.1:8080"; a trailing
// slash is stripped). No default timeout: callers bound calls via ctx.
func New(baseURL string, opts ...Option) *Client {
	c := &Client{baseURL: strings.TrimRight(baseURL, "/"), hc: &http.Client{}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Error is a non-2xx response from the server. Message is the server's
// {"error": ...} field, falling back to the trimmed body for non-JSON error
// pages (proxies etc.), always single-line.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("ladym server returned http %d: %s", e.StatusCode, e.Message)
}

// do performs one request and decodes the 2xx response into out (nil out =
// discard the body). Error texts are single-line and mirror the CLI's
// historical remote-mode messages (cli/remote.go depends on them).
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return fmt.Errorf("invalid server URL %q: %v", c.baseURL, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.password)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach ladym server at %s: %v", c.baseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response from ladym server at %s: %v", c.baseURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		var eb struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		return &Error{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response from ladym server at %s: %v", c.baseURL, err)
	}
	return nil
}

// post is the common case: one JSON POST to an /api/ endpoint.
func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}
