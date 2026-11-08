package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestEngineContextSharedWorkingMemory(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		first := e.WithContext(t.Context())
		second := first.WithContext(t.Context())
		engines := []*Engine{e, first, second}
		for _, workspace := range []string{"", "alpha", "beta"} {
			t.Run(workspace, func(t *testing.T) {
				for i, view := range engines {
					scope := view.Scope(workspace)
					if scope.Working == nil {
						t.Fatal("context view lost default L0 buffer")
					}
					note, err := scope.Remember(fmt.Sprintf("shared scratch note %d", i), schema.LayerWorking, schema.TypeNote, nil, nil, "test", "")
					if err != nil {
						t.Fatal(err)
					}
					for _, reader := range engines {
						working := reader.Scope(workspace).Working
						if working == nil {
							t.Fatal("context view lost default L0 buffer")
						}
						items := working.Items()
						if len(items) != i+1 || items[i].ID != note.ID {
							t.Fatalf("context view lost shared L0 write: got %d items, want %d", len(items), i+1)
						}
					}
				}
			})
		}
		if e.Scope("alpha").Working == e.Scope("beta").Working || e.Scope("alpha").Working == e.Working {
			t.Fatal("workspaces share an L0 buffer")
		}
		for _, workspace := range []string{"", "alpha", "beta"} {
			for _, view := range engines {
				if got := view.Scope(workspace).Working.Len(); got != 3 {
					t.Fatalf("workspace %q contaminated: %d notes", workspace, got)
				}
			}
		}
	})
}

func TestEngineContextConcurrentWorkingMemory(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		engines := []*Engine{e, e.WithContext(t.Context()), e.WithContext(t.Context())}
		var wg sync.WaitGroup
		for _, workspace := range []string{"", "alpha", "beta"} {
			for i, view := range engines {
				wg.Go(func() {
					for n := range 10 {
						scope := view.Scope(workspace)
						if scope.Working == nil {
							t.Error("context view lost default L0 buffer")
							return
						}
						_, err := scope.Remember(fmt.Sprintf("concurrent scratch %d %d", i, n), schema.LayerWorking, schema.TypeNote, nil, nil, "test", "")
						if err != nil {
							t.Error(err)
							return
						}
						_ = scope.Working.Items()
					}
				})
			}
		}
		wg.Wait()
		for _, workspace := range []string{"", "alpha", "beta"} {
			for _, view := range engines {
				working := view.Scope(workspace).Working
				if working == nil {
					t.Error("context view lost default L0 buffer")
					continue
				}
				if got := working.Len(); got != 30 {
					t.Errorf("workspace %q lost concurrent writes: got %d, want 30", workspace, got)
				}
			}
		}
	})
}

func TestEngineContextStoreIsolation(t *testing.T) {
	runClaimBackends(t, func(t *testing.T, e *Engine) {
		ctx, cancel := context.WithCancel(t.Context())
		first := e.WithContext(ctx)
		second := first.WithContext(t.Context())
		cancel()
		if err := first.Store.Ping(); err == nil {
			t.Fatal("cancelled view still performs database I/O")
		}
		if err := second.Store.Ping(); err != nil {
			t.Fatalf("context leaked into sibling view: %v", err)
		}
		if err := e.Store.Ping(); err != nil {
			t.Fatalf("context leaked into root: %v", err)
		}
	})
}
