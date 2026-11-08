package operations

import (
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// IsRetired reports whether mem was retired by an UPDATE or DELETE
// consolidation pass. A nil memory is treated as not-retired.
func IsRetired(mem *schema.Memory) bool {
	return schema.IsRetired(mem)
}

// Retire retires old. When newID is non-empty it writes a supersedes edge
// old→new (UPDATE chain); otherwise it sets superseded=true (DELETE). Outgoing
// still-valid edges of old are closed so the graph does not leak through a
// retired node.
func Retire(store storage.Store, old *schema.Memory, newID string) error {
	batch := storage.MemoryBatch{}
	stageRetirement(&batch, old, newID)
	if err := store.ApplyMemoryBatch(batch); err != nil {
		return err
	}
	old.Metadata = batch.Writes[0].Memory.Metadata
	return nil
}

func stageRetirement(batch *storage.MemoryBatch, old *schema.Memory, newID string) {
	guardMemory(batch, old)
	now := schema.Now()
	retired := old.Clone()
	if retired.Metadata == nil {
		retired.Metadata = map[string]any{}
	}
	retired.Metadata["superseded_at"] = now
	if newID != "" {
		retired.Metadata["superseded_by"] = newID
	} else {
		retired.Metadata["superseded"] = true
	}
	batch.CloseEdgesFor = append(batch.CloseEdgesFor, old.ID)
	batch.Writes = append(batch.Writes, storage.MemoryWrite{Memory: retired, PreserveEmbedding: true})
	if newID != "" {
		e := schema.NewEdge(old.ID, "supersedes", newID)
		e.ValidFrom = now
		batch.Edges = append(batch.Edges, e)
	}
}

// LatestInChain walks supersedes edges forward and returns the newest version id.
func LatestInChain(store storage.Store, memID string) (string, error) {
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
