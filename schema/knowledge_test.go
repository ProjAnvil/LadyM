package schema

import "testing"

func TestKnowledgeEligibility(t *testing.T) {
	if IsUsable(nil) || IsRetired(nil) {
		t.Fatal("nil memory eligibility")
	}
	for _, tc := range []struct {
		meta            map[string]any
		retired, usable bool
	}{
		{nil, false, true},
		{map[string]any{"superseded_by": "new"}, true, false},
		{map[string]any{"superseded": true}, true, false},
		{map[string]any{"knowledge_status": "stale"}, false, false},
		{map[string]any{"knowledge_status": "rejected"}, false, false},
		{map[string]any{"evidence_missing": true}, false, false},
		{map[string]any{"evidence_missing": "false"}, false, true},
		{map[string]any{"knowledge_status": "inferred"}, false, true},
	} {
		m := NewMemory(LayerSemantic, TypeFact)
		m.Metadata = tc.meta
		if IsRetired(m) != tc.retired || IsUsable(m) != tc.usable {
			t.Fatalf("eligibility for %v", tc.meta)
		}
	}
}
