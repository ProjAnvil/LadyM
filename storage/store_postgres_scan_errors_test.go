package storage

// Postgres scan-error and schema-setup failure paths. PG's typed columns
// reject wrong-type inserts, so scan errors are induced by relaxing the
// schema behind the store's back (DROP NOT NULL / ALTER COLUMN TYPE) and
// planting rows that no longer scan into the Go types. Schema-setup errors
// come from a conflicting pre-existing table and from a closed pool.

import (
	"os"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/jackc/pgx/v5"
)

// execPG runs a raw statement against the store's pool.
func execPG(t *testing.T, s *PostgresStore, stmt string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(t.Context(), stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

// TestPostgresMemoryScanErrorAfterColumnRetype: created_at retyped to text
// no longer scans into float64 — GetMemory and IterMemories must surface it.
func TestPostgresMemoryScanErrorAfterColumnRetype(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	if err := s.PutMemory(pgMem("m1"), nil); err != nil {
		t.Fatal(err)
	}
	execPG(t, s, "ALTER TABLE memories ALTER COLUMN created_at TYPE text USING created_at::text")

	if _, err := s.GetMemory("m1"); err == nil {
		t.Error("GetMemory: expected scan error after created_at retype")
	}
	if _, err := s.IterMemories("", "", ""); err == nil {
		t.Error("IterMemories: expected scan error after created_at retype")
	}
	if _, err := s.FindByHash("", ""); err == nil {
		t.Error("FindByHash: expected scan error after created_at retype")
	}
}

// TestPostgresScanErrorsFromNullMemoryColumns: NULLs planted after dropping
// NOT NULL constraints break the scans of EpisodicContentsSince, Count and
// Workspaces.
func TestPostgresScanErrorsFromNullMemoryColumns(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	execPG(t, s, "ALTER TABLE memories ALTER COLUMN content DROP NOT NULL")
	execPG(t, s, "ALTER TABLE memories ALTER COLUMN layer DROP NOT NULL")
	execPG(t, s, "ALTER TABLE memories ALTER COLUMN workspace DROP NOT NULL")
	// One planted row per failing scan: EpisodicContentsSince filters on
	// layer+workspace, so the NULL-content row must match those.
	execPG(t, s, `INSERT INTO memories (id, layer, type, content, workspace,
		created_at, updated_at, last_access_at) VALUES
		('e1', 'L1_episodic', 'event', NULL, 'w', 1, 0, 0),
		('e2', NULL, 'event', 'x', 'w', 1, 0, 0),
		('e3', 'L1_episodic', 'event', 'x', NULL, 1, 0, 0)`)

	if _, err := s.EpisodicContentsSince("w", 0); err == nil {
		t.Error("EpisodicContentsSince: expected scan error for NULL content")
	}
	if _, err := s.Count(""); err == nil {
		t.Error("Count: expected scan error for NULL layer")
	}
	if _, err := s.Workspaces(); err == nil {
		t.Error("Workspaces: expected scan error for NULL workspace")
	}
}

// TestPostgresEdgeScanErrors: a NULL weight fails scanEdgePG outright; a
// JSONB null metadata decodes to a nil map and is normalised to {}.
func TestPostgresEdgeScanErrors(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	for _, id := range []string{"a", "b"} {
		if err := s.PutMemory(pgMem(id), nil); err != nil {
			t.Fatal(err)
		}
	}
	execPG(t, s, "ALTER TABLE edges ALTER COLUMN weight DROP NOT NULL")
	execPG(t, s, `INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from)
		VALUES ('e-null-weight', 'a', 'rel', 'b', NULL, 0)`)
	if _, err := s.Neighbors("a", ""); err == nil {
		t.Error("Neighbors: expected scan error for NULL weight")
	}
	execPG(t, s, "DELETE FROM edges WHERE id = 'e-null-weight'")

	execPG(t, s, `INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from, metadata)
		VALUES ('e-null-meta', 'a', 'rel', 'b', 1, 0, 'null'::jsonb)`)
	edges, err := s.Neighbors("a", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Metadata == nil || len(edges[0].Metadata) != 0 {
		t.Errorf("Neighbors with jsonb null metadata = %+v, want one edge with empty non-nil metadata", edges)
	}
}

// TestPostgresNeighborCountsNullEndpoint: a NULL src_id (FKs pass on NULL)
// fails the UNION ALL scan.
func TestPostgresNeighborCountsNullEndpoint(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	if err := s.PutMemory(pgMem("b"), nil); err != nil {
		t.Fatal(err)
	}
	execPG(t, s, "ALTER TABLE edges ALTER COLUMN src_id DROP NOT NULL")
	execPG(t, s, `INSERT INTO edges (id, src_id, relation, dst_id, weight, valid_from)
		VALUES ('e1', NULL, 'rel', 'b', 1, 0)`)
	if _, err := s.NeighborCounts(); err == nil {
		t.Error("NeighborCounts: expected scan error for NULL src_id")
	}
}

// TestPostgresSymbolsForFileNullLineStart: a NULL line_start fails the
// code-symbol row scan.
func TestPostgresSymbolsForFileNullLineStart(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	if err := s.PutMemory(pgMem("m"), nil); err != nil {
		t.Fatal(err)
	}
	execPG(t, s, "ALTER TABLE code_symbols ALTER COLUMN line_start DROP NOT NULL")
	execPG(t, s, `INSERT INTO code_symbols (memory_id, file_path, symbol_kind, qualified_name, line_start)
		VALUES ('m', 'f.go', 'function', 'pkg.F', NULL)`)
	if _, err := s.SymbolsForFile("f.go"); err == nil {
		t.Error("SymbolsForFile: expected scan error for NULL line_start")
	}
}

// TestPostgresRefsForSymbolNullEndpoint: NULL endpoints in code_refs fail
// the scan on both directions.
func TestPostgresRefsForSymbolNullEndpoint(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	execPG(t, s, "ALTER TABLE code_refs ALTER COLUMN src_symbol DROP NOT NULL")
	execPG(t, s, "ALTER TABLE code_refs ALTER COLUMN dst_symbol DROP NOT NULL")
	execPG(t, s, `INSERT INTO code_refs (src_symbol, dst_symbol, ref_kind) VALUES
		('a', NULL, 'calls'), (NULL, 'b', 'calls')`)

	if _, err := s.RefsForSymbol("a", "out"); err == nil {
		t.Error("RefsForSymbol(out): expected scan error for NULL dst_symbol")
	}
	if _, err := s.RefsForSymbol("b", "in"); err == nil {
		t.Error("RefsForSymbol(in): expected scan error for NULL src_symbol")
	}
}

// TestPostgresUserScanNullCreatedAt: a NULL created_at fails the user row
// scan in both GetUser and ListUsers.
func TestPostgresUserScanNullCreatedAt(t *testing.T) {
	s := newPGStoreOrSkip(t, suiteDim)
	execPG(t, s, "ALTER TABLE users ALTER COLUMN created_at DROP NOT NULL")
	execPG(t, s, `INSERT INTO users (username, password_hash, workspace, admin, created_at)
		VALUES ('u', 'h', 'w', false, NULL)`)

	if _, err := s.GetUser("u"); err == nil {
		t.Error("GetUser: expected scan error for NULL created_at")
	}
	if _, err := s.ListUsers(); err == nil {
		t.Error("ListUsers: expected scan error for NULL created_at")
	}
}

// TestPostgresPutCodeRefsOnClosedPool: the transaction begin fails on a
// closed pool and the error is surfaced (no panic, no silent no-op).
func TestPostgresPutCodeRefsOnClosedPool(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	s, err := NewPostgresStore(freshPGDatabase(t, dsn), suiteDim)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	err = s.PutCodeRefs([]*schema.CodeRef{{SrcSymbol: "a", DstSymbol: "b", RefKind: "calls"}})
	if err == nil {
		t.Error("PutCodeRefs on a closed pool should fail at Begin")
	}
}

// TestNewPostgresStoreSchemaConflict: a pre-existing memories table with an
// incompatible shape makes the idempotent schema setup fail (the indexes
// reference columns the table does not have), and NewPostgresStore surfaces
// the wrapped error.
func TestNewPostgresStoreSchemaConflict(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	testDSN := freshPGDatabase(t, dsn)
	cfg, err := pgx.ParseConfig(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(t.Context(), "CREATE TABLE memories (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	conn.Close(t.Context())

	_, err = NewPostgresStore(testDSN, suiteDim)
	if err == nil || !strings.Contains(err.Error(), "postgres schema setup failed") {
		t.Fatalf("err = %v, want wrapped schema-setup failure", err)
	}
}

// TestApplyPGSchemaOnClosedPool: schema setup on a closed pool fails at
// connection acquisition with the wrapped message.
func TestApplyPGSchemaOnClosedPool(t *testing.T) {
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	s, err := NewPostgresStore(freshPGDatabase(t, dsn), suiteDim)
	if err != nil {
		t.Fatal(err)
	}
	s.pool.Close()
	if err := applyPGSchema(t.Context(), s.pool, suiteDim); err == nil ||
		!strings.Contains(err.Error(), "postgres schema setup failed") {
		t.Errorf("applyPGSchema on closed pool = %v, want wrapped failure", err)
	}
}
