package operations

// RunSystem2Cycle must sweep dangling edges as the maintenance step after
// Decay and surface the count in report.Maintain.

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	_ "modernc.org/sqlite"
)

// sweepRunner is the minimal cycleRunner with SweepDanglingEdges delegated
// to a real store (the pattern Engine uses).
type sweepRunner struct {
	cycleRunner
	store storage.Store
}

func (r *sweepRunner) SweepDanglingEdges() (int, error) { return r.store.SweepDanglingEdges() }

func TestRunSystem2CycleSweepsDanglingEdges(t *testing.T) {
	emb := storage.NewHashingEmbedding(256)
	path := filepath.Join(t.TempDir(), "cycle.db")
	store, err := storage.NewStore(path, emb.Dim(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	a := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "dangling endpoint", nil)
	b := putParityMem(t, store, emb, schema.LayerSemantic, schema.TypeFact, "survivor", nil)
	if err := store.PutEdge(schema.NewEdge(a.ID, "related_to", b.ID)); err != nil {
		t.Fatal(err)
	}

	// Drop A through a side connection with the FK off, reproducing a
	// legacy pre-FK database (the public API cascades edges away).
	side, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { side.Close() })
	if _, err := side.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := side.Exec("DELETE FROM memories WHERE id = ?", a.ID); err != nil {
		t.Fatal(err)
	}

	rep, err := RunSystem2Cycle(&sweepRunner{cycleRunner{episodes: 5}, store}, "test")
	if err != nil {
		t.Fatal(err)
	}

	edges, err := store.Neighbors(b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("Neighbors(b) after cycle = %v, want empty (dangling edge swept)", edges)
	}
	m, ok := rep.Maintain.(map[string]any)
	if !ok {
		t.Fatalf("report.Maintain = %#v, want map", rep.Maintain)
	}
	if m["swept"] != 1 {
		t.Errorf("report.Maintain[swept] = %#v, want 1", m["swept"])
	}
}
