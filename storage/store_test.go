//go:build !enterprise

package storage

import (
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "test.db"), 8, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestBusyTimeoutPragma(t *testing.T) {
	// Contending writers from other processes must wait instead of failing
	// immediately with "database is locked".
	s := openTestStore(t)
	var ms int
	if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&ms); err != nil {
		t.Fatal(err)
	}
	if ms != 10000 {
		t.Errorf("busy_timeout = %d, want 10000", ms)
	}
}

func (s *SQLiteStore) countCodeRefs(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM code_refs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPutCodeRefsSuccess(t *testing.T) {
	s := openTestStore(t)
	refs := []*schema.CodeRef{
		{SrcSymbol: "a.f", DstSymbol: "b.g", RefKind: "calls"},
		{SrcSymbol: "a.f", DstSymbol: "c.h", RefKind: "imports"},
	}
	if err := s.PutCodeRefs(refs); err != nil {
		t.Fatal(err)
	}
	if n := s.countCodeRefs(t); n != 2 {
		t.Errorf("code_refs count = %d, want 2", n)
	}
}

func TestPutCodeRefsAtomicRollback(t *testing.T) {
	s := openTestStore(t)
	// Force the second insert to fail via a uniqueness constraint.
	if _, err := s.db.Exec(
		"CREATE UNIQUE INDEX uniq_ref ON code_refs(src_symbol, dst_symbol, ref_kind)"); err != nil {
		t.Fatal(err)
	}
	refs := []*schema.CodeRef{
		{SrcSymbol: "a.f", DstSymbol: "b.g", RefKind: "calls"},
		{SrcSymbol: "a.f", DstSymbol: "b.g", RefKind: "calls"}, // duplicate → violates unique index
	}
	if err := s.PutCodeRefs(refs); err == nil {
		t.Fatal("expected error from duplicate ref")
	}
	// Atomic semantics (Python executemany single commit): no partial writes.
	if n := s.countCodeRefs(t); n != 0 {
		t.Errorf("code_refs count = %d after failed batch, want 0 (rolled back)", n)
	}
}
