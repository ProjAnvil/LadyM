//go:build !enterprise

package storage

// Golden cross-backend consistency: a fixed deterministic dataset and fixed
// query vectors must produce the identical top-k (same ids, same order, and
// similarity within float tolerance) from SQLiteStore's in-memory exact scan
// and PostgresStore's pgvector index. Gated on LADYM_TEST_PG_DSN.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

const goldenDim = 8

// goldenVector deterministically derives a dim-vector from a seed
// (sine/cosine mix) so both backends index identical data.
func goldenVector(seed int) []float32 {
	v := make([]float32, goldenDim)
	for i := range v {
		v[i] = float32(math.Sin(float64(seed*31+i*7+1)) + 0.5*math.Cos(float64(seed*13-i*3+2)))
	}
	return v
}

// goldenBackends opens one store per backend. The whole test skips when
// LADYM_TEST_PG_DSN is unset (the sqlite side alone has nothing to compare
// against — its self-consistency is already covered by vector_index tests).
func goldenBackends(t *testing.T) map[string]Store {
	t.Helper()
	dsn := os.Getenv("LADYM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("LADYM_TEST_PG_DSN not set")
	}
	lite, err := NewStore(filepath.Join(t.TempDir(), "golden.db"), goldenDim, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lite.Close() })
	pg, err := NewPostgresStore(freshPGDatabase(t, dsn), goldenDim)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Close() })
	return map[string]Store{"sqlite": lite, "postgres": pg}
}

func TestGoldenVectorSearchParity(t *testing.T) {
	stores := goldenBackends(t)

	// Fixed dataset: 24 memories with deterministic ids and vectors.
	const n = 24
	for _, s := range stores {
		for i := 0; i < n; i++ {
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.ID = fmt.Sprintf("m%02d", i)
			m.Content = "golden memory " + m.ID
			if err := s.PutMemory(m, goldenVector(i)); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Fixed queries: two arbitrary points plus one exactly matching m07.
	queries := [][]float32{goldenVector(100), goldenVector(200), goldenVector(7)}
	const topK = 5
	const simTol = 1e-5
	for qi, q := range queries {
		liteHits := stores["sqlite"].VectorSearch(q, topK)
		pgHits := stores["postgres"].VectorSearch(q, topK)
		if len(liteHits) != topK {
			t.Fatalf("query %d: sqlite returned %d hits, want %d", qi, len(liteHits), topK)
		}
		if len(pgHits) != len(liteHits) {
			t.Fatalf("query %d: hit counts differ: sqlite=%d postgres=%d", qi, len(liteHits), len(pgHits))
		}
		for i := range liteHits {
			if liteHits[i].ID != pgHits[i].ID {
				t.Errorf("query %d hit %d: id mismatch sqlite=%s postgres=%s (sqlite order %v, postgres order %v)",
					qi, i, liteHits[i].ID, pgHits[i].ID, hitIDs(liteHits), hitIDs(pgHits))
			}
			if math.Abs(liteHits[i].Similarity-pgHits[i].Similarity) > simTol {
				t.Errorf("query %d hit %d (%s): similarity sqlite=%v postgres=%v, tol %v",
					qi, i, liteHits[i].ID, liteHits[i].Similarity, pgHits[i].Similarity, simTol)
			}
		}
	}
}

// TestGoldenVectorSearchTiebreak pins the sim-desc / id-asc tiebreak: two
// vectors exactly equidistant from the query must come back in id order.
func TestGoldenVectorSearchTiebreak(t *testing.T) {
	stores := goldenBackends(t)

	vecs := map[string][]float32{
		"tie-a": {1, 0, 0, 0, 0, 0, 0, 0},
		"tie-b": {0, 1, 0, 0, 0, 0, 0, 0},
		"tie-c": {0, 0, 1, 0, 0, 0, 0, 0},
	}
	for _, s := range stores {
		for id, v := range vecs {
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.ID = id
			m.Content = "tie " + id
			if err := s.PutMemory(m, v); err != nil {
				t.Fatal(err)
			}
		}
	}

	query := []float32{1, 1, 0, 0, 0, 0, 0, 0} // cos sim = 1/√2 for tie-a and tie-b, 0 for tie-c
	wantOrder := []string{"tie-a", "tie-b", "tie-c"}
	wantSim := math.Sqrt(2) / 2
	for name, s := range stores {
		hits := s.VectorSearch(query, 3)
		if len(hits) != 3 {
			t.Fatalf("%s: %d hits, want 3", name, len(hits))
		}
		for i, wantID := range wantOrder {
			if hits[i].ID != wantID {
				t.Errorf("%s: hit %d = %s, want %s (order %v)", name, i, hits[i].ID, wantID, hitIDs(hits))
			}
		}
		if math.Abs(hits[0].Similarity-wantSim) > 1e-5 {
			t.Errorf("%s: tie similarity = %v, want ~%v", name, hits[0].Similarity, wantSim)
		}
	}
}

// TestGoldenVectorSearchDegenerateVectors pins degenerate-vector behaviour:
// a zero query vector (and a stored zero vector) make pgvector's cosine
// distance NaN, while the in-memory index yields similarity 0. Both backends
// must report Similarity == 0 (never NaN — a NaN would slip past recall's
// `Similarity < minSimilarity` filter) and agree on the id order.
func TestGoldenVectorSearchDegenerateVectors(t *testing.T) {
	stores := goldenBackends(t)

	vecs := map[string][]float32{
		"deg-a":   {1, 0, 0, 0, 0, 0, 0, 0},
		"deg-b":   {0, 1, 0, 0, 0, 0, 0, 0},
		"deg-neg": {-1, 0, 0, 0, 0, 0, 0, 0}, // negative cosine vs [1,0,...]
		"deg-z":   {0, 0, 0, 0, 0, 0, 0, 0},  // zero-norm stored vector
	}
	for _, s := range stores {
		for id, v := range vecs {
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.ID = id
			m.Content = "degenerate " + id
			if err := s.PutMemory(m, v); err != nil {
				t.Fatal(err)
			}
		}
	}

	queries := map[string][]float32{
		"zero-query":   {0, 0, 0, 0, 0, 0, 0, 0}, // every similarity degenerates
		"normal-query": {1, 0, 0, 0, 0, 0, 0, 0}, // only the stored zero vector degenerates
	}
	for qi, q := range queries {
		liteHits := stores["sqlite"].VectorSearch(q, 3)
		pgHits := stores["postgres"].VectorSearch(q, 3)
		for name, hits := range map[string][]SearchHit{"sqlite": liteHits, "postgres": pgHits} {
			for i, h := range hits {
				if math.IsNaN(h.Similarity) {
					t.Errorf("%s query %s hit %d (%s): similarity is NaN, want 0", name, qi, i, h.ID)
				}
			}
		}
		if len(liteHits) != len(pgHits) {
			t.Fatalf("query %s: hit counts differ: sqlite=%d postgres=%d", qi, len(liteHits), len(pgHits))
		}
		for i := range liteHits {
			if liteHits[i].ID != pgHits[i].ID {
				t.Errorf("query %s hit %d: id mismatch sqlite=%s postgres=%s (sqlite %v, postgres %v)",
					qi, i, liteHits[i].ID, pgHits[i].ID, hitIDs(liteHits), hitIDs(pgHits))
			}
		}
		// The degenerate entry must report exactly 0 on both backends.
		for name, hits := range map[string][]SearchHit{"sqlite": liteHits, "postgres": pgHits} {
			for _, h := range hits {
				if (qi == "zero-query" || h.ID == "deg-z") && h.Similarity != 0 {
					t.Errorf("%s query %s hit %s: similarity = %v, want 0", name, qi, h.ID, h.Similarity)
				}
			}
		}
	}

	// topK < row count: exercises the UNION-merge + truncate path — the
	// zero-norm row (sim 0) must displace the negative-similarity row, and a
	// zero query must return the pure id-ascending prefix.
	truncCases := []struct {
		name  string
		query []float32
		topK  int
		want  []string
	}{
		{"displace-negative", []float32{1, 0, 0, 0, 0, 0, 0, 0}, 3, []string{"deg-a", "deg-b", "deg-z"}},
		{"zero-query-prefix", []float32{0, 0, 0, 0, 0, 0, 0, 0}, 2, []string{"deg-a", "deg-b"}},
	}
	for _, tc := range truncCases {
		for name, s := range stores {
			hits := s.VectorSearch(tc.query, tc.topK)
			if len(hits) != len(tc.want) {
				t.Errorf("%s %s: %d hits, want %d (%v)", name, tc.name, len(hits), len(tc.want), hitIDs(hits))
				continue
			}
			for i, wantID := range tc.want {
				if hits[i].ID != wantID {
					t.Errorf("%s %s: hit %d = %s, want %s (order %v)", name, tc.name, i, hits[i].ID, wantID, hitIDs(hits))
				}
			}
		}
	}
}

func hitIDs(hits []SearchHit) []string {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	return ids
}
