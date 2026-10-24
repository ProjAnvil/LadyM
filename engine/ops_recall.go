package engine

import (
	"cmp"
	"errors"
	"slices"
	"strings"

	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

// ErrResourceRequired is returned when a recall_ops query omits the
// mandatory resource slot (front-ends should validate first).
var ErrResourceRequired = errors.New("recall_operations: resource is required")

// OperationsQuery is the recall_ops pivot query: which operations touched
// this resource, optionally narrowed by action, time window, and outcome
// (spec §7.1).
type OperationsQuery struct {
	Resource     string  // required: resource name or alias
	Action       string  // optional: action word (exact match after normalization)
	Since, Until float64 // optional: time window on episode creation time
	Outcome      string  // optional: "success" | "fail"
	Limit        int     // optional, default 10
}

// OperationsResponse is the recall_ops result: the resolved resource entity
// (nil on fallback), the matching episodes/facts as recall results, and the
// fallback marker for resource-miss degradation.
type OperationsResponse struct {
	Resource *schema.Memory
	Items    []*schema.RecallResult
	Fallback bool
}

// recallOpsDefaultLimit is the default cap on returned operations.
const recallOpsDefaultLimit = 10

// RecallOperations runs the three-stage pivot pipeline (spec §7.2), with
// zero LLM on every branch:
//
//	Stage 1 resource resolution: exact alias/content match, then vector
//	  nearest-neighbour with the read-side threshold (lower than the write
//	  side). A miss degrades to plain recall with Fallback=true.
//	Stage 2 graph connection: Neighbors(res, "operates_on") src side,
//	  filtered by workspace/retired/action_class/time window/outcome.
//	Stage 3 activation ranking: ACT-R ActivationScore with a flat sim proxy.
func (s *Scope) RecallOperations(q OperationsQuery) (*OperationsResponse, error) {
	// A whitespace-only resource carries no value (the pivot has no default),
	// so every front-end — MCP, CLI, HTTP — sees the same error for it.
	q.Resource = strings.TrimSpace(q.Resource)
	if q.Resource == "" {
		return nil, ErrResourceRequired
	}
	res, err := s.resolveResourceForRead(q.Resource)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return s.recallOperationsFallback(q)
	}

	actionClass := ""
	if q.Action != "" {
		actionClass = operations.NormalizeAction(q.Action, s.eng.Config)
	}

	var items []*schema.RecallResult
	edges, err := s.eng.Store.Neighbors(res.ID, "operates_on")
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		// Neighbors returns both directions; the other end is the
		// episode/fact that operates on the resource.
		otherID := e.DstID
		if e.DstID == res.ID {
			otherID = e.SrcID
		}
		m, err := s.eng.Store.GetMemory(otherID)
		if err != nil {
			return nil, err
		}
		if m == nil || m.Workspace != s.workspace || operations.IsRetired(m) {
			continue
		}
		if actionClass != "" && m.MetaString("action_class") != actionClass {
			continue
		}
		if q.Since != 0 && m.CreatedAt < q.Since {
			continue
		}
		if q.Until != 0 && m.CreatedAt > q.Until {
			continue
		}
		if q.Outcome != "" && m.MetaString("outcome") != q.Outcome {
			continue
		}
		items = append(items, &schema.RecallResult{
			Memory: m,
			Tier:   1,
			Via:    []string{res.ID},
		})
	}
	if err := s.rankOperations(items); err != nil {
		return nil, err
	}
	if limit := cmp.Or(q.Limit, recallOpsDefaultLimit); len(items) > limit {
		items = items[:limit]
	}
	return &OperationsResponse{Resource: res, Items: items}, nil
}

// rankOperations scores each candidate with the ACT-R activation formula and
// sorts by descending score.
func (s *Scope) rankOperations(items []*schema.RecallResult) error {
	neighbourCounts, err := s.eng.Store.NeighborCounts()
	if err != nil {
		return err
	}
	for _, it := range items {
		// Pivot candidates have no vector similarity to a query. Use a
		// flat proxy — direct neighbours 0.5, a hypothetical two-hop
		// expansion would take 0.5/2. This deliberately deviates from
		// tier2Expand's anchor.Score*0.5/depth formula (recall.go):
		// pivot queries have no anchor score to scale by.
		it.Score = operations.ActivationScore(it.Memory, 0.5,
			s.eng.Config.Activation, neighbourCounts, nil, 0)
	}
	slices.SortStableFunc(items, func(a, b *schema.RecallResult) int {
		return cmp.Compare(b.Score, a.Score)
	})
	return nil
}

// recallOperationsFallback degrades to plain recall on resource miss
// (spec §7.2): query = resource + " " + action, results returned as items
// with Fallback=true.
func (s *Scope) recallOperationsFallback(q OperationsQuery) (*OperationsResponse, error) {
	limit := cmp.Or(q.Limit, recallOpsDefaultLimit)
	resp, err := s.eng.Recall(strings.TrimSpace(q.Resource+" "+q.Action),
		s.workspace, limit, nil, nil, 0)
	if err != nil {
		return nil, err
	}
	return &OperationsResponse{Items: resp.Results, Fallback: true}, nil
}

// resolveResourceForRead resolves a resource name to a workspace resource
// entity using the read-side threshold, without creating one and without
// growing aliases (both are write-path behaviours). Returns nil when nothing
// matches (caller falls back).
func (s *Scope) resolveResourceForRead(name string) (*schema.Memory, error) {
	// Stage 1a: exact content/alias match, case-insensitive.
	resources, err := s.eng.Store.IterMemories(s.workspace,
		string(schema.LayerSemantic), string(schema.TypeResource))
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(name)
	for _, m := range resources {
		if strings.ToLower(m.Content) == want {
			return m, nil
		}
		for _, a := range memoryAliases(m) {
			if strings.ToLower(a) == want {
				return m, nil
			}
		}
	}

	// Stage 1b: vector nearest neighbour, Go-side type/workspace filter,
	// read threshold (0.75 default, below the write side's 0.85).
	queryVec, err := s.eng.Provider.Embed(name)
	if err != nil {
		return nil, err
	}
	for _, hit := range s.eng.Store.VectorSearch(queryVec, 50) {
		if hit.Similarity < s.eng.Config.Resource.ReadThreshold {
			continue
		}
		m, err := s.eng.Store.GetMemory(hit.ID)
		if err != nil {
			return nil, err
		}
		if m == nil || m.Type != schema.TypeResource || m.Workspace != s.workspace {
			continue
		}
		return m, nil
	}
	return nil, nil
}

// memoryAliases extracts a resource memory's alias list from metadata,
// tolerating both the fresh []string form and the JSON round-trip []any form.
func memoryAliases(m *schema.Memory) []string {
	switch v := m.Metadata["aliases"].(type) {
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
