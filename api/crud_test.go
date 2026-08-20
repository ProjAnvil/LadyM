//go:build !enterprise

// Console data-CRUD endpoints: /api/memories list/update/delete and the
// admin-only /api/users management endpoints (spec §3.1).
package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/api"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

// rememberWS writes one memory into workspace ws (no-auth handler) and
// returns its id.
func rememberWS(t *testing.T, h http.Handler, ws, content string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"content": content, "workspace": ws})
	rec := do(t, h, "/api/remember", "", "", string(body))
	if rec.Code != 200 {
		t.Fatalf("remember into %q: %d %s", ws, rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec)["id"].(string)
}

// listMemories GETs /api/memories and returns the decoded body.
func listMemories(t *testing.T, h http.Handler, user, pass, query string) (map[string]any, int) {
	t.Helper()
	rec := doReq(t, h, http.MethodGet, "/api/memories"+query, user, pass, "")
	return decodeBody(t, rec), rec.Code
}

// memoryIDs extracts the ids of a list response's "memories" array.
func memoryIDs(t *testing.T, m map[string]any) []string {
	t.Helper()
	mems, _ := m["memories"].([]any)
	ids := make([]string, 0, len(mems))
	for _, raw := range mems {
		ids = append(ids, raw.(map[string]any)["id"].(string))
	}
	return ids
}

// ---------------------------------------------------------------------------
// GET /api/memories — filters, pagination, total
// ---------------------------------------------------------------------------

func TestListMemoriesFiltersAndPagination(t *testing.T) {
	h := newTestHandler(t, nil)
	// 5 semantic facts in w1, 1 episodic event in w1, 1 fact in w2.
	ids := []string{}
	for i := 0; i < 5; i++ {
		ids = append(ids, rememberWS(t, h, "w1", fmt.Sprintf("quixotic fact number %d", i)))
	}
	rec := do(t, h, "/api/record_event", "", "", `{"agent": "a", "action": "ev", "workspace": "w1"}`)
	if rec.Code != 200 {
		t.Fatalf("record_event: %d %s", rec.Code, rec.Body.String())
	}
	rememberWS(t, h, "w2", "other workspace quixotic fact")

	// No filters: everything, total = 7.
	m, code := listMemories(t, h, "", "", "")
	if code != 200 {
		t.Fatalf("list: %d", code)
	}
	if n, _ := m["total"].(float64); n != 7 {
		t.Errorf("total = %v, want 7", m["total"])
	}

	// Workspace filter.
	m, _ = listMemories(t, h, "", "", "?workspace=w1")
	if n, _ := m["total"].(float64); n != 6 {
		t.Errorf("workspace=w1 total = %v, want 6", m["total"])
	}

	// Layer + type filters.
	m, _ = listMemories(t, h, "", "", "?layer=L1_episodic")
	if n, _ := m["total"].(float64); n != 1 {
		t.Errorf("layer filter total = %v, want 1", m["total"])
	}
	m, _ = listMemories(t, h, "", "", "?workspace=w1&type=fact")
	if n, _ := m["total"].(float64); n != 5 {
		t.Errorf("combined filter total = %v, want 5", m["total"])
	}

	// Pagination: limit=2&offset=1 over the 5 w1 facts — total stays 5.
	m, _ = listMemories(t, h, "", "", "?workspace=w1&type=fact&limit=2&offset=1")
	if n, _ := m["total"].(float64); n != 5 {
		t.Errorf("paged total = %v, want 5 (pre-pagination)", m["total"])
	}
	got := memoryIDs(t, m)
	if len(got) != 2 {
		t.Fatalf("page size = %d, want 2", len(got))
	}
	// Sorted by id: the page must be ids[1], ids[2].
	want := append([]string{}, ids...)
	sort.Strings(want)
	if got[0] != want[1] || got[1] != want[2] {
		t.Errorf("page = %v, want [%s %s]", got, want[1], want[2])
	}

	// Offset beyond the end: empty page, total intact.
	m, _ = listMemories(t, h, "", "", "?workspace=w1&type=fact&offset=99")
	if n, _ := m["total"].(float64); n != 5 {
		t.Errorf("offset-beyond total = %v, want 5", m["total"])
	}
	if got := memoryIDs(t, m); len(got) != 0 {
		t.Errorf("offset-beyond page = %v, want empty", got)
	}
	if mems, _ := m["memories"].([]any); mems == nil {
		t.Error("empty page must serialise as [], not null")
	}
}

func TestListMemoriesLimitBounds(t *testing.T) {
	h := newTestHandler(t, nil)
	for i := 0; i < 3; i++ {
		rememberWS(t, h, "w1", fmt.Sprintf("bounded quixotic fact %d", i))
	}

	// Invalid values -> 400.
	for _, q := range []string{
		"?limit=abc", "?limit=-1", "?limit=0", "?offset=xyz", "?offset=-2",
	} {
		if _, code := listMemories(t, h, "", "", q); code != 400 {
			t.Errorf("%s: %d, want 400", q, code)
		}
	}

	// Over the cap: clamped to 200, not an error.
	m, code := listMemories(t, h, "", "", "?limit=9999")
	if code != 200 {
		t.Fatalf("limit=9999: %d, want 200 (clamped)", code)
	}
	if got := memoryIDs(t, m); len(got) != 3 {
		t.Errorf("clamped page = %v, want all 3", got)
	}
}

// Non-admin users are locked to their workspace; the query parameter is
// ignored. Admins may pick one.
func TestListMemoriesWorkspaceEnforcement(t *testing.T) {
	h := newBasicAuthHandler(t)
	rememberWSAuth(t, h, "root", "s3cret-admin", "acme", "acme console quixotic fact")
	rememberWSAuth(t, h, "root", "s3cret-admin", "other", "other console quixotic fact")

	// alice (forced acme) sees only acme, even when asking for "other".
	m, code := listMemories(t, h, "alice", "pw-alice", "")
	if code != 200 {
		t.Fatalf("alice list: %d", code)
	}
	if n, _ := m["total"].(float64); n != 1 {
		t.Errorf("alice total = %v, want 1", m["total"])
	}
	m, _ = listMemories(t, h, "alice", "pw-alice", "?workspace=other")
	if n, _ := m["total"].(float64); n != 1 {
		t.Errorf("alice ?workspace=other escaped enforcement: total = %v, want 1", m["total"])
	}

	// Admin: unfiltered sees both; query filter scopes.
	m, _ = listMemories(t, h, "root", "s3cret-admin", "")
	if n, _ := m["total"].(float64); n != 2 {
		t.Errorf("admin total = %v, want 2", m["total"])
	}
	m, _ = listMemories(t, h, "root", "s3cret-admin", "?workspace=other")
	if n, _ := m["total"].(float64); n != 1 {
		t.Errorf("admin ?workspace=other total = %v, want 1", m["total"])
	}

	// Unauthenticated -> 401 like every other /api/* endpoint.
	if _, code := listMemories(t, h, "", "", ""); code != 401 {
		t.Errorf("no credentials: %d, want 401", code)
	}
}

// rememberWSAuth is rememberWS with Basic credentials.
func rememberWSAuth(t *testing.T, h http.Handler, user, pass, ws, content string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"content": content, "workspace": ws})
	rec := do(t, h, "/api/remember", user, pass, string(body))
	if rec.Code != 200 {
		t.Fatalf("remember into %q: %d %s", ws, rec.Code, rec.Body.String())
	}
	return decodeBody(t, rec)["id"].(string)
}

// ---------------------------------------------------------------------------
// PUT /api/memories/{id}
// ---------------------------------------------------------------------------

// vectorTopHit embeds text and returns the top VectorSearch hit and its
// cosine similarity — a direct, deterministic check of which vector the
// store holds for a memory.
func vectorTopHit(t *testing.T, eng *engine.Engine, text string) (id string, sim float64, ok bool) {
	t.Helper()
	vec, err := eng.Provider.Embed(text)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	hits := eng.Store.VectorSearch(vec, 5)
	if len(hits) == 0 {
		return "", 0, false
	}
	return hits[0].ID, hits[0].Similarity, true
}

func putMemory(t *testing.T, h http.Handler, user, pass, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, h, http.MethodPut, "/api/memories/"+id, user, pass, body)
}

func TestUpdateMemoryBranches(t *testing.T) {
	eng, _ := newTestEngine(t, nil)
	h := api.NewHandler(eng, eng.Config)
	id := rememberWS(t, h, "w1", "zeppelins drift over original quixotic plains")

	// Missing id -> 404.
	if rec := putMemory(t, h, "", "", "no-such-id", `{"summary": "x"}`); rec.Code != 404 {
		t.Fatalf("missing id: %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	// Empty update (no fields) -> 400.
	if rec := putMemory(t, h, "", "", id, `{}`); rec.Code != 400 {
		t.Fatalf("empty update: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}

	// Summary/tags-only update: content untouched, OLD vector must survive
	// (the PutMemory-nil trap would have NULLed it).
	rec := putMemory(t, h, "", "", id, `{"summary": "new summary", "tags": ["x", "y"]}`)
	if rec.Code != 200 {
		t.Fatalf("summary/tags update: %d %s", rec.Code, rec.Body.String())
	}
	m := decodeBody(t, rec)
	mem, _ := m["memory"].(map[string]any)
	if mem == nil || mem["summary"] != "new summary" || mem["content"] != "zeppelins drift over original quixotic plains" {
		t.Errorf("update response memory = %v", m)
	}
	stored, err := eng.Store.GetMemory(id)
	if err != nil || stored == nil {
		t.Fatalf("GetMemory after update: %v %v", stored, err)
	}
	if got, sim, ok := vectorTopHit(t, eng, "zeppelins drift over original quixotic plains"); !ok || got != id || sim < 0.99 {
		t.Errorf("old content no longer matches the stored vector after non-content update (hit=%q sim=%.3f ok=%v) — embedding lost", got, sim, ok)
	}
	oldHash := stored.ContentHash

	// Content update: re-embedded — the NEW content scores, the old does not.
	rec = putMemory(t, h, "", "", id, `{"content": "airships hover above crimson mesas"}`)
	if rec.Code != 200 {
		t.Fatalf("content update: %d %s", rec.Code, rec.Body.String())
	}
	stored, _ = eng.Store.GetMemory(id)
	if stored.Content != "airships hover above crimson mesas" {
		t.Errorf("content after update = %q", stored.Content)
	}
	if stored.ContentHash == oldHash || stored.ContentHash != schema.ContentHash("airships hover above crimson mesas") {
		t.Errorf("content_hash = %q, want hash of new content", stored.ContentHash)
	}
	// Summary/tags from the previous update survive a content-only update.
	if stored.Summary != "new summary" || len(stored.Tags) != 2 {
		t.Errorf("untouched fields clobbered: summary=%q tags=%v", stored.Summary, stored.Tags)
	}
	if got, sim, ok := vectorTopHit(t, eng, "airships hover above crimson mesas"); !ok || got != id || sim < 0.99 {
		t.Errorf("new content not matched by the stored vector after content update (hit=%q sim=%.3f ok=%v)", got, sim, ok)
	}
	if got, sim, _ := vectorTopHit(t, eng, "zeppelins drift over original quixotic plains"); got == id && sim > 0.99 {
		t.Errorf("stale vector still matches old content at %.3f after content update — embedding not recomputed", sim)
	}
	if stored.UpdatedAt == 0 {
		t.Error("updated_at not bumped")
	}
}

func TestUpdateMemoryWorkspaceEnforcement(t *testing.T) {
	h := newBasicAuthHandler(t)
	acmeID := rememberWSAuth(t, h, "root", "s3cret-admin", "acme", "acme editable quixotic fact")
	otherID := rememberWSAuth(t, h, "root", "s3cret-admin", "other", "other editable quixotic fact")

	// Cross-workspace -> 403, memory unchanged.
	if rec := putMemory(t, h, "alice", "pw-alice", otherID, `{"summary": "hijack"}`); rec.Code != 403 {
		t.Fatalf("cross-workspace update: %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	// Own workspace -> 200. Missing id -> 404 (not 403).
	if rec := putMemory(t, h, "alice", "pw-alice", acmeID, `{"summary": "ok"}`); rec.Code != 200 {
		t.Fatalf("same-workspace update: %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := putMemory(t, h, "alice", "pw-alice", "no-such-id", `{"summary": "x"}`); rec.Code != 404 {
		t.Fatalf("missing id as non-admin: %d, want 404", rec.Code)
	}
	// Admin is unconstrained.
	if rec := putMemory(t, h, "root", "s3cret-admin", otherID, `{"summary": "admin edit"}`); rec.Code != 200 {
		t.Fatalf("admin update: %d (%s)", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/memories/{id}
// ---------------------------------------------------------------------------

func TestDeleteMemoryREST(t *testing.T) {
	h := newTestHandler(t, nil)
	id := rememberWS(t, h, "w1", "disposable quixotic fact")

	if rec := doReq(t, h, http.MethodDelete, "/api/memories/no-such-id", "", "", ""); rec.Code != 404 {
		t.Fatalf("delete missing: %d, want 404", rec.Code)
	}
	rec := doReq(t, h, http.MethodDelete, "/api/memories/"+id, "", "", "")
	if rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if m := decodeBody(t, rec); m["deleted"] != id {
		t.Errorf("delete response = %v", m)
	}
	m, _ := listMemories(t, h, "", "", "")
	if n, _ := m["total"].(float64); n != 0 {
		t.Errorf("list after delete total = %v, want 0", m["total"])
	}
}

func TestDeleteMemoryWorkspaceEnforcement(t *testing.T) {
	h := newBasicAuthHandler(t)
	acmeID := rememberWSAuth(t, h, "root", "s3cret-admin", "acme", "acme deletable quixotic fact")
	otherID := rememberWSAuth(t, h, "root", "s3cret-admin", "other", "other deletable quixotic fact")

	if rec := doReq(t, h, http.MethodDelete, "/api/memories/"+otherID, "alice", "pw-alice", ""); rec.Code != 403 {
		t.Fatalf("cross-workspace delete: %d, want 403", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/memories/"+acmeID, "alice", "pw-alice", ""); rec.Code != 200 {
		t.Fatalf("same-workspace delete: %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/memories/"+otherID, "root", "s3cret-admin", ""); rec.Code != 200 {
		t.Fatalf("admin delete: %d", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/memories/"+otherID, "alice", "pw-alice", ""); rec.Code != 404 {
		t.Fatalf("delete after delete: %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// /api/users (admin only)
// ---------------------------------------------------------------------------

func TestUsersCRUDHappyPath(t *testing.T) {
	h := newBasicAuthHandler(t)

	// List: the three seeded users, no password_hash anywhere.
	rec := doReq(t, h, http.MethodGet, "/api/users", "root", "s3cret-admin", "")
	if rec.Code != 200 {
		t.Fatalf("list users: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Errorf("users list leaks password_hash: %s", rec.Body.String())
	}
	m := decodeBody(t, rec)
	users, _ := m["users"].([]any)
	if len(users) != 3 {
		t.Fatalf("users = %v, want 3", users)
	}
	first, _ := users[0].(map[string]any)
	if first["username"] == "" || first["created_at"] == nil {
		t.Errorf("user entry shape = %v", first)
	}

	// Create.
	rec = do(t, h, "/api/users", "root", "s3cret-admin",
		`{"username": "carol", "password": "pw-carol", "workspace": "initech", "admin": false}`)
	if rec.Code != 201 {
		t.Fatalf("create user: %d %s", rec.Code, rec.Body.String())
	}
	m = decodeBody(t, rec)
	if u, _ := m["user"].(map[string]any); u == nil || u["username"] != "carol" || u["workspace"] != "initech" || u["admin"] != false {
		t.Errorf("create response = %v", m)
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Errorf("create response leaks password_hash: %s", rec.Body.String())
	}
	// The password works.
	rec = do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "carol", "password": "pw-carol"}`)
	if rec.Code != 200 {
		t.Fatalf("login as created user: %d", rec.Code)
	}
	// Duplicate username -> 409.
	if rec := do(t, h, "/api/users", "root", "s3cret-admin", `{"username": "carol"}`); rec.Code != 409 {
		t.Fatalf("duplicate create: %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	// Missing username -> 400.
	if rec := do(t, h, "/api/users", "root", "s3cret-admin", `{"password": "x"}`); rec.Code != 400 {
		t.Fatalf("missing username: %d, want 400", rec.Code)
	}
	// Passwordless create (empty password) -> login with empty password.
	rec = do(t, h, "/api/users", "root", "s3cret-admin", `{"username": "dave", "workspace": "w"}`)
	if rec.Code != 201 {
		t.Fatalf("passwordless create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "dave", "password": ""}`); rec.Code != 200 {
		t.Fatalf("login as passwordless user: %d", rec.Code)
	}

	// Update: password + workspace + admin via pointer fields.
	rec = doReq(t, h, http.MethodPut, "/api/users/carol", "root", "s3cret-admin",
		`{"password": "pw-carol-2", "workspace": "hooli", "admin": true}`)
	if rec.Code != 200 {
		t.Fatalf("update user: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "carol", "password": "pw-carol-2"}`)
	if m := decodeBody(t, rec); rec.Code != 200 || m["workspace"] != "hooli" || m["admin"] != true {
		t.Fatalf("login after update: %d %v", rec.Code, m)
	}
	// Omitted password stays; explicit "" makes the user passwordless.
	rec = doReq(t, h, http.MethodPut, "/api/users/carol", "root", "s3cret-admin", `{"password": ""}`)
	if rec.Code != 200 {
		t.Fatalf("set passwordless: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "carol", "password": ""}`); rec.Code != 200 {
		t.Fatalf("login after passwordless update: %d", rec.Code)
	}
	// Update missing user -> 404.
	if rec := doReq(t, h, http.MethodPut, "/api/users/nobody", "root", "s3cret-admin", `{"admin": true}`); rec.Code != 404 {
		t.Fatalf("update missing user: %d, want 404", rec.Code)
	}

	// Delete.
	if rec := doReq(t, h, http.MethodDelete, "/api/users/nobody", "root", "s3cret-admin", ""); rec.Code != 404 {
		t.Fatalf("delete missing user: %d, want 404", rec.Code)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/users/dave", "root", "s3cret-admin", ""); rec.Code != 200 {
		t.Fatalf("delete user: %d", rec.Code)
	}
	// Self-delete refused.
	if rec := doReq(t, h, http.MethodDelete, "/api/users/root", "root", "s3cret-admin", ""); rec.Code != 400 {
		t.Fatalf("self delete: %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	// Deleted user can no longer authenticate.
	if rec := do(t, h, "/api/login", "root", "s3cret-admin", `{"username": "dave", "password": ""}`); rec.Code != 401 {
		t.Fatalf("login as deleted user: %d, want 401", rec.Code)
	}
}

// All four users endpoints require admin when auth is on.
func TestUsersEndpointsRequireAdmin(t *testing.T) {
	h := newBasicAuthHandler(t)
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"username": "x"}`},
		{http.MethodPut, "/api/users/alice", `{"admin": true}`},
		{http.MethodDelete, "/api/users/alice", ""},
	} {
		if rec := doReq(t, h, tc.method, tc.path, "alice", "pw-alice", tc.body); rec.Code != 403 {
			t.Errorf("%s %s as non-admin: %d, want 403", tc.method, tc.path, rec.Code)
		}
		// Unauthenticated: the Basic middleware 401s before the admin check.
		if rec := doReq(t, h, tc.method, tc.path, "", "", tc.body); rec.Code != 401 {
			t.Errorf("%s %s unauthenticated: %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// Auth off (personal mode): the users endpoints are open — v1 treats
// auth-disabled as implicit trust (documented in the report).
func TestUsersEndpointsAuthOff(t *testing.T) {
	h := newTestHandler(t, nil)
	rec := do(t, h, "/api/users", "", "", `{"username": "solo", "password": "pw", "admin": true}`)
	if rec.Code != 201 {
		t.Fatalf("auth-off create: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/users", "", "", "")
	if rec.Code != 200 {
		t.Fatalf("auth-off list: %d", rec.Code)
	}
	if users, _ := decodeBody(t, rec)["users"].([]any); len(users) != 1 {
		t.Errorf("auth-off users = %v, want 1", users)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/users/solo", "", "", ""); rec.Code != 200 {
		t.Fatalf("auth-off delete: %d", rec.Code)
	}
}
