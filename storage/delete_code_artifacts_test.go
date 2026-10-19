//go:build !enterprise

package storage

import (
	"testing"

	"github.com/ProjAnvil/LadyM/schema"
)

// putArtifactSymbol seeds one code_symbol memory plus its code_symbols
// projection row, mirroring the indexer's write shape.
func putArtifactSymbol(t *testing.T, s Store, ws, file, qname string) *schema.Memory {
	t.Helper()
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeSymbol)
	m.Content = "function " + qname
	m.Summary = "function " + qname
	m.Tags = []string{"code", "python"}
	m.Source = file
	m.Workspace = ws
	vec := make([]float32, suiteDim)
	for i := range vec {
		vec[i] = 1
	}
	suitePutMemory(t, s, m, vec)
	if err := s.PutCodeSymbol(&schema.CodeSymbol{
		MemoryID: m.ID, FilePath: file, SymbolKind: "function",
		QualifiedName: qname, LineStart: 1, LineEnd: 2, Language: "python",
	}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDeleteCodeArtifactsByFile(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		ws := "test"
		a1 := putArtifactSymbol(t, s, ws, "svc.py", "svc.alpha")
		a2 := putArtifactSymbol(t, s, ws, "svc.py", "svc.beta")
		b1 := putArtifactSymbol(t, s, ws, "other.py", "other.gamma")
		b2 := putArtifactSymbol(t, s, ws, "other.py", "other.delta")

		if err := s.PutCodeRefs([]*schema.CodeRef{
			{SrcSymbol: "svc.alpha", DstSymbol: "other.gamma", RefKind: "calls"}, // src side in file
			{SrcSymbol: "other.gamma", DstSymbol: "svc.beta", RefKind: "calls"},  // dst side in file
			{SrcSymbol: "other.delta", DstSymbol: "other.gamma", RefKind: "calls"},
		}); err != nil {
			t.Fatal(err)
		}

		if err := s.DeleteCodeArtifactsByFile(ws, "svc.py"); err != nil {
			t.Fatal(err)
		}

		// The file's symbol memories are gone; the other file's survive.
		for _, id := range []string{a1.ID, a2.ID} {
			got, err := s.GetMemory(id)
			if err != nil {
				t.Fatal(err)
			}
			if got != nil {
				t.Errorf("symbol memory %s survived the delete", id)
			}
		}
		if got, err := s.GetMemory(b1.ID); err != nil || got == nil {
			t.Errorf("unrelated symbol b1 = %v, %v; want present", got, err)
		}
		if got, err := s.GetMemory(b2.ID); err != nil || got == nil {
			t.Errorf("unrelated symbol b2 = %v, %v; want present", got, err)
		}

		// code_symbols projection rows: file's gone, other file's intact.
		if syms, err := s.SymbolsForFile("svc.py"); err != nil {
			t.Fatal(err)
		} else if len(syms) != 0 {
			t.Errorf("code_symbols rows for svc.py = %d, want 0", len(syms))
		}
		if syms, err := s.SymbolsForFile("other.py"); err != nil {
			t.Fatal(err)
		} else if len(syms) != 2 {
			t.Errorf("code_symbols rows for other.py = %d, want 2", len(syms))
		}

		// code_refs touching the file's symbols in either direction are gone;
		// the ref between unrelated symbols survives.
		refs, err := s.RefsForSymbol("other.gamma", "both")
		if err != nil {
			t.Fatal(err)
		}
		if len(refs) != 1 || refs[0].SrcSymbol != "other.delta" {
			t.Errorf("refs touching other.gamma = %+v, want only other.delta→other.gamma", refs)
		}

		// Vector index sync: deleted ids are no longer searchable, survivors
		// still are.
		q := make([]float32, suiteDim)
		for i := range q {
			q[i] = 1
		}
		seen := map[string]bool{}
		for _, h := range s.VectorSearch(q, 10) {
			seen[h.ID] = true
		}
		if seen[a1.ID] || seen[a2.ID] {
			t.Error("vector search returned a deleted symbol id; in-process index not synced")
		}
		if !seen[b1.ID] || !seen[b2.ID] {
			t.Error("vector search lost a surviving symbol id")
		}
	})
}

func TestDeleteCodeArtifactsByFileUnknownFileIsNoop(t *testing.T) {
	runStoreBackends(t, func(t *testing.T, s Store) {
		ws := "test"
		putArtifactSymbol(t, s, ws, "other.py", "other.gamma")

		if err := s.DeleteCodeArtifactsByFile(ws, "missing.py"); err != nil {
			t.Fatalf("deleting an unknown file should be a no-op, got %v", err)
		}
		if syms, err := s.SymbolsForFile("other.py"); err != nil {
			t.Fatal(err)
		} else if len(syms) != 1 {
			t.Errorf("code_symbols rows for other.py = %d, want 1 (untouched)", len(syms))
		}
	})
}
