//go:build eval

package eval

import (
	"encoding/json"
	"maps"
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
	// ActionRaw is the raw action string stamped into metadata. Asserting it stops
	// a mapping regression from silently widening or narrowing the vocabulary.
	ActionRaw string
	// Outcome separates PostToolUse from PostToolUseFailure: they produce rows
	// identical in every other field, so without this a regression that stamps
	// success on a failure event would pass the whole matrix green.
	Outcome string
	// Resources are the resource entity names expected to be linked.
	Resources []string
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

// repeat returns a string of exactly n runes. Newlines are substituted at every
// 64th position rather than appended, so the length assertion stays exact; the
// aim is only to break the input into multiple lines, not to vary the alphabet.
func repeat(ch string, n int) string {
	var b strings.Builder
	for i := range n {
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
			Want:      Want{Episodes: 1, ActionClass: "create", ActionRaw: "session_start", ObsRunes: len("startup")},
		},
		{
			Name: "claude/session-start-resume", Harness: "claude-code", EventName: "SessionStart",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("SessionStart"), map[string]any{"source": "resume"}),
			Want:      Want{Episodes: 1, ActionClass: "create", ActionRaw: "session_start", ObsRunes: len("resume")},
		},
		{
			Name: "claude/session-end", Harness: "claude-code", EventName: "SessionEnd",
			Workspace: evalWorkspace, Payload: newPayload("SessionEnd"),
			Want: Want{Episodes: 1, ActionRaw: "session_end"},
		},
		{
			Name: "claude/prompt-short", Harness: "claude-code", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": "fix the auth bug"}),
			Want:      Want{Episodes: 1, ActionRaw: "prompt_submit", ObsRunes: len("fix the auth bug")},
		},
		{
			Name: "claude/prompt-capped", Harness: "claude-code", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": repeat("x", 5000)}),
			Want:      Want{Episodes: 1, ActionRaw: "prompt_submit", ObsRunes: promptMaxRunes},
		},
		{
			Name: "claude/prompt-cjk", Harness: "claude-code", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": repeat("中", 3000)}),
			Want:      Want{Episodes: 1, ActionRaw: "prompt_submit", ObsRunes: promptMaxRunes},
		},
		{
			Name: "claude/stop", Harness: "claude-code", EventName: "Stop",
			Workspace: evalWorkspace, Payload: newPayload("Stop"),
			Want: Want{Episodes: 1, ActionRaw: "turn_stop", Outcome: "success"},
		},
		{
			Name: "claude/tool-read", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Read",
				map[string]any{"file_path": "/x/ladyM/engine/engine.go"}, "package engine"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "read", ActionRaw: "tool:Read", Outcome: "success", Resources: []string{"engine.go"}, ObsRunes: len("package engine")},
		},
		{
			Name: "claude/tool-write", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Write",
				map[string]any{"file_path": "/x/ladyM/main.go"}, "wrote file"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "write", ActionRaw: "tool:Write", Outcome: "success", Resources: []string{"main.go"}},
		},
		{
			Name: "claude/tool-notebook", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "NotebookEdit",
				map[string]any{"notebook_path": "/x/ladyM/demo.ipynb"}, "cell edited"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "update", ActionRaw: "tool:NotebookEdit", Outcome: "success", Resources: []string{"demo.ipynb"}},
		},
		{
			Name: "claude/tool-bash-no-resource", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Bash",
				map[string]any{"command": "go test ./..."}, "ok"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "execute", ActionRaw: "tool:Bash", Outcome: "success", ObsRunes: len("ok")},
		},
		{
			Name: "claude/tool-mcp", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "mcp__ladym__recall",
				map[string]any{"query": "anything"}, "no hits"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "", ActionRaw: "tool:mcp__ladym__recall", Outcome: "success"},
		},
		{
			Name: "claude/tool-response-capped", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload:   toolPayload("PostToolUse", "Read", map[string]any{}, repeat("y", 3000)),
			Want:      Want{Episodes: 1, Edges: 0, ActionClass: "read", ActionRaw: "tool:Read", Outcome: "success", ObsRunes: observationMaxRunes},
		},
		{
			Name: "claude/tool-response-object", Harness: "claude-code", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "Read", map[string]any{},
				map[string]any{"ok": true, "count": 3}),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "read", ActionRaw: "tool:Read", Outcome: "success", ObsRunes: len(`{"count":3,"ok":true}`)},
		},
		{
			Name: "claude/tool-failure", Harness: "claude-code", EventName: "PostToolUseFailure",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUseFailure", "Edit",
				map[string]any{"file_path": "/x/ladyM/broken.go"}, "syntax error"),
			Want: Want{Episodes: 1, Edges: 1, ActionClass: "update", ActionRaw: "tool:Edit", Outcome: "fail", Resources: []string{"broken.go"}},
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
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "update", ActionRaw: "tool:Edit", Outcome: "success"},
		},
		{
			Name: "codex/tool-shell", Harness: "codex", EventName: "PostToolUse",
			Workspace: evalWorkspace,
			Payload: toolPayload("PostToolUse", "shell",
				map[string]any{"command": "ls -la"}, "total 8"),
			Want: Want{Episodes: 1, Edges: 0, ActionClass: "execute", ActionRaw: "tool:shell", Outcome: "success"},
		},
		{
			Name: "codex/prompt", Harness: "codex", EventName: "UserPromptSubmit",
			Workspace: evalWorkspace,
			Payload:   merge(newPayload("UserPromptSubmit"), map[string]any{"prompt": "run the suite"}),
			Want:      Want{Episodes: 1, ActionRaw: "prompt_submit", ObsRunes: len("run the suite")},
		},
	}
}

// merge copies base and layers extras on top, so fixtures stay one-liners.
func merge(base, extras map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, base)
	maps.Copy(out, extras)
	return out
}
