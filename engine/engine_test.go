package engine

import (
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := New(config.ForTesting(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

func TestRememberAndRecall(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("auth uses JWT with 24h expiry", schema.LayerSemantic, schema.TypeFact, []string{"auth"}, nil, "cli", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID == "" {
		t.Fatal("remember returned empty id")
	}
	resp, err := eng.Recall("how does authentication work", "", 5, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one recall result")
	}
	if resp.Results[0].Memory.ID != m.ID {
		t.Errorf("top result id = %s, want %s", resp.Results[0].Memory.ID, m.ID)
	}
}

func TestRememberDropsNoise(t *testing.T) {
	eng := newTestEngine(t)
	m, err := eng.Remember("lol", schema.LayerSemantic, schema.TypeFact, nil, nil, "cli", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.MetaString("gated") != "dropped" {
		t.Errorf("gated = %q, want dropped", m.MetaString("gated"))
	}
	// not persisted
	got, err := eng.Store.GetMemory(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("dropped memory should not be persisted")
	}
}

func TestRecordEventAndConsolidate(t *testing.T) {
	eng := newTestEngine(t)
	_, err := eng.RecordEvent("claude", "fixed login bug", "auth was broken", "success", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.RecordEvent("claude", "fixed login bug", "auth was broken", "success", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := eng.Consolidate("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.KeptEpisodes != 2 {
		t.Errorf("kept_episodes = %d, want 2", report.KeptEpisodes)
	}
	// first episode → ADD; identical second → NOOP (hash equal)
	if report.Actions["ADD"] != 1 {
		t.Errorf("ADD = %d, want 1", report.Actions["ADD"])
	}
	if report.Actions["NOOP"] != 1 {
		t.Errorf("NOOP = %d, want 1", report.Actions["NOOP"])
	}
}

func TestSearchCodeAfterIndex(t *testing.T) {
	eng := newTestEngine(t)
	dir := t.TempDir()
	writeFile(t, dir+"/service.py", `def hash_password(password: str) -> str:
    """Hash a plaintext password."""
    return "hashed:" + password

class AuthService:
    def login(self, username: str, password: str) -> str:
        """Issue a JWT."""
        if verify_password(password, self.secret):
            return self._issue_token(username)

    def _issue_token(self, username: str) -> str:
        return "jwt." + username
`)
	report, err := eng.IndexCode(dir, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesIndexed != 1 {
		t.Fatalf("files_indexed = %d, want 1", report.FilesIndexed)
	}
	if report.SymbolsWritten < 4 {
		t.Errorf("symbols_written = %d, want >= 4", report.SymbolsWritten)
	}
	resp, err := eng.SearchCode("hash password", 5, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected search results")
	}
}

func TestStats(t *testing.T) {
	eng := newTestEngine(t)
	_, _ = eng.Remember("a fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "", "")
	s, err := eng.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalMemories != 1 {
		t.Errorf("total_memories = %d, want 1", s.TotalMemories)
	}
	if s.ByLayer[string(schema.LayerSemantic)] != 1 {
		t.Errorf("by_layer semantic = %d, want 1", s.ByLayer[string(schema.LayerSemantic)])
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
