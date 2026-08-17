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
	beforeAccess := m.LastAccessAt
	m.Touch()
	if m.AccessCount != before+1 {
		t.Errorf("AccessCount = %d, want %d", m.AccessCount, before+1)
	}
	if m.LastAccessAt < beforeAccess {
		t.Errorf("LastAccessAt = %f, want >= %f", m.LastAccessAt, beforeAccess)
	}
}

func TestMeta(t *testing.T) {
	m := NewMemory(LayerSemantic, TypeFact)
	m.Metadata["key"] = "value"
	if got := m.Meta("key"); got != "value" {
		t.Errorf("Meta(key) = %v, want value", got)
	}
	if got := m.Meta("missing"); got != nil {
		t.Errorf("Meta(missing) = %v, want nil", got)
	}
	var nilMeta Memory
	if got := nilMeta.Meta("key"); got != nil {
		t.Errorf("Meta on nil Metadata = %v, want nil", got)
	}
}

func TestMetaString(t *testing.T) {
	m := NewMemory(LayerSemantic, TypeFact)
	m.Metadata["s"] = "hello"
	m.Metadata["i"] = 42
	cases := []struct {
		key  string
		want string
	}{
		{"s", "hello"},
		{"i", ""},       // non-string yields ""
		{"missing", ""}, // absent key yields ""
	}
	for _, c := range cases {
		if got := m.MetaString(c.key); got != c.want {
			t.Errorf("MetaString(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestMetaBool(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string nonempty", "x", true},
		{"string empty", "", false},
		{"float nonzero", 1.5, true},
		{"float zero", float64(0), false},
		{"int nonzero", 3, true},
		{"int zero", 0, false},
		{"other non-nil", []string{"a"}, true},
		{"nil", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewMemory(LayerSemantic, TypeFact)
			if c.val != nil {
				m.Metadata["k"] = c.val
			} else {
				m.Metadata["k"] = nil
			}
			if got := m.MetaBool("k"); got != c.want {
				t.Errorf("MetaBool = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("missing key", func(t *testing.T) {
		m := NewMemory(LayerSemantic, TypeFact)
		if m.MetaBool("nope") {
			t.Error("MetaBool(missing) = true, want false")
		}
	})
}

func TestMetaFloat(t *testing.T) {
	cases := []struct {
		name   string
		val    any
		want   float64
		wantOK bool
	}{
		{"float64", 2.5, 2.5, true},
		{"int", 7, 7.0, true},
		{"int64", int64(9), 9.0, true},
		{"numeric string", "3.14", 3.14, true},
		{"non-numeric string", "abc", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewMemory(LayerSemantic, TypeFact)
			m.Metadata["k"] = c.val
			got, ok := m.MetaFloat("k")
			if ok != c.wantOK {
				t.Fatalf("MetaFloat ok = %v, want %v", ok, c.wantOK)
			}
			if got != c.want {
				t.Errorf("MetaFloat = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("missing key", func(t *testing.T) {
		m := NewMemory(LayerSemantic, TypeFact)
		if _, ok := m.MetaFloat("nope"); ok {
			t.Error("MetaFloat(missing) ok = true, want false")
		}
	})
}

func TestClone(t *testing.T) {
	m := NewMemory(LayerEpisodic, TypeEvent)
	m.Content = "original"
	m.Tags = []string{"a", "b"}
	m.Metadata["k"] = "v"

	c := m.Clone()

	// Scalar fields are copied.
	if c.ID != m.ID || c.Content != m.Content || c.Layer != m.Layer || c.Type != m.Type {
		t.Error("Clone did not copy scalar fields")
	}
	// Mutating the clone must not affect the original.
	c.Tags[0] = "changed"
	c.Metadata["k"] = "changed"
	if m.Tags[0] != "a" {
		t.Errorf("original Tags mutated: %v", m.Tags)
	}
	if m.Metadata["k"] != "v" {
		t.Errorf("original Metadata mutated: %v", m.Metadata)
	}
}

func TestCloneNilMetadata(t *testing.T) {
	m := &Memory{ID: "x"} // Metadata and Tags are nil
	c := m.Clone()
	if c.ID != "x" {
		t.Errorf("Clone ID = %q, want x", c.ID)
	}
	if c.Metadata != nil {
		t.Errorf("Clone Metadata = %v, want nil", c.Metadata)
	}
	if len(c.Tags) != 0 {
		t.Errorf("Clone Tags = %v, want empty", c.Tags)
	}
}

func TestNewEdge(t *testing.T) {
	e := NewEdge("src", "related_to", "dst")
	if e.SrcID != "src" || e.Relation != "related_to" || e.DstID != "dst" {
		t.Errorf("NewEdge fields = %+v", e)
	}
	if len(e.ID) != 32 {
		t.Errorf("NewEdge ID len = %d, want 32", len(e.ID))
	}
	if e.Weight != 1.0 {
		t.Errorf("NewEdge Weight = %f, want 1.0", e.Weight)
	}
	if e.ValidFrom <= 0 {
		t.Errorf("NewEdge ValidFrom = %f, want > 0", e.ValidFrom)
	}
	if e.ValidTo != nil {
		t.Errorf("NewEdge ValidTo = %v, want nil", e.ValidTo)
	}
	if e.Metadata == nil {
		t.Error("NewEdge Metadata = nil, want initialized map")
	}
}

func TestNewMemoryDefaults(t *testing.T) {
	m := NewMemory(LayerWorking, TypeNote)
	if m.Layer != LayerWorking || m.Type != TypeNote {
		t.Errorf("NewMemory layer/type = %v/%v", m.Layer, m.Type)
	}
	if m.Workspace != "default" {
		t.Errorf("Workspace = %q, want default", m.Workspace)
	}
	if m.Tags == nil || len(m.Tags) != 0 {
		t.Errorf("Tags = %v, want empty non-nil slice", m.Tags)
	}
	if m.Metadata == nil {
		t.Error("Metadata = nil, want initialized map")
	}
	if m.CreatedAt != m.UpdatedAt || m.CreatedAt != m.LastAccessAt {
		t.Error("timestamps should all be equal at creation")
	}
	if len(m.ID) != 32 {
		t.Errorf("ID len = %d, want 32", len(m.ID))
	}
}

func TestNowIsRecent(t *testing.T) {
	got := Now()
	if got <= 0 {
		t.Errorf("Now() = %f, want positive", got)
	}
}
