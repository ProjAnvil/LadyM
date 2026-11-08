//go:build !enterprise

package engine

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/internal/testutil"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type migrationEmbedder struct {
	identity schema.EmbeddingIdentity
	calls    []string
	failAt   int
}

func (p *migrationEmbedder) Identity() schema.EmbeddingIdentity { return p.identity }
func (p *migrationEmbedder) Dim() int                           { return p.identity.Dim }
func (p *migrationEmbedder) HealthCheck() (bool, string)        { return true, "" }
func (p *migrationEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	var out [][]float32
	for _, text := range texts {
		v, err := p.Embed(text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (p *migrationEmbedder) Embed(text string) ([]float32, error) {
	p.calls = append(p.calls, text)
	if p.failAt > 0 && len(p.calls) == p.failAt {
		return nil, errors.New("injected embedding failure")
	}
	v := make([]float32, p.Dim())
	v[1] = 1
	return v, nil
}

func engineMigrationBackends(t *testing.T, fn func(*testing.T, *config.Config)) {
	t.Run("sqlite", func(t *testing.T) { fn(t, config.ForTesting(t.TempDir())) })
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("LADYM_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("LADYM_TEST_PG_DSN not set")
		}
		cfg := config.ForTesting(t.TempDir())
		cfg.StoreBackend, cfg.StoreDSN = "postgres", testutil.PGDatabase(t, dsn)
		fn(t, cfg)
	})
}

func TestMigrationFingerprintAndResume(t *testing.T) {
	engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
		old := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "old", Dim: 8, Tokenizer: "v1"}}
		eng, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: old})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			m.ID, m.Content = id, id
			if err := eng.Store.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
				t.Fatal(err)
			}
		}
		eng.Close()
		next := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "new", Dim: 8, Tokenizer: "v1"}, failAt: 3}
		if _, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: next}); err == nil {
			t.Fatal("same-dim different-model reopen succeeded")
		}
		s, err := storage.OpenStore(cfg, 8)
		if err != nil {
			t.Fatal(err)
		}
		if err := MigrateEmbeddings(t.Context(), s, next.identity, next, 2, true); err == nil {
			t.Fatal("provider failure swallowed")
		}
		status, _ := s.EmbeddingMigrationStatus()
		if status.Processed != 2 || status.Cursor != "b" {
			t.Fatalf("failed checkpoint: %+v", status)
		}
		s.Close()
		s, err = storage.OpenStore(cfg, 8)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		next.calls, next.failAt = nil, 0
		if err := MigrateEmbeddings(t.Context(), s, next.identity, next, 2, true); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(next.calls, []string{"c", "d"}) {
			t.Fatalf("re-embedded committed rows: %v", next.calls)
		}
		status, _ = s.EmbeddingMigrationStatus()
		if !status.RollbackAvailable || status.ActiveFingerprint != next.identity.Fingerprint() {
			t.Fatalf("explicit migration discarded rollback: %+v", status)
		}
		eng, err = NewWithModels(cfg, &adapter.ModelRouting{Embedding: next})
		if err != nil {
			t.Fatal(err)
		}
		eng.Close()
	})
}

func TestMigrationPathAndLegacyAdopt(t *testing.T) {
	engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
		eng, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID, m.Content = "fact", "fact"
		if err := eng.Store.PutMemory(m, make([]float32, eng.Provider.Dim())); err != nil {
			t.Fatal(err)
		}
		path := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
		path.ID, path.Content, path.Metadata["resource_identity"] = "path", "/source/file", "path"
		if err := eng.Store.PutMemory(path, nil); err != nil {
			t.Fatal(err)
		}
		eng.Close()
		cfg.EmbeddingDim = 16
		cfg.EmbeddingAllowDimChange = true
		eng, err = New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
		status, _ := eng.Store.EmbeddingMigrationStatus()
		if status.RollbackAvailable || status.State != "idle" {
			t.Fatalf("compat migration not finalized: %+v", status)
		}
		if v, _ := eng.Store.GetEmbedding("path"); v != nil {
			t.Fatal("path embedded")
		}
		if err := eng.Store.UpdateMemoryContent("fact", "new", "", nil, nil, 1); err != nil {
			t.Fatal(err)
		}
	})
}

func TestMigrationAutomaticStartupCannotTakeOverExplicitStaging(t *testing.T) {
	engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
		store, err := storage.OpenStore(cfg, 8)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		old := schema.EmbeddingIdentity{Provider: "test", Model: "old", Dim: 8, Tokenizer: "v1"}
		if err := store.AdoptEmbeddingIdentity(old); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c", "d"} {
			memory := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
			memory.ID, memory.Content = id, id
			if err := store.PutMemory(memory, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
				t.Fatal(err)
			}
		}
		target := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "next", Dim: 8, Tokenizer: "v1"}, failAt: 3}
		if err := MigrateEmbeddings(t.Context(), store, target.identity, target, 2, true); err == nil {
			t.Fatal("provider failure did not interrupt explicit staging")
		}
		pending, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		cfg.EmbeddingAllowDimChange = true
		target.calls, target.failAt = nil, 0
		eng, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: target})
		if eng != nil {
			eng.Close()
		}
		if !errors.Is(err, storage.ErrEmbeddingMigration) {
			t.Fatalf("automatic startup took over explicit staging: %v", err)
		}
		if err := MigrateEmbeddings(t.Context(), store, target.identity, target, 2, false); !errors.Is(err, storage.ErrEmbeddingMigration) {
			t.Fatalf("automatic runner took over explicit staging: %v", err)
		}
		unchanged, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		if unchanged.AutoFinalize || unchanged.Generation != pending.Generation || unchanged.Processed != 2 || unchanged.Cursor != "b" || len(target.calls) != 0 {
			t.Fatalf("automatic takeover changed explicit checkpoint: %+v calls=%v", unchanged, target.calls)
		}
		if err := MigrateEmbeddings(t.Context(), store, target.identity, target, 2, true); err != nil {
			t.Fatal(err)
		}
		retained, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		if retained.State != "rollback_window" || !retained.RollbackAvailable || retained.AutoFinalize || !slices.Equal(target.calls, []string{"c", "d"}) {
			t.Fatalf("administrative resume lost explicit policy/cursor: %+v calls=%v", retained, target.calls)
		}
		if err := store.UpdateMemoryContent("a", "frozen", "", nil, nil, 1); err == nil {
			t.Fatal("explicit rollback window allowed content writes")
		}
		if err := store.RollbackEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		vector, err := store.GetEmbedding("a")
		if err != nil || !slices.Equal(vector, []float32{1, 0, 0, 0, 0, 0, 0, 0}) {
			t.Fatalf("explicit backup was erased: %v %v", vector, err)
		}
	})
}
