//go:build !enterprise

package engine

// Scope workspace-binding tests: the per-workspace L0 buffer cache, the
// default-workspace fallback, and the Workspace accessor.

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestScope_NonDefaultWorkspace_GetsOwnCachedWorkingBuffer(t *testing.T) {
	eng := newTestEngine(t)

	s1 := eng.Scope("alpha")
	if got := s1.Workspace(); got != "alpha" {
		t.Errorf("Workspace() = %q, want alpha", got)
	}
	if s1.Working == eng.Working {
		t.Error("non-default scope must not share the engine's default L0 buffer")
	}

	// A second Scope for the same workspace reuses the cached buffer.
	s2 := eng.Scope("alpha")
	if s1.Working != s2.Working {
		t.Error("repeated Scope(alpha) should share the cached L0 buffer")
	}

	// Writes through the scope land in the scoped buffer only.
	if _, err := s1.Remember("scoped scratch note", schema.LayerWorking, schema.TypeNote, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	if s1.Working.Len() != 1 {
		t.Errorf("scoped working len = %d, want 1", s1.Working.Len())
	}
	if eng.Working.Len() != 0 {
		t.Errorf("default working len = %d, want 0 (scopes are isolated)", eng.Working.Len())
	}
}

func TestScope_EmptyWorkspace_FallsBackToEngineDefault(t *testing.T) {
	eng := newTestEngine(t)

	s := eng.Scope("")
	if got := s.Workspace(); got != eng.Config.Workspace {
		t.Errorf("Workspace() = %q, want engine default %q", got, eng.Config.Workspace)
	}
	if s.Working != eng.Working {
		t.Error("default-workspace scope should share the engine's L0 buffer")
	}
}
