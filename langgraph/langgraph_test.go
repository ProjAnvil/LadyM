//go:build !enterprise

package langgraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
	lgruntime "github.com/projanvil/langchain-golang/langgraph/runtime"
)

func newTestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	eng, err := engine.New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

func TestCreateToolsRecallAndRemember(t *testing.T) {
	eng := newTestEngine(t)
	tools, err := CreateTools(eng, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("got %d tools, want 3", len(tools))
	}
	byName := map[string]int{}
	for _, tl := range tools {
		byName[tl.Name()] = 1
	}
	for _, want := range []string{"recall_memory", "remember_fact", "search_code"} {
		if byName[want] == 0 {
			t.Fatalf("missing tool %s", want)
		}
	}

	// remember_fact stores through the engine (heuristic gate in tests).
	for _, tl := range tools {
		if tl.Name() != "remember_fact" {
			continue
		}
		res, err := tl.Invoke(context.Background(), map[string]any{"content": "Alice likes green tea", "tags": []string{"pref"}})
		if err != nil {
			t.Fatalf("remember_fact: %v", err)
		}
		if !strings.Contains(res.Content, "stored id=") {
			t.Fatalf("remember_fact content = %q", res.Content)
		}
	}

	// recall_memory finds it back.
	for _, tl := range tools {
		if tl.Name() != "recall_memory" {
			continue
		}
		res, err := tl.Invoke(context.Background(), map[string]any{"query": "green tea"})
		if err != nil {
			t.Fatalf("recall_memory: %v", err)
		}
		if !strings.Contains(res.Content, "green tea") {
			t.Fatalf("recall_memory content = %q", res.Content)
		}
	}

	// search_code on an empty index returns the no-hits marker.
	for _, tl := range tools {
		if tl.Name() != "search_code" {
			continue
		}
		res, err := tl.Invoke(context.Background(), map[string]any{"query": "anything"})
		if err != nil {
			t.Fatalf("search_code: %v", err)
		}
		if res.Content != "(no hits)" {
			t.Fatalf("search_code content = %q, want (no hits)", res.Content)
		}
	}
}

func TestRecallNodeInjectsSystemMessage(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.Remember("deploy runbook: restart nginx first", schema.LayerSemantic, schema.TypeFact, nil, nil, "test", ""); err != nil {
		t.Fatal(err)
	}
	node := CreateRecallNode(eng, 6, "", nil)
	out, err := node(lgruntime.Runtime{}, map[string]any{
		"messages": []lcmessages.Message{lcmessages.Human("how do I deploy?")},
	})
	if err != nil {
		t.Fatal(err)
	}
	update, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("node returned %T, want map", out)
	}
	msgs, ok := update["messages"].([]lcmessages.Message)
	if !ok || len(msgs) != 1 {
		t.Fatalf("update messages = %v", update["messages"])
	}
	if msgs[0].Role != lcmessages.RoleSystem {
		t.Fatalf("role = %s, want system", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "Relevant long-term memory:") {
		t.Fatalf("system content = %q", msgs[0].Content)
	}
}

func TestRecallNodeNoHits(t *testing.T) {
	eng := newTestEngine(t)
	node := CreateRecallNode(eng, 6, "", nil)
	out, err := node(lgruntime.Runtime{}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if update := out.(map[string]any); len(update) != 0 {
		t.Fatalf("empty state should yield empty update, got %v", update)
	}

	// Messages present but nothing stored: recall yields no hits.
	out, err = node(lgruntime.Runtime{}, map[string]any{
		"messages": []lcmessages.Message{lcmessages.Human("nothing stored yet")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if update := out.(map[string]any); len(update) != 0 {
		t.Fatalf("no hits should yield empty update, got %v", update)
	}
}

func TestRetainNodeStoresTurn(t *testing.T) {
	eng := newTestEngine(t)
	node := CreateRetainNode(eng, nil)
	out, err := node(lgruntime.Runtime{}, map[string]any{
		"messages": []lcmessages.Message{
			lcmessages.Human("what is the deploy command?"),
			lcmessages.AI("make deploy-prod"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if update := out.(map[string]any); len(update) != 0 {
		t.Fatalf("retain node should not update state, got %v", update)
	}
	resp, err := eng.Recall("deploy command", eng.Config.Workspace, 5, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("retained turn not found in memory")
	}
	found := false
	for _, r := range resp.Results {
		if strings.Contains(r.Memory.Content, "make deploy-prod") {
			found = true
		}
	}
	if !found {
		t.Fatalf("retained content missing: %v", resp.Results)
	}
}

func TestCreateToolsDefaultsAndRecallNoHits(t *testing.T) {
	eng := newTestEngine(t)
	tools, err := CreateTools(eng, "", 0) // defaultTopK <= 0 falls back to 8
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name() != "recall_memory" {
			continue
		}
		res, err := tl.Invoke(context.Background(), map[string]any{"query": "nothing stored", "top_k": 3})
		if err != nil {
			t.Fatalf("recall_memory: %v", err)
		}
		if res.Content != "(no hits)" {
			t.Fatalf("recall_memory content = %q, want (no hits)", res.Content)
		}
	}
}

func TestCreateToolsSearchCodeHits(t *testing.T) {
	eng := newTestEngine(t)
	dir := t.TempDir()
	src := `def hash_password(password: str) -> str:
    """Hash a plaintext password."""
    return "hashed:" + password
`
	if err := os.WriteFile(filepath.Join(dir, "service.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.IndexCode(dir, false, "", nil); err != nil {
		t.Fatal(err)
	}
	tools, err := CreateTools(eng, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name() != "search_code" {
			continue
		}
		res, err := tl.Invoke(context.Background(), map[string]any{"query": "hash password", "top_k": 5})
		if err != nil {
			t.Fatalf("search_code: %v", err)
		}
		if !strings.Contains(res.Content, "hash_password") || !strings.Contains(res.Content, " :: ") {
			t.Fatalf("search_code content = %q", res.Content)
		}
	}
}

func TestCreateToolsEngineErrors(t *testing.T) {
	eng, err := engine.New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	tools, err := CreateTools(eng, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	argsByTool := map[string]map[string]any{
		"recall_memory": {"query": "anything"},
		"remember_fact": {"content": "a durable fact about coverage testing"},
		"search_code":   {"query": "anything"},
	}
	for _, tl := range tools {
		if _, err := tl.Invoke(context.Background(), argsByTool[tl.Name()]); err == nil {
			t.Fatalf("%s: expected error from closed engine", tl.Name())
		}
	}
}

// storeSummarylessFact writes a fact with an empty Summary directly through
// the store (PutFact always backfills Summary), to exercise the
// summary-is-empty content fallback in the recall paths.
func storeSummarylessFact(t *testing.T, eng *engine.Engine, content string) {
	t.Helper()
	vec, err := eng.Provider.Embed(content)
	if err != nil {
		t.Fatal(err)
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.Content = content
	m.Workspace = eng.Config.Workspace
	if err := eng.Store.PutMemory(m, vec); err != nil {
		t.Fatal(err)
	}
}

func TestRecallNodeContentFallback(t *testing.T) {
	eng := newTestEngine(t)
	storeSummarylessFact(t, eng, "the zyxquark protocol runs on port nine")
	node := CreateRecallNode(eng, 0, "MEM:", nil) // topK <= 0 falls back to 6
	out, err := node(lgruntime.Runtime{}, map[string]any{
		"messages": []lcmessages.Message{lcmessages.Human("how does the zyxquark protocol work?")},
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, ok := out.(map[string]any)["messages"].([]lcmessages.Message)
	if !ok || len(msgs) != 1 {
		t.Fatalf("update messages = %v", out)
	}
	if !strings.HasPrefix(msgs[0].Content, "MEM:\n") {
		t.Fatalf("system content = %q, want MEM: prefix", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "zyxquark protocol") {
		t.Fatalf("fallback to content missing: %q", msgs[0].Content)
	}

	// Same fallback through the recall_memory tool.
	tools, err := CreateTools(eng, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name() != "recall_memory" {
			continue
		}
		res, err := tl.Invoke(context.Background(), map[string]any{"query": "zyxquark protocol"})
		if err != nil {
			t.Fatalf("recall_memory: %v", err)
		}
		if !strings.Contains(res.Content, "zyxquark protocol") {
			t.Fatalf("recall_memory content = %q", res.Content)
		}
	}
}

func TestRecallNodeEngineError(t *testing.T) {
	eng, err := engine.New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	node := CreateRecallNode(eng, 6, "", nil)
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := node(lgruntime.Runtime{}, map[string]any{
		"messages": []lcmessages.Message{lcmessages.Human("anything")},
	}); err == nil {
		t.Fatal("expected error from closed engine")
	}
}

func TestResolveWorkspace(t *testing.T) {
	eng := newTestEngine(t)
	if got := resolveWorkspace(eng, nil, lgruntime.Runtime{}); got != eng.Config.Workspace {
		t.Fatalf("nil wsFn: got %q, want engine default", got)
	}
	empty := func(lgruntime.Runtime) string { return "" }
	if got := resolveWorkspace(eng, empty, lgruntime.Runtime{}); got != eng.Config.Workspace {
		t.Fatalf("empty wsFn: got %q, want engine default", got)
	}
	named := func(lgruntime.Runtime) string { return "team-x" }
	if got := resolveWorkspace(eng, named, lgruntime.Runtime{}); got != "team-x" {
		t.Fatalf("named wsFn: got %q, want team-x", got)
	}
}

func TestStateMessagesVariants(t *testing.T) {
	msgs := stateMessages(map[string]any{
		"messages": []lcmessages.MessageUpdate{lcmessages.Human("hi")},
	})
	if len(msgs) != 1 || msgs[0].Role != lcmessages.RoleHuman {
		t.Fatalf("MessageUpdate state: got %v", msgs)
	}
	if got := stateMessages(map[string]any{"messages": "nope"}); got != nil {
		t.Fatalf("unexpected element type: got %v, want nil", got)
	}
}

func TestRetainNodeIncompleteTurn(t *testing.T) {
	eng := newTestEngine(t)
	node := CreateRetainNode(eng, nil)
	for name, state := range map[string]map[string]any{
		"no messages": {},
		"human only":  {"messages": []lcmessages.Message{lcmessages.Human("a question with no answer")}},
		"ai only":     {"messages": []lcmessages.Message{lcmessages.AI("an answer with no question")}},
	} {
		out, err := node(lgruntime.Runtime{}, state)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if update := out.(map[string]any); len(update) != 0 {
			t.Fatalf("%s: expected empty update, got %v", name, update)
		}
	}
}

func TestRetainNodeWorkspaceOverride(t *testing.T) {
	eng := newTestEngine(t)
	node := CreateRetainNode(eng, WorkspaceFromUserID())
	rt := lgruntime.Runtime{Context: map[string]any{"user_id": "team-override"}}
	out, err := node(rt, map[string]any{
		"messages": []lcmessages.Message{
			lcmessages.Human("how do I rotate the quixplorer keys?"),
			lcmessages.AI("run quixplorer rotate --force"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if update := out.(map[string]any); len(update) != 0 {
		t.Fatalf("retain node should not update state, got %v", update)
	}
	// The short-lived engine has its own in-memory vector index, so verify
	// through the shared store instead of eng.Recall.
	mems, err := eng.Store.IterMemories("team-override", "", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range mems {
		if strings.Contains(m.Content, "quixplorer rotate --force") {
			found = true
		}
	}
	if !found {
		t.Fatalf("turn not retained in override workspace: %v", mems)
	}
}

func TestRetainNodeEngineError(t *testing.T) {
	eng, err := engine.New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	node := CreateRetainNode(eng, nil)
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := node(lgruntime.Runtime{}, map[string]any{
		"messages": []lcmessages.Message{
			lcmessages.Human("a durable question about coverage"),
			lcmessages.AI("a durable answer about coverage"),
		},
	}); err == nil {
		t.Fatal("expected error from closed engine")
	}
}
