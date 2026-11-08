package mcp

import (
	"reflect"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestMCPProvenanceWhitelist(t *testing.T) {
	allowed := map[string]any{
		"session_id": "session", "source_episode": "ep1", "source_episodes": []any{"ep1", "ep2"},
		"knowledge_status": "observed", "evidence_missing": false, "verified_at": 123.5,
		"action_class": "execute", "tool_outcome": "success", "task_outcome": "success",
	}
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m.Workspace, m.CreatedAt, m.UpdatedAt = "project:ws", 100, 200
	for k, v := range allowed {
		m.Metadata[k] = v
	}
	for _, key := range []string{"unknown", "password", "cwd", "transcript_path", "tool_input", "hook_event_name", "source_ids"} {
		m.Metadata[key] = "must not leak"
	}
	got := recallResultsJSON([]*schema.RecallResult{{Memory: m, Score: 0.7, Tier: 1, Via: []string{"anchor"}}})[0]
	memory := got["memory"].(map[string]any)
	if !reflect.DeepEqual(memory["metadata"], allowed) {
		t.Fatalf("whitelist = %#v, want %#v", memory["metadata"], allowed)
	}
	for key, want := range map[string]any{"workspace": "project:ws", "created_at": float64(100), "updated_at": float64(200)} {
		if memory[key] != want {
			t.Errorf("%s = %v, want %v", key, memory[key], want)
		}
	}
	m.Metadata = nil
	empty := recallResultsJSON([]*schema.RecallResult{{Memory: m}})[0]["memory"].(map[string]any)
	if meta, ok := empty["metadata"].(map[string]any); !ok || len(meta) != 0 {
		t.Errorf("empty metadata must remain an object: %#v", empty["metadata"])
	}
}
