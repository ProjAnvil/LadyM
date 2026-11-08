//go:build !enterprise

package operations

import (
	"errors"
	"os"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

func runRecallFreshnessBackends(t *testing.T, fn func(*testing.T, storage.Store)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		s, _ := trustedStore(t)
		fn(t, s)
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("LADYM_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("LADYM_TEST_PG_DSN not set")
		}
		s, err := storage.NewPostgresStore(testutil.PGDatabase(t, dsn), 2)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		fn(t, s)
	})
}

func recallFreshnessMemory(t *testing.T, s storage.Store, id string, typ schema.MemoryType, meta map[string]any, vector []float32) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerSemantic, typ)
	m.ID, m.Content, m.Workspace = id, "fallback symbol", "test"
	if meta != nil {
		m.Metadata = meta
	}
	if err := s.PutMemory(m, vector); err != nil {
		t.Fatal(err)
	}
	return m
}

func recallCodeFile(t *testing.T, s storage.Store) (*schema.RecallResponse, error) {
	t.Helper()
	cfg := config.Default()
	cfg.Recall.EnableTier2 = true
	cfg.Recall.ReflectionMinHits = 2
	cfg.Recall.GraphHops = 0
	return Recall(s, trustedEmbedding{}, "fallback symbol", cfg, "test", 10, nil, nil, 0)
}

func TestRecallCodeFileFallbackFreshness(t *testing.T) {
	for _, tc := range []struct {
		name        string
		expiredFile bool
		expiredDep  bool
		depth       int
		alternative bool
		wantFile    string
	}{
		{name: "fresh", wantFile: "file-a"},
		{name: "expired", expiredFile: true},
		{name: "fresh direct dependency", depth: 1, wantFile: "file-a"},
		{name: "fresh transitive dependency", depth: 2, wantFile: "file-a"},
		{name: "expired direct dependency", depth: 1, expiredDep: true},
		{name: "expired transitive dependency", depth: 2, expiredDep: true},
		{name: "fresh alternative", expiredFile: true, alternative: true, wantFile: "file-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runRecallFreshnessBackends(t, func(t *testing.T, s storage.Store) {
				recallFreshnessMemory(t, s, "symbol", schema.TypeCodeSymbol, map[string]any{"file_path": "fallback.go"}, []float32{1, 0})
				meta := map[string]any{"file_path": "fallback.go"}
				if tc.expiredFile {
					meta["valid_until"] = schema.Now() - 60
				}
				if tc.depth > 0 {
					until := schema.Now() + 3600
					if tc.expiredDep {
						until = schema.Now() - 60
					}
					leaf := recallFreshnessMemory(t, s, "leaf", schema.TypeEvidence, map[string]any{"valid_until": until}, nil)
					source := leaf.ID
					if tc.depth == 2 {
						bridge := recallFreshnessMemory(t, s, "bridge", schema.TypeFact, nil, nil)
						if err := s.PutEdge(schema.NewEdge(bridge.ID, "produced_from", leaf.ID)); err != nil {
							t.Fatal(err)
						}
						source = bridge.ID
					}
					meta["source_episodes"] = []string{source}
				}
				file := recallFreshnessMemory(t, s, "file-a", schema.TypeCodeFile, meta, nil)
				if tc.alternative {
					recallFreshnessMemory(t, s, "file-b", schema.TypeCodeFile, map[string]any{"file_path": "fallback.go"}, nil)
				}
				if !schema.IsUsable(file) {
					t.Fatal("fixture must exercise freshness, not usability filtering")
				}
				resp, err := recallCodeFile(t, s)
				if err != nil {
					t.Fatal(err)
				}
				if resp.TierReached != 2 {
					t.Fatalf("fallback not reached: tier %d", resp.TierReached)
				}
				gotFile, gotSymbol := "", false
				for _, result := range resp.Results {
					if result.Memory.Type == schema.TypeCodeFile {
						if gotFile != "" || result.Tier != 2 {
							t.Fatalf("unexpected file fallback: %+v", resp.Results)
						}
						gotFile = result.Memory.ID
					}
					gotSymbol = gotSymbol || result.Memory.ID == "symbol"
				}
				if !gotSymbol || gotFile != tc.wantFile {
					t.Fatalf("symbol=%v file=%q, want file=%q", gotSymbol, gotFile, tc.wantFile)
				}
			})
		})
	}
}

type recallFreshnessErrorStore struct {
	storage.Store
	getID, neighborsID string
	err                error
}

func (s recallFreshnessErrorStore) GetMemory(id string) (*schema.Memory, error) {
	if id == s.getID {
		return nil, s.err
	}
	return s.Store.GetMemory(id)
}

func (s recallFreshnessErrorStore) Neighbors(id, relation string) ([]*schema.Edge, error) {
	if id == s.neighborsID {
		return nil, s.err
	}
	return s.Store.Neighbors(id, relation)
}

func TestRecallCodeFileFallbackFreshnessErrors(t *testing.T) {
	runRecallFreshnessBackends(t, func(t *testing.T, s storage.Store) {
		recallFreshnessMemory(t, s, "symbol", schema.TypeCodeSymbol, map[string]any{"file_path": "fallback.go"}, []float32{1, 0})
		recallFreshnessMemory(t, s, "source", schema.TypeEvidence, nil, nil)
		recallFreshnessMemory(t, s, "file", schema.TypeCodeFile, map[string]any{
			"file_path": "fallback.go", "source_episodes": []string{"source"},
		}, nil)
		boom := errors.New("freshness storage unavailable")
		for _, failing := range []recallFreshnessErrorStore{
			{Store: s, getID: "source", err: boom},
			{Store: s, neighborsID: "file", err: boom},
		} {
			if resp, err := recallCodeFile(t, failing); !errors.Is(err, boom) || resp != nil {
				t.Errorf("freshness error swallowed: response=%v err=%v", resp, err)
			}
		}
	})
}
