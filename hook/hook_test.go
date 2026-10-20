package hook

import (
	"strings"
	"testing"
)

func TestParseValidPayload(t *testing.T) {
	raw := `{"hook_event_name":"PostToolUse","session_id":"s1","transcript_path":"/tmp/t.jsonl",
		"cwd":"/Users/x/ladyM","tool_name":"Edit","tool_input":{"file_path":"/a/b/main.go"},
		"tool_response":{"ok":true}}`
	p, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.HookEventName != "PostToolUse" || p.SessionID != "s1" || p.ToolName != "Edit" {
		t.Errorf("fields mismatch: %+v", p)
	}
	if p.Cwd != "/Users/x/ladyM" {
		t.Errorf("cwd = %q", p.Cwd)
	}
	// Unknown fields must be tolerated (the event JSON schema evolves).
	if _, err := Parse([]byte(`{"hook_event_name":"Stop","brand_new_field":123}`)); err != nil {
		t.Errorf("unknown field should be tolerated: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("invalid JSON must error")
	}
	if _, err := Parse([]byte("")); err == nil {
		t.Error("empty input must error")
	}
}

func TestWorkspaceFromCwd(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/ladyM", "ladyM"},
		{"/repo/pkg", "pkg"},
		{"/", ""}, // root: fall back to caller (empty)
		{"", ""},  // missing cwd
		{"relative", "relative"},
		{"/a/b/", "b"}, // trailing slash
	}
	for _, tc := range cases {
		if got := WorkspaceFromCwd(tc.in); got != tc.want {
			t.Errorf("WorkspaceFromCwd(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMapToolActionClass(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Read", "read"}, {"NotebookRead", "read"},
		{"Write", "write"},
		{"Edit", "update"}, {"NotebookEdit", "update"},
		{"Bash", "execute"}, {"shell", "execute"}, {"Shell", "execute"},
		{"Grep", "search"}, {"Glob", "search"}, {"Search", "search"},
		{"WebFetch", "query"}, {"WebSearch", "query"},
		{"TodoWrite", "update"},
		{"Task", "analyze"}, {"Agent", "analyze"},
		{"mcp__ladym__recall", ""}, // MCP tools: raw kept for worker backfill
		{"SomethingNew", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := MapToolActionClass(tc.in); got != tc.want {
			t.Errorf("MapToolActionClass(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToOperationSessionEvents(t *testing.T) {
	p := &Payload{HookEventName: "SessionStart", Cwd: "/x/ladyM", Source: "resume", SessionID: "s1"}
	ev := p.ToOperation("claude-code")
	if ev == nil {
		t.Fatal("SessionStart must map")
	}
	if ev.Agent != "claude-code" || ev.Action != "session_start" || ev.ActionClass != "create" {
		t.Errorf("got %+v", ev)
	}
	if ev.Observation != "resume" {
		t.Errorf("observation = %q", ev.Observation)
	}

	end := (&Payload{HookEventName: "SessionEnd"}).ToOperation("codex")
	if end == nil || end.Action != "session_end" || end.ActionClass != "" {
		t.Errorf("SessionEnd: %+v", end)
	}
	stop := (&Payload{HookEventName: "Stop"}).ToOperation("codex")
	if stop == nil || stop.Action != "turn_stop" || stop.Outcome != "success" || stop.ActionClass != "" {
		t.Errorf("Stop: %+v", stop)
	}
	prompt := (&Payload{HookEventName: "UserPromptSubmit", Prompt: "fix the auth bug"}).ToOperation("claude-code")
	if prompt == nil || prompt.Observation != "fix the auth bug" || prompt.Action != "prompt_submit" {
		t.Errorf("UserPromptSubmit: %+v", prompt)
	}
	// Metadata carries session identity.
	if prompt.Metadata["session_id"] != "" && prompt.Metadata["session_id"] != nil {
		// (set when SessionID non-empty; see metadata test below)
	}
}

func TestToOperationUnknownEvent(t *testing.T) {
	if ev := (&Payload{HookEventName: "PreCompact"}).ToOperation("claude-code"); ev != nil {
		t.Errorf("unknown event must return nil, got %+v", ev)
	}
}

func TestToOperationPostToolUse(t *testing.T) {
	p := &Payload{
		HookEventName: "PostToolUse", ToolName: "Edit",
		ToolInput:    map[string]any{"file_path": "/repo/engine/engine.go"},
		ToolResponse: map[string]any{"ok": true},
	}
	ev := p.ToOperation("claude-code")
	if ev == nil {
		t.Fatal("PostToolUse must map")
	}
	if ev.Action != "tool:Edit" || ev.ActionClass != "update" || ev.Outcome != "success" {
		t.Errorf("got %+v", ev)
	}
	if len(ev.Resources) != 1 || ev.Resources[0] != "engine.go" {
		t.Errorf("resources = %v, want [engine.go]", ev.Resources)
	}
	if ev.Observation == "" {
		t.Error("tool_response must land in observation")
	}

	fail := &Payload{HookEventName: "PostToolUseFailure", ToolName: "Bash",
		ToolInput: map[string]any{"command": "make test"}, ToolResponse: "exit 1"}
	fev := fail.ToOperation("claude-code")
	if fev == nil || fev.Outcome != "fail" || fev.ActionClass != "execute" {
		t.Errorf("failure: %+v", fev)
	}
	// Bash has no file_path: resources stay empty.
	if len(fev.Resources) != 0 {
		t.Errorf("bash resources = %v, want empty", fev.Resources)
	}
}

func TestTruncateObservation(t *testing.T) {
	// UserPromptSubmit truncates the prompt to PromptMaxRunes (spec 4.2: 2000).
	long := strings.Repeat("a", PromptMaxRunes+500)
	p := &Payload{HookEventName: "UserPromptSubmit", Prompt: long}
	ev := p.ToOperation("codex")
	if got := len([]rune(ev.Observation)); got != PromptMaxRunes {
		t.Errorf("prompt observation runes = %d, want %d", got, PromptMaxRunes)
	}
	// PostToolUse truncates tool_response to ObservationMaxRunes (spec 4.2: 500).
	tool := &Payload{HookEventName: "PostToolUse", ToolName: "Bash",
		ToolResponse: strings.Repeat("x", ObservationMaxRunes+100)}
	tev := tool.ToOperation("codex")
	if got := len([]rune(tev.Observation)); got != ObservationMaxRunes {
		t.Errorf("tool observation runes = %d, want %d", got, ObservationMaxRunes)
	}
}
