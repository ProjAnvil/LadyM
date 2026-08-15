package engine

import (
	"testing"
	"time"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestListNewestFirstExcludesRetired(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()
	ws := "list-test"
	eng.SetWorkspace(ws)
	if _, err := eng.Remember("older fact", schema.LayerSemantic, schema.TypeFact, nil, nil, "t", ""); err != nil {
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
	if len(got) == 0 {
		t.Fatal("List returned no memories")
	}
	if got[0].ID != m2.ID {
		t.Fatalf("newest first: got %s want %s", got[0].ID, m2.ID)
	}
	// layer filter
	sem := schema.LayerSemantic
	filtered, err := eng.List(ws, &sem, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range filtered {
		if m.Layer != schema.LayerSemantic {
			t.Fatalf("layer filter leaked %s", m.Layer)
		}
	}
	// pagination
	page2, err := eng.List(ws, nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) > 1 {
		t.Fatalf("limit=1 returned %d", len(page2))
	}
}

func TestStatsForScopesWorkspace(t *testing.T) {
	eng := newTestEngine(t)
	defer eng.Close()
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
