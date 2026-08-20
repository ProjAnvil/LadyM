//go:build !enterprise

// Package api_test exercises the HTTP data-plane (mirrors of the 9 MCP tools).
package api_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	"golang.org/x/crypto/bcrypt"
)

// newTestHandler builds an engine + HTTP handler against a temp sqlite db.
func newTestHandler(t *testing.T, mutate func(*config.Config)) http.Handler {
	t.Helper()
	eng, _ := newTestEngine(t, mutate)
	return api.NewHandler(eng, eng.Config)
}

// newTestEngine builds an engine against a temp sqlite db (returned so tests
// can seed the users table directly).
func newTestEngine(t *testing.T, mutate func(*config.Config)) (*engine.Engine, *config.Config) {
	t.Helper()
	cfg := config.ForTesting(t.TempDir())
	if mutate != nil {
		mutate(cfg)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng, cfg
}

// addUser seeds one row in the users table. A non-empty password is bcrypt
// hashed; an empty password creates a passwordless user.
func addUser(t *testing.T, eng *engine.Engine, username, password, workspace string, admin bool) {
	t.Helper()
	hash := ""
	if password != "" {
		b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		hash = string(b)
	}
	if err := eng.Store.PutUser(&schema.User{
		Username: username, PasswordHash: hash, Workspace: workspace,
		Admin: admin, CreatedAt: schema.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// do issues one POST against the handler and returns the recorder. user/pass
// become an HTTP Basic Authorization header; both empty = no header.
func do(t *testing.T, h http.Handler, path, user, pass, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, h, http.MethodPost, path, user, pass, body)
}

// doReq issues one request against the handler and returns the recorder.
func doReq(t *testing.T, h http.Handler, method, path, user, pass, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response is not JSON: %q (%v)", rec.Body.String(), err)
	}
	return m
}

// ---------------------------------------------------------------------------
// happy path — all 9 endpoints
// ---------------------------------------------------------------------------

func TestRememberAndRecall(t *testing.T) {
	h := newTestHandler(t, nil)

	rec := do(t, h, "/api/remember", "", "", `{"content": "quixotic zeppelins drift over ladym"}`)
	if rec.Code != 200 {
		t.Fatalf("remember: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["id"] == nil || m["id"] == "" {
		t.Fatalf("remember response missing id: %v", m)
	}
	id := m["id"].(string)

	rec = do(t, h, "/api/recall", "", "", `{"query": "quixotic zeppelins"}`)
	if rec.Code != 200 {
		t.Fatalf("recall: %d %s", rec.Code, rec.Body.String())
	}
	m = decodeBody(t, rec)
	results, _ := m["results"].([]any)
	found := false
	for _, r := range results {
		rm := r.(map[string]any)["memory"].(map[string]any)
		if rm["id"] == id {
			found = true
		}
	}
	if !found {
		t.Errorf("recall did not return remembered id %s: %v", id, m)
	}
}

func TestRecordEvent(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/record_event", "", "", `{"agent": "tester", "action": "ran tests", "outcome": "green"}`)
	if rec.Code != 200 {
		t.Fatalf("record_event: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["id"] == nil || m["layer"] != "L1_episodic" {
		t.Errorf("unexpected record_event response: %v", m)
	}
}

func TestSearchCodeAndIndexCode(t *testing.T) {
	root := t.TempDir()
	src := `package main

func QuixoticZeppelin() int { return 42 }

func main() {}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newTestHandler(t, nil)

	body, _ := json.Marshal(map[string]any{"root": root})
	rec := do(t, h, "/api/index_code", "", "", string(body))
	if rec.Code != 200 {
		t.Fatalf("index_code: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if n, _ := m["files_indexed"].(float64); n < 1 {
		t.Errorf("index_code files_indexed = %v, want >= 1", m["files_indexed"])
	}

	rec = do(t, h, "/api/search_code", "", "", `{"query": "QuixoticZeppelin"}`)
	if rec.Code != 200 {
		t.Fatalf("search_code: %d %s", rec.Code, rec.Body.String())
	}
	m = decodeBody(t, rec)
	results, _ := m["results"].([]any)
	if len(results) == 0 {
		t.Errorf("search_code returned no results for indexed symbol: %v", m)
	}
}

func TestConsolidate(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/consolidate", "", "", `{}`)
	if rec.Code != 200 {
		t.Fatalf("consolidate: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if _, ok := m["kept_episodes"]; !ok {
		t.Errorf("consolidate response missing kept_episodes: %v", m)
	}
}

func TestStats(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/stats", "", "", `{}`)
	if rec.Code != 200 {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if _, ok := m["total_memories"]; !ok {
		t.Errorf("stats response missing total_memories: %v", m)
	}
	if _, ok := m["workspaces"]; !ok {
		t.Errorf("stats response missing workspaces: %v", m)
	}
}

func TestLinkAndForget(t *testing.T) {
	h := newTestHandler(t, nil)
	r1 := do(t, h, "/api/remember", "", "", `{"content": "first linked fact about zeppelins"}`)
	r2 := do(t, h, "/api/remember", "", "", `{"content": "second linked fact about airships"}`)
	id1 := decodeBody(t, r1)["id"].(string)
	id2 := decodeBody(t, r2)["id"].(string)

	body, _ := json.Marshal(map[string]any{"src": id1, "dst": id2})
	rec := do(t, h, "/api/link", "", "", string(body))
	if rec.Code != 200 {
		t.Fatalf("link: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["src"] != id1 || m["dst"] != id2 {
		t.Errorf("link response: %v", m)
	}

	body, _ = json.Marshal(map[string]any{"memory_id": id1})
	rec = do(t, h, "/api/forget", "", "", string(body))
	if rec.Code != 200 {
		t.Fatalf("forget: %d %s", rec.Code, rec.Body.String())
	}
	m = decodeBody(t, rec)
	if m["forgotten"] != id1 {
		t.Errorf("forget response: %v", m)
	}
}

// ---------------------------------------------------------------------------
// auth: [auth] enabled=false (default) passes everything through
// ---------------------------------------------------------------------------

func TestAuthDisabledAllowsAll(t *testing.T) {
	h := newTestHandler(t, nil)
	if rec := do(t, h, "/api/stats", "", "", `{}`); rec.Code != 200 {
		t.Fatalf("personal mode: %d %s", rec.Code, rec.Body.String())
	}
	// A stray Basic header from a generic client must not break personal mode.
	if rec := do(t, h, "/api/stats", "nobody", "whatever", `{}`); rec.Code != 200 {
		t.Fatalf("personal mode with stray header: %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// auth: Basic credential matrix
// ---------------------------------------------------------------------------

// newBasicAuthHandler builds an auth-enabled handler with three users:
// root (admin, password), alice (workspace acme, password) and nopw
// (workspace globex, passwordless).
func newBasicAuthHandler(t *testing.T) http.Handler {
	t.Helper()
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	addUser(t, eng, "root", "s3cret-admin", "", true)
	addUser(t, eng, "alice", "pw-alice", "acme", false)
	addUser(t, eng, "nopw", "", "globex", false)
	return api.NewHandler(eng, cfg)
}

func TestBasicAuthMatrix(t *testing.T) {
	h := newBasicAuthHandler(t)

	cases := []struct {
		name       string
		user, pass string
		want       int
	}{
		{"no header", "", "", 401},
		{"unknown user", "mallory", "x", 401},
		{"wrong password", "alice", "wrong", 401},
		{"empty password on password user", "alice", "", 401},
		{"correct user+password", "alice", "pw-alice", 200},
		{"admin correct", "root", "s3cret-admin", 200},
		{"passwordless user, empty password", "nopw", "", 200},
		{"passwordless user, non-empty password", "nopw", "x", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := do(t, h, "/api/stats", c.user, c.pass, `{}`)
			if rec.Code != c.want {
				t.Fatalf("%s: %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
			}
			if c.want == 401 {
				if m := decodeBody(t, rec); m["error"] != "unauthorized" {
					t.Errorf("%s: 401 body = %v", c.name, m)
				}
			}
		})
	}
}

// auth enabled with an empty users table: every /api/* request is 401.
func TestAuthEnabledNoUsers(t *testing.T) {
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	h := api.NewHandler(eng, cfg)
	if rec := do(t, h, "/api/stats", "anyone", "x", `{}`); rec.Code != 401 {
		t.Fatalf("no users: %d, want 401", rec.Code)
	}
	if rec := do(t, h, "/api/stats", "", "", `{}`); rec.Code != 401 {
		t.Fatalf("no users, no header: %d, want 401", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// workspace enforcement: non-admin forced, admin free
// ---------------------------------------------------------------------------

func TestNonAdminForcesWorkspace(t *testing.T) {
	h := newBasicAuthHandler(t)

	// Alice's body workspace is ignored; the mapping wins and is echoed.
	rec := do(t, h, "/api/remember", "alice", "pw-alice",
		`{"content": "alice scoped zeppelin fact", "workspace": "not-acme"}`)
	if rec.Code != 200 {
		t.Fatalf("alice remember: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Ladym-Workspace"); got != "acme" {
		t.Errorf("X-Ladym-Workspace = %q, want acme", got)
	}

	// The write landed in "acme" (checked with the admin account).
	rec = do(t, h, "/api/stats", "root", "s3cret-admin", `{"workspace": "acme"}`)
	m := decodeBody(t, rec)
	if n, _ := m["total_memories"].(float64); n != 1 {
		t.Errorf("acme stats total_memories = %v, want 1 (write forced to acme): %v", m["total_memories"], m)
	}
}

func TestAdminChoosesWorkspace(t *testing.T) {
	h := newBasicAuthHandler(t)
	rec := do(t, h, "/api/remember", "root", "s3cret-admin",
		`{"content": "admin picked workspace zeppelin fact", "workspace": "picked"}`)
	if rec.Code != 200 {
		t.Fatalf("admin remember: %d %s", rec.Code, rec.Body.String())
	}
	// No forced workspace -> no echo header.
	if got := rec.Header().Get("X-Ladym-Workspace"); got != "" {
		t.Errorf("admin X-Ladym-Workspace = %q, want empty", got)
	}
	rec = do(t, h, "/api/stats", "root", "s3cret-admin", `{"workspace": "picked"}`)
	if n, _ := decodeBody(t, rec)["total_memories"].(float64); n != 1 {
		t.Errorf("picked stats total_memories = %v, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// POST /api/login
// ---------------------------------------------------------------------------

func TestLogin(t *testing.T) {
	h := newBasicAuthHandler(t)

	// Valid credentials -> 200 with the account profile.
	rec := do(t, h, "/api/login", "alice", "pw-alice", `{"username": "alice", "password": "pw-alice"}`)
	if rec.Code != 200 {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["username"] != "alice" || m["workspace"] != "acme" || m["admin"] != false {
		t.Errorf("login response = %v", m)
	}
	rec = do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "root", "password": "s3cret-admin"}`)
	m = decodeBody(t, rec)
	if m["admin"] != true {
		t.Errorf("admin login response = %v", m)
	}
	// Passwordless account logs in with an empty password.
	rec = do(t, h, "/api/login", "nopw", "", `{"username": "nopw", "password": ""}`)
	if rec.Code != 200 {
		t.Fatalf("passwordless login: %d %s", rec.Code, rec.Body.String())
	}

	// Wrong password and unknown user share one indistinguishable 401.
	for _, body := range []string{
		`{"username": "alice", "password": "wrong"}`,
		`{"username": "mallory", "password": "x"}`,
	} {
		rec := do(t, h, "/api/login", "alice", "pw-alice", body)
		if rec.Code != 401 {
			t.Fatalf("login %s: %d, want 401", body, rec.Code)
		}
		if m := decodeBody(t, rec); m["error"] != "invalid credentials" {
			t.Errorf("login 401 body = %v", m)
		}
	}

	// Login is behind the auth middleware like every other /api/* endpoint.
	if rec := do(t, h, "/api/login", "", "", `{"username": "alice", "password": "pw-alice"}`); rec.Code != 401 {
		t.Fatalf("login without Basic header: %d, want 401", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// bad requests
// ---------------------------------------------------------------------------

func TestMalformedJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	for _, path := range []string{"/api/recall", "/api/stats"} {
		rec := do(t, h, path, "", "", `{not json`)
		if rec.Code != 400 {
			t.Fatalf("%s malformed JSON: %d, want 400", path, rec.Code)
		}
		if m := decodeBody(t, rec); m["error"] == nil {
			t.Errorf("%s 400 body missing error field: %v", path, m)
		}
	}
}

func TestMissingRequiredField(t *testing.T) {
	h := newTestHandler(t, nil)
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/recall", `{}`},
		{"/api/remember", `{}`},
		{"/api/record_event", `{"agent": "a"}`},
		{"/api/search_code", `{}`},
		{"/api/index_code", `{}`},
		{"/api/link", `{"src": "x"}`},
		{"/api/forget", `{}`},
	} {
		if rec := do(t, h, tc.path, "", "", tc.body); rec.Code != 400 {
			t.Errorf("%s %s: %d, want 400", tc.path, tc.body, rec.Code)
		}
	}
}

func TestUnknownPath404(t *testing.T) {
	h := newTestHandler(t, nil)
	if rec := do(t, h, "/api/nope", "", "", `{}`); rec.Code != 404 {
		t.Fatalf("unknown path: %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// healthz / metrics / request log (ops baseline)
// ---------------------------------------------------------------------------

// brokenPingStore wraps a live Store but makes Ping fail (with a multi-line
// message, so the healthz handler's single-line detail is exercised).
type brokenPingStore struct {
	storage.Store
}

func (b *brokenPingStore) Ping() error { return errors.New("pg: connection refused\nsecond line") }

func TestHealthzOK(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := doReq(t, h, http.MethodGet, "/healthz", "", "", "")
	if rec.Code != 200 {
		t.Fatalf("healthz: %d %s", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); m["status"] != "ok" {
		t.Errorf("healthz body = %v", m)
	}
}

// healthz is registered before auth: with auth enabled it still answers
// without credentials.
func TestHealthzSkipsAuth(t *testing.T) {
	h := newBasicAuthHandler(t)
	rec := doReq(t, h, http.MethodGet, "/healthz", "", "", "")
	if rec.Code != 200 {
		t.Fatalf("healthz without credentials: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHealthzStoreDown(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	eng.Store = &brokenPingStore{eng.Store}
	h := api.NewHandler(eng, cfg)

	rec := doReq(t, h, http.MethodGet, "/healthz", "", "", "")
	if rec.Code != 503 {
		t.Fatalf("healthz with dead store: %d, want 503", rec.Code)
	}
	m := decodeBody(t, rec)
	if m["status"] != "error" {
		t.Errorf("healthz body = %v", m)
	}
	detail, _ := m["detail"].(string)
	if detail == "" || strings.Contains(detail, "\n") {
		t.Errorf("detail must be a non-empty single line: %q", detail)
	}
}

func TestMetrics(t *testing.T) {
	h := newBasicAuthHandler(t)

	// Auth required, like every other /api/* endpoint.
	if rec := doReq(t, h, http.MethodGet, "/api/metrics", "", "", ""); rec.Code != 401 {
		t.Fatalf("metrics without credentials: %d, want 401", rec.Code)
	}

	// One good recall, one bad-request recall, one good stats.
	if rec := do(t, h, "/api/recall", "root", "s3cret-admin", `{"query": "zeppelins"}`); rec.Code != 200 {
		t.Fatalf("recall: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "/api/recall", "root", "s3cret-admin", `{}`); rec.Code != 400 {
		t.Fatalf("recall missing query: %d, want 400", rec.Code)
	}
	if rec := do(t, h, "/api/stats", "root", "s3cret-admin", `{}`); rec.Code != 200 {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}

	rec := doReq(t, h, http.MethodGet, "/api/metrics", "root", "s3cret-admin", "")
	if rec.Code != 200 {
		t.Fatalf("metrics: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	endpoints, _ := m["endpoints"].(map[string]any)
	if endpoints == nil {
		t.Fatalf("metrics missing endpoints: %v", m)
	}
	recall, _ := endpoints["/api/recall"].(map[string]any)
	if recall == nil {
		t.Fatalf("metrics missing /api/recall: %v", endpoints)
	}
	if n, _ := recall["requests"].(float64); n != 2 {
		t.Errorf("/api/recall requests = %v, want 2", recall["requests"])
	}
	if n, _ := recall["errors"].(float64); n != 1 {
		t.Errorf("/api/recall errors = %v, want 1 (the 400)", recall["errors"])
	}
	stats, _ := endpoints["/api/stats"].(map[string]any)
	if stats == nil {
		t.Fatalf("metrics missing /api/stats: %v", endpoints)
	}
	if n, _ := stats["requests"].(float64); n != 1 {
		t.Errorf("/api/stats requests = %v, want 1", stats["requests"])
	}
	avg, ok := m["recall_avg_ms"].(float64)
	if !ok || avg < 0 {
		t.Errorf("recall_avg_ms = %v, want a non-negative number", m["recall_avg_ms"])
	}
}

// The request log is one stderr line per /api/* request:
// "method path status duration_ms workspace".
func TestRequestLogLine(t *testing.T) {
	h := newTestHandler(t, nil)

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()

	if rec := do(t, h, "/api/stats", "", "", `{}`); rec.Code != 200 {
		t.Fatalf("stats: %d", rec.Code)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 1 {
		t.Fatalf("request log lines = %d, want 1: %q", len(lines), out)
	}
	fields := strings.Fields(lines[0])
	// method path status duration_ms workspace
	if len(fields) != 5 {
		t.Fatalf("log line fields = %v, want 5 fields", fields)
	}
	if fields[0] != "POST" || fields[1] != "/api/stats" || fields[2] != "200" {
		t.Errorf("log line = %q", lines[0])
	}
	if !strings.HasSuffix(fields[3], "ms") {
		t.Errorf("duration field = %q, want <n>ms", fields[3])
	}
}

// ---------------------------------------------------------------------------
// workspace isolation: stats scoping + cross-workspace write protection
// (non-admin users are bound to their own workspace)
// ---------------------------------------------------------------------------

// newIsolationHandler wires an auth-enabled handler with admin root and
// non-admin alice (workspace "acme"), plus a remember helper that writes one
// memory into workspace ws via the admin account.
func newIsolationHandler(t *testing.T) (http.Handler, func(ws, content string) string) {
	t.Helper()
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	addUser(t, eng, "root", "s3cret-admin", "", true)
	addUser(t, eng, "alice", "pw-alice", "acme", false)
	h := api.NewHandler(eng, cfg)
	remember := func(ws, content string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"content": content, "workspace": ws})
		rec := do(t, h, "/api/remember", "root", "s3cret-admin", string(body))
		if rec.Code != 200 {
			t.Fatalf("remember into %q: %d %s", ws, rec.Code, rec.Body.String())
		}
		return decodeBody(t, rec)["id"].(string)
	}
	return h, remember
}

func TestNonAdminStatsIsolation(t *testing.T) {
	h, remember := newIsolationHandler(t)
	remember("acme", "acme-only quixotic fact one")
	remember("acme", "acme-only quixotic fact two")
	remember("other", "other-workspace quixotic fact")

	// Non-admin: counts scoped to "acme", workspaces list contains only
	// "acme" — the full store roster must not leak.
	rec := do(t, h, "/api/stats", "alice", "pw-alice", `{}`)
	if rec.Code != 200 {
		t.Fatalf("alice stats: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if n, _ := m["total_memories"].(float64); n != 2 {
		t.Errorf("alice stats total_memories = %v, want 2: %v", m["total_memories"], m)
	}
	wss, _ := m["workspaces"].([]any)
	if len(wss) != 1 || wss[0] != "acme" {
		t.Errorf("alice stats workspaces = %v, want [acme]", m["workspaces"])
	}

	// Admin: full workspaces list still visible, and body workspace scopes
	// the counts.
	rec = do(t, h, "/api/stats", "root", "s3cret-admin", `{}`)
	if rec.Code != 200 {
		t.Fatalf("admin stats: %d %s", rec.Code, rec.Body.String())
	}
	m = decodeBody(t, rec)
	seen := map[string]bool{}
	for _, ws := range m["workspaces"].([]any) {
		seen[ws.(string)] = true
	}
	if !seen["acme"] || !seen["other"] {
		t.Errorf("admin stats workspaces = %v, want both acme and other", m["workspaces"])
	}
	if n, _ := m["total_memories"].(float64); n != 0 {
		t.Errorf("admin stats (default ws) total_memories = %v, want 0", m["total_memories"])
	}

	rec = do(t, h, "/api/stats", "root", "s3cret-admin", `{"workspace": "other"}`)
	m = decodeBody(t, rec)
	if n, _ := m["total_memories"].(float64); n != 1 {
		t.Errorf("admin stats workspace=other total_memories = %v, want 1", m["total_memories"])
	}
}

// A non-admin stats request must not be able to escape its workspace via a
// body workspace field either.
func TestNonAdminStatsBodyWorkspaceIgnored(t *testing.T) {
	h, remember := newIsolationHandler(t)
	remember("other", "other-workspace quixotic fact")
	rec := do(t, h, "/api/stats", "alice", "pw-alice", `{"workspace": "other"}`)
	if rec.Code != 200 {
		t.Fatalf("alice stats: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if n, _ := m["total_memories"].(float64); n != 0 {
		t.Errorf("alice stats with body workspace=other escaped enforcement: %v", m)
	}
}

func TestNonAdminForgetCrossWorkspace(t *testing.T) {
	h, remember := newIsolationHandler(t)
	acmeID := remember("acme", "acme-owned quixotic fact")
	otherID := remember("other", "other-workspace quixotic fact")

	// Cross-workspace forget -> 403, and the memory survives.
	body, _ := json.Marshal(map[string]any{"memory_id": otherID})
	rec := do(t, h, "/api/forget", "alice", "pw-alice", string(body))
	if rec.Code != 403 {
		t.Fatalf("cross-workspace forget: %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); m["error"] == nil {
		t.Errorf("403 body missing error field: %v", m)
	}
	rec = do(t, h, "/api/recall", "root", "s3cret-admin", `{"query": "other-workspace quixotic", "workspace": "other"}`)
	results, _ := decodeBody(t, rec)["results"].([]any)
	if len(results) == 0 {
		t.Error("cross-workspace memory was deleted despite 403")
	}

	// Same workspace -> 200.
	body, _ = json.Marshal(map[string]any{"memory_id": acmeID})
	if rec := do(t, h, "/api/forget", "alice", "pw-alice", string(body)); rec.Code != 200 {
		t.Fatalf("same-workspace forget: %d %s", rec.Code, rec.Body.String())
	}

	// Missing id keeps the existing semantics (DeleteMemory is a no-op).
	body, _ = json.Marshal(map[string]any{"memory_id": "no-such-id"})
	if rec := do(t, h, "/api/forget", "alice", "pw-alice", string(body)); rec.Code != 200 {
		t.Fatalf("forget missing id: %d, want 200 (unchanged no-op semantics)", rec.Code)
	}

	// Admin is not workspace-forced: no check, forgets anywhere.
	otherID2 := remember("other", "another other-workspace quixotic fact")
	body, _ = json.Marshal(map[string]any{"memory_id": otherID2})
	if rec := do(t, h, "/api/forget", "root", "s3cret-admin", string(body)); rec.Code != 200 {
		t.Fatalf("admin forget: %d %s", rec.Code, rec.Body.String())
	}
}

func TestNonAdminLinkCrossWorkspace(t *testing.T) {
	h, remember := newIsolationHandler(t)
	acmeID := remember("acme", "acme linkable quixotic fact")
	acmeID2 := remember("acme", "second acme linkable quixotic fact")
	otherID := remember("other", "other linkable quixotic fact")

	// dst in another workspace -> 403.
	body, _ := json.Marshal(map[string]any{"src": acmeID, "dst": otherID})
	if rec := do(t, h, "/api/link", "alice", "pw-alice", string(body)); rec.Code != 403 {
		t.Fatalf("link with foreign dst: %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	// src in another workspace -> 403.
	body, _ = json.Marshal(map[string]any{"src": otherID, "dst": acmeID})
	if rec := do(t, h, "/api/link", "alice", "pw-alice", string(body)); rec.Code != 403 {
		t.Fatalf("link with foreign src: %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	// Both in the user's workspace -> 200.
	body, _ = json.Marshal(map[string]any{"src": acmeID, "dst": acmeID2})
	if rec := do(t, h, "/api/link", "alice", "pw-alice", string(body)); rec.Code != 200 {
		t.Fatalf("same-workspace link: %d %s", rec.Code, rec.Body.String())
	}
	// Admin: no enforcement.
	body, _ = json.Marshal(map[string]any{"src": acmeID2, "dst": otherID})
	if rec := do(t, h, "/api/link", "root", "s3cret-admin", string(body)); rec.Code != 200 {
		t.Fatalf("admin link: %d %s", rec.Code, rec.Body.String())
	}
}
