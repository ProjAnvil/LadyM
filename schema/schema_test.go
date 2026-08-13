package schema

import "testing"

func TestContentHashGolden(t *testing.T) {
	// Golden value captured from Python's ladym.layers.semantic.content_hash.
	got := ContentHash("auth uses JWT with 24h expiry")
	want := "f0d62f8e497d333aee48761e52f72956"
	if got != want {
		t.Errorf("ContentHash = %s, want %s", got, want)
	}
}

func TestNewIDIsHex32(t *testing.T) {
	id := NewID()
	if len(id) != 32 {
		t.Errorf("NewID len = %d, want 32", len(id))
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("NewID contains non-hex char %q", c)
		}
	}
}

func TestMemoryTouch(t *testing.T) {
	m := NewMemory(LayerSemantic, TypeFact)
	before := m.AccessCount
	m.Touch()
	if m.AccessCount != before+1 {
		t.Errorf("AccessCount = %d, want %d", m.AccessCount, before+1)
	}
}
