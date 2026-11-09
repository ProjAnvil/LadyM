package engine

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/adapter"
	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type coldStartMetadataStore struct {
	storage.Store
	afterDimensionRead func() error
	migrationStarts    int
}

func (s *coldStartMetadataStore) GetMeta(key string) (string, error) {
	value, err := s.Store.GetMeta(key)
	if err != nil || key != "embedding_dim" || s.afterDimensionRead == nil {
		return value, err
	}
	initialize := s.afterDimensionRead
	s.afterDimensionRead = nil
	if err := initialize(); err != nil {
		return "", err
	}
	return value, nil
}

func (s *coldStartMetadataStore) BeginAutomaticEmbeddingMigrationGeneration(identity schema.EmbeddingIdentity) (string, error) {
	s.migrationStarts++
	return s.Store.BeginAutomaticEmbeddingMigrationGeneration(identity)
}

func TestEmbeddingColdStartConcurrentAdoption(t *testing.T) {
	for _, allowMigration := range []bool{false, true} {
		t.Run(fmt.Sprintf("allow_migration=%t", allowMigration), func(t *testing.T) {
			for _, writeMemory := range []bool{false, true} {
				t.Run(fmt.Sprintf("winner_writes=%t", writeMemory), func(t *testing.T) {
					runBackendConfigs(t, func(t *testing.T, cfg *config.Config) {
						cfg.EmbeddingAllowDimChange = allowMigration
						store, err := storage.OpenStore(cfg, cfg.EmbeddingDim)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { store.Close() })
						provider, err := storage.MakeProvider(cfg)
						if err != nil {
							t.Fatal(err)
						}
						first := &Engine{Config: cfg, Provider: provider}
						interleaved := &coldStartMetadataStore{
							Store: store,
							afterDimensionRead: func() error {
								second, err := New(cfg)
								if err != nil {
									return err
								}
								t.Cleanup(func() { second.Close() })
								if writeMemory {
									_, err = second.Remember("winner committed a first memory", schema.LayerSemantic,
										schema.TypeFact, nil, nil, "regression", "")
									return err
								}
								return nil
							},
						}
						first.Store = interleaved

						if err := first.enforceEmbeddingFingerprint(); err != nil {
							t.Fatalf("same-identity concurrent cold start failed: %v", err)
						}
						if interleaved.migrationStarts != 0 {
							t.Fatalf("same-identity initialization started %d unnecessary migrations", interleaved.migrationStarts)
						}
						status, err := store.EmbeddingMigrationStatus()
						if err != nil {
							t.Fatal(err)
						}
						if status.State != "idle" || status.Generation != "" || status.RollbackAvailable {
							t.Fatalf("initialization unnecessarily started a migration: %+v", status)
						}
						dim, err := store.GetMeta("embedding_dim")
						if err != nil || dim != strconv.Itoa(cfg.EmbeddingDim) {
							t.Fatalf("persisted dimension = %q, err=%v", dim, err)
						}
					})
				})
			}
		})
	}
}

func TestEmbeddingColdStartRejectsDifferentIdentity(t *testing.T) {
	runBackendConfigs(t, func(t *testing.T, cfg *config.Config) {
		store, err := storage.OpenStore(cfg, cfg.EmbeddingDim)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		provider, err := storage.MakeProvider(cfg)
		if err != nil {
			t.Fatal(err)
		}
		other := &migrationEmbedder{identity: schema.EmbeddingIdentity{
			Provider: "host", Model: "another-vector-space", Tokenizer: "v1", Dim: cfg.EmbeddingDim,
		}}
		first := &Engine{Config: cfg, Provider: provider}
		first.Store = &coldStartMetadataStore{
			Store: store,
			afterDimensionRead: func() error {
				second, err := NewWithModels(cfg, &adapter.ModelRouting{Embedding: other})
				if err != nil {
					return err
				}
				t.Cleanup(func() { second.Close() })
				return nil
			},
		}
		if err := first.enforceEmbeddingFingerprint(); err == nil {
			t.Fatal("concurrent initializer replaced an incompatible vector identity")
		}
		status, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		if status.ActiveFingerprint != other.identity.Fingerprint() || status.Generation != "" {
			t.Fatalf("incompatible initialization changed existing identity: %+v", status)
		}
	})
}

func TestEmbeddingColdStartDoesNotAdoptLegacyVectors(t *testing.T) {
	runBackendConfigs(t, func(t *testing.T, cfg *config.Config) {
		store, err := storage.OpenStore(cfg, cfg.EmbeddingDim)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		memory := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		memory.Content = "unidentified legacy vector"
		vector := make([]float32, cfg.EmbeddingDim)
		vector[0] = 1
		if err := store.PutMemory(memory, vector); err != nil {
			t.Fatal(err)
		}
		eng, err := New(cfg)
		if eng != nil {
			eng.Close()
		}
		if _, ok := errors.AsType[*storage.EmbeddingFingerprintMismatch](err); !ok {
			t.Fatalf("unidentified vectors were implicitly adopted: %v", err)
		}
		status, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := store.GetEmbedding(memory.ID)
		if err != nil || status.ActiveFingerprint != "" || !slices.Equal(vector, persisted) {
			t.Fatalf("legacy data was mutated: status=%+v vector=%v err=%v", status, persisted, err)
		}
	})
}

func TestEmbeddingColdStartParallelEngines(t *testing.T) {
	runBackendConfigs(t, func(t *testing.T, cfg *config.Config) {
		// Isolate identity initialization from SQLite's separate schema-creation lock.
		store, err := storage.OpenStore(cfg, cfg.EmbeddingDim)
		if err != nil {
			t.Fatal(err)
		}

		fingerprint, err := store.GetMeta("embedding_fingerprint")
		closeErr := store.Close()
		if err != nil || closeErr != nil || fingerprint != "" {
			t.Fatalf("expected uninitialized embedding identity: %q, read=%v, close=%v", fingerprint, err, closeErr)
		}
		const count = 8
		engines := make([]*Engine, count)
		failures := make([]error, count)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := range count {
			workers.Go(func() {
				<-start
				engines[i], failures[i] = New(cfg)
			})
		}
		close(start)
		workers.Wait()
		for i, eng := range engines {
			if eng != nil {
				t.Cleanup(func() { eng.Close() })
			}
			if failures[i] != nil {
				t.Errorf("initializer %d failed: %v", i, failures[i])
			}
		}
		if t.Failed() {
			return
		}
		var ids []string
		for i, eng := range engines {
			memory, err := eng.Remember(fmt.Sprintf("parallel cold start fact %d", i),
				schema.LayerSemantic, schema.TypeFact, nil, nil, "regression", "")
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, memory.ID)
		}
		for i, eng := range engines {
			response, err := eng.Recall("parallel cold start fact", cfg.Workspace, count, nil, nil, 0)
			if err != nil {
				t.Fatalf("initializer %d cannot recall after bootstrap: %v", i, err)
			}
			for _, id := range ids {
				if !slices.ContainsFunc(response.Results, func(hit *schema.RecallResult) bool {
					return hit.Memory.ID == id
				}) {
					t.Errorf("initializer %d cannot see committed memory %s", i, id)
				}
			}
		}
	})
}
