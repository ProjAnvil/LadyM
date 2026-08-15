package engine

import (
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

func TestListNewestFirstExcludesRetired(t *testing.T) {
	eng := newTestEngine(t)
	ws := "list-test"
	eng.SetWorkspace(ws)
	m1, err := eng.Remember("older fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	// ensure strictly newer UpdatedAt than the first memory
	time.Sleep(10 * time.Millisecond)
	m2, err := eng.Remember("newer fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.List(ws, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d memories, want 2", len(got))
	}
	if got[0].ID != m2.ID {
		t.Fatalf("newest first: got %s want %s", got[0].ID, m2.ID)
	}

	// retired memories are excluded
	if err := operations.Retire(eng.Store, m1, ""); err != nil {
		t.Fatal(err)
	}
	got, err = eng.List(ws, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got {
		if m.ID == m1.ID {
			t.Fatalf("retired memory %s leaked into List", m1.ID)
		}
	}

	// a non-semantic (L1 episodic) memory appears unfiltered but is
	// excluded by the layer filter
	time.Sleep(10 * time.Millisecond)
	ev, err := eng.RecordEvent("t", "act", "obs", "success", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = eng.List(ws, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range got {
		if m.ID == ev.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("episodic event missing from unfiltered list")
	}

	sem := schema.LayerSemantic
	filtered, err := eng.List(ws, &sem, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range filtered {
		if m.Layer != schema.LayerSemantic {
			t.Fatalf("layer filter leaked %s", m.Layer)
		}
		if m.ID == ev.ID {
			t.Fatalf("layer filter should exclude episodic event %s", ev.ID)
		}
	}

	// pagination: live set is [ev, m2] newest first; limit=1 offset=1 -> m2
	page2, err := eng.List(ws, nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 {
		t.Fatalf("limit=1 offset=1 returned %d items, want exactly 1", len(page2))
	}
	if page2[0].ID != m2.ID {
		t.Fatalf("page2 id = %s, want older live memory %s", page2[0].ID, m2.ID)
	}

	// offset past the end returns an empty, non-nil slice
	past, err := eng.List(ws, nil, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 0 {
		t.Fatalf("offset past end returned %d items, want 0", len(past))
	}
	if past == nil {
		t.Fatal("offset past end returned nil slice, want empty non-nil")
	}
}

func TestListOffsetBounds(t *testing.T) {
	eng := newTestEngine(t)
	ws := "offset-test"
	eng.SetWorkspace(ws)
	for _, content := range []string{"fact one", "fact two", "fact three"} {
		if _, err := eng.Remember(content, schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cases := []struct {
		name          string
		limit, offset int
		wantLen       int
	}{
		{"negative offset clamps to zero", 10, -1, 3},
		{"negative limit and offset", -3, -2, 3},
		{"offset past end", 10, 99, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eng.List(ws, nil, tc.limit, tc.offset)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("List(limit=%d, offset=%d) returned %d items, want %d",
					tc.limit, tc.offset, len(got), tc.wantLen)
			}
			if got == nil {
				t.Fatal("List returned nil slice, want non-nil")
			}
		})
	}
}

func TestStatsForScopesWorkspace(t *testing.T) {
	eng := newTestEngine(t)
	eng.SetWorkspace("stats-ws")
	if _, err := eng.Remember("fact a", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
		t.Fatal(err)
	}
	st, err := eng.StatsFor("stats-other-ws")
	if err != nil {
		t.Fatal(err)
	}
	if st.TotalMemories != 0 {
		t.Fatalf("other workspace should be empty, got %d", st.TotalMemories)
	}
}
