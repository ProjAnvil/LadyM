package storage

// Postgres-only error-path and dim-rebuild tests (no sqlite involvement, so
// no build tag: they run in both editions when LADYM_TEST_PG_DSN is set, and
// skip otherwise). Pattern mirrors the SQLite-side store_errors_test.go:
// break the schema behind the store's back (DROP TABLE), then assert every
// query surfaces the error instead of a silent nil.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

// newPGStoreOrSkip returns a PostgresStore on a fresh per-test database, or
// skips when LADYM_TEST_PG_DSN is unset.
func newPGStoreOrSkip(t *testing.T, dim int) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	s, err := NewPostgresStore(freshPGDatabase(t, dsn), dim)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func pgMem(id string) *schema.Memory {
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.ID = id
	m.Content = "content of " + id
	m.Summary = "summary of " + id
	m.Tags = []string{}
	m.Metadata = map[string]any{}
	return m
}

// A DSN that fails to parse is an immediate error; a parseable but
// unreachable server fails at ping with the wrapped connect message.
func TestNewPostgresStoreConnectErrors(t *testing.T) {
	if _, err := NewPostgresStore("postgres://%", suiteDim); err == nil {
		t.Error("NewPostgresStore with malformed DSN should fail at ParseConfig")
	}
	dsn := "postgres://postgres:ladym@127.0.0.1:1/ladym?sslmode=disable&connect_timeout=1"
	_, err := NewPostgresStore(dsn, suiteDim)
	if err == nil {
		t.Fatal("NewPostgresStore to a closed port should fail")
	}
	if !strings.Contains(err.Error(), "cannot connect to postgres store") {
		t.Errorf("unreachable-server error = %v, want the wrapped connect message", err)
	}
}

// RebuildVectorIndex at a new dim: drops the HNSW index, NULLs every stored
// embedding and retypes the column — verified by inserting/searching at the
// new dim afterwards (an 8-dim insert must now be rejected by the column).
func TestPostgresRebuildVectorIndex(t *testing.T) {
	s := newPGStoreOrSkip(t, 8)
	vec8 := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	if err := s.PutMemory(pgMem("m1"), vec8); err != nil {
		t.Fatal(err)
	}
	if hits := s.VectorSearch(vec8, 5); len(hits) != 1 || hits[0].ID != "m1" {
		t.Fatalf("pre-rebuild VectorSearch = %v, want m1", hits)
	}

	s.RebuildVectorIndex(4)
	if s.dim != 4 {
		t.Errorf("dim after rebuild = %d, want 4", s.dim)
	}
	var n int
	if err := s.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM memories WHERE embedding IS NOT NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("embeddings after rebuild = %d non-NULL, want 0 (index reset nulls them)", n)
	}

	// The column is vector(4) now: a 4-dim insert + search roundtrip works...
	vec4 := []float32{1, 0, 0, 0}
	if err := s.PutMemory(pgMem("m2"), vec4); err != nil {
		t.Fatalf("4-dim PutMemory after rebuild: %v", err)
	}
	if hits := s.VectorSearch(vec4, 5); len(hits) != 1 || hits[0].ID != "m2" {
		t.Errorf("post-rebuild VectorSearch = %v, want m2", hits)
	}
	// ...and the old 8-dim shape is rejected by the retyped column.
	if err := s.PutMemory(pgMem("m3"), vec8); err == nil {
		t.Error("8-dim PutMemory after rebuild to dim 4 should fail (column is vector(4))")
	}
}

// RebuildVectorIndex swallows failures into a stderr WARNING (the interface
// returns no error): with the memories table dropped the rebuild must leave
// the dim unchanged and the rest of the store must be untouched.
func TestPostgresRebuildVectorIndexFailureKeepsDim(t *testing.T) {
	s := newPGStoreOrSkip(t, 8)
	if _, err := s.pool.Exec(context.Background(), "DROP TABLE memories CASCADE"); err != nil {
		t.Fatal(err)
	}
	s.RebuildVectorIndex(4)
	if s.dim != 8 {
		t.Errorf("dim after failed rebuild = %d, want 8 (unchanged)", s.dim)
	}
}

// On a closed pool even the transaction begin fails; the rebuild warns and
// leaves the dim alone.
func TestPostgresRebuildVectorIndexOnClosedPool(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	s, err := NewPostgresStore(freshPGDatabase(t, dsn), suiteDim)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s.RebuildVectorIndex(4)
	if s.dim != suiteDim {
		t.Errorf("dim after rebuild on closed pool = %d, want %d", s.dim, suiteDim)
	}
}

// The pg advisory index lock: a second acquisition while held fails fast with
// ErrIndexLockHeld; after release it can be taken again.
func TestPostgresIndexLockContention(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	release, err := s.TryAcquireIndexLock()
	if err != nil {
		t.Fatalf("first TryAcquireIndexLock: %v", err)
	}
	if _, err := s.TryAcquireIndexLock(); !errors.Is(err, ErrIndexLockHeld) {
		t.Errorf("second TryAcquireIndexLock = %v, want ErrIndexLockHeld", err)
	}
	release()
	release2, err := s.TryAcquireIndexLock()
	if err != nil {
		t.Fatalf("TryAcquireIndexLock after release: %v", err)
	}
	release2()
}

// Acquiring the index lock on a closed pool surfaces the acquire error.
func TestPostgresIndexLockAfterClose(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	s, err := NewPostgresStore(freshPGDatabase(t, dsn), suiteDim)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := s.TryAcquireIndexLock(); err == nil {
		t.Error("TryAcquireIndexLock on a closed pool should fail")
	}
}

// Zero-norm vectors bypass the ANN path (pgvector cosine is NaN for them): a
// zero-norm query takes the id-ordered scan, and a stored zero-norm vector is
// appended via the UNION branch with a literal 0 similarity.
func TestPostgresVectorSearchZeroNorm(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	hot := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	zero := make([]float32, suiteDim)
	if err := s.PutMemory(pgMem("b-hot"), hot); err != nil {
		t.Fatal(err)
	}
	if err := s.PutMemory(pgMem("a-zero"), zero); err != nil {
		t.Fatal(err)
	}

	// Zero-norm query: id-ordered scan (COLLATE "C"), similarity 0.
	hits := s.VectorSearch(zero, 5)
	if len(hits) != 2 || hits[0].ID != "a-zero" || hits[1].ID != "b-hot" {
		t.Fatalf("zero-norm query hits = %v, want [a-zero b-hot] (id order)", hits)
	}
	for _, h := range hits {
		if h.Similarity != 0 {
			t.Errorf("zero-norm query hit %s similarity = %v, want 0", h.ID, h.Similarity)
		}
	}

	// Non-zero query: the hot vector scores ~1; the stored zero-norm vector
	// joins via UNION ALL with similarity exactly 0, ranked last.
	hits = s.VectorSearch(hot, 5)
	if len(hits) != 2 || hits[0].ID != "b-hot" || hits[1].ID != "a-zero" {
		t.Fatalf("hot query hits = %v, want [b-hot a-zero]", hits)
	}
	if hits[0].Similarity < 0.99 || hits[1].Similarity != 0 {
		t.Errorf("hot query similarities = %v / %v, want ~1 / 0", hits[0].Similarity, hits[1].Similarity)
	}

	// topK <= 0 is a nil-return no-op.
	if hits := s.VectorSearch(hot, 0); hits != nil {
		t.Errorf("VectorSearch(topK=0) = %v, want nil", hits)
	}
}

// DROP TABLE behind the store's back: every method touching the dropped
// tables must return the SQL error (VectorSearch degrades to nil by design).
func TestPostgresStoreErrorsAfterDropTables(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	ctx := context.Background()
	for _, table := range []string{"code_refs", "code_symbols", "edges", "users", "index_state", "meta", "memories"} {
		if _, err := s.pool.Exec(ctx, "DROP TABLE "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	memErrs := map[string]error{}
	var mem *schema.Memory
	mem, memErrs["GetMemory"] = s.GetMemory("x")
	_ = mem
	var mems []*schema.Memory
	mems, memErrs["IterMemories"] = s.IterMemories("", "", "")
	_ = mems
	mem, memErrs["FindByHash"] = s.FindByHash("h", "")
	_ = mem
	_, memErrs["EpisodicContentsSince"] = s.EpisodicContentsSince("", 0)
	_, memErrs["Count"] = s.Count("")
	_, memErrs["Workspaces"] = s.Workspaces()
	memErrs["PutMemory"] = s.PutMemory(pgMem("x"), nil)
	memErrs["DeleteMemory"] = s.DeleteMemory("x")
	memErrs["UpdateMemoryContent"] = s.UpdateMemoryContent("x", "c", "s", nil, nil, 0)
	memErrs["TouchMemories"] = s.TouchMemories([]string{"x"}, 0)
	_, memErrs["Neighbors"] = s.Neighbors("x", "")
	_, memErrs["NeighborCounts"] = s.NeighborCounts()
	_, memErrs["CountEdges"] = s.CountEdges()
	memErrs["PutEdge"] = s.PutEdge(&schema.Edge{ID: "e1", SrcID: "a", DstID: "b", Relation: "related_to"})
	memErrs["InvalidateEdge"] = s.InvalidateEdge("e1", 1)
	_, memErrs["SymbolsForFile"] = s.SymbolsForFile("f.go")
	_, memErrs["RefsForSymbol-out"] = s.RefsForSymbol("q", "out")
	_, memErrs["RefsForSymbol-in"] = s.RefsForSymbol("q", "in")
	_, memErrs["CountCodeSymbols"] = s.CountCodeSymbols()
	memErrs["PutCodeSymbol"] = s.PutCodeSymbol(&schema.CodeSymbol{MemoryID: "m", FilePath: "f.go", QualifiedName: "q"})
	memErrs["PutCodeRefs"] = s.PutCodeRefs([]*schema.CodeRef{{SrcSymbol: "a", DstSymbol: "b", RefKind: "calls"}})
	_, memErrs["GetUser"] = s.GetUser("u")
	_, memErrs["ListUsers"] = s.ListUsers()
	memErrs["PutUser"] = s.PutUser(&schema.User{Username: "u"})
	memErrs["DeleteUser"] = s.DeleteUser("u")
	_, memErrs["GetIndexedHash"] = s.GetIndexedHash("f.go")
	memErrs["SetIndexed"] = s.SetIndexed("f.go", "h", 0)
	_, memErrs["GetMeta"] = s.GetMeta("k")
	memErrs["SetMeta"] = s.SetMeta("k", "v")
	memErrs["DeleteMemoriesByTypeSource"] = s.DeleteMemoriesByTypeSource("t", "s", "")
	memErrs["DeleteSymbolMemories"] = s.DeleteSymbolMemories("q", "")

	for name, err := range memErrs {
		if err == nil {
			t.Errorf("%s after DROP TABLE should return the SQL error, got nil", name)
		}
	}

	// VectorSearch degrades to nil (documented error-free Search signature),
	// on both the ANN and the zero-norm scan paths.
	if hits := s.VectorSearch([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 5); hits != nil {
		t.Errorf("VectorSearch after DROP TABLE = %v, want nil", hits)
	}
	if hits := s.VectorSearch(make([]float32, suiteDim), 5); hits != nil {
		t.Errorf("zero-norm VectorSearch after DROP TABLE = %v, want nil", hits)
	}
}
