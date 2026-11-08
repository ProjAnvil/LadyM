//go:build !enterprise

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/hook"
)

func TestHookCmdBadJSONExits1(t *testing.T) {
	db := isolateEnv(t)
	cmd := hookCmd()
	cmd.SetIn(bytes.NewReader([]byte("not json")))
	out, err := runCmd(t, cmd, "--db", db)
	if err == nil {
		t.Fatalf("bad JSON must error, out=%q", out)
	}
	if !strings.Contains(out, "bad JSON") && !strings.Contains(out, "ladym hook") {
		t.Errorf("stderr diagnostic missing: %q", out)
	}
}

func TestCanonicalHookCLIIdentity(t *testing.T) {
	db := isolateEnv(t)
	t.Setenv("LADYM_CAPTURE_IDENTITY_MODE", "canonical")
	t.Setenv("LADYM_WORKSPACE", "configured-must-not-win")
	root := filepath.Join(t.TempDir(), "空 格")
	nested := filepath.Join(root, "pkg")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	t.Chdir(nested)
	_, ws, err := hook.ProjectIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cwd   string
		flags []string
		ws    string
	}{{nested, nil, ws}, {"", nil, ws}, {root, []string{"-w", "explicit"}, "explicit"}} {
		p := hook.Payload{HookEventName: "PostToolUse", Cwd: tc.cwd, ToolName: "Edit",
			ToolInput: map[string]any{"file_path": filepath.Join(nested, "main.go")}}
		raw, _ := json.Marshal(p)
		cmd := hookCmd()
		cmd.SetIn(bytes.NewReader(raw))
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(append([]string{"--db", db, "--harness", "codex"}, tc.flags...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("hook: %v, stderr=%s", err, &stderr)
		}
		if stdout.Len() != 0 {
			t.Fatalf("observer stdout = %q", &stdout)
		}
		eng, err := newEngine(db, "")
		if err != nil {
			t.Fatal(err)
		}
		got, err := eng.Scope(tc.ws).RecallOperations(engine.OperationsQuery{Resource: "pkg/main.go"})
		eng.Close()
		if err != nil || got.Fallback || len(got.Items) == 0 {
			t.Fatalf("workspace %q: %+v %v", tc.ws, got, err)
		}
		if got.Items[0].Memory.MetaString("knowledge_status") != "observed" {
			t.Fatal("CLI dropped observed status")
		}
	}
}

func TestHookCmdUnknownEventExits0(t *testing.T) {
	db := isolateEnv(t)
	cmd := hookCmd()
	cmd.SetIn(bytes.NewReader([]byte(`{"hook_event_name":"PreCompact"}`)))
	if _, err := runCmd(t, cmd, "--db", db); err != nil {
		t.Fatalf("unknown event must exit 0: %v", err)
	}
}

func TestHookCmdWritesMemoryAndEdge(t *testing.T) {
	db := isolateEnv(t)
	raw := `{"hook_event_name":"PostToolUse","session_id":"s1","cwd":"/x/ladyM",
		"tool_name":"Edit","tool_input":{"file_path":"/x/ladyM/engine/engine.go"},
		"tool_response":{"ok":true}}`
	cmd := hookCmd()
	cmd.SetIn(bytes.NewReader([]byte(raw)))
	if out, err := runCmd(t, cmd, "--db", db, "--harness", "claude-code"); err != nil {
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
