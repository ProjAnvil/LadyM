package client

// Data-plane methods: one per /api/ tool endpoint (api/api.go). Request
// bodies and response decoding mirror the server shapes exactly — the cli
// package's remote mode is built on these and its tests pin the wire format.

import (
	"context"
	"net/http"

	"github.com/ProjAnvil/LadyM/schema"
)

// Ping probes /healthz (exempt from auth); nil means the server and its
// store are reachable.
func (c *Client) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/healthz", nil, nil, nil)
}

// Login verifies the client's own credentials against the users table and
// returns the account (username/workspace/admin; the password hash never
// leaves the server). No session is created — the data-plane is stateless.
func (c *Client) Login(ctx context.Context) (*schema.User, error) {
	var u schema.User
	err := c.post(ctx, "/api/login",
		map[string]string{"username": c.user, "password": c.password}, &u)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// RememberResult is the /api/remember response. On an attention-gate drop
// nothing is persisted: ID/Hash are empty, Gated is "dropped" and Reason
// explains why (see Dropped).
type RememberResult struct {
	ID     string `json:"id"`
	Hash   string `json:"hash"`
	Gated  string `json:"gated"`
	Reason string `json:"reason"`
}

// Dropped reports whether the server's attention gate dropped the write.
func (r *RememberResult) Dropped() bool { return r.Gated == "dropped" }

// Remember writes a semantic fact (server labels the source "http").
func (c *Client) Remember(ctx context.Context, content string, tags []string, workspace string) (*RememberResult, error) {
	return c.RememberWithSource(ctx, content, "", tags, workspace)
}

// RememberWithSource is Remember with an explicit source label (the CLI
// passes "cli"; an empty source lets the server default to "http").
func (c *Client) RememberWithSource(ctx context.Context, content, source string, tags []string, workspace string) (*RememberResult, error) {
	var out RememberResult
	err := c.post(ctx, "/api/remember", map[string]any{
		"content": content, "source": source, "tags": tags, "workspace": workspace,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RecallOptions tunes Recall. TopK 0 lets the server default (8); CodeOnly
// restricts to code items (the server maps it to SearchCode, so there is no
// separate SearchCode client method).
type RecallOptions struct {
	Workspace string
	TopK      int
	CodeOnly  bool
}

// Recall queries memories. The response decodes straight into
// schema.RecallResponse — the api package already mirrors its JSON shape.
func (c *Client) Recall(ctx context.Context, query string, opts RecallOptions) (*schema.RecallResponse, error) {
	body := struct {
		Query     string `json:"query"`
		TopK      int    `json:"top_k"`
		Workspace string `json:"workspace"`
		CodeOnly  bool   `json:"code_only,omitzero"`
	}{Query: query, TopK: opts.TopK, Workspace: opts.Workspace, CodeOnly: opts.CodeOnly}
	var out schema.RecallResponse
	if err := c.post(ctx, "/api/recall", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RecordEventResult is the /api/record_event response.
type RecordEventResult struct {
	ID    string `json:"id"`
	Layer string `json:"layer"`
	Type  string `json:"type"`
}

// RecordEvent writes an L1 episodic event.
func (c *Client) RecordEvent(ctx context.Context, agent, action, observation, outcome string, tags []string, workspace string) (*RecordEventResult, error) {
	var out RecordEventResult
	err := c.post(ctx, "/api/record_event", map[string]any{
		"agent": agent, "action": action, "observation": observation,
		"outcome": outcome, "tags": tags, "workspace": workspace,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// OperationsQuery mirrors the POST /api/recall_ops body. Resource is
// required; Limit 0 lets the server default (10, capped at 100); Since and
// Until are unix seconds (0 = unbounded).
type OperationsQuery struct {
	Workspace string
	Resource  string
	Action    string
	Since     float64
	Until     float64
	Outcome   string
	Limit     int
}

// OperationsResult is the /api/recall_ops response. Resource is the resolved
// entity id ("" on a fallback pivot); Items decode straight into
// schema.RecallResult — the server mirrors the MCP recall_ops item shape.
type OperationsResult struct {
	Resource string                 `json:"resource"`
	Items    []*schema.RecallResult `json:"items"`
	Fallback bool                   `json:"fallback"`
}

// RecallOperations asks "how did I operate on this resource?" — the
// operation-history pivot (resource × action filter, operates_on graph join,
// ACT-R ranking). An unknown resource degrades to plain recall with
// Fallback=true and an empty Resource.
func (c *Client) RecallOperations(ctx context.Context, q OperationsQuery) (*OperationsResult, error) {
	var out OperationsResult
	err := c.post(ctx, "/api/recall_ops", map[string]any{
		"workspace": q.Workspace, "resource": q.Resource, "action": q.Action,
		"since": q.Since, "until": q.Until, "outcome": q.Outcome, "limit": q.Limit,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RecordOperationInput mirrors the POST /api/record_operation body. Agent ""
// lets the server default to "api"; the action is normalized server-side
// onto the controlled vocabulary (Resources must be non-empty).
type RecordOperationInput struct {
	Action      string
	Resources   []string
	Observation string
	Outcome     string
	Agent       string
	Tags        []string
}

// RecordOperationResult is the /api/record_operation response. Dropped is
// always false today (the attention gate covers the Remember path only);
// the field keeps the wire shape forward-compatible, with Reason explaining
// a drop if one ever applies.
type RecordOperationResult struct {
	ID      string `json:"id"`
	Dropped bool   `json:"dropped"`
	Reason  string `json:"reason"`
}

// RecordOperation writes one structured L1 operation: each resource is
// resolved (or created) as a workspace entity and linked to the episode
// with operates_on.
func (c *Client) RecordOperation(ctx context.Context, ws string, ev RecordOperationInput) (*RecordOperationResult, error) {
	var out RecordOperationResult
	err := c.post(ctx, "/api/record_operation", map[string]any{
		"workspace": ws, "action": ev.Action, "resources": ev.Resources,
		"observation": ev.Observation, "outcome": ev.Outcome,
		"agent": ev.Agent, "tags": ev.Tags,
	}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ConsolidateResult is the /api/consolidate report.
type ConsolidateResult struct {
	KeptEpisodes        int            `json:"kept_episodes"`
	PromotedToSemantic  int            `json:"promoted_to_semantic"`
	SkippedConsolidated int            `json:"skipped_consolidated"`
	Actions             map[string]int `json:"actions"`
}

// Consolidate runs one System2 consolidation cycle. since (unix seconds)
// bounds the pass to episodes created at or after that time; 0 processes the
// whole pending backlog. It can take far longer than a plain read — callers
// should use a generous context deadline.
func (c *Client) Consolidate(ctx context.Context, workspace string, since float64) (*ConsolidateResult, error) {
	var out ConsolidateResult
	err := c.post(ctx, "/api/consolidate", map[string]any{"workspace": workspace, "since": since}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Stats returns aggregate statistics (workspace-scoped when the server
// forces one on the authenticated user).
func (c *Client) Stats(ctx context.Context, workspace string) (*schema.Stats, error) {
	var out schema.Stats
	err := c.post(ctx, "/api/stats", map[string]any{"workspace": workspace}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Link creates an associative edge src -[relation]-> dst and returns the
// edge id. An empty relation defaults to "related_to" server-side.
func (c *Client) Link(ctx context.Context, src, dst, relation string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := c.post(ctx, "/api/link",
		map[string]string{"src": src, "dst": dst, "relation": relation}, &out)
	if err != nil {
		return "", err
	}
	return out.ID, nil
}

// Forget deletes a memory by id (no-op when missing, MCP semantics; for
// 404-on-missing semantics use DeleteMemory).
func (c *Client) Forget(ctx context.Context, id string) error {
	return c.post(ctx, "/api/forget", map[string]string{"memory_id": id}, nil)
}
