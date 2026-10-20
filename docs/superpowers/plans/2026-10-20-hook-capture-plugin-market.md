# 实施计划：hook 静默捕获与 plugin market

> 日期：2026-10-20。分支：`feat/hook-capture-plugin-market`（已建，规格已提交 f9d020f）。
> 规格：`docs/superpowers/specs/2026-10-20-hook-capture-plugin-market-design.md`。
> 纪律：每个任务先写失败的测试（RED），再写实现（GREEN），再提交。全程 hashing embedding，零网络零 key。

## 总览

| 批次 | 任务 | 包 |
|---|---|---|
| M0 | 1-5 | 新包 `hook/`：解析与映射（纯函数） |
| M1 | 6-7 | `cli/`：`ladym hook` 子命令 + 集成测试 |
| M2 | 8-11 | `cli/`：`ladym hook install/uninstall` |
| M3 | 12 | `plugins/ladym/`：plugin 目录 + 静态校验 |
| M4 | 13 | `docs/hooks.md` 安装指引 |

---

## 任务 1：hook 包骨架 + Payload 解析

- 文件：`hook/hook.go`（新建）、`hook/hook_test.go`（新建）
- TDD：先写 `hook_test.go`：

```go
package hook

import (
	"encoding/json"
	"errors"
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
	// 未知字段必须被宽容忽略（事件 JSON 在演进）。
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
```

- 再写 `hook/hook.go`：

```go
// Package hook parses harness lifecycle-hook events (Claude Code / Codex)
// and maps them onto engine write operations. Parsing is tolerant: unknown
// fields are ignored so upstream schema drift never breaks the binary.
package hook

import (
	"encoding/json"
	"fmt"
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
```

（补 `strings`、`errors` import。）
- 验证：`go test ./hook/` 先红后绿。

## 任务 2：WorkspaceFromCwd

- 文件：`hook/hook.go`、`hook/hook_test.go`
- 测试：

```go
func TestWorkspaceFromCwd(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/ladyM", "ladyM"},
		{"/repo/pkg", "pkg"},
		{"/", ""},          // root: fall back to caller (empty)
		{"", ""},           // missing cwd
		{"relative", "relative"},
		{"/a/b/", "b"},     // trailing slash
	}
	for _, tc := range cases {
		if got := WorkspaceFromCwd(tc.in); got != tc.want {
			t.Errorf("WorkspaceFromCwd(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- 实现：

```go
// WorkspaceFromCwd derives the memory workspace from the event's cwd by
// taking its base directory name (spec §2: cwd directory name).
func WorkspaceFromCwd(cwd string) string {
	if cwd == "" {
		return ""
	}
	return path.Base(strings.TrimRight(cwd, "/"))
}
```

- 验证：`go test ./hook/ -run Workspace` 先红后绿。

## 任务 3：工具名 → ActionClass 映射表

- 文件：`hook/hook.go`、`hook/hook_test.go`
- 测试（表驱动，全条目）：

```go
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
		{"mcp__ladym__recall", ""},   // MCP tools: raw kept for worker backfill
		{"SomethingNew", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := MapToolActionClass(tc.in); got != tc.want {
			t.Errorf("MapToolActionClass(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- 实现：

```go
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
```

- 验证：`go test ./hook/ -run MapTool` 先红后绿。

## 任务 4：ToOperation —— 会话与回合类事件

- 文件：`hook/hook.go`、`hook/hook_test.go`
- 说明：`hook` 包 import `engine`（engine 不依赖 hook，无环）。
- 测试：

```go
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
```

- 实现：

```go
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
```

- 验证：`go test ./hook/ -run ToOperation` 先红后绿。

## 任务 5：ToOperation —— 工具类事件 + Resources 提取

- 文件：`hook/hook.go`、`hook/hook_test.go`
- 测试：

```go
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
	long := strings.Repeat("a", 600)
	p := &Payload{HookEventName: "UserPromptSubmit", Prompt: long}
	ev := p.ToOperation("codex")
	if got := len([]rune(ev.Observation)); got != 500 {
		t.Errorf("observation runes = %d, want 500", got)
	}
}
```

- 实现（补进 switch）：

```go
	case "PostToolUse", "PostToolUseFailure":
		base.Action = "tool:" + p.ToolName
		base.ActionClass = MapToolActionClass(p.ToolName)
		base.Resources = resourcesFromInput(p.ToolInput)
		base.Observation = stringifyResponse(p.ToolResponse)
		base.Outcome = "fail"
		if p.HookEventName == "PostToolUse" {
			base.Outcome = "success"
		}
```

辅助函数：

```go
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
```

- 验证：`go test ./hook/` 全绿。`go vet ./hook/` 干净。

## 任务 6：`ladym hook` 子命令

- 文件：`cli/hook.go`（新建）、`cli/cli.go`（注册命令）
- `cli/cli.go` 的 `newRootCmd` 里 `root.AddCommand(...)` 追加 `hookCmd()`。
- `cli/hook.go`：

```go
package cli

import (
	"cmp"
	"fmt"
	"io"
	"os"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/hook"
	"github.com/spf13/cobra"
)

// hookCmd is `ladym hook`: the harness lifecycle-hook carrier. It reads the
// event JSON from stdin, maps it, writes it through the engine, and exits.
// The hook is a pure observer: stdout stays empty and the exit code is never
// 2, so it never blocks or feeds decisions back into the harness.
func hookCmd() *cobra.Command {
	var db, workspace, harness string
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Record a harness hook event from stdin (observer, never blocks).",
		RunE: func(c *cobra.Command, args []string) error {
			data, err := io.ReadAll(c.InOrStdin())
			if err != nil {
				return &exitError{1}
			}
			p, err := hook.Parse(data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ladym hook: %v\n", err)
				return &exitError{1}
			}
			ev := p.ToOperation(harness)
			if ev == nil {
				// Unknown event type: tolerate schema drift, exit 0.
				fmt.Fprintf(os.Stderr, "ladym hook: ignored event %q\n", p.HookEventName)
				return nil
			}
			ws := cmp.Or(workspace, hook.WorkspaceFromCwd(p.Cwd))
			eng, err := newEngine(db, "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "ladym hook: %v\n", err)
				return &exitError{1}
			}
			defer eng.Close()
			if _, err := eng.Scope(ws).RecordOperation(*ev); err != nil {
				fmt.Fprintf(os.Stderr, "ladym hook: %v\n", err)
				return &exitError{1}
			}
			return nil
		},
	}
	addDBWS(cmd, &db, &workspace)
	cmd.Flags().StringVar(&harness, "harness", "", "Event source (claude-code | codex); lands in the Agent field.")
	return cmd
}
```

- 测试：`cli/hook_test.go`——损坏 stdin exit 1（错误信息进 stderr）、未知事件 exit 0：

```go
//go:build !enterprise

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHookCmdBadJSONExits1(t *testing.T) {
	db := isolateEnv(t)
	cmd := hookCmd()
	cmd.SetArgs("--db", db)
	cmd.SetIn(bytes.NewReader([]byte("not json")))
	out, err := runCmd(t, cmd)
	if err == nil {
		t.Fatalf("bad JSON must error, out=%q", out)
	}
	if !strings.Contains(out, "bad JSON") && !strings.Contains(out, "ladym hook") {
		t.Errorf("stderr diagnostic missing: %q", out)
	}
}

func TestHookCmdUnknownEventExits0(t *testing.T) {
	db := isolateEnv(t)
	cmd := hookCmd()
	cmd.SetArgs("--db", db)
	cmd.SetIn(bytes.NewReader([]byte(`{"hook_event_name":"PreCompact"}`)))
	if _, err := runCmd(t, cmd); err != nil {
		t.Fatalf("unknown event must exit 0: %v", err)
	}
}
```

- 验证：`go test ./cli/ -run TestHookCmd` 先红后绿。

## 任务 7：stdin → db 集成测试（记忆 + operates_on 边）

- 文件：`cli/hook_test.go`
- 测试：

```go
func TestHookCmdWritesMemoryAndEdge(t *testing.T) {
	db := isolateEnv(t)
	raw := `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/x/ladyM",
		"tool_name":"Edit","tool_input":{"file_path":"/x/ladyM/engine/engine.go"},
		"tool_response":{"ok":true}}`
	cmd := hookCmd()
	cmd.SetArgs("--db", db, "--harness", "claude-code")
	cmd.SetIn(bytes.NewReader([]byte(raw)))
	if out, err := runCmd(t, cmd); err != nil {
		t.Fatalf("hook run: %v, out=%q", err, out)
	}

	eng, err := newEngine(db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	resp, err := eng.Scope("ladyM").RecallOperations(engine.OperationsQuery{Resource: "engine.go", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fallback {
		t.Fatal("resource engine.go must resolve (operates_on edge missing?)")
	}
	if len(resp.Items) == 0 {
		t.Fatal("no episodes recalled for engine.go")
	}
	m := resp.Items[0].Memory
	if m.MetaString("session_id") != "s1" {
		t.Errorf("session_id metadata = %q", m.MetaString("session_id"))
	}
}
```

- 验证：`go test ./cli/ -run TestHookCmdWrites` 先红后绿（这条同时证明 workspace 推断与确定性建边落库）。

## 任务 8：install 核心 —— 合并 / 幂等 / 备份

- 文件：`cli/hook_install.go`（新建）、`cli/hook_install_test.go`（新建）
- 核心函数（与命令层解耦，路径由调用方传入，便于测试）：

```go
package cli

import (
	"encoding/json"
	"fmt"
	"os"
)

// hookCommand is the command string install writes; the prefix is the
// identity marker for idempotent install/uninstall.
const hookCommandPrefix = "ladym hook "

// claudeHookEvents / codexHookEvents are the V1 event sets (spec §5.1).
var (
	claudeHookEvents = []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "Stop",
	}
	codexHookEvents = []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "Stop",
	}
)

// installHookFile merges LadyM hook entries into the JSON config at path.
// Existing user hooks are preserved verbatim; a file with our prefix already
// present is a no-op (idempotent). The original file is backed up once to
// path+".ladym-bak" before the first write. A corrupt (unparseable) file is
// an error — we never overwrite what we cannot read.
func installHookFile(path, harness string, events []string, withTimeout bool) error {
	hooks, err := readHookConfig(path)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if hasOurHook(hooks, ev) {
			continue
		}
		entry := map[string]any{"hooks": []any{newHookEntry(harness, withTimeout)}}
		if list, ok := hooks[ev].([]any); ok {
			hooks[ev] = append(list, entry)
		} else if hooks[ev] == nil {
			hooks[ev] = []any{entry}
		} else {
			return fmt.Errorf("hooks.%s has unexpected shape", ev)
		}
	}
	return writeHookConfig(path, map[string]any{"hooks": hooks})
}

func newHookEntry(harness string, withTimeout bool) map[string]any {
	e := map[string]any{"type": "command", "command": hookCommandPrefix + "--harness " + harness}
	if withTimeout {
		e["timeout"] = 10
	}
	return e
}

func readHookConfig(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("hook install: %s is not valid JSON: %w", path, err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	return hooks, nil
}

func writeHookConfig(path string, doc map[string]any) error {
	if _, err := os.Stat(path); err == nil {
		bak := path + ".ladym-bak"
		if _, err := os.Stat(bak); os.IsNotExist(err) {
			if err := copyFile(path, bak); err != nil {
				return err
			}
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// hasOurHook reports whether event ev already carries a command starting
// with our prefix (tolerates --harness flag differences).
func hasOurHook(hooks map[string]any, ev string) bool {
	list, ok := hooks[ev].([]any)
	if !ok {
		return false
	}
	for _, g := range list {
		group, ok := g.(map[string]any)
		if !ok {
			continue
		}
		hs, _ := group["hooks"].([]any)
		for _, h := range hs {
			entry, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := entry["command"].(string); strings.HasPrefix(cmd, hookCommandPrefix) {
				return true
			}
		}
	}
	return false
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
```

- 测试（`cli/hook_install_test.go`）：空文件生成、已有用户 hook 合并不覆盖、二次 install 幂等（文件内容不变）、损坏 JSON 拒写、备份恰好一份：

```go
//go:build !enterprise

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func installFixture(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "settings.json")
}

func TestInstallHookFileCreates(t *testing.T) {
	p := installFixture(t)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUseFailure"]; !ok {
		t.Error("PostToolUseFailure event missing")
	}
}

func TestInstallHookFilePreservesUserHooks(t *testing.T) {
	p := installFixture(t)
	user := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-send done"}]}]},"model":"sonnet"}`
	if err := os.WriteFile(p, []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var doc map[string]any
	json.Unmarshal(b, &doc)
	if doc["model"] != "sonnet" {
		t.Error("non-hook keys must survive")
	}
	hooks := doc["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop groups = %d, want 2 (user + ours)", len(stop))
	}
}

func TestInstallHookFileIdempotent(t *testing.T) {
	p := installFixture(t)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b1, _ := os.ReadFile(p)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(p)
	if string(b1) != string(b2) {
		t.Error("second install must be a no-op")
	}
}

func TestInstallHookFileRejectsCorruptFile(t *testing.T) {
	p := installFixture(t)
	os.WriteFile(p, []byte("{broken"), 0o644)
	if err := installHookFile(p, "claude-code", claudeHookEvents, false); err == nil {
		t.Fatal("corrupt file must be refused")
	}
}

func TestInstallHookFileBackupOnce(t *testing.T) {
	p := installFixture(t)
	os.WriteFile(p, []byte(`{"hooks":{}}`), 0o644)
	installHookFile(p, "claude-code", claudeHookEvents, false)
	os.WriteFile(p, []byte(`{"hooks":{},"x":1}`), 0o644) // user edits between installs
	installHookFile(p, "claude-code", claudeHookEvents, false)
	bak, err := os.ReadFile(p + ".ladym-bak")
	if err != nil {
		t.Fatal("backup must exist")
	}
	if string(bak) != `{"hooks":{}}`+"\n" && string(bak) != `{"hooks":{}}` {
		// backup holds the FIRST original, not the later edit
		t.Errorf("backup = %q", string(bak))
	}
}
```

（备份断言按实际 marshal 输出微调。）
- 验证：`go test ./cli/ -run TestInstallHookFile` 先红后绿。

## 任务 9：uninstall 核心

- 文件：`cli/hook_install.go`、`cli/hook_install_test.go`
- 实现：

```go
// uninstallHookFile removes every LadyM hook entry from the config at path,
// leaving user entries untouched. Empty event arrays and an empty hooks
// object are pruned; an absent file is a no-op.
func uninstallHookFile(path string) error {
	hooks, err := readHookConfig(path)
	if err != nil {
		return err
	}
	for ev := range hooks {
		list, ok := hooks[ev].([]any)
		if !ok {
			continue
		}
		var kept []any
		for _, g := range list {
			group, ok := g.(map[string]any)
			if !ok {
				kept = append(kept, g)
				continue
			}
			hs, _ := group["hooks"].([]any)
			var keptHooks []any
			for _, h := range hs {
				if entry, ok := h.(map[string]any); ok {
					if cmd, _ := entry["command"].(string); strings.HasPrefix(cmd, hookCommandPrefix) {
						continue
					}
				}
				keptHooks = append(keptHooks, h)
			}
			if len(keptHooks) == 0 {
				continue // group emptied: drop it
			}
			group["hooks"] = keptHooks
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	if len(hooks) == 0 {
		// Prune the whole doc only if it held nothing but hooks.
		b, err := os.ReadFile(path)
		if err == nil {
			var doc map[string]any
			if json.Unmarshal(b, &doc) == nil && len(doc) <= 1 {
				return os.Remove(path)
			}
		}
	}
	return writeHookConfig(path, map[string]any{"hooks": hooks})
}
```

- 测试：只删自己人条目（用户 notify-send 存活）、空事件键被清、文件不存在 no-op。
- 验证：`go test ./cli/ -run TestUninstall` 先红后绿。

## 任务 10：install / uninstall 子命令挂到 CLI

- 文件：`cli/hook.go`
- 实现（挂在 hookCmd 下）：

```go
func init() { /* 注册见 hookInstallCmd 内部 */ }

func hookInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write LadyM hook entries into harness user configs.",
		RunE: func(c *cobra.Command, args []string) error {
			var any bool
			if claude || all {
				p := filepath.Join(homeDir(), ".claude", "settings.json")
				if err := installHookFile(p, "claude-code", claudeHookEvents, false); err != nil {
					return err
				}
				fmt.Printf("installed LadyM hooks into %s (restart your session to activate)\n", p)
				any = true
			}
			if codex || all {
				p := filepath.Join(homeDir(), ".codex", "hooks.json")
				if err := installHookFile(p, "codex", codexHookEvents, true); err != nil {
					return err
				}
				fmt.Printf("installed LadyM hooks into %s (run /hooks in Codex to trust them)\n", p)
				any = true
			}
			if !any {
				return errors.New("pick one: --claude, --codex, or --all")
			}
			if _, err := exec.LookPath("ladym"); err != nil {
				fmt.Fprintln(os.Stderr, "warning: ladym is not on PATH; hooks will fail to start")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&claude, "claude", false, "Install into ~/.claude/settings.json")
	cmd.Flags().BoolVar(&codex, "codex", false, "Install into ~/.codex/hooks.json")
	cmd.Flags().BoolVar(&all, "all", false, "Install into both")
	return cmd
}

func hookUninstallCmd() *cobra.Command {
	// --claude/--codex/--all same shape; calls uninstallHookFile on the
	// corresponding paths; missing file is a no-op success.
}

// homeDir resolves the user home (os.UserHomeDir with fallback).
func homeDir() string { h, _ := os.UserHomeDir(); return h }
```

`hookCmd()` 里：`cmd.AddCommand(hookInstallCmd(), hookUninstallCmd())`。flag 变量收进闭包/结构体，避免包级全局。
- 测试：isolateEnv 下 `runCmd` 调 `hookCmd()` 带 `install --claude`，断言 `$HOME/.claude/settings.json` 生成且含 6 事件；再 `uninstall --claude` 后文件不再含我们的 command。
- 验证：`go test ./cli/ -run "TestHook(Install|Uninstall)Cmd"` 先红后绿。

## 任务 11：plugin 目录 + 静态校验

- 前置：抓取并核对两份官方文档——developers.openai.com/codex 的 plugin（manifest 字段、默认 hooks/hooks.json 规则）、code.claude.com 的 create-marketplace / manifest-reference。**字段以文档为准，下面结构是占位**。
- 文件（新建）：

```
plugins/ladym/
├── .claude-plugin/plugin.json     # {"name":"ladym","version":"<Edition>","description":"LadyM silent memory capture + recall"}
├── hooks/hooks.json               # spec §5.1 Claude 事件集，command: ladym hook --harness claude-code
└── .mcp.json                      # mcpServers: ladym → command "ladym", args ["serve"]
plugins/.claude-plugin/marketplace.json   # catalog 条目指向 ladym plugin（字段按官方文档）
```

Codex plugin 形态按核对结果落（若 Codex plugin 需要 TOML manifest 或独立目录结构，加 `plugins/ladym-codex/`）。
- 测试：`cli/hook_plugin_test.go`——读 `../plugins/ladym/hooks/hooks.json` 断言 JSON 合法、事件集恰为 6 类、每条 command 以 `ladym hook` 开头；`plugin.json` 断言 name/version。
- 验证：`go test ./cli/ -run TestPlugin` 绿；手工把 plugin 装进本机 Claude Code 过一遍真实 hook 触发。

## 任务 12：安装指引文档

- 文件：`docs/hooks.md`（新建）
- 内容：两家的安装/卸载命令、零配置默认（每项目一个 ladym.db）、统一库配置（LADYM_DB）、workspace 推断规则、Codex trust 步骤、已知取舍（脱敏 V1 不做）、故障排查（ladym 不在 PATH、db 锁、60 秒超时）。
- 验证：按文档在新机器/干净 HOME 手工过一遍 install → 触发 → recall-ops 查得到。

## 收尾

- `go test ./...` 全绿 + `go vet ./...` 干净。
- 覆盖率不低于现有基线（operations 94.6% / engine 96.5% 的口径不适用于新包；hook 包目标 ≥90%）。
- 向用户报告：合并 / 开 PR / 保留 / 丢弃 四选一。
