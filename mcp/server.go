// Package mcp implements LadyM's MCP server (JSON-RPC 2.0 over stdio).
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// Run starts the MCP stdio server using cfg.
func Run(cfg *config.Config) error {
	eng, err := engine.New(cfg)
	if err != nil {
		return err
	}
	defer eng.Close()
	s := &server{eng: eng, cfg: cfg}
	return s.loop()
}

type server struct {
	eng *engine.Engine
	cfg *config.Config
}

func (s *server) loop() error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 1<<20)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	write := func(resp rpcResponse) error {
		b, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if _, err := out.Write(b); err != nil {
			return err
		}
		if err := out.WriteByte('\n'); err != nil {
			return err
		}
		return out.Flush()
	}

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		// Notifications (no id) get no response.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp, err := s.handle(req)
		if err != nil {
			resp = rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32603, Message: err.Error()}}
		}
		if err := write(resp); err != nil {
			return err
		}
	}
	return in.Err()
}

func (s *server) handle(req rpcRequest) (rpcResponse, error) {
	base := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ladym", "version": "0.5.0"},
		}}, nil
	case "ping":
		base.Result = map[string]any{}
		return base, nil
	case "tools/list":
		base.Result = map[string]any{"tools": s.tools()}
		return base, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return base, err
		}
		text, err := s.call(params.Name, params.Arguments)
		if err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: callResult{
				Content: []textContent{{Type: "text", Text: fmt.Sprintf(`{"error": %q}`, err.Error())}},
				IsError: true,
			}}, nil
		}
		base.Result = callResult{Content: []textContent{{Type: "text", Text: text}}}
		return base, nil
	default:
		return base, fmt.Errorf("method not found: %s", req.Method)
	}
}

// JSON-Schema property helpers for tools/list. The schemas mirror the
// signatures FastMCP generates from the Python server (mcp/server.py on the
// main branch).
func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp() map[string]any { return map[string]any{"type": "string"} }

func strDefProp(def string) map[string]any {
	return map[string]any{"type": "string", "default": def}
}

func intProp(def int) map[string]any { return map[string]any{"type": "integer", "default": def} }

func boolProp(def bool) map[string]any { return map[string]any{"type": "boolean", "default": def} }

func strListProp() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}

func (s *server) tools() []toolDef {
	return []toolDef{
		{
			Name: "recall",
			Description: "Recall memories matching a natural-language query.\n\n" +
				"Use code_only=true to restrict to codebase analysis (symbols + file summaries). " +
				"Returns ranked results with tier (1=lightweight, 2=deep) and activation score.",
			InputSchema: objSchema(map[string]any{
				"query": strProp(), "top_k": intProp(8),
				"code_only": boolProp(false), "workspace": strProp(),
			}, "query"),
		},
		{
			Name: "remember",
			Description: "Write a semantic fact / note that future recall can retrieve.\n\n" +
				"Routes through the attention gate (via eng.Remember): noise / recent-duplicate " +
				"content is dropped (not persisted); the response then carries " +
				`{"gated":"dropped","reason":...}` + " with null id/hash so the caller can tell the write was filtered.",
			InputSchema: objSchema(map[string]any{
				"content": strProp(), "tags": strListProp(),
				"source": strDefProp(""), "workspace": strProp(),
			}, "content"),
		},
		{
			Name: "record_event",
			Description: "Record an L1 episodic event (agent, action, observation, outcome).\n\n" +
				"Episodic events feed the System2 worker's consolidation (L1 → L2) and the " +
				"gated L5 mental-model / L6 forward-intent extractors — record ~3+ to arm " +
				"those cycles. Note: this bypasses the attention gate (explicit event " +
				"logging), unlike remember which writes a consolidated L2 fact directly.",
			InputSchema: objSchema(map[string]any{
				"agent": strProp(), "action": strProp(),
				"observation": strDefProp(""), "outcome": strDefProp(""),
				"tags": strListProp(), "workspace": strProp(),
			}, "agent", "action"),
		},
		{
			Name:        "search_code",
			Description: "Search indexed code symbols + file summaries by keyword.",
			InputSchema: objSchema(map[string]any{
				"query": strProp(), "top_k": intProp(10), "workspace": strProp(),
			}, "query"),
		},
		{
			Name: "index_code",
			Description: "Index (or re-index) a codebase at root. Incremental by default; pass " +
				"force=true to rebuild from scratch. languages filters by language id.",
			InputSchema: objSchema(map[string]any{
				"root": strProp(), "force": boolProp(false),
				"languages": strListProp(), "workspace": strProp(),
			}, "root"),
		},
		{
			Name:        "consolidate",
			Description: "Promote episodic events into consolidated semantic facts (L1 → L2).",
			InputSchema: objSchema(map[string]any{
				"workspace": strProp(),
			}),
		},
		{
			Name:        "stats",
			Description: "Return memory-store statistics.",
			InputSchema: objSchema(map[string]any{}),
		},
		{
			Name:        "link",
			Description: "Create an associative edge between two memory ids (Zettelkasten link).",
			InputSchema: objSchema(map[string]any{
				"src": strProp(), "dst": strProp(), "relation": strDefProp("related_to"),
			}, "src", "dst"),
		},
		{
			Name:        "forget",
			Description: "Delete a single memory by id.",
			InputSchema: objSchema(map[string]any{
				"memory_id": strProp(),
			}, "memory_id"),
		},
	}
}

func (s *server) call(name string, args map[string]any) (string, error) {
	getStr := func(k, def string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return def
	}
	getInt := func(k string, def int) int {
		if v, ok := args[k].(float64); ok {
			return int(v)
		}
		return def
	}
	getBool := func(k string, def bool) bool {
		if v, ok := args[k].(bool); ok {
			return v
		}
		return def
	}
	getStrList := func(k string) []string {
		raw, ok := args[k].([]any)
		if !ok {
			return nil
		}
		var out []string
		for _, x := range raw {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}

	switch name {
	case "recall":
		query := getStr("query", "")
		topK := getInt("top_k", 8)
		codeOnly := getBool("code_only", false)
		ws := getStr("workspace", "")
		var resp *schema.RecallResponse
		var err error
		if codeOnly {
			resp, err = s.eng.SearchCode(query, topK, ws)
		} else {
			resp, err = s.eng.Recall(query, ws, topK, nil, nil, 0)
		}
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{
			"query": resp.Query, "tier_reached": resp.TierReached,
			"reflected_sufficient": resp.ReflectedSufficient, "elapsed_ms": resp.ElapsedMs,
			"results": recallResultsJSON(resp.Results),
		}), nil
	case "remember":
		content := getStr("content", "")
		ws := getStr("workspace", "")
		s.eng.SetWorkspace(ws)
		// Python: `source or "mcp"` — an explicitly empty source falls back too.
		source := getStr("source", "mcp")
		if source == "" {
			source = "mcp"
		}
		m, err := s.eng.Remember(content, schema.LayerSemantic, schema.TypeFact, getStrList("tags"), nil, source, "")
		if err != nil {
			return "", err
		}
		if m.MetaString("gated") == "dropped" {
			return mustJSON(map[string]any{"id": nil, "hash": nil, "gated": "dropped", "reason": m.MetaString("reason")}), nil
		}
		return mustJSON(map[string]any{"id": m.ID, "hash": m.ContentHash}), nil
	case "record_event":
		ws := getStr("workspace", "")
		s.eng.SetWorkspace(ws)
		m, err := s.eng.RecordEvent(getStr("agent", ""), getStr("action", ""), getStr("observation", ""), getStr("outcome", ""), getStrList("tags"), nil)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"id": m.ID, "layer": m.Layer, "type": m.Type}), nil
	case "search_code":
		resp, err := s.eng.SearchCode(getStr("query", ""), getInt("top_k", 10), getStr("workspace", ""))
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{
			"results": recallResultsJSON(resp.Results), "elapsed_ms": resp.ElapsedMs,
		}), nil
	case "index_code":
		report, err := s.eng.IndexCode(getStr("root", ""), getBool("force", false), getStr("workspace", ""), getStrList("languages"))
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{
			"files_seen": report.FilesSeen, "files_indexed": report.FilesIndexed,
			"files_skipped_unchanged": report.FilesSkippedUnchanged,
			"symbols_written":         report.SymbolsWritten, "refs_written": report.RefsWritten,
			"elapsed_ms": report.ElapsedMs, "errors": report.Errors,
		}), nil
	case "consolidate":
		report, err := s.eng.Consolidate(getStr("workspace", ""), 0)
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{
			"kept_episodes": report.KeptEpisodes, "promoted_to_semantic": report.PromotedToSemantic,
			"actions": report.Actions,
		}), nil
	case "stats":
		st, err := s.eng.Stats()
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(st)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case "link":
		edge, err := s.eng.Link(getStr("src", ""), getStr("dst", ""), getStr("relation", "related_to"))
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"id": edge.ID, "src": edge.SrcID, "dst": edge.DstID, "relation": edge.Relation}), nil
	case "forget":
		id := getStr("memory_id", "")
		if err := s.eng.Forget(id); err != nil {
			return "", err
		}
		return mustJSON(map[string]any{"forgotten": id}), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

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

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
