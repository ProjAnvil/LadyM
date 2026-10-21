//go:build eval

package eval

import (
	"encoding/json"
	"strings"
)

// Fixture couples one harness payload with the persisted state we expect it to
// produce. Payloads are built from real harness shapes, including the keys each
// harness actually sends.
type Fixture struct {
	Name      string
	Harness   string
	EventName string
	Workspace string
	Payload   map[string]any
	Want      Want
}

// Want describes the persisted state expected after one event.
type Want struct {
	ExitCode    int
	Episodes    int
	Edges       int
	ActionClass string
	// ObsRunes asserts an exact observation length in runes; 0 skips the check.
	// Every assertion here counts runes, never bytes, because the hook truncates
	// by rune and CJK input is routine.
	ObsRunes int
}

// PayloadJSON marshals the payload deterministically: encoding/json sorts map
// keys, so repeated runs feed byte-identical stdin.
func (f Fixture) PayloadJSON() (string, error) {
	b, err := json.Marshal(f.Payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

const (
	promptMaxRunes      = 2000
	observationMaxRunes = 500
	evalCwd             = "/x/ladyM"
	evalWorkspace       = "ladyM"
	evalSessionID       = "eval-session"
)

func newPayload(event string) map[string]any {
	return map[string]any{
		"hook_event_name": event,
		"session_id":      evalSessionID,
		"transcript_path": "/tmp/eval-transcript.jsonl",
		"cwd":             evalCwd,
	}
}

func toolPayload(event, tool string, input map[string]any, response any) map[string]any {
	p := newPayload(event)
	p["tool_name"] = tool
	p["tool_input"] = input
	p["tool_response"] = response
	return p
}

// repeat returns a string of exactly n runes, alternating a separator every 64
// characters so embeddings and content hashes see varied tokens rather than one
// giant repeated block.
func repeat(ch string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 && i%64 == 0 {
			b.WriteString("\n")
			continue
		}
		b.WriteString(ch)
	}
	return b.String()
}

// Fixtures is the full fidelity matrix: every registered hook event, both
// harnesses, both truncation ceilings, resource present and absent.
func Fixtures() []Fixture {
	return []Fixture{
		{
			Name: "claude/session-start", Harness: "claude-code", EventName: "SessionStart",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("SessionStart"), map[string]any{"source": "startup"}),
			Want:      Want{Episodes: 1, ActionClass: "create", ObsRunes: len("startup")},
		},
		{
			Name: "claude/session-end", Harness: "claude-code", EventName: "SessionEnd",
			Workspace: evalWorkspace, Payload: newPayload("SessionEnd"),
			Want: Want{Episodes: 1},
		},
		{
			Name: "claude/prompt-short", Harness: "claude-code", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": "fix the auth bug"}),
			Want:      Want{Episodes: 1, ObsRunes: len("fix the auth bug")},
		},
		{
			Name: "claude/prompt-capped", Harness: "claude-code", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": repeat("x", 5000)}),
			Want:      Want{Episodes: 1, ObsRunes: promptMaxRunes},
		},
		{
			Name: "claude/prompt-cjk", Harness: "claude-code", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": repeat("中", 3000)}),
			Want:      Want{Episodes: 1, ObsRunes: promptMaxRunes},
		},
		{
			Name: "claude/stop", Harness: "claude-code", EventName: "Stop",
			Workspace: evalWorkspace, Payload: newPayload("Stop"),
			Want: Want{Episodes: 1},
		},
		{
			Name: "claude/tool-read", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Read",
				map[string]any{"file_path": "/x/ladyM/engine/engine.go"}, "package engine"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "read", ObsRunes: len("package engine")},
		},
		{
			Name: "claude/tool-write", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Write",
				map[string]any{"file_path": "/x/ladyM/main.go"}, "wrote file"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "write"},
		},
		{
			Name: "claude/tool-notebook", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "NotebookEdit",
				map[string]any{"notebook_path": "/x/ladyM/demo.ipynb"}, "cell edited"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "update"},
		},
		{
			Name: "claude/tool-bash-no-resource", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Bash",
				map[string]any{"command": "go test ./..."}, "ok"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "execute", ObsRunes: len("ok")},
		},
		{
			Name: "claude/tool-mcp", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "mcp__ladym__recall",
				map[string]any{"query": "anything"}, "no hits"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: ""},
		},
		{
			Name: "claude/tool-response-capped", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Read", map[string]any{}, repeat("y", 3000)),
			Want:      Want{Episodes: 1, Edges: 0, ActionClass: "read", ObsRunes: observationMaxRunes},
		},
		{
			Name: "claude/tool-response-object", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Read", map[string]any{},
				map[string]any{"ok": true, "count": 3}),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "read", ObsRunes: len(`{"count":3,"ok":true}`)},
		},
		{
			Name: "claude/tool-failure", Harness: "claude-code", EventName: "PostToolUseFailure",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUseFailure", "Edit",
				map[string]any{"file_path": "/x/ladyM/broken.go"}, "syntax error"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "update"},
		},
		{
			Name: "claude/unknown-event", Harness: "claude-code", EventName: "PreCompact",
			Workspace: evalWorkspace, Payload: newPayload("PreCompact"),
			Want: Want{Episodes: 0, Edges: 0},
		},
		{
			Name: "claude/tool-edit-no-path", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Edit",
				map[string]any{"old_string": "a", "new_string": "b"}, "patched"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "update"},
		},
		{
			Name: "codex/tool-shell", Harness: "codex", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "shell",
				map[string]any{"command": "ls -la"}, "total 8"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "execute"},
		},
		{
			Name: "codex/prompt", Harness: "codex", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": "run the suite"}),
			Want:      Want{Episodes: 1, ObsRunes: len("run the suite")},
		},
	}
}

// merge copies base and layers extras on top, so fixtures stay one-liners.
func merge(base, extras map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extras {
		out[k] = v
	}
	return out
}
