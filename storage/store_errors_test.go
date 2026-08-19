package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/secrets"
)

// insertParentMemories adds minimal memory rows so edge/symbol inserts
// satisfy the foreign-key constraints.
func insertParentMemories(t *testing.T, s *SQLiteStore, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := s.db.Exec(
			`INSERT INTO memories (id, layer, type, content, created_at, updated_at, last_access_at)
			 VALUES (?, 'L1_episodic', 'event', 'x', 0, 0, 0)`, id); err != nil {
			t.Fatal(err)
		}
	}
}

// closedDBStore returns a store whose underlying DB connection is closed, so
// every query/exec hits the driver's "database is closed" error path.
func closedDBStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := openTestStore(t)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreErrorsOnClosedDB(t *testing.T) {
	s := closedDBStore(t)
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	e := schema.NewEdge("a", "rel", "b")

	checks := map[string]func() error{
		"PutMemory":     func() error { return s.PutMemory(m, nil) },
		"DeleteMemory":  func() error { return s.DeleteMemory("x") },
		"TouchMemory":   func() error { return s.TouchMemory("x", 1) },
		"PutEdge":       func() error { return s.PutEdge(e) },
		"PutCodeSymbol": func() error { return s.PutCodeSymbol(&schema.CodeSymbol{MemoryID: "m"}) },
		"PutCodeRefs":   func() error { return s.PutCodeRefs([]*schema.CodeRef{{SrcSymbol: "a"}}) },
		"SetIndexed":    func() error { return s.SetIndexed("f", "h", 1) },
		"SetMeta":       func() error { return s.SetMeta("k", "v") },
	}
	for name, fn := range checks {
		if err := fn(); err == nil {
			t.Errorf("%s on closed DB: expected error", name)
		}
	}

	errChecks := map[string]func() error{
		"GetMemory":      func() error { _, err := s.GetMemory("x"); return err },
		"IterMemories":   func() error { _, err := s.IterMemories("", "", ""); return err },
		"FindByHash":     func() error { _, err := s.FindByHash("h", ""); return err },
		"Episodic":       func() error { _, err := s.EpisodicContentsSince("w", 0); return err },
		"Count":          func() error { _, err := s.Count(""); return err },
		"Neighbors":      func() error { _, err := s.Neighbors("x", ""); return err },
		"CountEdges":     func() error { _, err := s.CountEdges(); return err },
		"NeighborCounts": func() error { _, err := s.NeighborCounts(); return err },
		"SymbolsForFile": func() error { _, err := s.SymbolsForFile("f"); return err },
		"RefsOut":        func() error { _, err := s.RefsForSymbol("s", "out"); return err },
		"RefsIn":         func() error { _, err := s.RefsForSymbol("s", "in"); return err },
		"RefsBoth":       func() error { _, err := s.RefsForSymbol("s", "both"); return err },
		"GetIndexedHash": func() error { _, err := s.GetIndexedHash("f"); return err },
		"Workspaces":     func() error { _, err := s.Workspaces(); return err },
		"GetMeta":        func() error { _, err := s.GetMeta("k"); return err },
	}
	for name, fn := range errChecks {
		if err := fn(); err == nil {
			t.Errorf("%s on closed DB: expected error", name)
		}
	}

	// warmIndexFromBlobs swallows query errors.
	s.warmIndexFromBlobs()
}

func TestScanMemoryTypeMismatch(t *testing.T) {
	s := openTestStore(t)
	// created_at is REAL; store a non-numeric string so scanning into float64
	// fails.
	if _, err := s.db.Exec(
		`INSERT INTO memories (id, layer, type, content, created_at, updated_at, last_access_at)
		 VALUES ('bad', 'L1_episodic', 'event', 'x', 'notanum', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMemory("bad"); err == nil {
		t.Error("GetMemory: expected scan error")
	}
	if _, err := s.IterMemories("", "", ""); err == nil {
		t.Error("IterMemories: expected scan error")
	}
	if _, err := s.FindByHash("", ""); err == nil {
		t.Error("FindByHash: expected scan error")
	}
}

func TestScanEdgeTypeMismatch(t *testing.T) {
	s := openTestStore(t)
	insertParentMemories(t, s, "a", "b")
	if _, err := s.db.Exec(
		`INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from)
		 VALUES ('e1', 'a', 'rel', 'b', 'notanum', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Neighbors("a", ""); err == nil {
		t.Error("Neighbors: expected scan error")
	}
}

func TestScanEdgeNilMetaAndValidTo(t *testing.T) {
	s := openTestStore(t)
	insertParentMemories(t, s, "a", "b")
	vto := 42.0
	e := schema.NewEdge("a", "rel", "b")
	e.ValidTo = &vto
	e.Metadata = nil // marshals to "null" → scanEdge normalises to {}
	if err := s.PutEdge(e); err != nil {
		t.Fatal(err)
	}
	// Neighbors filters valid_to IS NULL, so query the row directly to reach
	// scanEdge's non-nil valid_to branch.
	rows, err := s.db.Query("SELECT "+edgeCols+" FROM edges WHERE id = ?", e.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("edge row missing")
	}
	got, err := s.scanEdge(rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.ValidTo == nil || *got.ValidTo != 42.0 {
		t.Errorf("ValidTo = %v, want 42", got.ValidTo)
	}
	if got.Metadata == nil || len(got.Metadata) != 0 {
		t.Errorf("Metadata = %v, want empty non-nil", got.Metadata)
	}
}

func TestSymbolsForFileTypeMismatch(t *testing.T) {
	s := openTestStore(t)
	insertParentMemories(t, s, "m")
	if _, err := s.db.Exec(
		`INSERT INTO code_symbols (memory_id, file_path, symbol_kind, qualified_name, line_start)
		 VALUES ('m', 'f.go', 'function', 'pkg.F', 'notanum')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SymbolsForFile("f.go"); err == nil {
		t.Error("SymbolsForFile: expected scan error")
	}
}

func TestNewStoreMkdirError(t *testing.T) {
	// Parent path is a regular file, so MkdirAll fails.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(filepath.Join(f, "x.db"), 8, false, false); err == nil {
		t.Error("expected MkdirAll error")
	}
}

func TestNewStoreAddsEmbeddingColumnToLegacyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	s, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-migration database (no embedding column).
	if _, err := s.db.Exec("ALTER TABLE memories DROP COLUMN embedding"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStore(dbPath, 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// The migration re-added the column: PutMemory with a vector works.
	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	if err := s2.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if s2.vectorIndex.Len() != 1 {
		t.Errorf("index len = %d, want 1", s2.vectorIndex.Len())
	}
}

func TestHashingEmbedEmptyText(t *testing.T) {
	// No features → all-zero vector → norm falls back to 1.0.
	vec, err := NewHashingEmbedding(8).Embed("")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 8 {
		t.Fatalf("len = %d, want 8", len(vec))
	}
	for i, v := range vec {
		if v != 0 {
			t.Errorf("vec[%d] = %v, want 0", i, v)
		}
	}
}

func TestResolveEmbeddingKeyFromSecretStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := secrets.NewStore("")
	if _, err := store.SetMasterKey("test-master-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("LADYM_TEST_STORED_KEY", "from-store"); err != nil {
		t.Fatal(err)
	}
	// Fresh Store instance so the cache doesn't shortcut the lookup.
	t.Setenv("LADYM_TEST_STORED_KEY", "from-env")
	v, err := resolveEmbeddingKey(&config.Config{EmbeddingAPIKeyEnv: "LADYM_TEST_STORED_KEY"})
	if err != nil {
		t.Fatal(err)
	}
	if v != "from-store" {
		t.Errorf("resolveEmbeddingKey = %q, want from-store", v)
	}
}

func TestResolveEmbeddingKeyStoreError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A malformed entry (no nonce:ciphertext shape) makes Get fail.
	dir := filepath.Join(home, ".ladyM")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets.enc"), []byte("LADYM_TEST_BAD_KEY=garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveEmbeddingKey(&config.Config{EmbeddingAPIKeyEnv: "LADYM_TEST_BAD_KEY"}); err == nil {
		t.Error("expected secret-store error")
	}

	// MakeProvider surfaces the same error for the openai provider.
	_, err := MakeProvider(&config.Config{
		EmbeddingProvider:  "openai",
		EmbeddingAPIKeyEnv: "LADYM_TEST_BAD_KEY",
		EmbeddingDim:       8,
	})
	if err == nil {
		t.Error("expected MakeProvider to surface the secret-store error")
	}
}

func TestHTTPEmbeddingBadVectorShape(t *testing.T) {
	h := NewHTTPEmbedding(HTTPEmbeddingOptions{
		BaseURL:      "http://x",
		Request:      `{"input": "{text}"}`,
		ResponsePath: "embedding",
		Dim:          2,
		Poster: &FakeHTTPPoster{Responder: func(payload any) (any, error) {
			return map[string]any{"embedding": "not-an-array"}, nil
		}},
	})
	if _, err := h.Embed("hi"); err == nil {
		t.Error("expected toFloat32Slice error")
	}
}

func TestCachedEmbeddingTouchMissing(t *testing.T) {
	// Defensive branch: touching a key that is not in the index is a no-op.
	c := NewCachedEmbedding(&stubProvider{dim: 1}, 2)
	c.touch("missing")
	if len(c.order) != 0 {
		t.Errorf("order = %v, want empty", c.order)
	}
}
