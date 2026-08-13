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
			"serverInfo":      map[string]any{"name": "ladym", "version": "0.2.1"},
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

func strSchema(desc string, required ...string) map[string]any {
	props := map[string]any{}
	for _, r := range required {
		props[r] = map[string]any{"type": "string"}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (s *server) tools() []toolDef {
	str := func(desc string, req ...string) map[string]any { return strSchema(desc, req...) }
	return []toolDef{
		{Name: "recall", Description: "Recall memories matching a natural-language query.", InputSchema: str("recall", "query")},
		{Name: "remember", Description: "Write a semantic fact / note that future recall can retrieve.", InputSchema: str("remember", "content")},
		{Name: "record_event", Description: "Record an L1 episodic event.", InputSchema: str("record_event", "agent", "action")},
		{Name: "search_code", Description: "Search indexed code symbols + file summaries by keyword.", InputSchema: str("search_code", "query")},
		{Name: "index_code", Description: "Index (or re-index) a codebase.", InputSchema: str("index_code", "root")},
		{Name: "consolidate", Description: "Promote episodic events into consolidated semantic facts.", InputSchema: str("consolidate")},
		{Name: "stats", Description: "Return memory-store statistics.", InputSchema: str("stats")},
		{Name: "link", Description: "Create an associative edge between two memory ids.", InputSchema: str("link", "src", "dst")},
		{Name: "forget", Description: "Delete a single memory by id.", InputSchema: str("forget", "memory_id")},
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
		m, err := s.eng.Remember(content, schema.LayerSemantic, schema.TypeFact, getStrList("tags"), nil, getStr("source", "mcp"), "")
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
