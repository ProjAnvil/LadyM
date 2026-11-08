//go:build !enterprise

package engine

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type migrationFinalizeBarrier struct {
	storage.Store
	started    chan struct{}
	release    chan struct{}
	statusRead bool
}

func (s *migrationFinalizeBarrier) EmbeddingMigrationStatus() (*schema.MigrationStatus, error) {
	status, err := s.Store.EmbeddingMigrationStatus()
	if s.statusRead {
		close(s.started)
		<-s.release
	}
	return status, err
}

func (s *migrationFinalizeBarrier) FinalizeEmbeddingMigration() error {
	if !s.statusRead {
		close(s.started)
		<-s.release
	}
	return s.Store.FinalizeEmbeddingMigration()
}

func (s *migrationFinalizeBarrier) FinalizeAutomaticEmbeddingMigration(generation, target string) error {
	if !s.statusRead {
		close(s.started)
		<-s.release
	}
	return s.Store.FinalizeAutomaticEmbeddingMigration(generation, target)
}

func TestMigrationOldAutomaticFinalizeCannotEraseNewGeneration(t *testing.T) {
	for _, statusRead := range []bool{false, true} {
		for _, sameTarget := range []bool{false, true} {
			for _, administrative := range []bool{false, true} {
				t.Run(fmt.Sprintf("status-read=%t/same-target=%t/administrative=%t", statusRead, sameTarget, administrative), func(t *testing.T) {
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
						memory := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
						memory.ID, memory.Content = "a", "a"
						if err := store.PutMemory(memory, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
							t.Fatal(err)
						}
						target := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "next", Dim: 8, Tokenizer: "v1"}}
						if statusRead {
							if err := MigrateEmbeddings(t.Context(), migrationCrashAfterActivation{store}, target.identity, target, 2, false); err == nil {
								t.Fatal("crash fixture did not interrupt finalization")
							}
						} else if administrative {
							if _, err := store.BeginAutomaticEmbeddingMigrationGeneration(target.identity); err != nil {
								t.Fatal(err)
							}
						}
						barrier := &migrationFinalizeBarrier{Store: store, started: make(chan struct{}), release: make(chan struct{}), statusRead: statusRead}
						done := make(chan error, 1)
						go func() { done <- MigrateEmbeddings(t.Context(), barrier, target.identity, target, 2, administrative) }()
						<-barrier.started
						finished := false
						defer func() {
							if !finished {
								close(barrier.release)
								<-done
							}
						}()
						// A different handle represents an administrative rollback/restart.
						current, err := storage.OpenStore(cfg, 8)
						if err != nil {
							t.Fatal(err)
						}
						defer current.Close()
						if err := current.RollbackEmbeddingMigration(); err != nil {
							t.Fatal(err)
						}
						next := &migrationEmbedder{identity: target.identity}
						if !sameTarget {
							next.identity.Model = "later"
						}
						if err := MigrateEmbeddings(t.Context(), current, next.identity, next, 2, true); err != nil {
							t.Fatal(err)
						}
						close(barrier.release)
						err = <-done
						finished = true
						if !errors.Is(err, storage.ErrEmbeddingMigration) {
							t.Errorf("old automatic runner erased a later backup: %v", err)
						}
						status, err := current.EmbeddingMigrationStatus()
						if err != nil {
							t.Fatal(err)
						}
						if status.State != "rollback_window" || !status.RollbackAvailable || status.ActiveFingerprint != next.identity.Fingerprint() {
							t.Fatalf("new generation backup lost: %+v", status)
						}
						if err := current.RollbackEmbeddingMigration(); err != nil {
							t.Fatal(err)
						}
						vector, err := current.GetEmbedding("a")
						if err != nil || !slices.Equal(vector, []float32{1, 0, 0, 0, 0, 0, 0, 0}) {
							t.Fatalf("later backup could not restore original vectors: %v %v", vector, err)
						}
					})
				})
			}
		}
	}
}

type migrationCrashAfterActivation struct{ storage.Store }

func (s migrationCrashAfterActivation) ActivateEmbeddingMigration(generation string) error {
	if err := s.Store.ActivateEmbeddingMigration(generation); err != nil {
		return err
	}
	return errors.New("simulated crash after committed activation")
}

func TestMigrationRestartResumesOnlyAutomaticFinalization(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("automatic=%t/allow=%t", automatic, allow), func(t *testing.T) {
				engineMigrationBackends(t, func(t *testing.T, cfg *config.Config) {
					first, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					memory := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
					memory.ID, memory.Content = "a", "a"
					if err := first.Store.PutMemory(memory, make([]float32, first.Provider.Dim())); err != nil {
						t.Fatal(err)
					}
					if err := first.Close(); err != nil {
						t.Fatal(err)
					}
					cfg.EmbeddingDim = 8
					provider, identity, err := MigrationProvider(cfg)
					if err != nil {
						t.Fatal(err)
					}
					store, err := storage.OpenStore(cfg, 8)
					if err != nil {
						t.Fatal(err)
					}
					if err := MigrateEmbeddings(t.Context(), migrationCrashAfterActivation{store}, identity, provider, 2, !automatic); err == nil {
						t.Fatal("crash fixture did not interrupt finalization")
					}
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					cfg.EmbeddingAllowDimChange = allow
					restarted, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					defer restarted.Close()
					status, err := restarted.Store.EmbeddingMigrationStatus()
					if err != nil {
						t.Fatal(err)
					}
					wantState := "rollback_window"
					if automatic {
						wantState = "idle"
					}
					if status.State != wantState || status.RollbackAvailable == automatic {
						t.Fatalf("restart mishandled durable migration intent: %+v", status)
					}
					vector, err := restarted.Provider.Embed("a")
					if err != nil {
						t.Fatal(err)
					}
					err = restarted.Store.PutMemory(memory, vector)
					if automatic && err != nil || !automatic && err == nil {
						t.Fatalf("restart write gate: automatic=%t error=%v", automatic, err)
					}
				})
			})
		}
	}

}

func TestMigrationAdministrativeResumeFinalizesAutomaticWindow(t *testing.T) {
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
		memory := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
		memory.ID, memory.Content = "a", "a"
		if err := store.PutMemory(memory, []float32{1, 0, 0, 0, 0, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
		target := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "next", Dim: 8, Tokenizer: "v1"}}
		if err := MigrateEmbeddings(t.Context(), migrationCrashAfterActivation{store}, target.identity, target, 2, false); err == nil {
			t.Fatal("crash fixture did not interrupt finalization")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = storage.OpenStore(cfg, 8)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		target.calls = nil
		if err := MigrateEmbeddings(t.Context(), store, target.identity, target, 2, true); err != nil {
			t.Fatal(err)
		}
		status, err := store.EmbeddingMigrationStatus()
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "idle" || status.RollbackAvailable || len(target.calls) != 0 {
			t.Fatalf("administrative resume failed pending automatic finalization: %+v calls=%v", status, target.calls)
		}
		if err := store.UpdateMemoryContent("a", "writable", "", nil, nil, 1); err != nil {
			t.Fatalf("completed automatic migration still freezes writes: %v", err)
		}
	})
}
