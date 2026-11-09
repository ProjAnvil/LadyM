//go:build !enterprise

package engine

// Store error-branch tests: enforceEmbeddingDim / reembedAll / StatsFor must
// propagate storage failures instead of panicking or writing partial state.
// The Store interface hides *sql.DB, so a stub wraps the real store and fails
// one method at a time.

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// stubStore delegates everything to the wrapped store except the methods a
// test has overridden with a hook.
type stubStore struct {
	storage.Store
	getMeta            func(key string) (string, error)
	setMeta            func(key, value string) error
	iterMemories       func(workspace, layer, typ string) ([]*schema.Memory, error)
	putMemory          func(mem *schema.Memory, vector []float32) error
	workspaces         func() ([]string, error)
	adoptIdentity      func(schema.EmbeddingIdentity) error
	initializeIdentity func(schema.EmbeddingIdentity) error
	migrationStatus    func() (*schema.MigrationStatus, error)
	migrationBatch     func(string, int) ([]*schema.Memory, error)
	stageMigration     func(string, []float32, string, string) error
	activateMigration  func(string) error
}

func (s *stubStore) InitializeEmbeddingIdentity(identity schema.EmbeddingIdentity) error {
	if s.initializeIdentity != nil {
		return s.initializeIdentity(identity)
	}
	return s.Store.InitializeEmbeddingIdentity(identity)
}

func (s *stubStore) AdoptEmbeddingIdentity(identity schema.EmbeddingIdentity) error {
	if s.adoptIdentity != nil {
		return s.adoptIdentity(identity)
	}
	return s.Store.AdoptEmbeddingIdentity(identity)
}

func (s *stubStore) EmbeddingMigrationStatus() (*schema.MigrationStatus, error) {
	if s.migrationStatus != nil {
		return s.migrationStatus()
	}
	return s.Store.EmbeddingMigrationStatus()
}

func (s *stubStore) EmbeddingMigrationBatch(generation string, limit int) ([]*schema.Memory, error) {
	if s.migrationBatch != nil {
		return s.migrationBatch(generation, limit)
	}
	return s.Store.EmbeddingMigrationBatch(generation, limit)
}

func (s *stubStore) StageEmbeddingMigration(id string, vector []float32, fingerprint, generation string) error {
	if s.stageMigration != nil {
		return s.stageMigration(id, vector, fingerprint, generation)
	}
	return s.Store.StageEmbeddingMigration(id, vector, fingerprint, generation)
}

func (s *stubStore) ActivateEmbeddingMigration(generation string) error {
	if s.activateMigration != nil {
		return s.activateMigration(generation)
	}
	return s.Store.ActivateEmbeddingMigration(generation)
}

func (s *stubStore) GetMeta(key string) (string, error) {
	if s.getMeta != nil {
		return s.getMeta(key)
	}
	return s.Store.GetMeta(key)
}

func (s *stubStore) SetMeta(key, value string) error {
	if s.setMeta != nil {
		return s.setMeta(key, value)
	}
	return s.Store.SetMeta(key, value)
}

func (s *stubStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	if s.iterMemories != nil {
		return s.iterMemories(workspace, layer, typ)
	}
	return s.Store.IterMemories(workspace, layer, typ)
}

func (s *stubStore) PutMemory(mem *schema.Memory, vector []float32) error {
	if s.putMemory != nil {
		return s.putMemory(mem, vector)
	}
	return s.Store.PutMemory(mem, vector)
}

func (s *stubStore) Workspaces() ([]string, error) {
	if s.workspaces != nil {
		return s.workspaces()
	}
	return s.Store.Workspaces()
}

func TestEnforceEmbeddingDim_MetaReadError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	boom := errors.New("meta read boom")
	eng.Store = &stubStore{Store: eng.Store, getMeta: func(string) (string, error) {
		return "", boom
	}}

	if err := eng.enforceEmbeddingDim(); !errors.Is(err, boom) {
		t.Fatalf("enforceEmbeddingDim err = %v, want %v", err, boom)
	}
}

func TestEnforceEmbeddingDim_FreshStoreMetaWriteError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	boom := errors.New("meta write boom")
	eng.Store = &stubStore{
		Store:              eng.Store,
		getMeta:            func(string) (string, error) { return "", nil }, // fresh store
		migrationStatus:    func() (*schema.MigrationStatus, error) { return &schema.MigrationStatus{State: "idle"}, nil },
		initializeIdentity: func(schema.EmbeddingIdentity) error { return boom },
	}

	if err := eng.enforceEmbeddingDim(); !errors.Is(err, boom) {
		t.Fatalf("enforceEmbeddingDim err = %v, want %v", err, boom)
	}
}

func TestEnforceEmbeddingDim_DimChangeMetaWriteError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	eng.Config.EmbeddingAllowDimChange = true
	boom := errors.New("post-reembed meta write boom")
	eng.Store = &stubStore{
		Store: eng.Store,
		// A stored dim that never matches the provider forces the dim-change
		// path; the empty store makes reembedAll a no-op.
		getMeta: func(key string) (string, error) {
			if key == "embedding_dim" {
				return "9999", nil
			}
			return "", nil
		},
		activateMigration: func(string) error { return boom },
	}

	if err := eng.enforceEmbeddingDim(); !errors.Is(err, boom) {
		t.Fatalf("enforceEmbeddingDim err = %v, want %v", err, boom)
	}
}

func TestReembedAll_IterError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	boom := errors.New("iter boom")
	eng.Store = &stubStore{Store: eng.Store, migrationBatch: func(string, int) ([]*schema.Memory, error) {
		return nil, boom
	}}

	if err := eng.reembedAll(); !errors.Is(err, boom) {
		t.Fatalf("reembedAll err = %v, want %v", err, boom)
	}
}

func TestReembedAll_PutError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	if _, err := eng.Remember("memory doomed to fail re-embedding", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("put boom")
	eng.Store = &stubStore{Store: eng.Store, stageMigration: func(string, []float32, string, string) error {
		return boom
	}}

	if err := eng.reembedAll(); !errors.Is(err, boom) {
		t.Fatalf("reembedAll err = %v, want %v", err, boom)
	}
}

func TestStatsFor_IterMemoriesError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	boom := errors.New("stats iter boom")
	eng.Store = &stubStore{Store: eng.Store, iterMemories: func(string, string, string) ([]*schema.Memory, error) {
		return nil, boom
	}}

	if _, err := eng.StatsFor(""); !errors.Is(err, boom) {
		t.Fatalf("StatsFor err = %v, want %v", err, boom)
	}
}

func TestStatsFor_WorkspacesError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	boom := errors.New("workspaces boom")
	eng.Store = &stubStore{Store: eng.Store, workspaces: func() ([]string, error) {
		return nil, boom
	}}

	if _, err := eng.StatsFor(""); !errors.Is(err, boom) {
		t.Fatalf("StatsFor err = %v, want %v", err, boom)
	}
}
