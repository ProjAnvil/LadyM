//go:build !enterprise

// Store-failure error paths for the console data-CRUD endpoints (the happy
// paths and workspace-enforcement matrix live in crud_test.go): every store
// error must surface as a 500 carrying the engine error via engineError.

package api_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestListMemoriesStoreError(t *testing.T) {
	h := newFailHandler(t, nil, &failStore{iterErr: errBoom})
	rec := doReq(t, h, http.MethodGet, "/api/memories", "", "", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list memories with broken IterMemories: %d, want 500", rec.Code)
	}
	if m := decodeBody(t, rec); !strings.Contains(m["error"].(string), errBoom.Error()) {
		t.Errorf("500 body should carry the store error: %v", m)
	}
}

func TestUpdateMemoryStoreErrors(t *testing.T) {
	// GetMemory failure -> 500 before any workspace check.
	h := newFailHandler(t, nil, &failStore{getMemoryErr: errBoom})
	if rec := putMemory(t, h, "", "", "mem-1", `{"summary": "x"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update with broken GetMemory: %d, want 500", rec.Code)
	}

	// UpdateMemoryContent failure -> 500, and the stored row is untouched.
	h = newFailHandler(t, nil, &failStore{updateContentErr: errBoom})
	id := rememberWS(t, h, "w1", "immutable quixotic fact under failure")
	if rec := putMemory(t, h, "", "", id, `{"summary": "should not stick"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update with broken UpdateMemoryContent: %d, want 500", rec.Code)
	}
	m, code := listMemories(t, h, "", "", "?workspace=w1")
	if code != 200 {
		t.Fatalf("list after failed update: %d", code)
	}
	mems, _ := m["memories"].([]any)
	if len(mems) != 1 || mems[0].(map[string]any)["summary"] == "should not stick" {
		t.Errorf("failed update must not persist: %v", m)
	}
}

func TestDeleteMemoryStoreErrors(t *testing.T) {
	// GetMemory failure -> 500.
	h := newFailHandler(t, nil, &failStore{getMemoryErr: errBoom})
	if rec := doReq(t, h, http.MethodDelete, "/api/memories/mem-1", "", "", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete with broken GetMemory: %d, want 500", rec.Code)
	}

	// DeleteMemory failure -> 500, memory survives.
	h = newFailHandler(t, nil, &failStore{deleteMemoryErr: errBoom})
	id := rememberWS(t, h, "w1", "undeletable quixotic fact under failure")
	if rec := doReq(t, h, http.MethodDelete, "/api/memories/"+id, "", "", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete with broken DeleteMemory: %d, want 500", rec.Code)
	}
	m, code := listMemories(t, h, "", "", "?workspace=w1")
	if code != 200 {
		t.Fatalf("list after failed delete: %d", code)
	}
	if n, _ := m["total"].(float64); n != 1 {
		t.Errorf("failed delete must keep the memory: total = %v", m["total"])
	}
}

// The users endpoints with the store failing at each step (auth disabled:
// the admin gate is open, so the store calls are reached directly).
func TestUsersEndpointsStoreErrors(t *testing.T) {
	// ListUsers failure.
	h := newFailHandler(t, nil, &failStore{listUsersErr: errBoom})
	if rec := doReq(t, h, http.MethodGet, "/api/users", "", "", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("list users with broken ListUsers: %d, want 500", rec.Code)
	}

	// Create: GetUser (existence probe) failure.
	h = newFailHandler(t, nil, &failStore{getUserErr: errBoom})
	if rec := do(t, h, "/api/users", "", "", `{"username": "x"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create user with broken GetUser: %d, want 500", rec.Code)
	}

	// Create: PutUser failure.
	h = newFailHandler(t, nil, &failStore{putUserErr: errBoom})
	if rec := do(t, h, "/api/users", "", "", `{"username": "x"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("create user with broken PutUser: %d, want 500", rec.Code)
	}

	// Update: GetUser failure.
	h = newFailHandler(t, nil, &failStore{getUserErr: errBoom})
	if rec := doReq(t, h, http.MethodPut, "/api/users/x", "", "", `{"admin": true}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("update user with broken GetUser: %d, want 500", rec.Code)
	}

	// Delete: GetUser failure.
	if rec := doReq(t, h, http.MethodDelete, "/api/users/x", "", "", ""); rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete user with broken GetUser: %d, want 500", rec.Code)
	}

	// Delete: DeleteUser failure (user exists).
	h = newFailHandler(t, nil, &failStore{deleteUserErr: errBoom})
	if rec := do(t, h, "/api/users", "", "", `{"username": "victim"}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed user: %d %s", rec.Code, rec.Body.String())
	}
	rec := doReq(t, h, http.MethodDelete, "/api/users/victim", "", "", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete user with broken DeleteUser: %d, want 500", rec.Code)
	}
	// The user must survive the failed delete.
	if rec := doReq(t, h, http.MethodGet, "/api/users", "", "", ""); rec.Code != 200 ||
		len(decodeBody(t, rec)["users"].([]any)) != 1 {
		t.Errorf("failed user delete must keep the row: %d %s", rec.Code, rec.Body.String())
	}
}

// A password longer than bcrypt's 72-byte limit makes hashPassword fail —
// the create endpoint surfaces that as a 500, not a partial write.
func TestCreateUserOverlongPassword(t *testing.T) {
	h := newTestHandler(t, nil)
	long := strings.Repeat("a", 100)
	rec := do(t, h, "/api/users", "", "", `{"username": "longpw", "password": "`+long+`"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create user with >72-byte password: %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	// No partial row: the user must not exist after the hashing failure.
	rec = doReq(t, h, http.MethodGet, "/api/users", "", "", "")
	if users := decodeBody(t, rec)["users"].([]any); len(users) != 0 {
		t.Errorf("overlong-password create must not persist a user: %v", users)
	}
}

// Malformed bodies on the body-decoding CRUD endpoints -> 400.
func TestCRUDMalformedJSON(t *testing.T) {
	h := newTestHandler(t, nil)
	if rec := putMemory(t, h, "", "", "mem-1", `{"summary":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("update memory malformed JSON: %d, want 400", rec.Code)
	}
	if rec := do(t, h, "/api/users", "", "", `{"username":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("create user malformed JSON: %d, want 400", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPut, "/api/users/x", "", "", `{"admin":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("update user malformed JSON: %d, want 400", rec.Code)
	}
}
