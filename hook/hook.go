// Package hook parses harness lifecycle-hook events (Claude Code / Codex)
// and maps them onto engine write operations. Parsing is tolerant: unknown
// fields are ignored so upstream schema drift never breaks the binary.
package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ProjAnvil/LadyM/engine"
)

// Payload is the tolerant parse of a hook event JSON document. The two
// harnesses use near-identical shapes, so one struct serves both.
type Payload struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      string         `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	Cwd            string         `json:"cwd"`
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	ToolResponse   any            `json:"tool_response"`
	Prompt         string         `json:"prompt"`
	Source         string         `json:"source"`
}

// Parse decodes a hook event document. Unknown fields are ignored.
func Parse(data []byte) (*Payload, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, errors.New("hook: empty stdin")
	}
	var p Payload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("hook: bad JSON: %w", err)
	}
	return &p, nil
}

// WorkspaceFromCwd derives the memory workspace from the event's cwd by
// taking its base directory name (spec §2: cwd directory name).
func WorkspaceFromCwd(cwd string) string {
	trimmed := strings.TrimRight(cwd, "/")
	if trimmed == "" {
		return "" // root or empty cwd: fall back to caller
	}
	return path.Base(trimmed)
}

// toolActionClass maps harness tool names onto the canonical action
// vocabulary (operations/actions.go). Misses return "" so the raw action
// string stays available for the worker's LLM backfill.
var toolActionClass = map[string]string{
	"Read": "read", "NotebookRead": "read",
	"Write": "write",
	"Edit": "update", "NotebookEdit": "update", "TodoWrite": "update",
	"Bash": "execute", "shell": "execute", "Shell": "execute",
	"Grep": "search", "Glob": "search", "Search": "search",
	"WebFetch": "query", "WebSearch": "query",
	"Task": "analyze", "Agent": "analyze",
}

func MapToolActionClass(tool string) string {
	if strings.HasPrefix(tool, "mcp__") {
		return ""
	}
	return toolActionClass[tool]
}

// Harness names the event source; it lands in the OperationEvent Agent
// field. stdin JSON is identical across harnesses, so the caller passes it
// explicitly (CLI --harness flag, written by `ladym hook install`).
type Harness = string

const (
	PromptMaxRunes      = 2000
	ObservationMaxRunes = 500
)

// ToOperation maps the payload onto an engine write. It returns nil when the
// event is outside the V1 set (spec §4.2): the CLI then exits 0 silently.
func (p *Payload) ToOperation(harness Harness) *engine.OperationEvent {
	meta := map[string]any{
		"hook_event_name": p.HookEventName,
		"session_id":      p.SessionID,
		"transcript_path": p.TranscriptPath,
		"cwd":             p.Cwd,
	}
	base := engine.OperationEvent{Agent: harness, Tags: []string{"hook"}, Metadata: meta}
	switch p.HookEventName {
	case "SessionStart":
		base.Action = "session_start"
		base.ActionClass = "create"
		base.Observation = p.Source
	case "SessionEnd":
		base.Action = "session_end"
	case "UserPromptSubmit":
		base.Action = "prompt_submit"
		base.Observation = truncate(p.Prompt, PromptMaxRunes)
	case "Stop":
		base.Action = "turn_stop"
		base.Outcome = "success"
	case "PostToolUse", "PostToolUseFailure":
		base.Action = "tool:" + p.ToolName
		base.ActionClass = MapToolActionClass(p.ToolName)
		base.Resources = resourcesFromInput(p.ToolInput)
		base.Observation = stringifyResponse(p.ToolResponse)
		base.Outcome = "fail"
		if p.HookEventName == "PostToolUse" {
			base.Outcome = "success"
		}
	default:
		return nil
	}
	return &base
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// resourcesFromInput pulls file-path-ish fields out of tool_input and keeps
// their base names: the resource entities LadyM resolves and links with
// operates_on edges.
func resourcesFromInput(in map[string]any) []string {
	var out []string
	for _, key := range []string{"file_path", "notebook_path"} {
		if v, ok := in[key].(string); ok && v != "" {
			out = append(out, path.Base(v))
		}
	}
	return out
}

// stringifyResponse renders tool_response as a bounded observation string:
// strings pass through, anything else is compact JSON.
func stringifyResponse(resp any) string {
	switch v := resp.(type) {
	case nil:
		return ""
	case string:
		return truncate(v, ObservationMaxRunes)
	default:
		b, err := json.Marshal(resp)
		if err != nil {
			return ""
		}
		return truncate(string(b), ObservationMaxRunes)
	}
}
