//go:build !enterprise

package engine

import (
	"testing"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

type migrationStartBarrier struct {
	storage.Store
	started chan struct{}
	release chan struct{}
}

func (s *migrationStartBarrier) BeginEmbeddingMigrationGeneration(target schema.EmbeddingIdentity) (string, error) {
	generation, err := s.Store.BeginEmbeddingMigrationGeneration(target)
	close(s.started)
	<-s.release
	return generation, err
}

func (s *migrationStartBarrier) BeginEmbeddingMigration(target schema.EmbeddingIdentity) error {
	err := s.Store.BeginEmbeddingMigration(target)
	close(s.started)
	<-s.release
	return err
}

func TestMigrationRunnerCannotInheritNewGeneration(t *testing.T) {
	cfg := config.ForTesting(t.TempDir())
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.ID, m.Content = "a", "a"
	if err := eng.Store.PutMemory(m, make([]float32, eng.Provider.Dim())); err != nil {
		t.Fatal(err)
	}
	provider := &migrationEmbedder{identity: schema.EmbeddingIdentity{Provider: "test", Model: "v2", Dim: 8, Tokenizer: "v1"}}
	barrier := &migrationStartBarrier{Store: eng.Store, started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- MigrateEmbeddings(t.Context(), barrier, provider.identity, provider, 2, true) }()
	<-barrier.started
	if err := eng.Store.RollbackEmbeddingMigration(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Store.BeginEmbeddingMigration(provider.identity); err != nil {
		t.Fatal(err)
	}
	close(barrier.release)
	if err := <-done; err == nil {
		t.Fatal("abandoned runner inherited and published a new generation")
	}
	status, _ := eng.Store.EmbeddingMigrationStatus()
	if status.Processed != 0 || len(provider.calls) != 0 {
		t.Fatalf("abandoned runner staged new generation: %+v", status)
	}
}
