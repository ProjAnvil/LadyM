//go:build !enterprise

// Error-path tests for the HTTP data-plane: engine error → HTTP status
// mapping (engineError), auth-summary helper, malformed bodies, the remember
// attention-gate drop shape, and store-failure injection (same pattern as the
// brokenPingStore used by the healthz test).

package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

var errBoom = errors.New("boom: store exploded")

// failStore wraps a live Store and fails selected methods with errBoom, so
// handler error branches can be exercised without breaking the rest.
type failStore struct {
	storage.Store
	getMemoryErr     error
	iterErr          error
	countErr         error
	getUserErr       error
	putUserErr       error
	listUsersErr     error
	deleteUserErr    error
	updateContentErr error
	deleteMemoryErr  error
	putMemoryErr     error
	putEdgeErr       error
	episodicErr      error
	tryLockErr       error
}

func (s *failStore) GetMemory(id string) (*schema.Memory, error) {
	if s.getMemoryErr != nil {
		return nil, s.getMemoryErr
	}
	return s.Store.GetMemory(id)
}

func (s *failStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	if s.iterErr != nil {
		return nil, s.iterErr
	}
	return s.Store.IterMemories(workspace, layer, typ)
}

func (s *failStore) Count(workspace string) (map[string]int, error) {
	if s.countErr != nil {
		return nil, s.countErr
	}
	return s.Store.Count(workspace)
}

func (s *failStore) GetUser(username string) (*schema.User, error) {
	if s.getUserErr != nil {
		return nil, s.getUserErr
	}
	return s.Store.GetUser(username)
}

func (s *failStore) PutUser(u *schema.User) error {
	if s.putUserErr != nil {
		return s.putUserErr
	}
	return s.Store.PutUser(u)
}

func (s *failStore) ListUsers() ([]*schema.User, error) {
	if s.listUsersErr != nil {
		return nil, s.listUsersErr
	}
	return s.Store.ListUsers()
}

func (s *failStore) TryAcquireIndexLock() (func(), error) {
	if s.tryLockErr != nil {
		return nil, s.tryLockErr
	}
	return s.Store.TryAcquireIndexLock()
}

func (s *failStore) DeleteUser(username string) error {
	if s.deleteUserErr != nil {
		return s.deleteUserErr
	}
	return s.Store.DeleteUser(username)
}

func (s *failStore) UpdateMemoryContent(id, content, summary string, tags []string, vector []float32, now float64) error {
	if s.updateContentErr != nil {
		return s.updateContentErr
	}
	return s.Store.UpdateMemoryContent(id, content, summary, tags, vector, now)
}

func (s *failStore) DeleteMemory(id string) error {
	if s.deleteMemoryErr != nil {
		return s.deleteMemoryErr
	}
	return s.Store.DeleteMemory(id)
}

func (s *failStore) PutMemory(mem *schema.Memory, vector []float32) error {
	if s.putMemoryErr != nil {
		return s.putMemoryErr
	}
	return s.Store.PutMemory(mem, vector)
}

func (s *failStore) PutEdge(e *schema.Edge) error {
	if s.putEdgeErr != nil {
		return s.putEdgeErr
	}
	return s.Store.PutEdge(e)
}

func (s *failStore) EpisodicContentsSince(workspace string, since float64) ([]string, error) {
	if s.episodicErr != nil {
		return nil, s.episodicErr
	}
	return s.Store.EpisodicContentsSince(workspace, since)
}

// newFailHandler builds a handler whose engine store fails per the
// failStore flags.
func newFailHandler(t *testing.T, mutate func(*config.Config), fs *failStore) http.Handler {
	t.Helper()
	eng, cfg := newTestEngine(t, mutate)
	fs.Store = eng.Store
	eng.Store = fs
	return api.NewHandler(eng, cfg)
}

// DescribeAuth summarizes the startup auth mode: off in personal mode, a
// bootstrap warning when auth is on with an empty users table, plain "on"
// once users exist or when the users table cannot be read (the store error
// must not turn into a false "no users" warning).
func TestDescribeAuth(t *testing.T) {
	eng, cfg := newTestEngine(t, nil)

	if got := api.DescribeAuth(cfg, eng.Store); got != "off" {
		t.Errorf("auth disabled: DescribeAuth = %q, want %q", got, "off")
	}

	cfg.AuthEnabled = true
	got := api.DescribeAuth(cfg, eng.Store)
	if !strings.Contains(got, "WARNING") || !strings.Contains(got, "ladym user add") {
		t.Errorf("auth on with no users: DescribeAuth = %q, want bootstrap warning", got)
	}

	addUser(t, eng, "root", "s3cret-admin", "", true)
	if got := api.DescribeAuth(cfg, eng.Store); got != "on" {
		t.Errorf("auth on with users: DescribeAuth = %q, want %q", got, "on")
	}

	if got := api.DescribeAuth(cfg, &failStore{Store: eng.Store, listUsersErr: errBoom}); got != "on" {
		t.Errorf("auth on with unreadable users table: DescribeAuth = %q, want %q", got, "on")
	}
}

// engineError mapping: a generic store failure is a 500; an
// IndexInProgressError (user-actionable: another index run holds the lock)
// is a 400.
func TestEngineErrorMapping(t *testing.T) {
	// Generic engine failure -> 500 with the error message.
	h := newFailHandler(t, nil, &failStore{countErr: errBoom})
	rec := do(t, h, "/api/stats", "", "", `{}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("stats with broken store: %d, want 500", rec.Code)
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the engine error: %v", m)
	}

	// Index lock held -> 400 (client-actionable, mirrors the MCP error class).
	eng, cfg := newTestEngine(t, nil)
	release, err := eng.Store.TryAcquireIndexLock()
	if err != nil {
		t.Fatalf("TryAcquireIndexLock: %v", err)
	}
	defer release()
	h2 := api.NewHandler(eng, cfg)
	rec = do(t, h2, "/api/index_code", "", "", `{"root": "."}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("index_code with lock held: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), "index") {
		t.Errorf("400 body should mention the index in progress: %v", m)
	}
}

// handleLogin: malformed body -> 400; well-formed but wrong credentials ->
// 401; the Basic header (middleware level) must also be valid.
func TestLoginErrorPaths(t *testing.T) {
	h := newBasicAuthHandler(t)

	if rec := do(t, h, "/api/login", "root", "s3cret-admin", `{"username":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("login malformed JSON: %d, want 400", rec.Code)
	}
	rec := do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "alice", "password": "wrong"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login wrong password: %d, want 401", rec.Code)
	}
	if m := decodeBody(t, rec); m["error"] != "invalid credentials" {
		t.Errorf("login 401 body = %v", m)
	}
	// Unknown username takes the same path (no user-existence oracle).
	if rec := do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "mallory", "password": "x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("login unknown user: %d, want 401", rec.Code)
	}
}

// The attention gate drops noise: the HTTP response carries gated=dropped
// with null id/hash (mirrors the MCP remember shape).
func TestRememberGatedNoiseHTTP(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/remember", "", "", `{"content": "lol"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("remember noise: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	if m["gated"] != "dropped" || m["id"] != nil || m["hash"] != nil {
		t.Errorf("gated response = %v, want gated=dropped with null id/hash", m)
	}
	if reason, _ := m["reason"].(string); reason == "" {
		t.Errorf("gated response missing reason: %v", m)
	}
}

// /api/recall with code_only=true routes to SearchCode over the code-symbol
// projections instead of the layered memory recall.
func TestRecallCodeOnly(t *testing.T) {
	h := newTestHandler(t, nil)
	root := t.TempDir()
	src := "package main\n\nfunc QuixoticZeppelin() int { return 42 }\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"root": root})
	if rec := do(t, h, "/api/index_code", "", "", string(body)); rec.Code != 200 {
		t.Fatalf("index_code: %d %s", rec.Code, rec.Body.String())
	}

	rec := do(t, h, "/api/recall", "", "", `{"query": "QuixoticZeppelin", "code_only": true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("recall code_only: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	results, _ := m["results"].([]any)
	found := false
	for _, r := range results {
		rm, _ := r.(map[string]any)["memory"].(map[string]any)
		if rm != nil && strings.Contains(rm["summary"].(string), "QuixoticZeppelin") {
			found = true
		}
	}
	if !found {
		t.Errorf("code_only recall did not find the indexed symbol: %v", m)
	}
}

// An index-lock acquisition failure that is not "already held" (e.g. the
// store is broken) is a generic engine error -> 500.
func TestIndexCodeStoreError(t *testing.T) {
	h := newFailHandler(t, nil, &failStore{tryLockErr: errBoom})
	rec := do(t, h, "/api/index_code", "", "", `{"root": "."}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("index_code with broken lock: %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}

// /api/consolidate with a malformed body is a 400 (decode error, not an
// engine call).
func TestConsolidateMalformedJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	if rec := do(t, h, "/api/consolidate", "", "", `{"workspace":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("consolidate malformed JSON: %d, want 400", rec.Code)
	}
}

// A non-admin forget whose GetMemory lookup fails (store error) must surface
// as a 500 from the workspace-enforcement guard, not a silent allow.
func TestEnforceMemoryWorkspaceStoreError(t *testing.T) {
	eng, cfg := newTestEngine(t, func(cfg *config.Config) { cfg.AuthEnabled = true })
	addUser(t, eng, "root", "s3cret-admin", "", true)
	addUser(t, eng, "alice", "pw-alice", "acme", false)
	eng.Store = &failStore{Store: eng.Store, getMemoryErr: errBoom}
	h := api.NewHandler(eng, cfg)

	rec := do(t, h, "/api/forget", "alice", "pw-alice", `{"memory_id": "mem-1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forget with broken GetMemory: %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}

// Engine write-path failures surface as 500 on the write endpoints:
// remember (the attention gate's recent-duplicate scan), forget
// (DeleteMemory), link (the edges table's FK to memories rejects endpoints
// that don't exist). Note the engine layers capture the store at engine.New,
// so only failures on paths that consult eng.Store at call time can be
// injected by wrapping.
func TestWriteEndpointsEngineErrors(t *testing.T) {
	h := newFailHandler(t, nil, &failStore{episodicErr: errBoom})
	if rec := do(t, h, "/api/remember", "", "", `{"content": "a substantive quixotic fact worth storing"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("remember with broken attention-gate scan: %d, want 500 (%s)", rec.Code, rec.Body.String())
	}

	h = newFailHandler(t, nil, &failStore{deleteMemoryErr: errBoom})
	rec := do(t, h, "/api/forget", "", "", `{"memory_id": "mem-1"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("forget with broken DeleteMemory: %d, want 500 (%s)", rec.Code, rec.Body.String())
	}

	// Linking two nonexistent ids violates the edges FK -> 500.
	h = newTestHandler(t, nil)
	rec = do(t, h, "/api/link", "", "", `{"src": "no-such-src", "dst": "no-such-dst"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("link with dangling endpoints: %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}
