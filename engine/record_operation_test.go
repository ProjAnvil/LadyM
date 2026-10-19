//go:build !enterprise

package engine

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

func TestRecordOperationSlotsAndEdges(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")

	ep, err := sc.RecordOperation(OperationEvent{
		Agent:     "a",
		Action:    "migrate",
		Resources: []string{"ladym.db"},
		Outcome:   "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("episode not persisted")
	}
	if ac := got.MetaString("action_class"); ac != "migrate" {
		t.Errorf("action_class = %q, want migrate", ac)
	}
	if raw := got.MetaString("action_raw"); raw != "migrate" {
		t.Errorf("action_raw = %q, want %q", raw, "migrate")
	}

	edges, err := eng.Store.Neighbors(ep.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("operates_on edges = %d, want 1", len(edges))
	}
	if edges[0].SrcID != ep.ID {
		t.Errorf("edge src = %s, want episode %s", edges[0].SrcID, ep.ID)
	}
	resID := edges[0].DstID
	if want := schema.ProjectedEdgeID(ep.ID, "operates_on", resID); edges[0].ID != want {
		t.Errorf("edge id = %s, want projected %s", edges[0].ID, want)
	}

	res, err := eng.Store.GetMemory(resID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Layer != schema.LayerSemantic || res.Type != schema.TypeResource {
		t.Errorf("resource layer/type = %s/%s, want %s/%s",
			res.Layer, res.Type, schema.LayerSemantic, schema.TypeResource)
	}

	// The resource entity is reused by a second operation on the same name.
	ep2, err := sc.RecordOperation(OperationEvent{
		Agent: "a", Action: "deploy", Resources: []string{"ladym.db"}, Outcome: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	edges2, err := eng.Store.Neighbors(ep2.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges2) != 1 || edges2[0].DstID != resID {
		t.Fatalf("second operation edges = %+v, want one edge to %s", edges2, resID)
	}
}

func TestRecordOperationMultiwordActionMiss(t *testing.T) {
	// Spec §5.5: normalization is exact-match; a multi-word action misses the
	// vocabulary, leaves action_class empty, and keeps action_raw for backfill.
	eng := newTestEngine(t)
	sc := eng.Scope("")
	ep, err := sc.RecordOperation(OperationEvent{
		Agent: "a", Action: "migrate schema", Resources: []string{"ladym.db"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ac := got.MetaString("action_class"); ac != "" {
		t.Errorf("action_class = %q, want empty (vocabulary miss)", ac)
	}
	if raw := got.MetaString("action_raw"); raw != "migrate schema" {
		t.Errorf("action_raw = %q, want %q", raw, "migrate schema")
	}
}

func TestRecordOperationExplicitActionClass(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	ep, err := sc.RecordOperation(OperationEvent{
		Agent:      "a",
		Action:     "frobnicate the widget",
		ActionClass: "configure",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := eng.Store.GetMemory(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ac := got.MetaString("action_class"); ac != "configure" {
		t.Errorf("action_class = %q, want configure (explicit)", ac)
	}
}

func TestRecordOperationNoResourcesNoEdges(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	ep, err := sc.RecordOperation(OperationEvent{Agent: "a", Action: "deploy nginx", Outcome: "success"})
	if err != nil {
		t.Fatal(err)
	}
	edges, err := eng.Store.Neighbors(ep.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("operates_on edges = %d, want 0", len(edges))
	}
}

func TestRecordEventContentFormatUnchanged(t *testing.T) {
	eng := newTestEngine(t)
	ep, err := eng.RecordEvent("claude", "fixed login bug", "auth was broken", "success", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "agent=claude | action=fixed login bug | observation=auth was broken | outcome=success"
	if ep.Content != want {
		t.Errorf("content = %q, want %q", ep.Content, want)
	}
	edges, err := eng.Store.Neighbors(ep.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 0 {
		t.Errorf("old RecordEvent must build no operates_on edges, got %d", len(edges))
	}
}

func TestEngineRecordOperationDelegates(t *testing.T) {
	eng := newTestEngine(t)
	ep, err := eng.RecordOperation(OperationEvent{Agent: "a", Action: "migrate schema", Resources: []string{"ladym.db"}})
	if err != nil {
		t.Fatal(err)
	}
	if ep.ID == "" {
		t.Fatal("engine RecordOperation returned empty id")
	}
	edges, err := eng.Store.Neighbors(ep.ID, "operates_on")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("operates_on edges = %d, want 1", len(edges))
	}
}
