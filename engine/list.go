package engine

import (
	"cmp"
	"slices"

	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

// List returns a workspace's memories newest-first, excluding retired items
// (expired predictions, superseded models). limit<=0 means 20; offset pages
// (negative offset is clamped to 0). Read-only: it never calls the LLM.
func (e *Engine) List(workspace string, layer *schema.Layer, limit, offset int) ([]*schema.Memory, error) {
	workspace = cmp.Or(workspace, e.Config.Workspace)
	layerStr := ""
	if layer != nil {
		layerStr = string(*layer)
	}
	all, err := e.Store.IterMemories(workspace, layerStr, "")
	if err != nil {
		return nil, err
	}
	live := make([]*schema.Memory, 0, len(all))
	for _, m := range all {
		if !operations.IsRetired(m) {
			live = append(live, m)
		}
	}
	if offset < 0 {
		offset = 0
	}
	slices.SortStableFunc(live, func(a, b *schema.Memory) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(b.ID, a.ID)) // deterministic tiebreak for equal timestamps
	})
	if offset >= len(live) {
		return []*schema.Memory{}, nil
	}
	live = live[offset:]
	if limit <= 0 {
		limit = 20
	}
	if len(live) > limit {
		live = live[:limit]
	}
	return live, nil
}
