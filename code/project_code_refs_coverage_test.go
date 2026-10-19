//go:build !enterprise

package code

// Coverage-gap tests for indexer.go's projectCodeRefs (Task 22): the empty
// pass short-circuit, non-call ref kinds, missing endpoints, and the two
// failure paths that are recorded on the report instead of aborting. Store
// errors are injected with a stub wrapping the real store (same pattern as
// engine/store_error_branches_test.go).

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// codeStubStore delegates every Store method to the wrapped store except the
// ones a test has overridden with a hook.
type codeStubStore struct {
	storage.Store
	iterMemories func(workspace, layer, typ string) ([]*schema.Memory, error)
	putEdge      func(e *schema.Edge) error
}

func (s *codeStubStore) IterMemories(workspace, layer, typ string) ([]*schema.Memory, error) {
	if s.iterMemories != nil {
		return s.iterMemories(workspace, layer, typ)
	}
	return s.Store.IterMemories(workspace, layer, typ)
}

func (s *codeStubStore) PutEdge(e *schema.Edge) error {
	if s.putEdge != nil {
		return s.putEdge(e)
	}
	return s.Store.PutEdge(e)
}

// refFixtureEnv returns a store holding two code_symbol memories with
// qualified names svc.caller and svc.helper (ws "w"), plus its embedder.
func refFixtureEnv(t *testing.T) (storage.Store, *storage.HashingEmbedding, string, string) {
	t.Helper()
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "db.sqlite"), 64, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	emb := storage.NewHashingEmbedding(64)

	var callerID, helperID string
	for _, qname := range []string{"svc.caller", "svc.helper"} {
		m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
		m.Workspace = "w"
		m.Content = "def " + qname
		m.Metadata = map[string]any{"qualified_name": qname}
		vec, err := emb.Embed(m.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutMemory(m, vec); err != nil {
			t.Fatal(err)
		}
		if qname == "svc.caller" {
			callerID = m.ID
		} else {
			helperID = m.ID
		}
	}
	return store, emb, callerID, helperID
}

func TestProjectCodeRefsKindAndEndpointFilters(t *testing.T) {
	store, _, callerID, helperID := refFixtureEnv(t)
	report := &IndexReport{}

	// "calls" and "imports" are the only projectable kinds; "inherits" is
	// skipped, and a ref whose endpoint symbol has no memory is skipped too.
	refs := []RawRef{
		{SrcSymbol: "svc.caller", DstSymbol: "svc.helper", RefKind: "calls"},
		{SrcSymbol: "svc.caller", DstSymbol: "svc.helper", RefKind: "imports"},
		{SrcSymbol: "svc.caller", DstSymbol: "svc.helper", RefKind: "inherits"},
		{SrcSymbol: "svc.caller", DstSymbol: "svc.gone", RefKind: "calls"},
		{SrcSymbol: "svc.gone", DstSymbol: "svc.helper", RefKind: "calls"},
	}
	projectCodeRefs(store, "w", refs, report)

	if report.ProjectedEdges != 2 {
		t.Errorf("ProjectedEdges = %d, want 2 (calls + imports only)", report.ProjectedEdges)
	}
	if len(report.Errors) != 0 {
		t.Errorf("Errors = %v, want none", report.Errors)
	}
	calls, err := store.Neighbors(callerID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].DstID != helperID ||
		calls[0].ID != schema.ProjectedEdgeID(callerID, "calls", helperID) {
		t.Errorf("calls edges = %v, want one projected caller->helper edge", calls)
	}
	imports, err := store.Neighbors(callerID, "imports")
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 || imports[0].DstID != helperID {
		t.Errorf("imports edges = %v, want one caller->helper edge", imports)
	}
	// The non-projectable kind left no edge of its own relation behind.
	inh, err := store.Neighbors(callerID, "inherits")
	if err != nil {
		t.Fatal(err)
	}
	if len(inh) != 0 {
		t.Errorf("inherits edges = %v, want none (kind not projectable)", inh)
	}
}

func TestProjectCodeRefsEmptyShortCircuits(t *testing.T) {
	store, _, _, _ := refFixtureEnv(t)
	// A failing IterMemories proves the empty pass never touches the store:
	// the early return must leave the report untouched.
	boom := errors.New("iter boom")
	stub := &codeStubStore{Store: store, iterMemories: func(string, string, string) ([]*schema.Memory, error) {
		return nil, boom
	}}
	report := &IndexReport{}

	projectCodeRefs(stub, "w", nil, report)
	if len(report.Errors) != 0 || report.ProjectedEdges != 0 {
		t.Errorf("empty pass mutated the report: %+v", report)
	}
}

func TestProjectCodeRefsIterErrorRecordedOnReport(t *testing.T) {
	store, _, _, _ := refFixtureEnv(t)
	boom := errors.New("iter boom")
	stub := &codeStubStore{Store: store, iterMemories: func(string, string, string) ([]*schema.Memory, error) {
		return nil, boom
	}}
	report := &IndexReport{}

	projectCodeRefs(stub, "w", []RawRef{{SrcSymbol: "svc.caller", DstSymbol: "svc.helper", RefKind: "calls"}}, report)
	if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "code_refs projection:") {
		t.Fatalf("Errors = %v, want one code_refs projection entry", report.Errors)
	}
	if !strings.Contains(report.Errors[0], boom.Error()) {
		t.Errorf("Error entry %q does not carry the store error %q", report.Errors[0], boom.Error())
	}
	if report.ProjectedEdges != 0 {
		t.Errorf("ProjectedEdges = %d, want 0 on store failure", report.ProjectedEdges)
	}
}

func TestProjectCodeRefsPutEdgeErrorRecordedOnReport(t *testing.T) {
	store, _, callerID, _ := refFixtureEnv(t)
	boom := errors.New("put edge boom")
	stub := &codeStubStore{Store: store, putEdge: func(*schema.Edge) error { return boom }}
	report := &IndexReport{}

	projectCodeRefs(stub, "w", []RawRef{{SrcSymbol: "svc.caller", DstSymbol: "svc.helper", RefKind: "calls"}}, report)
	if len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "svc.caller") ||
		!strings.Contains(report.Errors[0], "svc.helper") {
		t.Fatalf("Errors = %v, want one entry naming src and dst symbols", report.Errors)
	}
	if report.ProjectedEdges != 0 {
		t.Errorf("ProjectedEdges = %d, want 0 on PutEdge failure", report.ProjectedEdges)
	}
	// Failures never abort the pass: nothing was written for this ref.
	if edges, err := store.Neighbors(callerID, "calls"); err != nil || len(edges) != 0 {
		t.Errorf("calls edges = %v (%v), want none behind the failing stub", edges, err)
	}
}
