package storage

import (
	"testing"
)

func TestVectorIndexDelete(t *testing.T) {
	ix := NewInMemoryVectorIndex(2)
	// Deleting from an empty index is a no-op.
	ix.Delete("missing")
	if ix.Len() != 0 {
		t.Errorf("Len = %d, want 0", ix.Len())
	}

	vecs := map[string][]float32{
		"a": {1, 0},
		"b": {0, 1},
		"c": {1, 1},
	}
	for id, v := range vecs {
		if err := ix.Upsert(id, v); err != nil {
			t.Fatal(err)
		}
	}
	if ix.Len() != 3 {
		t.Fatalf("Len = %d, want 3", ix.Len())
	}

	// Delete a middle element: the last element is swapped into its slot.
	ix.Delete("a")
	if ix.Len() != 2 {
		t.Fatalf("Len after delete = %d, want 2", ix.Len())
	}
	hits := ix.Search([]float32{1, 0}, 10)
	for _, h := range hits {
		if h.ID == "a" {
			t.Errorf("deleted id still in search results: %v", hits)
		}
	}
	// topK larger than the index size is clamped.
	if len(hits) != 2 {
		t.Errorf("len(hits) = %d, want 2 (topK clamped)", len(hits))
	}

	// Delete the last element (idx == last path).
	ix.Delete("c")
	if ix.Len() != 1 {
		t.Fatalf("Len after second delete = %d, want 1", ix.Len())
	}
	// Deleting the same id again is a no-op.
	ix.Delete("c")
	if ix.Len() != 1 {
		t.Errorf("Len after redundant delete = %d, want 1", ix.Len())
	}
}

func TestVectorIndexUpsertAndSearchEdges(t *testing.T) {
	ix := NewInMemoryVectorIndex(2)
	// Dim mismatch.
	if err := ix.Upsert("x", []float32{1, 2, 3}); err == nil {
		t.Error("expected dim-mismatch error")
	}
	// Empty index search returns nil.
	if hits := ix.Search([]float32{1, 0}, 5); hits != nil {
		t.Errorf("Search on empty index = %v, want nil", hits)
	}

	// Zero vector is stored un-normalised (norm == 0 branch).
	if err := ix.Upsert("zero", []float32{0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := ix.Upsert("one", []float32{3, 4}); err != nil {
		t.Fatal(err)
	}
	// Zero-vector query (norm == 0 branch in Search): similarities all 0,
	// tie broken by id.
	hits := ix.Search([]float32{0, 0}, 1)
	if len(hits) != 1 || hits[0].ID != "one" {
		t.Errorf("zero-query hits = %v, want [one]", hits)
	}

	// Upsert an existing id replaces the vector in place.
	if err := ix.Upsert("one", []float32{0, 5}); err != nil {
		t.Fatal(err)
	}
	if ix.Len() != 2 {
		t.Errorf("Len after re-upsert = %d, want 2", ix.Len())
	}
	hits = ix.Search([]float32{0, 1}, 2)
	if len(hits) != 2 || hits[0].ID != "one" {
		t.Errorf("hits after re-upsert = %v, want one first", hits)
	}
	if hits[0].Similarity < 0.999 {
		t.Errorf("similarity = %v, want ~1", hits[0].Similarity)
	}
}
