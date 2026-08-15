package layers

import (
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// AssociativeMemory is L4 — the Zettelkasten graph.
type AssociativeMemory struct {
	Store *storage.SQLiteStore
}

// NewAssociativeMemory builds an AssociativeMemory.
func NewAssociativeMemory(store *storage.SQLiteStore) *AssociativeMemory {
	return &AssociativeMemory{Store: store}
}

// Link creates an edge between two memories. A nil weight defaults to 1.0; an
// explicitly supplied weight (including 0) is stored verbatim.
func (a *AssociativeMemory) Link(srcID, dstID, relation string, weight *float64, metadata map[string]any, validFrom, validTo *float64) (*schema.Edge, error) {
	if relation == "" {
		relation = "related_to"
	}
	w := 1.0
	if weight != nil {
		w = *weight
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	if validFrom == nil {
		vf := schema.Now()
		validFrom = &vf
	}
	e := &schema.Edge{
		ID:        schema.NewID(),
		SrcID:     srcID,
		Relation:  relation,
		DstID:     dstID,
		Weight:    w,
		ValidFrom: *validFrom,
		ValidTo:   validTo,
		Metadata:  metadata,
	}
	if err := a.Store.PutEdge(e); err != nil {
		return nil, err
	}
	return e, nil
}

// Neighbors returns valid edges touching memID.
func (a *AssociativeMemory) Neighbors(memID, relation string) ([]*schema.Edge, error) {
	return a.Store.Neighbors(memID, relation)
}

// NeighborCounts returns {memory_id: neighbour_count}.
func (a *AssociativeMemory) NeighborCounts() (map[string]int, error) {
	return a.Store.NeighborCounts()
}

// Retire marks an edge no longer current (sets valid_to).
func (a *AssociativeMemory) Retire(edgeID string, when *float64) error {
	t := schema.Now()
	if when != nil {
		t = *when
	}
	_, err := a.Store.DB().Exec("UPDATE edges SET valid_to = ? WHERE id = ?", t, edgeID)
	return err
}
