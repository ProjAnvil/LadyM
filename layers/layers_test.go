package layers

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	_ "modernc.org/sqlite"
)

func newTestStoreWithPath(t *testing.T) (storage.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := storage.NewStore(dbPath, 16, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dbPath
}

func newTestStore(t *testing.T) storage.Store {
	t.Helper()
	s, _ := newTestStoreWithPath(t)
	return s
}

// backdoorExec runs raw SQL on a private connection — used only to simulate
// storage-level failures (dropped tables) and forced timestamps. The Store
// interface intentionally hides *sql.DB, so tests open their own connection.
func backdoorExec(t *testing.T, dbPath, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func newTestEpisodic(t *testing.T) *EpisodicMemory {
	t.Helper()
	return NewEpisodicMemory(newTestStore(t), storage.NewHashingEmbedding(16), "test")
}

// Fix 3: Recent must honour limit and return newest-first (created_at DESC).
func TestEpisodicRecentOrdersAndLimits(t *testing.T) {
	store, dbPath := newTestStoreWithPath(t)
	e := NewEpisodicMemory(store, storage.NewHashingEmbedding(16), "test")
	ids := make([]string, 4)
	for i := 0; i < 4; i++ {
		m, err := e.Record("agent", "act"+string(rune('a'+i)), "", "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = m.ID
		// Deterministic increasing timestamps: ids[0] oldest … ids[3] newest.
		backdoorExec(t, dbPath, "UPDATE memories SET created_at = ? WHERE id = ?", float64(i+1), m.ID)
	}

	recent, err := e.Recent(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 {
		t.Fatalf("Recent(2) returned %d memories, want 2", len(recent))
	}
	if recent[0].ID != ids[3] || recent[1].ID != ids[2] {
		t.Errorf("Recent(2) order = [%s %s], want newest-first [%s %s]",
			recent[0].ID, recent[1].ID, ids[3], ids[2])
	}

	all, err := e.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("Recent(0) returned %d memories, want all 4", len(all))
	}
	for i, m := range all {
		if m.ID != ids[3-i] {
			t.Errorf("Recent(0)[%d] = %s, want %s (DESC order)", i, m.ID, ids[3-i])
		}
	}
}

// Fix 4: Record must use setdefault semantics — caller-supplied agent/action
// keys in metadata win.
func TestEpisodicRecordMetadataSetdefault(t *testing.T) {
	e := newTestEpisodic(t)
	m, err := e.Record("real-agent", "real-action", "", "", nil,
		map[string]any{"agent": "caller-agent", "action": "caller-action"})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.MetaString("agent"); got != "caller-agent" {
		t.Errorf("meta agent = %q, want caller-supplied %q", got, "caller-agent")
	}
	if got := m.MetaString("action"); got != "caller-action" {
		t.Errorf("meta action = %q, want caller-supplied %q", got, "caller-action")
	}

	m2, err := e.Record("real-agent", "real-action", "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.MetaString("agent"); got != "real-agent" {
		t.Errorf("meta agent = %q, want defaulted %q", got, "real-agent")
	}
	if got := m2.MetaString("action"); got != "real-action" {
		t.Errorf("meta action = %q, want defaulted %q", got, "real-action")
	}
}

// Fix 5: Push must normalise nil tags/metadata to empty slice/map, like the
// other layers do (Python: “tags or []“, “metadata or {}“).
func TestWorkingPushNormalizesNil(t *testing.T) {
	w := NewWorkingMemory(10, "ws")
	m := w.Push("note", nil, nil, "src")
	if m.Tags == nil {
		t.Error("Tags should be [] after Push with nil tags, got nil")
	}
	if m.Metadata == nil {
		t.Error("Metadata should be {} after Push with nil metadata, got nil")
	}
	m2 := w.Push("note2", []string{"a"}, map[string]any{"k": "v"}, "src")
	if len(m2.Tags) != 1 || m2.Tags[0] != "a" {
		t.Errorf("Tags = %v, want [a]", m2.Tags)
	}
	if m2.Metadata["k"] != "v" {
		t.Errorf("Metadata = %v, want k=v", m2.Metadata)
	}
}

// Fix 6: PlaybookContent must match Python “name + "\n" + "\n".join(steps)“:
// empty steps keeps the trailing newline; non-empty steps have no trailing newline.
func TestPlaybookContent(t *testing.T) {
	if got := PlaybookContent("deploy", nil); got != "deploy\n" {
		t.Errorf("PlaybookContent(empty steps) = %q, want %q", got, "deploy\n")
	}
	if got := PlaybookContent("deploy", []string{}); got != "deploy\n" {
		t.Errorf("PlaybookContent([] steps) = %q, want %q", got, "deploy\n")
	}
	want := "deploy\n1. build\n2. ship"
	if got := PlaybookContent("deploy", []string{"build", "ship"}); got != want {
		t.Errorf("PlaybookContent(steps) = %q, want %q", got, want)
	}
}

// Fix 1 (layer level): an explicitly-set zero weight must be preserved; an
// unset (nil) weight defaults to 1.0.
func TestAssociativeLinkWeightSemantics(t *testing.T) {
	store := newTestStore(t)
	a := NewAssociativeMemory(store)
	putMem := func(id string) {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID = id
		m.Content = "mem " + id
		if err := store.PutMemory(m, nil); err != nil {
			t.Fatal(err)
		}
	}
	putMem("s")
	putMem("d")
	zero := 0.0
	e, err := a.Link("s", "d", "related_to", &zero, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Weight != 0 {
		t.Errorf("explicit weight 0 stored as %v, want 0", e.Weight)
	}
	e2, err := a.Link("s", "d", "related_to", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e2.Weight != 1.0 {
		t.Errorf("unset weight defaulted to %v, want 1.0", e2.Weight)
	}
}
