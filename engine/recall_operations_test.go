//go:build !enterprise

package engine

import (
	"errors"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// recordOpsFixture records three operations on a fresh scope: two migrates on
// ladym.db (one success, one fail) and one deploy on nginx.
func recordOpsFixture(t *testing.T, sc *Scope) (epMigrateOK, epMigrateFail, epDeploy *schema.Memory) {
	t.Helper()
	epMigrateOK, err := sc.RecordOperation(OperationEvent{
		Agent: "a", Action: "migrate", Resources: []string{"ladym.db"},
		Observation: "schema v1 -> v2", Outcome: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	epMigrateFail, err = sc.RecordOperation(OperationEvent{
		Agent: "a", Action: "migrate", Resources: []string{"ladym.db"},
		Observation: "locked table", Outcome: "fail",
	})
	if err != nil {
		t.Fatal(err)
	}
	epDeploy, err = sc.RecordOperation(OperationEvent{
		Agent: "a", Action: "deploy", Resources: []string{"nginx"},
		Observation: "rolled out", Outcome: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	return epMigrateOK, epMigrateFail, epDeploy
}

func TestRecallOperationsPivotFilterAndOrder(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	epOK, epFail, _ := recordOpsFixture(t, sc)

	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Action: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fallback {
		t.Error("Fallback = true on resolved resource, want false")
	}
	if resp.Resource == nil || resp.Resource.Type != schema.TypeResource {
		t.Fatalf("Resource = %+v, want resolved resource entity", resp.Resource)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2 (migrate ops on ladym.db)", len(resp.Items))
	}
	got := map[string]bool{}
	for i, it := range resp.Items {
		if it.Memory == nil {
			t.Fatalf("item %d has nil memory", i)
		}
		if ac := it.Memory.MetaString("action_class"); ac != "migrate" {
			t.Errorf("item %d action_class = %q, want migrate", i, ac)
		}
		if out := it.Memory.MetaString("outcome"); out == "" {
			t.Errorf("item %d has empty outcome slot", i)
		}
		if i > 0 && resp.Items[i-1].Score < it.Score {
			t.Errorf("items not sorted by activation: %v then %v",
				resp.Items[i-1].Score, it.Score)
		}
		got[it.Memory.ID] = true
	}
	if !got[epOK.ID] || !got[epFail.ID] {
		t.Errorf("returned ids = %v, want both %s and %s", got, epOK.ID, epFail.ID)
	}
}

func TestRecallOperationsEmptyActionReturnsAllForResource(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	recordOpsFixture(t, sc)

	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fallback {
		t.Error("Fallback = true on resolved resource, want false")
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2 (all ladym.db ops)", len(resp.Items))
	}
}

func TestRecallOperationsAliasResolution(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	ep, err := sc.RecordOperation(OperationEvent{
		Agent: "a", Action: "migrate", Resources: []string{"postgres primary db"},
		Outcome: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	edges, err := eng.Store.Neighbors(ep.ID, "operates_on")
	if err != nil || len(edges) != 1 {
		t.Fatalf("operates_on edges: %v, %v", edges, err)
	}
	resID := edges[0].DstID

	// Hashing embeddings do not reach the default 0.75 read threshold for
	// near-neighbour names, so lower it to just under the real cosine to
	// exercise the read-side vector branch (same fallback as Task 7's test).
	va, _ := eng.Provider.Embed("postgres primary db")
	vb, _ := eng.Provider.Embed("postgres primary database")
	cos := storage.CosineSimilarity(va, vb)
	if cos <= 0.01 {
		t.Fatalf("cosine between test names too low: %f", cos)
	}
	eng.Config.Resource.ReadThreshold = cos - 0.01

	resp, err := sc.RecallOperations(OperationsQuery{Resource: "postgres primary database"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fallback {
		t.Fatal("Fallback = true, want vector resolution to hit")
	}
	if resp.Resource == nil || resp.Resource.ID != resID {
		t.Fatalf("resolved resource = %+v, want id %s", resp.Resource, resID)
	}
	if len(resp.Items) != 1 || resp.Items[0].Memory.ID != ep.ID {
		t.Fatalf("items = %+v, want the recorded episode %s", resp.Items, ep.ID)
	}
}

func TestRecallOperationsTimeWindow(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	epOK, epFail, _ := recordOpsFixture(t, sc)

	// Age the first episode by 2h directly in the DB (episode IDs are opaque).
	backdoorExec(t, eng.Config.DBPath,
		"UPDATE memories SET created_at = created_at - 7200 WHERE id = ?", epOK.ID)
	now := schema.Now()

	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Since: now - 3600})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Memory.ID != epFail.ID {
		t.Fatalf("Since items = %+v, want only the fresh episode %s", resp.Items, epFail.ID)
	}

	resp, err = sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Until: now - 3600})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Memory.ID != epOK.ID {
		t.Fatalf("Until items = %+v, want only the aged episode %s", resp.Items, epOK.ID)
	}
}

func TestRecallOperationsOutcomeFilter(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	_, epFail, _ := recordOpsFixture(t, sc)

	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Outcome: "fail"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Memory.ID != epFail.ID {
		t.Fatalf("items = %+v, want only the failed op %s", resp.Items, epFail.ID)
	}
	if out := resp.Items[0].Memory.MetaString("outcome"); out != "fail" {
		t.Errorf("outcome slot = %q, want fail", out)
	}
}

func TestRecallOperationsResourceMissFallsBack(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	recordOpsFixture(t, sc)

	resp, err := sc.RecallOperations(OperationsQuery{Resource: "nonexistent-xyz", Action: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Fallback {
		t.Error("Fallback = false on unresolved resource, want true")
	}
	if resp.Resource != nil {
		t.Errorf("Resource = %s, want nil on fallback", resp.Resource.ID)
	}
	for i, it := range resp.Items {
		if it == nil || it.Memory == nil {
			t.Fatalf("fallback item %d is nil", i)
		}
	}
}

func TestRecallOperationsColdStartFallsBack(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Action: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Fallback {
		t.Error("Fallback = false on cold start, want true")
	}
	if resp.Resource != nil {
		t.Errorf("Resource = %s, want nil on cold start", resp.Resource.ID)
	}
}

func TestRecallOperationsZeroLLMReadPath(t *testing.T) {
	// Hard constraint: the recall_ops read path never touches an LLM. The
	// test engine runs with LLMProvider "none" (no agents can be built), so
	// completing the pivot query here is the assertion.
	eng := newTestEngine(t)
	if eng.Config.LLMProvider != "none" {
		t.Fatalf("test engine LLMProvider = %q, want none", eng.Config.LLMProvider)
	}
	sc := eng.Scope("")
	recordOpsFixture(t, sc)
	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Action: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fallback || len(resp.Items) != 2 {
		t.Errorf("resp = fallback:%v items:%d, want pivot hit with 2 items",
			resp.Fallback, len(resp.Items))
	}
}

func TestEngineRecallOperationsDelegates(t *testing.T) {
	eng := newTestEngine(t)
	recordOpsFixture(t, eng.Scope(""))
	resp, err := eng.RecallOperations(OperationsQuery{Resource: "ladym.db", Action: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Fallback || len(resp.Items) != 2 {
		t.Errorf("resp = fallback:%v items:%d, want pivot hit with 2 items",
			resp.Fallback, len(resp.Items))
	}
}

func TestRecallOperationsLimit(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	recordOpsFixture(t, sc)
	resp, err := sc.RecallOperations(OperationsQuery{Resource: "ladym.db", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Errorf("items = %d, want Limit truncation to 1", len(resp.Items))
	}
}

// A whitespace-only resource carries no value — the pivot has no default —
// so it errors exactly like an empty one (all front-ends route through here,
// so MCP/CLI/HTTP stay unified on this).
func TestRecallOperationsBlankResource(t *testing.T) {
	eng := newTestEngine(t)
	sc := eng.Scope("")
	_, err := sc.RecallOperations(OperationsQuery{Resource: "   "})
	if err == nil || !errors.Is(err, ErrResourceRequired) {
		t.Errorf("blank-resource recall err = %v, want ErrResourceRequired", err)
	}
}
