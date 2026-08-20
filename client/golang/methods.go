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
		CodeOnly  bool   `json:"code_only,omitempty"`
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

// ConsolidateResult is the /api/consolidate report.
type ConsolidateResult struct {
	KeptEpisodes       int            `json:"kept_episodes"`
	PromotedToSemantic int            `json:"promoted_to_semantic"`
	Actions            map[string]int `json:"actions"`
}

// Consolidate runs one System2 consolidation cycle. It can take far longer
// than a plain read — callers should use a generous context deadline.
func (c *Client) Consolidate(ctx context.Context, workspace string) (*ConsolidateResult, error) {
	var out ConsolidateResult
	err := c.post(ctx, "/api/consolidate", map[string]any{"workspace": workspace}, &out)
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
