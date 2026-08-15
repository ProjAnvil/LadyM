package langgraph

import (
	"context"
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
