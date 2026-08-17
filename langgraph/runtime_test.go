package langgraph

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	lgruntime "github.com/projanvil/langchain-golang/langgraph/runtime"
)

func TestResolveEnginePassthrough(t *testing.T) {
	eng := newTestEngine(t)
	got, err := ResolveEngine(eng, "ignored-ws")
	if err != nil {
		t.Fatal(err)
	}
	if got != eng {
		t.Fatal("expected the same engine back, workspace ignored")
	}
}

func TestResolveEngineFromConfig(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := ResolveEngine(cfg, "team-a")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if eng.Config.Workspace != "team-a" {
		t.Fatalf("workspace = %q, want team-a", eng.Config.Workspace)
	}
	if cfg.Workspace == "team-a" {
		t.Fatal("caller config mutated")
	}
	if eng.Config.DBPath != cfg.DBPath {
		t.Fatalf("db_path = %q, want %q", eng.Config.DBPath, cfg.DBPath)
	}
}

func TestResolveEngineFromDBPath(t *testing.T) {
	db := filepath.Join(t.TempDir(), "mem.db")
	eng, err := ResolveEngine(db, "team-b")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if eng.Config.DBPath != db {
		t.Fatalf("db_path = %q, want %q", eng.Config.DBPath, db)
	}
	if eng.Config.Workspace != "team-b" {
		t.Fatalf("workspace = %q, want team-b", eng.Config.Workspace)
	}
}

func TestResolveEngineNil(t *testing.T) {
	t.Chdir(t.TempDir()) // keep the default db path inside the sandbox
	eng, err := ResolveEngine(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
}

func TestResolveEngineNilWithWorkspace(t *testing.T) {
	t.Chdir(t.TempDir()) // keep the default db path inside the sandbox
	eng, err := ResolveEngine(nil, "team-nil")
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if eng.Config.Workspace != "team-nil" {
		t.Fatalf("workspace = %q, want team-nil", eng.Config.Workspace)
	}
}

func TestResolveEngineUnsupported(t *testing.T) {
	if _, err := ResolveEngine(42, ""); err == nil {
		t.Fatal("expected error for unsupported source type")
	}
}

func TestWorkspaceFromUserID(t *testing.T) {
	wsFn := WorkspaceFromUserID()

	rt := lgruntime.Runtime{Context: map[string]any{"user_id": "alice"}}
	if got := wsFn(rt); got != "alice" {
		t.Fatalf("got %q, want alice", got)
	}

	for name, rt := range map[string]lgruntime.Runtime{
		"no context":    {},
		"no user_id":    {Context: map[string]any{"other": "x"}},
		"non-string":    {Context: map[string]any{"user_id": 7}},
		"empty user_id": {Context: map[string]any{"user_id": ""}},
	} {
		if got := wsFn(rt); got != "" {
			t.Fatalf("%s: got %q, want empty (engine default wins)", name, got)
		}
	}
}
