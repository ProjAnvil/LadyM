//go:build !enterprise

package storage

// NeighborCounts must join edge endpoints against memories: a dangling edge
// (one endpoint memory deleted) must not contribute to centrality counts,
// while valid edges still do. Runs against both backends.
//
// NOTE on setup: both schemas declare ON DELETE CASCADE foreign keys on
// edges, so the public DeleteMemory cascades edges away and cannot produce a
// dangling edge. Dangling edges occur in legacy databases created before the
// FK existed (CREATE TABLE IF NOT EXISTS never upgrades them), so these tests
// bypass the FK on purpose to reproduce that state.

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

// deleteMemoryNoCascade removes a memory row without the FK cascade, the way
// a legacy pre-FK database behaves. It simulates the state the dangling-edge
// correctness fixes defend against. The FK is always restored (deferred), and
// the SQLite PRAGMA toggle runs outside any transaction (single Exec =
// autocommit), as connection-scoped PRAGmas require.
func deleteMemoryNoCascade(t *testing.T, s Store, id string) {
	t.Helper()
	switch st := s.(type) {
	case *SQLiteStore:
		if _, err := st.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := st.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
				t.Errorf("restore PRAGMA foreign_keys = ON: %v", err)
			}
		}()
		if _, err := st.db.Exec("DELETE FROM memories WHERE id = ?", id); err != nil {
			t.Fatal(err)
		}
	case *PostgresStore:
		ctx := t.Context()
		if _, err := st.pool.Exec(ctx, "ALTER TABLE memories DISABLE TRIGGER ALL"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := st.pool.Exec(ctx, "ALTER TABLE memories ENABLE TRIGGER ALL"); err != nil {
				t.Errorf("re-enable triggers on memories: %v", err)
			}
		}()
		if _, err := st.pool.Exec(ctx, "DELETE FROM memories WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown store type %T", s)
	}
}

func TestNeighborCountsExcludesDanglingEdges(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		a := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		b := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		suitePutMemory(t, s, a, nil)
		suitePutMemory(t, s, b, nil)
		if err := s.PutEdge(schema.NewEdge(a.ID, "related_to", b.ID)); err != nil {
			t.Fatal(err)
		}

		// Dropping A without the cascade leaves the A-B edge dangling.
		deleteMemoryNoCascade(t, s, a.ID)

		counts, err := s.NeighborCounts()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := counts[a.ID]; ok {
			t.Errorf("NeighborCounts contains deleted endpoint %s: %v", a.ID, counts)
		}
		if counts[b.ID] != 0 {
			t.Errorf("NeighborCounts[%s] = %d, want 0 (dangling edge must not count)", b.ID, counts[b.ID])
		}

		// A valid edge still counts for both endpoints.
		c := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		suitePutMemory(t, s, c, nil)
		if err := s.PutEdge(schema.NewEdge(b.ID, "related_to", c.ID)); err != nil {
			t.Fatal(err)
		}
		counts, err = s.NeighborCounts()
		if err != nil {
			t.Fatal(err)
		}
		if counts[b.ID] != 1 || counts[c.ID] != 1 {
			t.Errorf("NeighborCounts after valid edge = %v, want b=1 c=1", counts)
		}
	})
}
