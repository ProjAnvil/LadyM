//go:build !enterprise

package operations

// RunSystem2Cycle ends with the full MaintainL4 step (GC + backfill +
// linking + alias merge) after Decay, and reports a *MaintainReport. The
// cycle order is Consolidate -> Proceduralize -> L5/L6 (gated) -> Decay ->
// Maintain.

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
	_ "modernc.org/sqlite"
)

// maintainRunner is the minimal cycleRunner with MaintainL4 delegated to a
// real store (the pattern Engine uses).
type maintainRunner struct {
	cycleRunner
	store storage.Store
	emb   *storage.HashingEmbedding
	cfg   *config.Config
}

func (r *maintainRunner) MaintainL4(workspace string) (*MaintainReport, error) {
	return MaintainL4(r.store, r.emb, r.cfg, workspace, nil)
}

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
	// legacy pre-FK database (the public API cascades edges away). One
	// pinned *sql.Conn keeps the connection-scoped PRAGMA and the DELETE on
	// the same session; the PRAGMA is restored (deferred) and toggled
	// outside any transaction (single Exec = autocommit).
	side, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { side.Close() })
	conn, err := side.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(t.Context(), "PRAGMA foreign_keys = ON"); err != nil {
			t.Errorf("restore PRAGMA foreign_keys = ON: %v", err)
		}
	}()
	if _, err := conn.ExecContext(t.Context(), "DELETE FROM memories WHERE id = ?", a.ID); err != nil {
		t.Fatal(err)
	}

	rep, err := RunSystem2Cycle(&maintainRunner{cycleRunner{episodes: 5}, store, emb, config.Default()}, "test")
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
	m, ok := rep.Maintain.(*MaintainReport)
	if !ok {
		t.Fatalf("report.Maintain = %#v, want *MaintainReport", rep.Maintain)
	}
	if m.Swept != 1 {
		t.Errorf("report.Maintain.Swept = %d, want 1", m.Swept)
	}
}

// orderRunner records the call sequence of one cycle.
type orderRunner struct {
	order    []string
	episodes int
	fail     string
}

func (r *orderRunner) Consolidate(workspace string, since float64) (*ConsolidationReport, error) {
	r.order = append(r.order, "consolidate")
	if r.fail == "consolidate" {
		return nil, errBoom
	}
	return newConsolidationReport(), nil
}
func (r *orderRunner) Proceduralize(workspace string, minClusterSize int) (*ProceduralizeReport, error) {
	r.order = append(r.order, "proceduralize")
	if r.fail == "proceduralize" {
		return nil, errBoom
	}
	return newProceduralizeReport(), nil
}
func (r *orderRunner) ExtractMentalModels(workspace string) (*L5ExtractionReport, error) {
	r.order = append(r.order, "l5")
	if r.fail == "l5" {
		return nil, errBoom
	}
	return &L5ExtractionReport{}, nil
}
func (r *orderRunner) PredictForwardIntents(workspace string) (*L6PredictionReport, error) {
	r.order = append(r.order, "l6")
	if r.fail == "l6" {
		return nil, errBoom
	}
	return &L6PredictionReport{}, nil
}
func (r *orderRunner) Decay(workspace string, dryRun bool, maxAgeS, activationFloor float64) (*DecayReport, error) {
	r.order = append(r.order, "decay")
	if r.fail == "decay" {
		return nil, errBoom
	}
	return &DecayReport{}, nil
}
func (r *orderRunner) CountRecentEpisodes(workspace string) (int, error) {
	return r.episodes, nil
}
func (r *orderRunner) MinEpisodesToRun() int { return 3 }
func (r *orderRunner) MaintainL4(workspace string) (*MaintainReport, error) {
	r.order = append(r.order, "maintain")
	if r.fail == "maintain" {
		return nil, errBoom
	}
	return &MaintainReport{}, nil
}

func TestRunSystem2CycleOrder(t *testing.T) {
	r := &orderRunner{episodes: 5}
	rep, err := RunSystem2Cycle(r, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"consolidate", "proceduralize", "l5", "l6", "decay", "maintain"}
	if len(r.order) != len(want) {
		t.Fatalf("cycle order = %v, want %v", r.order, want)
	}
	for i := range want {
		if r.order[i] != want[i] {
			t.Errorf("cycle order = %v, want %v", r.order, want)
			break
		}
	}
	if rep.Maintain == nil {
		t.Error("report.Maintain = nil, want the MaintainL4 report")
	}
}

func TestRunSystem2CycleOrderSkippedLLM(t *testing.T) {
	r := &orderRunner{episodes: 1}
	rep, err := RunSystem2Cycle(r, "test")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"consolidate", "proceduralize", "decay", "maintain"}
	if len(r.order) != len(want) {
		t.Fatalf("cycle order = %v, want %v (LLM steps gated off)", r.order, want)
	}
	for i := range want {
		if r.order[i] != want[i] {
			t.Errorf("cycle order = %v, want %v", r.order, want)
			break
		}
	}
	if !rep.SkippedLLMSteps {
		t.Error("SkippedLLMSteps = false, want true")
	}
}

func TestRunSystem2CycleMaintainErrorPropagates(t *testing.T) {
	if _, err := RunSystem2Cycle(&orderRunner{episodes: 5, fail: "maintain"}, "test"); err == nil {
		t.Error("maintain failure should propagate")
	}
}
