package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
)

// newTestServer builds a server backed by a throwaway engine in t.TempDir().
func newTestServer(t *testing.T) *server {
	t.Helper()
	cfg := config.ForTesting(t.TempDir())
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return &server{eng: eng, cfg: cfg}
}

// TestHandleMethods exercises handle() for every RPC method plus the error
// branches (bad tools/call params, failing tool, unknown method).
func TestHandleMethods(t *testing.T) {
	s := newTestServer(t)

	t.Run("initialize", func(t *testing.T) {
		resp, err := s.handle(rpcRequest{ID: json.RawMessage(`1`), Method: "initialize"})
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		res, ok := resp.Result.(map[string]any)
		if !ok {
			t.Fatalf("result type = %T, want map", resp.Result)
		}
		if res["protocolVersion"] != "2024-11-05" {
			t.Errorf("protocolVersion = %v", res["protocolVersion"])
		}
	})

	t.Run("ping", func(t *testing.T) {
		resp, err := s.handle(rpcRequest{ID: json.RawMessage(`2`), Method: "ping"})
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		if _, ok := resp.Result.(map[string]any); !ok {
			t.Errorf("ping result type = %T, want map", resp.Result)
		}
	})

	t.Run("tools/list", func(t *testing.T) {
		resp, err := s.handle(rpcRequest{ID: json.RawMessage(`3`), Method: "tools/list"})
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		res := resp.Result.(map[string]any)
		tools, ok := res["tools"].([]toolDef)
		if !ok || len(tools) == 0 {
			t.Errorf("tools/list returned %v", res["tools"])
		}
	})

	t.Run("tools/call ok", func(t *testing.T) {
		resp, err := s.handle(rpcRequest{
			ID:     json.RawMessage(`4`),
			Method: "tools/call",
			Params: json.RawMessage(`{"name":"stats","arguments":{}}`),
		})
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		cr, ok := resp.Result.(callResult)
		if !ok {
			t.Fatalf("result type = %T, want callResult", resp.Result)
		}
		if cr.IsError || len(cr.Content) != 1 || cr.Content[0].Type != "text" {
			t.Errorf("unexpected callResult: %+v", cr)
		}
	})

	t.Run("tools/call bad params", func(t *testing.T) {
		// name has the wrong JSON type, so params unmarshal must fail.
		_, err := s.handle(rpcRequest{
			ID:     json.RawMessage(`5`),
			Method: "tools/call",
			Params: json.RawMessage(`{"name": 123}`),
		})
		if err == nil {
			t.Fatal("expected error for malformed tools/call params")
		}
	})

	t.Run("tools/call tool error", func(t *testing.T) {
		resp, err := s.handle(rpcRequest{
			ID:     json.RawMessage(`6`),
			Method: "tools/call",
			Params: json.RawMessage(`{"name":"no_such_tool","arguments":{}}`),
		})
		if err != nil {
			t.Fatalf("handle: %v", err)
		}
		cr := resp.Result.(callResult)
		if !cr.IsError {
			t.Errorf("IsError = false, want true for unknown tool")
		}
		if !strings.Contains(cr.Content[0].Text, "unknown tool") {
			t.Errorf("error text = %q", cr.Content[0].Text)
		}
	})

	t.Run("unknown method", func(t *testing.T) {
		_, err := s.handle(rpcRequest{ID: json.RawMessage(`7`), Method: "bogus/method"})
		if err == nil || !strings.Contains(err.Error(), "method not found") {
			t.Errorf("err = %v, want method not found", err)
		}
	})
}

// TestCallAllTools drives every tool branch of call() against a real engine,
// including the argument-getter default paths (wrong JSON types) and the
// attention-gate "dropped" remember response.
func TestCallAllTools(t *testing.T) {
	s := newTestServer(t)

	// remember: with tags, explicit source.
	out, err := s.call("remember", map[string]any{
		"content": "pluto vexillology hypercube coverage fact one",
		"tags":    []any{"alpha", 42, "beta"}, // non-strings are skipped
		"source":  "test-suite",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	var rem struct {
		ID   string `json:"id"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(out), &rem); err != nil {
		t.Fatalf("remember output not JSON: %v (%q)", err, out)
	}
	if rem.ID == "" || rem.Hash == "" {
		t.Errorf("remember returned empty id/hash: %q", out)
	}

	// remember gated drop: pure noise content is dropped by the attention gate.
	out, err = s.call("remember", map[string]any{"content": "ok"})
	if err != nil {
		t.Fatalf("remember noise: %v", err)
	}
	var gated struct {
		Gated  string `json:"gated"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &gated); err != nil {
		t.Fatalf("gated output not JSON: %v (%q)", err, out)
	}
	if gated.Gated != "dropped" || gated.Reason == "" {
		t.Errorf("noise remember not dropped: %q", out)
	}

	// remember with wrong-typed tags (covers getStrList default path).
	if _, err := s.call("remember", map[string]any{
		"content": "pluto vexillology hypercube coverage fact two",
		"tags":    "not-a-list",
	}); err != nil {
		t.Fatalf("remember wrong-typed tags: %v", err)
	}

	// recall (non-code) with wrong-typed args (covers getStr/getInt/getBool
	// default branches).
	out, err = s.call("recall", map[string]any{
		"query":     "pluto vexillology hypercube",
		"top_k":     "not-a-number",
		"code_only": "not-a-bool",
		"workspace": 7,
	})
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	var rec struct {
		Query       string `json:"query"`
		TierReached int    `json:"tier_reached"`
		Results     []struct {
			Memory struct {
				ID      string   `json:"id"`
				Source  string   `json:"source"`
				Tags    []string `json:"tags"`
				Summary string   `json:"summary"`
			} `json:"memory"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("recall output not JSON: %v (%q)", err, out)
	}
	if rec.Query != "pluto vexillology hypercube" {
		t.Errorf("recall query echo = %q", rec.Query)
	}
	if len(rec.Results) == 0 {
		t.Fatal("recall returned no results")
	}
	foundSource := false
	for _, r := range rec.Results {
		if r.Memory.Source == "test-suite" {
			foundSource = true
		}
	}
	if !foundSource {
		t.Errorf("no recall result with source %q", "test-suite")
	}

	// recall with code_only=true routes through SearchCode.
	if _, err := s.call("recall", map[string]any{
		"query": "anything", "code_only": true,
	}); err != nil {
		t.Fatalf("recall code_only: %v", err)
	}

	// record_event.
	out, err = s.call("record_event", map[string]any{
		"agent": "tester", "action": "cover",
		"observation": "obs", "outcome": "ok",
		"tags": []any{"x"},
	})
	if err != nil {
		t.Fatalf("record_event: %v", err)
	}
	var ev struct {
		ID    string `json:"id"`
		Layer string `json:"layer"`
	}
	if err := json.Unmarshal([]byte(out), &ev); err != nil {
		t.Fatalf("record_event output not JSON: %v (%q)", err, out)
	}
	if ev.ID == "" || !strings.Contains(ev.Layer, "episodic") {
		t.Errorf("record_event returned %q", out)
	}

	// index_code over a tiny Go file, then search_code finds it.
	srcDir := t.TempDir()
	goFile := filepath.Join(srcDir, "widget.go")
	if err := os.WriteFile(goFile, []byte("package widget\n\n// Frobnicator frobs.\nfunc Frobnicator() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = s.call("index_code", map[string]any{
		"root": srcDir, "force": true,
		"languages": []any{"go", 9}, // mixed list covers the skip branch
		"workspace": "codews",
	})
	if err != nil {
		t.Fatalf("index_code: %v", err)
	}
	var idx struct {
		FilesIndexed   int `json:"files_indexed"`
		SymbolsWritten int `json:"symbols_written"`
	}
	if err := json.Unmarshal([]byte(out), &idx); err != nil {
		t.Fatalf("index_code output not JSON: %v (%q)", err, out)
	}
	if idx.FilesIndexed == 0 || idx.SymbolsWritten == 0 {
		t.Errorf("index_code indexed nothing: %q", out)
	}

	out, err = s.call("search_code", map[string]any{
		"query": "Frobnicator", "top_k": 5, "workspace": "codews",
	})
	if err != nil {
		t.Fatalf("search_code: %v", err)
	}
	var sc struct {
		Results []struct {
			Memory struct {
				Summary string `json:"summary"`
			} `json:"memory"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &sc); err != nil {
		t.Fatalf("search_code output not JSON: %v (%q)", err, out)
	}
	if len(sc.Results) == 0 {
		t.Error("search_code found no results after index_code")
	}

	// consolidate: promotes the recorded episodic event (heuristic mode).
	if _, err := s.call("consolidate", map[string]any{"workspace": ""}); err != nil {
		t.Fatalf("consolidate: %v", err)
	}

	// stats.
	out, err = s.call("stats", map[string]any{})
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if !strings.Contains(out, "total_memories") && !strings.Contains(out, "memories") {
		t.Errorf("stats output looks wrong: %q", out)
	}

	// link between two real memories, then forget one of them.
	var rem2 struct {
		ID string `json:"id"`
	}
	out, err = s.call("remember", map[string]any{"content": "pluto vexillology hypercube coverage fact three"})
	if err != nil {
		t.Fatalf("remember three: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &rem2); err != nil {
		t.Fatalf("remember three output not JSON: %v (%q)", err, out)
	}

	out, err = s.call("link", map[string]any{"src": rem.ID, "dst": rem2.ID})
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	var edge struct {
		ID       string `json:"id"`
		Relation string `json:"relation"`
	}
	if err := json.Unmarshal([]byte(out), &edge); err != nil {
		t.Fatalf("link output not JSON: %v (%q)", err, out)
	}
	if edge.Relation != "related_to" {
		t.Errorf("link relation = %q, want default related_to", edge.Relation)
	}

	out, err = s.call("forget", map[string]any{"memory_id": rem2.ID})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if !strings.Contains(out, rem2.ID) {
		t.Errorf("forget output = %q, want id echoed", out)
	}

	// unknown tool.
	if _, err := s.call("definitely_not_a_tool", map[string]any{}); err == nil {
		t.Error("unknown tool: expected error")
	}
}

// TestCallEngineErrors covers the error-return branch of every tool by calling
// them on a server whose engine has been closed.
func TestCallEngineErrors(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s := &server{eng: eng, cfg: cfg}

	// index_code only errors once it actually writes, so give it a real file.
	idxDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(idxDir, "x.go"), []byte("package x\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := map[string]map[string]any{
		"recall":       {"query": "x"},
		"remember":     {"content": "some real fact worth storing"},
		"record_event": {"agent": "a", "action": "b"},
		"search_code":  {"query": "x"},
		"index_code":   {"root": idxDir},
		"consolidate":  {},
		"stats":        {},
		"link":         {"src": "a", "dst": "b"},
		"forget":       {"memory_id": "x"},
	}
	for name, args := range calls {
		if _, err := s.call(name, args); err == nil {
			t.Errorf("%s on closed engine: expected error, got nil", name)
		}
	}
}

// withStdio swaps os.Stdin/os.Stdout for pipes while fn runs. Input is
// written (and the write end closed) before fn is invoked; the captured
// stdout bytes are returned after fn completes.
func withStdio(t *testing.T, input string, fn func() error) (string, error) {
	t.Helper()
	oldIn, oldOut := os.Stdin, os.Stdout
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin, os.Stdout = inR, outW

	if _, err := io.WriteString(inW, input); err != nil {
		t.Fatal(err)
	}
	if err := inW.Close(); err != nil {
		t.Fatal(err)
	}

	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		done <- b
	}()

	runErr := fn()

	if err := outW.Close(); err != nil {
		t.Fatal(err)
	}
	return string(<-done), runErr
}

// TestLoopOverStdio drives the stdio read loop: blank lines, malformed JSON,
// notifications, normal requests and a failing method.
func TestLoopOverStdio(t *testing.T) {
	s := newTestServer(t)

	var input strings.Builder
	input.WriteString("\n") // blank line: skipped
	input.WriteString("{not json\n")
	input.WriteString(`{"jsonrpc":"2.0","method":"ping"}` + "\n")           // notification: no id
	input.WriteString(`{"jsonrpc":"2.0","id":null,"method":"ping"}` + "\n") // id null: no response
	input.WriteString(`{"jsonrpc":"2.0","id":"1","method":"ping"}` + "\n")
	input.WriteString(`{"jsonrpc":"2.0","id":"2","method":"initialize"}` + "\n")
	input.WriteString(`{"jsonrpc":"2.0","id":"3","method":"tools/list"}` + "\n")
	input.WriteString(`{"jsonrpc":"2.0","id":"4","method":"tools/call","params":{"name":"stats","arguments":{}}}` + "\n")
	input.WriteString(`{"jsonrpc":"2.0","id":"5","method":"nope/here"}` + "\n")
	input.WriteString(`{"jsonrpc":"2.0","id":"6","method":"tools/call","params":{"name":"nope"}}` + "\n")

	output, err := withStdio(t, input.String(), s.loop)
	if err != nil {
		t.Fatalf("loop: %v", err)
	}

	resps := map[string]rpcResponse{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		var id string
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			t.Fatalf("response id not a string: %s", resp.ID)
		}
		resps[id] = resp
	}
	if len(resps) != 6 {
		t.Errorf("got %d responses, want 6 (notifications get none): %q", len(resps), output)
	}
	for id, wantErr := range map[string]bool{"1": false, "2": false, "3": false, "4": false, "5": true, "6": false} {
		resp, ok := resps[id]
		if !ok {
			t.Errorf("missing response for id %s", id)
			continue
		}
		if wantErr {
			if resp.Error == nil || resp.Error.Code != -32603 {
				t.Errorf("id %s: error = %+v, want -32603", id, resp.Error)
			}
		} else if resp.Error != nil {
			t.Errorf("id %s: unexpected error %+v", id, resp.Error)
		}
	}
	// id 6: tools/call with unknown tool → isError result, not a protocol error.
	if resp, ok := resps["6"]; ok {
		b, _ := json.Marshal(resp.Result)
		if !strings.Contains(string(b), `"isError":true`) {
			t.Errorf("id 6 result = %s, want isError", b)
		}
	}
}

// TestRunOverStdio covers Run(): engine construction, the loop, and engine
// shutdown, all through the stdio pipes.
func TestRunOverStdio(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	input := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n")
	output, err := withStdio(t, input, func() error { return Run(cfg) })
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(output, `"id":1`) {
		t.Errorf("Run output = %q, want ping response", output)
	}
}
