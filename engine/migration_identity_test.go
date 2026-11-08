//go:build !enterprise

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type anonymousMigrationEmbedding struct{ storage.EmbeddingProvider }

func TestMigrationLiveHashingEnginesKeepTokenizer(t *testing.T) {
	restoreCJKMode(t)
	oldDir := storage.CJKDictStatusNow().Dir
	t.Cleanup(func() { storage.SetCJKDictDir(oldDir) })
	dict := t.TempDir()
	for _, name := range []string{"s_1.txt", "t_1.txt"} {
		if err := os.WriteFile(filepath.Join(dict, name), []byte("数据库 1024 n\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, cache := range []int{0, 1} {
		for _, mode := range []string{storage.CJKTokenizerDict, storage.CJKTokenizerChars} {
			t.Run(fmt.Sprintf("%s/cache=%d", mode, cache), func(t *testing.T) {
				engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
					cfg.CJKDictDir, cfg.EmbeddingCJKTokenizer, cfg.EmbeddingQueryCacheSize = dict, mode, cache
					first, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer first.Close()
					identity, err := first.embeddingIdentity()
					if err != nil {
						t.Fatal(err)
					}
					before, err := first.Provider.Embed("数据库")
					if err != nil {
						t.Fatal(err)
					}
					secondCfg := config.ForTesting(t.TempDir())
					secondCfg.CJKDictDir, secondCfg.EmbeddingQueryCacheSize = dict, cache
					secondCfg.EmbeddingCJKTokenizer = storage.CJKTokenizerChars
					if mode == storage.CJKTokenizerChars {
						secondCfg.EmbeddingCJKTokenizer = storage.CJKTokenizerDict
					}
					second, err := New(secondCfg)
					if err != nil {
						t.Fatal(err)
					}
					defer second.Close()
					other, err := second.Provider.Embed("数据库")
					if err != nil {
						t.Fatal(err)
					}
					if slices.Equal(before, other) {
						t.Fatal("fixture did not distinguish dictionary and character vectors")
					}
					// Evict the first provider's cached entry, then also bypass its cache.
					if _, err := first.Provider.Embed("eviction"); err != nil {
						t.Fatal(err)
					}
					after, err := first.Provider.Embed("数据库")
					if err != nil {
						t.Fatal(err)
					}
					batch, err := first.Provider.EmbedBatch([]string{"数据库"})
					if err != nil {
						t.Fatal(err)
					}
					current, err := first.embeddingIdentity()
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(before, after) || !slices.Equal(before, batch[0]) || identity.Fingerprint() != current.Fingerprint() {
						t.Fatal("second live Engine silently changed first provider's vectors or fingerprint")
					}
					if err := first.enforceEmbeddingFingerprint(); err != nil {
						t.Fatal(err)
					}
				})
			})
		}
	}
}

func TestMigrationAnonymousLegacyIdentity(t *testing.T) {
	engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
		provider := anonymousMigrationEmbedding{storage.NewHashingEmbedding(8)}
		eng, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: provider})
		if err != nil {
			t.Fatalf("fresh anonymous injection incompatible: %v", err)
		}
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID, m.Content = "a", "a"
		if err := eng.Store.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
		eng.Close()
		if _, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: provider}); err == nil {
			t.Fatal("unknown injection silently equated existing same-dim space")
		}
		known := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "host", Model: "attested-v1", Dim: 8, Tokenizer: "local-v1"}}
		if _, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: known}); err == nil {
			t.Fatal("identified runtime adopted legacy vectors implicitly")
		}
		s, err := storage.OpenStore(cfg, 8)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AdoptEmbeddingIdentity(schema.EmbeddingIdentity{Provider: "injected", Model: "unknown", Dim: 8, Tokenizer: "unknown"}); err == nil {
			t.Fatal("meaningless identity accepted")
		}
		if err := s.AdoptEmbeddingIdentity(known.identity); err != nil {
			t.Fatal(err)
		}
		s.Close()
		eng, err = NewWithModels(cfg, &adapter.ModelRouting{Embedding: known})
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
		if len(known.calls) != 0 {
			t.Fatal("adoption called embedding provider")
		}
	})
}

func TestMigrationCancellationAndTargetFence(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	target := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "v2", Dim: 8, Tokenizer: "v1"}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := MigrateEmbeddings(ctx, eng.Store, target.identity, target, 2, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
	if len(target.calls) != 0 {
		t.Fatal("cancelled migration called provider")
	}
	if err := MigrateEmbeddings(t.Context(), eng.Store, target.identity, target, 0, true); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	other := target.identity
	other.Model = "different"
	if err := MigrateEmbeddings(t.Context(), eng.Store, other, target, 2, true); err == nil {
		t.Fatal("identified provider different same-dim target accepted")
	}
}

func TestMigrationOpenEngineCannotMixSpace(t *testing.T) {
	engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
		old := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "old", Dim: 8, Tokenizer: "v1"}}
		eng, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: old})
		if err != nil {
			t.Fatal(err)
		}
		defer eng.Close()
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		m.ID, m.Content = "a", "a"
		if err := eng.Store.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
		store, err := storage.OpenStore(cfg, 8)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		next := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "new", Dim: 8, Tokenizer: "v1"}}
		if err := MigrateEmbeddings(t.Context(), store, next.identity, next, 2, true); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Store.SearchMemories([]float32{1, 0, 0, 0, 0, 0, 0, 0}, 1, storage.MemoryFilter{}); err == nil {
			t.Fatal("old Engine queried new same-dimensional space")
		}
		if err := store.FinalizeEmbeddingMigration(); err != nil {
			t.Fatal(err)
		}
		if err := eng.Store.PutMemory(m, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err == nil {
			t.Fatal("old Engine published vectors into new same-dimensional space")
		}
	})
}
