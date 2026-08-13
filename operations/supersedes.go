package operations

import (
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// IsRetired reports whether mem was retired by an UPDATE or DELETE
// consolidation pass. A nil memory is treated as not-retired.
func IsRetired(mem *schema.Memory) bool {
	if mem == nil {
		return false
	}
	return mem.MetaString("superseded_by") != "" || mem.MetaBool("superseded")
}

// Retire retires old. When newID is non-empty it writes a supersedes edge
// old→new (UPDATE chain); otherwise it sets superseded=true (DELETE). Outgoing
// still-valid edges of old are closed so the graph does not leak through a
// retired node.
func Retire(store *storage.SQLiteStore, old *schema.Memory, newID string) error {
	now := schema.Now()
	if old.Metadata == nil {
		old.Metadata = map[string]any{}
	}
	old.Metadata["superseded_at"] = now
	if newID != "" {
		old.Metadata["superseded_by"] = newID
	} else {
		old.Metadata["superseded"] = true
	}
	neighbors, err := store.Neighbors(old.ID, "")
	if err != nil {
		return err
	}
	for _, e := range neighbors {
		if e.ValidTo == nil {
			e.ValidTo = &now
			if err := store.PutEdge(e); err != nil {
				return err
			}
		}
	}
	if newID != "" {
		e := schema.NewEdge(old.ID, "supersedes", newID)
		e.ValidFrom = now
		if err := store.PutEdge(e); err != nil {
			return err
		}
	}
	return store.PutMemory(old, nil)
}

// LatestInChain walks supersedes edges forward and returns the newest version id.
func LatestInChain(store *storage.SQLiteStore, memID string) (string, error) {
	seen := map[string]bool{}
	cur := memID
	for !seen[cur] {
		seen[cur] = true
		neighbors, err := store.Neighbors(cur, "supersedes")
		if err != nil {
			return cur, err
		}
		var best *schema.Edge
		for _, e := range neighbors {
			if e.SrcID != cur {
				continue
			}
			if best == nil || e.ValidFrom > best.ValidFrom {
				best = e
			}
		}
		if best == nil {
			return cur, nil
		}
		cur = best.DstID
	}
	return cur, nil
}
