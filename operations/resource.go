package operations

import (
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// resourceAliasKey is the metadata key holding a resource entity's alias list.
const resourceAliasKey = "aliases"

// resourceAliases extracts the alias list from a resource memory's metadata,
// tolerating both the fresh []string form and the JSON round-trip []any form.
func resourceAliases(m *schema.Memory) []string {
	switch v := m.Metadata[resourceAliasKey].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// appendAlias adds name to m's alias list (case-insensitively deduped) and
// reports whether the list changed.
func appendAlias(m *schema.Memory, name string) bool {
	cur := resourceAliases(m)
	for _, a := range cur {
		if strings.EqualFold(a, name) {
			return false
		}
	}
	m.Metadata[resourceAliasKey] = append(cur, name)
	return true
}

// ResolveResource resolves a resource name (or alias) to a workspace-scoped
// L2 resource entity, creating one when nothing matches (spec §5.1):
//
//  1. Exact alias/content match over the workspace's resource memories
//     (case-insensitive) — zero vector work.
//  2. Vector nearest neighbor: embed the name, VectorSearch top-50, then
//     filter in Go for type=resource in this workspace (VectorSearch has no
//     type filter). A hit with sim >= cfg.Resource.ResolveThreshold wins and
//     the queried name is appended to its aliases (lazy alias growth).
//  3. Otherwise create a new resource entity.
//
// The bool result reports whether a new entity was created.
func ResolveResource(store storage.Store, embedder storage.EmbeddingProvider,
	cfg *config.Config, ws, name string) (*schema.Memory, bool, error) {

	// Stage 1: exact alias/content match.
	existing, err := store.IterMemories(ws, string(schema.LayerSemantic), string(schema.TypeResource))
	if err != nil {
		return nil, false, err
	}
	want := strings.ToLower(name)
	for _, m := range existing {
		if strings.ToLower(m.Content) == want {
			return m, false, nil
		}
		for _, a := range resourceAliases(m) {
			if strings.ToLower(a) == want {
				return m, false, nil
			}
		}
	}

	// Stage 2: vector nearest neighbor, Go-side type/workspace filter.
	queryVec, err := embedder.Embed(name)
	if err != nil {
		return nil, false, err
	}
	for _, hit := range store.VectorSearch(queryVec, 50) {
		if hit.Similarity < cfg.Resource.ResolveThreshold {
			continue
		}
		m, err := store.GetMemory(hit.ID)
		if err != nil {
			return nil, false, err
		}
		if m == nil || m.Type != schema.TypeResource || m.Workspace != ws {
			continue
		}
		if appendAlias(m, name) {
			// Rewrite with a freshly embedded content vector. scanMemory
			// never reads Memory.Embedding back from the DB, so reusing
			// m.Embedding (nil after a fetch) would NULL the embedding
			// column and silently drop the resource from vector search.
			contentVec, err := embedder.Embed(m.Content)
			if err != nil {
				return nil, false, err
			}
			if err := store.PutMemory(m, contentVec); err != nil {
				return nil, false, err
			}
		}
		return m, false, nil
	}

	// Stage 3: create a new resource entity.
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeResource)
	m.Workspace = ws
	m.Content = name
	m.Summary = name
	m.Metadata[resourceAliasKey] = []string{name}
	if err := store.PutMemory(m, queryVec); err != nil {
		return nil, false, err
	}
	return m, true, nil
}
