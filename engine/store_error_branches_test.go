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
	getMeta      func(key string) (string, error)
	setMeta      func(key, value string) error
	iterMemories func(workspace, layer, typ string) ([]*schema.Memory, error)
	putMemory    func(mem *schema.Memory, vector []float32) error
	workspaces   func() ([]string, error)
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
		Store:   eng.Store,
		getMeta: func(string) (string, error) { return "", nil }, // fresh store
		setMeta: func(string, string) error { return boom },
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
		setMeta: func(string, string) error { return boom },
	}

	if err := eng.enforceEmbeddingDim(); !errors.Is(err, boom) {
		t.Fatalf("enforceEmbeddingDim err = %v, want %v", err, boom)
	}
}

func TestReembedAll_IterError_Propagates(t *testing.T) {
	eng := newTestEngine(t)
	boom := errors.New("iter boom")
	eng.Store = &stubStore{Store: eng.Store, iterMemories: func(string, string, string) ([]*schema.Memory, error) {
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
	eng.Store = &stubStore{Store: eng.Store, putMemory: func(*schema.Memory, []float32) error {
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
