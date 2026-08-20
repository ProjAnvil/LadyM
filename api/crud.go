// Console data-CRUD endpoints (spec §3.1): memories list/update/delete and
// the admin-only users management API. Auth/workspace semantics are the T1
// ones: every endpoint sits behind the Basic middleware, non-admin users are
// locked to their forced workspace, and the users endpoints additionally
// require admin (auth disabled = implicit trust).
package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/ProjAnvil/LadyM/schema"
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// GET /api/memories
// ---------------------------------------------------------------------------

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// parsePagination reads limit/offset from the query string. limit defaults to
// 50 and is clamped to 200; offset defaults to 0. Non-numeric or negative
// values (and limit=0) are a 400.
func parsePagination(r *http.Request) (limit, offset int, err error) {
	limit, offset = defaultListLimit, 0
	q := r.URL.Query()
	if s := q.Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil || limit <= 0 {
			return 0, 0, fmt.Errorf("invalid limit %q (want 1..%d)", s, maxListLimit)
		}
		if limit > maxListLimit {
			limit = maxListLimit
		}
	}
	if s := q.Get("offset"); s != "" {
		offset, err = strconv.Atoi(s)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("invalid offset %q (want >= 0)", s)
		}
	}
	return limit, offset, nil
}

// handleListMemories lists memories with workspace/layer/type filters and
// in-memory pagination (v1 accepts the in-memory slice: IterMemories has no
// SQL-level LIMIT yet). total is the filtered count before pagination.
// IterMemories is a pure store read (database/sql / pgxpool are
// concurrency-safe), so this handler skips the engine mutex.
func (h *Handler) handleListMemories(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parsePagination(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	ws := h.effectiveWS(r, q.Get("workspace"))
	mems, err := h.eng.Store.IterMemories(ws, q.Get("layer"), q.Get("type"))
	if err != nil {
		engineError(w, err)
		return
	}
	sort.Slice(mems, func(i, j int) bool { return mems[i].ID < mems[j].ID })
	total := len(mems)
	page := make([]*schema.Memory, 0, limit)
	if offset < total {
		end := offset + limit
		if end > total {
			end = total
		}
		page = mems[offset:end]
	}
	writeJSON(w, http.StatusOK, map[string]any{"memories": page, "total": total})
}

// ---------------------------------------------------------------------------
// PUT /api/memories/{id} — partial update of content/summary/tags
// ---------------------------------------------------------------------------

func (h *Handler) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Content *string  `json:"content"`
		Summary *string  `json:"summary"`
		Tags    []string `json:"tags"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Content == nil && body.Summary == nil && body.Tags == nil {
		writeError(w, http.StatusBadRequest, "at least one of content/summary/tags is required")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	m, err := h.eng.Store.GetMemory(id)
	if err != nil {
		engineError(w, err)
		return
	}
	if m == nil {
		writeError(w, http.StatusNotFound, "memory not found: "+id)
		return
	}
	if !h.enforceMemoryWorkspace(w, r, id) {
		return
	}

	content, summary, tags := m.Content, m.Summary, m.Tags
	if body.Content != nil {
		content = *body.Content
	}
	if body.Summary != nil {
		summary = *body.Summary
	}
	if body.Tags != nil {
		tags = body.Tags
	}
	// The PutMemory upsert NULLs the embedding column when the vector is nil,
	// so updates go through Store.UpdateMemoryContent instead: a nil vector
	// preserves the stored embedding; a content change re-embeds and rewrites
	// both embedding and content_hash.
	var vector []float32
	if content != m.Content {
		vector, err = h.eng.Provider.Embed(content)
		if err != nil {
			engineError(w, err)
			return
		}
	}
	if err := h.eng.Store.UpdateMemoryContent(id, content, summary, tags, vector, 0); err != nil {
		engineError(w, err)
		return
	}
	updated, err := h.eng.Store.GetMemory(id)
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory": updated})
}

// ---------------------------------------------------------------------------
// DELETE /api/memories/{id}
// ---------------------------------------------------------------------------

// handleDeleteMemory is the RESTful-path counterpart of /api/forget (which
// keeps its MCP-aligned no-op-on-missing semantics): here a missing id 404s.
func (h *Handler) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	h.mu.Lock()
	defer h.mu.Unlock()
	m, err := h.eng.Store.GetMemory(id)
	if err != nil {
		engineError(w, err)
		return
	}
	if m == nil {
		writeError(w, http.StatusNotFound, "memory not found: "+id)
		return
	}
	if !h.enforceMemoryWorkspace(w, r, id) {
		return
	}
	if err := h.eng.Store.DeleteMemory(id); err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// ---------------------------------------------------------------------------
// /api/users — admin-only account management
// ---------------------------------------------------------------------------

// userJSON serialises an account for the console. PasswordHash never leaves
// the server.
func userJSON(u *schema.User) map[string]any {
	return map[string]any{
		"username": u.Username, "workspace": u.Workspace,
		"admin": u.Admin, "created_at": u.CreatedAt,
	}
}

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	users, err := h.eng.Store.ListUsers()
	if err != nil {
		engineError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, userJSON(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var body struct {
		Username  string `json:"username"`
		Password  string `json:"password"` // empty = passwordless account
		Workspace string `json:"workspace"`
		Admin     bool   `json:"admin"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Username == "" {
		writeError(w, http.StatusBadRequest, "missing required field: username")
		return
	}
	existing, err := h.eng.Store.GetUser(body.Username)
	if err != nil {
		engineError(w, err)
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "user already exists: "+body.Username)
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		engineError(w, err)
		return
	}
	u := &schema.User{
		Username: body.Username, PasswordHash: hash, Workspace: body.Workspace,
		Admin: body.Admin, CreatedAt: schema.Now(),
	}
	if err := h.eng.Store.PutUser(u); err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": userJSON(u)})
}

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	username := r.PathValue("username")
	// Pointer fields distinguish "leave unchanged" (absent) from "set"
	// (present); an explicit empty password makes the account passwordless.
	var body struct {
		Password  *string `json:"password"`
		Workspace *string `json:"workspace"`
		Admin     *bool   `json:"admin"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	u, err := h.eng.Store.GetUser(username)
	if err != nil {
		engineError(w, err)
		return
	}
	if u == nil {
		writeError(w, http.StatusNotFound, "user not found: "+username)
		return
	}
	if body.Password != nil {
		hash, err := hashPassword(*body.Password)
		if err != nil {
			engineError(w, err)
			return
		}
		u.PasswordHash = hash
	}
	if body.Workspace != nil {
		u.Workspace = *body.Workspace
	}
	if body.Admin != nil {
		u.Admin = *body.Admin
	}
	if err := h.eng.Store.PutUser(u); err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": userJSON(u)})
}

func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	username := r.PathValue("username")
	if caller, _ := r.Context().Value(ctxAuthUser).(*schema.User); caller != nil && caller.Username == username {
		writeError(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	u, err := h.eng.Store.GetUser(username)
	if err != nil {
		engineError(w, err)
		return
	}
	if u == nil {
		writeError(w, http.StatusNotFound, "user not found: "+username)
		return
	}
	if err := h.eng.Store.DeleteUser(username); err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": username})
}

// hashPassword bcrypts pw; "" stays "" (passwordless user). Same cost as the
// CLI bootstrap path (cli/user.go).
func hashPassword(pw string) (string, error) {
	if pw == "" {
		return "", nil
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
