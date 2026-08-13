package storage

import (
	"fmt"
	"math"
	"sort"
)

// SearchHit is one (id, similarity) pair returned by Search.
type SearchHit struct {
	ID         string
	Similarity float64
}

// VectorIndex inserts/queries/deletes vectors keyed by an arbitrary id.
type VectorIndex interface {
	Upsert(itemID string, vector []float32) error
	Search(query []float32, topK int) []SearchHit
	Delete(itemID string)
	Len() int
}

// InMemoryVectorIndex is a brute-force cosine index (deterministic, perfect for
// tests and small workspaces). It replaces the sqlite-vec extension used by the
// Python port with a pure-Go equivalent; vectors are still persisted as BLOBs
// in the store so a reopened store can rebuild the index.
type InMemoryVectorIndex struct {
	dim     int
	ids     []string
	idToIdx map[string]int
	mat     [][]float32 // shape (N, dim), L2-normalised
}

// NewInMemoryVectorIndex returns an empty index of the given dim.
func NewInMemoryVectorIndex(dim int) *InMemoryVectorIndex {
	return &InMemoryVectorIndex{dim: dim, idToIdx: map[string]int{}}
}

func l2Norm(vec []float32) float64 {
	var n float64
	for _, v := range vec {
		n += float64(v) * float64(v)
	}
	return math.Sqrt(n)
}

func (ix *InMemoryVectorIndex) Upsert(itemID string, vector []float32) error {
	if len(vector) != ix.dim {
		return fmt.Errorf("vector dim %d != index dim %d", len(vector), ix.dim)
	}
	vec := append([]float32{}, vector...)
	if n := l2Norm(vec); n > 0 {
		for i := range vec {
			vec[i] = float32(float64(vec[i]) / n)
		}
	}
	if idx, ok := ix.idToIdx[itemID]; ok {
		ix.mat[idx] = vec
		return nil
	}
	ix.mat = append(ix.mat, vec)
	ix.idToIdx[itemID] = len(ix.ids)
	ix.ids = append(ix.ids, itemID)
	return nil
}

func (ix *InMemoryVectorIndex) Search(query []float32, topK int) []SearchHit {
	if len(ix.ids) == 0 {
		return nil
	}
	q := append([]float32{}, query...)
	if n := l2Norm(q); n > 0 {
		for i := range q {
			q[i] = float32(float64(q[i]) / n)
		}
	}
	type scored struct {
		id  string
		sim float64
	}
	all := make([]scored, 0, len(ix.ids))
	for i, vec := range ix.mat {
		var dot float64
		for j := range vec {
			dot += float64(vec[j]) * float64(q[j])
		}
		all = append(all, scored{ix.ids[i], dot})
	}
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].sim != all[b].sim {
			return all[a].sim > all[b].sim
		}
		return all[a].id < all[b].id
	})
	k := topK
	if k > len(all) {
		k = len(all)
	}
	out := make([]SearchHit, 0, k)
	for _, s := range all[:k] {
		out = append(out, SearchHit{ID: s.id, Similarity: s.sim})
	}
	return out
}

func (ix *InMemoryVectorIndex) Delete(itemID string) {
	idx, ok := ix.idToIdx[itemID]
	if !ok {
		return
	}
	delete(ix.idToIdx, itemID)
	last := len(ix.ids) - 1
	if idx != last {
		ix.mat[idx] = ix.mat[last]
		swappedID := ix.ids[last]
		ix.ids[idx] = swappedID
		ix.idToIdx[swappedID] = idx
	}
	ix.ids = ix.ids[:last]
	ix.mat = ix.mat[:last]
}

func (ix *InMemoryVectorIndex) Len() int { return len(ix.ids) }
