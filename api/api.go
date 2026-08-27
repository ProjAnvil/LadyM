// Package api implements LadyM's HTTP data-plane front-end (`ladym serve
// --http`): one POST endpoint per MCP tool, mirroring the engine calls and
// parameter semantics of mcp/server.go, with optional database-backed HTTP
// Basic auth (users table) and per-user workspace enforcement.
//
// Concurrency: engine.Engine is built for single-process CLI/MCP use (shared
// Config, per-layer Workspace fields, lazily-resolved LLM agents). Rather than
// auditing every call path for races, every engine call is serialized through
// one request-level mutex — simple and safe; throughput is not a goal of this
// front-end.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ProjAnvil/LadyM/code"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	"golang.org/x/crypto/bcrypt"
)

// Handler serves the /api/* endpoints against one engine.
type Handler struct {
	eng *engine.Engine
	cfg *config.Config

	// mu serializes all engine calls (see package doc).
	mu sync.Mutex

	authEnabled bool // [auth] enabled: /api/* requires users-table Basic auth

	metrics *httpMetrics // in-process request counters for /api/metrics
}

type ctxKey int

const (
	ctxForcedWorkspace ctxKey = iota
	ctxAuthUser
)

// NewHandler builds the HTTP front-end (mux + auth middleware) for eng: the
// data-plane mux (NewMux) plus the edition-dependent console mount at "/"
// (console_mount_*.go), wrapped in the standard middleware.
func NewHandler(eng *engine.Engine, cfg *config.Config) http.Handler {
	mux, wrap := NewMux(eng, cfg)
	mountConsole(mux)
	return wrap(mux)
}

// NewMux builds the data-plane mux for eng — /healthz plus every /api/*
// endpoint — with NOTHING registered at "/": the caller decides what lives
// there (the personal edition mounts the embedded console inside NewHandler;
// the enterprise ladymconsole binary mounts it via console.Mount). The
// returned wrapper applies the standard middleware chain (auth +
// observability) and must be applied AFTER any extra top-level mounts.
func NewMux(eng *engine.Engine, cfg *config.Config) (mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	h := &Handler{eng: eng, cfg: cfg, authEnabled: cfg.AuthEnabled, metrics: newHTTPMetrics()}

	mux = http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handleHealthz)
	mux.HandleFunc("GET /api/metrics", h.handleMetrics)
	mux.HandleFunc("POST /api/login", h.handleLogin)
	mux.HandleFunc("POST /api/recall", h.handleRecall)
	mux.HandleFunc("POST /api/remember", h.handleRemember)
	mux.HandleFunc("POST /api/record_event", h.handleRecordEvent)
	mux.HandleFunc("POST /api/search_code", h.handleSearchCode)
	mux.HandleFunc("POST /api/index_code", h.handleIndexCode)
	mux.HandleFunc("POST /api/consolidate", h.handleConsolidate)
	mux.HandleFunc("POST /api/stats", h.handleStats)
	mux.HandleFunc("POST /api/link", h.handleLink)
	mux.HandleFunc("POST /api/forget", h.handleForget)
	// Console data CRUD (spec §3.1): memories list/update/delete plus the
	// admin-only users management endpoints.
	mux.HandleFunc("GET /api/memories", h.handleListMemories)
	mux.HandleFunc("PUT /api/memories/{id}", h.handleUpdateMemory)
	mux.HandleFunc("DELETE /api/memories/{id}", h.handleDeleteMemory)
	mux.HandleFunc("GET /api/users", h.handleListUsers)
	mux.HandleFunc("POST /api/users", h.handleCreateUser)
	mux.HandleFunc("PUT /api/users/{username}", h.handleUpdateUser)
	mux.HandleFunc("DELETE /api/users/{username}", h.handleDeleteUser)
	mux.HandleFunc("GET /api/cjk_dict", h.handleCJKDictStatus)
	mux.HandleFunc("POST /api/cjk_dict/download", h.handleCJKDictDownload)
	mux.HandleFunc("DELETE /api/cjk_dict", h.handleCJKDictRemove)
	// Observability wraps auth so rejected (401) /api/* requests are also
	// logged and counted. /healthz is not under /api/ and stays exempt.
	return mux, func(inner http.Handler) http.Handler {
		return h.withObservability(h.withAuth(inner))
	}
}

// DescribeAuth summarizes the configured auth mode for the startup banner.
// Auth disabled is the default (personal mode — no warning needed); when
// enabled with an empty users table, every request would 401, so the summary
// points at the CLI bootstrap path.
func DescribeAuth(cfg *config.Config, store storage.Store) string {
	if !cfg.AuthEnabled {
		return "off"
	}
	users, err := store.ListUsers()
	if err == nil && len(users) == 0 {
		return "on — WARNING: auth enabled but no users; every /api/* request will 401 — use `ladym user add` to create one"
	}
	return "on"
}

// ---------------------------------------------------------------------------
// auth
// ---------------------------------------------------------------------------

// checkCredentials resolves username/password against the users table: the
// user must exist; a non-empty password_hash is bcrypt-compared; an empty hash
// (passwordless user) only authenticates with an empty password.
func (h *Handler) checkCredentials(username, password string) *schema.User {
	if username == "" {
		return nil
	}
	u, err := h.eng.Store.GetUser(username)
	if err != nil || u == nil {
		return nil
	}
	if u.PasswordHash == "" {
		if password != "" {
			return nil
		}
		return u
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return nil
	}
	return u
}

// authenticate resolves the request's Basic credentials to the effective
// workspace forcing: a non-admin user's workspace is forced (falling back to
// the server default when unset); admins are unconstrained. With auth
// disabled, everything passes (personal mode). The matched user (nil when
// auth is off) is returned so admin-only endpoints can check it.
func (h *Handler) authenticate(r *http.Request) (u *schema.User, forcedWS string, ok bool) {
	if !h.authEnabled {
		return nil, "", true
	}
	username, password, _ := r.BasicAuth()
	u = h.checkCredentials(username, password)
	if u == nil {
		return nil, "", false
	}
	if u.Admin {
		return u, "", true
	}
	forcedWS = u.Workspace
	if forcedWS == "" {
		forcedWS = h.cfg.Workspace
	}
	return u, forcedWS, true
}

func (h *Handler) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			u, forcedWS, ok := h.authenticate(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if forcedWS != "" {
				w.Header().Set("X-Ladym-Workspace", forcedWS)
			}
			ctx := context.WithValue(r.Context(), ctxForcedWorkspace, forcedWS)
			r = r.WithContext(context.WithValue(ctx, ctxAuthUser, u))
		}
		next.ServeHTTP(w, r)
	})
}

// requireAdmin gates the users-management endpoints. With auth disabled the
// endpoints are open (v1: auth off = implicit trust); otherwise the
// authenticated user must be an admin.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !h.authEnabled {
		return true
	}
	u, _ := r.Context().Value(ctxAuthUser).(*schema.User)
	if u == nil || !u.Admin {
		writeError(w, http.StatusForbidden, "admin privileges required")
		return false
	}
	return true
}

// handleLogin verifies credentials for management consoles and clients (no
// session, no state). Like every /api/* endpoint it sits behind the auth
// middleware, so the Basic header must already be valid; the body credentials
// are what get verified.
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	u := h.checkCredentials(body.Username, body.Password)
	if u == nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username": u.Username, "workspace": u.Workspace, "admin": u.Admin,
	})
}

// ---------------------------------------------------------------------------
// ops baseline: /healthz, request log, minimal metrics
// ---------------------------------------------------------------------------

// handleHealthz reports storage connectivity. Registered outside the /api/
// prefix, so the auth middleware never gates it (load balancers probe it
// without credentials). detail is flattened to one line.
func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := h.eng.Store.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "error", "detail": strings.Join(strings.Fields(err.Error()), " "),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// httpMetrics is the process-internal minimal metrics store: per-endpoint
// request/error counts plus a running average of recall latency. One mutex,
// no registry abstraction.
type httpMetrics struct {
	mu        sync.Mutex
	endpoints map[string]*endpointStats
	recallSum float64 // total recall duration, ms
	recallN   int
}

type endpointStats struct {
	Requests int `json:"requests"`
	Errors   int `json:"errors"` // non-2xx responses
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{endpoints: map[string]*endpointStats{}}
}

func (m *httpMetrics) record(path string, status int, ms float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.endpoints[path]
	if st == nil {
		st = &endpointStats{}
		m.endpoints[path] = st
	}
	st.Requests++
	if status < 200 || status >= 300 {
		st.Errors++
	}
	if path == "/api/recall" {
		m.recallSum += ms
		m.recallN++
	}
}

// statusRecorder captures the response status for the request log/metrics.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// withObservability emits one stderr log line per /api/* request
// ("method path status duration_ms workspace", same fmt.Fprintf(os.Stderr)
// style as the project's WARNING lines) and feeds the /api/metrics counters.
// It wraps the auth middleware so 401s are logged and counted too.
func (h *Handler) withObservability(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		ms := float64(time.Since(start).Microseconds()) / 1000
		h.metrics.record(r.URL.Path, rec.status, ms)
		// The effective workspace: the forced user workspace (echoed by auth
		// as X-Ladym-Workspace) or, failing that, the server default. A
		// body-level workspace override by an admin caller is not visible at
		// this layer.
		ws := rec.Header().Get("X-Ladym-Workspace")
		if ws == "" {
			ws = h.cfg.Workspace
		}
		fmt.Fprintf(os.Stderr, "%s %s %d %.1fms %s\n", r.Method, r.URL.Path, rec.status, ms, ws)
	})
}

// handleMetrics returns the in-process counters (auth-gated like the rest of
// /api/*).
func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	h.metrics.mu.Lock()
	defer h.metrics.mu.Unlock()
	endpoints := make(map[string]endpointStats, len(h.metrics.endpoints))
	for path, st := range h.metrics.endpoints {
		endpoints[path] = *st
	}
	var recallAvg float64
	if h.metrics.recallN > 0 {
		recallAvg = h.metrics.recallSum / float64(h.metrics.recallN)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoints": endpoints, "recall_avg_ms": recallAvg,
	})
}

// ---------------------------------------------------------------------------
// request/response plumbing
// ---------------------------------------------------------------------------

type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(w, `{"error": %q}`, err.Error())
		return
	}
	_, _ = w.Write(b)
}

// decodeBody parses the JSON request body; an empty body decodes as "no args"
// (stats/consolidate take none).
func decodeBody(r *http.Request, v any) error {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == io.EOF {
		return nil
	}
	return err
}

// engineError maps an engine error to an HTTP status. User-actionable errors
// (config problems, index already in progress) are 400 — the MCP layer returns
// them as plain error text, and HTTP keeps them in the 4xx class consistently.
func engineError(w http.ResponseWriter, err error) {
	var cfgErr *config.ConfigError
	var inProg *code.IndexInProgressError
	switch {
	case errors.As(err, &cfgErr), errors.As(err, &inProg):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// effectiveWS returns the workspace a request runs in: a non-admin user's
// forced workspace overrides any workspace field in the request body.
func (h *Handler) effectiveWS(r *http.Request, bodyWS string) string {
	if forced, _ := r.Context().Value(ctxForcedWorkspace).(string); forced != "" {
		return forced
	}
	return bodyWS
}

// withWorkspace runs fn with the engine's write workspace temporarily
// retargeted. engine.Remember/RecordEvent have no per-call workspace parameter
// (their layers read Config.Workspace); since every engine call is serialized
// under h.mu, mutating + restoring these fields is race-free and leaves no
// shared-state change behind. engine.SetWorkspace is deliberately not used —
// it would also rebuild the WorkingMemory layer, which HTTP writes don't need.
func (h *Handler) withWorkspace(ws string, fn func() error) error {
	if ws == "" {
		return fn()
	}
	e := h.eng
	oldCfg, oldSem, oldEpi := e.Config.Workspace, e.Semantic.Workspace, e.Episodic.Workspace
	e.Config.Workspace, e.Semantic.Workspace, e.Episodic.Workspace = ws, ws, ws
	defer func() {
		e.Config.Workspace, e.Semantic.Workspace, e.Episodic.Workspace = oldCfg, oldSem, oldEpi
	}()
	return fn()
}

// recallResultsJSON mirrors the MCP recall result shape (mcp/server.go).
func recallResultsJSON(results []*schema.RecallResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"score": r.Score, "tier": r.Tier, "via": r.Via,
			"memory": map[string]any{
				"id": r.Memory.ID, "layer": r.Memory.Layer, "type": r.Memory.Type,
				"summary": r.Memory.Summary, "content": r.Memory.Content,
				"source": r.Memory.Source, "tags": r.Memory.Tags,
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// endpoints (one per MCP tool; field names mirror the MCP input schemas)
// ---------------------------------------------------------------------------

func (h *Handler) handleRecall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query     string `json:"query"`
		TopK      int    `json:"top_k"`
		CodeOnly  bool   `json:"code_only"`
		Workspace string `json:"workspace"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Query == "" {
		writeError(w, http.StatusBadRequest, "missing required field: query")
		return
	}
	if body.TopK == 0 {
		body.TopK = 8
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	var resp *schema.RecallResponse
	var err error
	if body.CodeOnly {
		resp, err = h.eng.SearchCode(body.Query, body.TopK, ws)
	} else {
		resp, err = h.eng.Recall(body.Query, ws, body.TopK, nil, nil, 0)
	}
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query": resp.Query, "tier_reached": resp.TierReached,
		"reflected_sufficient": resp.ReflectedSufficient, "elapsed_ms": resp.ElapsedMs,
		"results": recallResultsJSON(resp.Results),
	})
}

func (h *Handler) handleRemember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content   string   `json:"content"`
		Tags      []string `json:"tags"`
		Source    string   `json:"source"`
		Workspace string   `json:"workspace"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Content == "" {
		writeError(w, http.StatusBadRequest, "missing required field: content")
		return
	}
	// MCP falls back to source "mcp"; the HTTP front-end labels itself "http".
	source := body.Source
	if source == "" {
		source = "http"
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	var m *schema.Memory
	err := h.withWorkspace(ws, func() error {
		var err error
		m, err = h.eng.Remember(body.Content, schema.LayerSemantic, schema.TypeFact, body.Tags, nil, source, "")
		return err
	})
	if err != nil {
		engineError(w, err)
		return
	}
	if m.MetaString("gated") == "dropped" {
		writeJSON(w, http.StatusOK, map[string]any{"id": nil, "hash": nil, "gated": "dropped", "reason": m.MetaString("reason")})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": m.ID, "hash": m.ContentHash})
}

func (h *Handler) handleRecordEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent       string   `json:"agent"`
		Action      string   `json:"action"`
		Observation string   `json:"observation"`
		Outcome     string   `json:"outcome"`
		Tags        []string `json:"tags"`
		Workspace   string   `json:"workspace"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Agent == "" || body.Action == "" {
		writeError(w, http.StatusBadRequest, "missing required field: agent/action")
		return
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	var m *schema.Memory
	err := h.withWorkspace(ws, func() error {
		var err error
		m, err = h.eng.RecordEvent(body.Agent, body.Action, body.Observation, body.Outcome, body.Tags, nil)
		return err
	})
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": m.ID, "layer": m.Layer, "type": m.Type})
}

func (h *Handler) handleSearchCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query     string `json:"query"`
		TopK      int    `json:"top_k"`
		Workspace string `json:"workspace"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Query == "" {
		writeError(w, http.StatusBadRequest, "missing required field: query")
		return
	}
	if body.TopK == 0 {
		body.TopK = 10
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	resp, err := h.eng.SearchCode(body.Query, body.TopK, ws)
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": recallResultsJSON(resp.Results), "elapsed_ms": resp.ElapsedMs,
	})
}

func (h *Handler) handleIndexCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Root      string   `json:"root"`
		Force     bool     `json:"force"`
		Languages []string `json:"languages"`
		Workspace string   `json:"workspace"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Root == "" {
		writeError(w, http.StatusBadRequest, "missing required field: root")
		return
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	report, err := h.eng.IndexCode(body.Root, body.Force, ws, body.Languages)
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files_seen": report.FilesSeen, "files_indexed": report.FilesIndexed,
		"files_skipped_unchanged": report.FilesSkippedUnchanged,
		"symbols_written":         report.SymbolsWritten, "refs_written": report.RefsWritten,
		"elapsed_ms": report.ElapsedMs, "errors": report.Errors,
	})
}

func (h *Handler) handleConsolidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string  `json:"workspace"`
		Since     float64 `json:"since"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	report, err := h.eng.Consolidate(ws, body.Since)
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kept_episodes": report.KeptEpisodes, "promoted_to_semantic": report.PromotedToSemantic,
		"skipped_consolidated": report.SkippedConsolidated, "actions": report.Actions,
	})
}

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Workspace string `json:"workspace"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	ws := h.effectiveWS(r, body.Workspace)

	h.mu.Lock()
	defer h.mu.Unlock()
	st, err := h.eng.StatsFor(ws)
	if err != nil {
		engineError(w, err)
		return
	}
	// StatsFor scopes the memory counts; the workspaces list is store-wide.
	// For a non-admin user the full roster would leak the other users'
	// workspace names, so it is narrowed to the forced workspace.
	if forced, _ := r.Context().Value(ctxForcedWorkspace).(string); forced != "" {
		st.Workspaces = []string{forced}
	}
	writeJSON(w, http.StatusOK, st)
}

// enforceMemoryWorkspace guards id-addressed writes (forget/link) against
// cross-workspace access. It applies only when the request's workspace was
// forced (non-admin user); admin and no-auth callers are not constrained
// (aligned with the MCP/CLI semantics, which have no such check). Missing
// ids pass through — the engine keeps its existing not-found behavior.
func (h *Handler) enforceMemoryWorkspace(w http.ResponseWriter, r *http.Request, ids ...string) bool {
	forced, _ := r.Context().Value(ctxForcedWorkspace).(string)
	if forced == "" {
		return true
	}
	for _, id := range ids {
		m, err := h.eng.Store.GetMemory(id)
		if err != nil {
			engineError(w, err)
			return false
		}
		if m != nil && m.Workspace != forced {
			writeError(w, http.StatusForbidden,
				fmt.Sprintf("memory %s belongs to workspace %q; user is scoped to %q", id, m.Workspace, forced))
			return false
		}
	}
	return true
}

func (h *Handler) handleLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Src      string `json:"src"`
		Dst      string `json:"dst"`
		Relation string `json:"relation"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.Src == "" || body.Dst == "" {
		writeError(w, http.StatusBadRequest, "missing required field: src/dst")
		return
	}
	if body.Relation == "" {
		body.Relation = "related_to"
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.enforceMemoryWorkspace(w, r, body.Src, body.Dst) {
		return
	}
	edge, err := h.eng.Link(body.Src, body.Dst, body.Relation)
	if err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": edge.ID, "src": edge.SrcID, "dst": edge.DstID, "relation": edge.Relation})
}

func (h *Handler) handleForget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MemoryID string `json:"memory_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if body.MemoryID == "" {
		writeError(w, http.StatusBadRequest, "missing required field: memory_id")
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.enforceMemoryWorkspace(w, r, body.MemoryID) {
		return
	}
	if err := h.eng.Forget(body.MemoryID); err != nil {
		engineError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forgotten": body.MemoryID})
}
