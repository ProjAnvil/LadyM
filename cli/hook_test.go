//go:build !enterprise

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/engine"
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
