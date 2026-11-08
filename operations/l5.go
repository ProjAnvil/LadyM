package operations

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

const l5Layer = schema.LayerL5Mental
const abstractsRelation = "abstracts"

var abstractable = map[[2]string]bool{
	{string(schema.LayerSemantic), string(schema.TypeFact)}:       true,
	{string(schema.LayerSemantic), string(schema.TypeNote)}:       true,
	{string(schema.LayerProcedural), string(schema.TypePlaybook)}: true,
	{string(schema.LayerProcedural), string(schema.TypeSnippet)}:  true,
}

const l5DefaultPrompt = `You are a memory-consolidation engine for a brain-inspired agent memory system. Your job is to
abstract several concrete memories into ONE concise, general mental model.

You are given a list of memories, each prefixed with its type (e.g. "(fact) ..."). Produce a
single higher-level mental model that captures the shared concept, rule, or pattern they point to.

Reply ONLY with JSON matching exactly this schema:
  {"title": "<short label, at most 8 words>", "model": "<one to three sentences>"}

Rules:
- Generalise; do not merely concatenate the inputs.
- Stay faithful to the inputs — never invent specifics they do not support.
- Even if the memories seem loosely related, produce the best single umbrella statement you can.
`

// L5ExtractionReport reports the outcome of L5 mental-model extraction.
type L5ExtractionReport struct {
	NewModels    int
	MergedModels int
	Clusters     []map[string]any
	Skipped      bool
}

func coveredMemberIDs(store storage.Store, workspace string) (map[string]bool, error) {
	covered := map[string]bool{}
	l5s, err := store.IterMemories(workspace, string(l5Layer), "")
	if err != nil {
		return nil, err
	}
	for _, l5 := range l5s {
		if !schema.IsUsable(l5) {
			continue
		}
		edges, err := store.Neighbors(l5.ID, abstractsRelation)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if e.SrcID == l5.ID && e.ValidTo == nil {
				covered[e.DstID] = true
			}
		}
	}
	return covered, nil
}

// connectedComponents groups ids by cosine similarity >= threshold.
func connectedComponents(ids []string, vecs [][]float32, threshold float64) [][]string {
	if len(ids) == 0 {
		return nil
	}
	// L2-normalise so dot product == cosine.
	unit := make([][]float32, len(vecs))
	for i, v := range vecs {
		unit[i] = normalize(v)
	}
	parent := make([]int, len(ids))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if dot(unit[i], unit[j]) >= threshold {
				union(i, j)
			}
		}
	}
	groups := map[int][]string{}
	for i := range ids {
		groups[find(i)] = append(groups[find(i)], ids[i])
	}
	out := make([][]string, 0, len(groups))
	for _, g := range groups {
		slices.Sort(g)
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b []string) int { return cmp.Compare(a[0], b[0]) })
	return out
}

func normalize(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if n == 0 {
		copy(out, v)
		return out
	}
	sqrt := math.Sqrt(n)
	for i, x := range v {
		out[i] = float32(float64(x) / sqrt)
	}
	return out
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func summarise(llm providers.LLMProvider, prompt string, members []*schema.Memory) (map[string]any, error) {
	lines := make([]string, 0, len(members))
	for _, m := range members {
		lines = append(lines, "- ("+string(m.Type)+") "+m.Content)
	}
	corpus := strings.Join(lines, "\n")
	msgs := []providers.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Abstract these memories into one mental model:\n" + corpus},
	}
	return llm.CompleteStructured(msgs, providers.JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "description": "Short title for the mental model"},
			"model": map[string]any{"type": "string", "description": "The abstracted mental model body"},
		},
		"required": []string{"title", "model"},
	})
}

func buildModel(embedder storage.EmbeddingProvider, title, body, workspace, source string, extraMeta map[string]any) (*schema.Memory, []float32, error) {
	content := title
	if body != "" {
		content = title + ": " + body
	}
	meta := map[string]any{}
	maps.Copy(meta, extraMeta)
	m := schema.NewMemory(l5Layer, schema.TypeMentalModel)
	m.Content = content
	m.Summary = title
	m.Tags = []string{"mental_model"}
	m.Metadata = meta
	m.Metadata["knowledge_status"] = "inferred"
	m.Source = source
	m.Workspace = workspace
	m.ContentHash = schema.ContentHash(content)
	vec, err := embedder.Embed(content)
	if err != nil {
		return nil, nil, err
	}
	return m, vec, nil
}

func mergeL5(store storage.Store, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, llm providers.LLMProvider, prompt string, report *L5ExtractionReport) (*L5ExtractionReport, error) {
	all, err := store.IterMemories(workspace, string(l5Layer), "")
	if err != nil {
		return nil, err
	}
	var models []*schema.Memory
	for _, m := range all {
		if schema.IsUsable(m) {
			models = append(models, m)
		}
	}
	if len(models) < 2 {
		return report, nil
	}
	byID := map[string]*schema.Memory{}
	ids := make([]string, 0, len(models))
	vecs := make([][]float32, 0, len(models))
	contents := make([]string, 0, len(models))
	for _, m := range models {
		byID[m.ID] = m
		ids = append(ids, m.ID)
		contents = append(contents, m.Content)
	}
	vecs, err = embedder.EmbedBatch(contents)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(contents) {
		return nil, fmt.Errorf("l5 merge: embedding batch returned %d vectors for %d memories", len(vecs), len(contents))
	}
	components := connectedComponents(ids, vecs, cfg.System2.L5MergeSimilarity)
	for _, comp := range components {
		if len(comp) < 2 {
			continue
		}
		var oldModels []*schema.Memory
		for _, mid := range comp {
			oldModels = append(oldModels, byID[mid])
		}
		var members []*schema.Memory
		seen := map[string]bool{}
		for _, om := range oldModels {
			edges, err := store.Neighbors(om.ID, abstractsRelation)
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				if e.SrcID == om.ID && e.ValidTo == nil && !seen[e.DstID] {
					seen[e.DstID] = true
					mb, err := store.GetMemory(e.DstID)
					if err != nil {
						return nil, err
					}
					if !schema.IsUsable(mb) || mb.Workspace != workspace {
						return nil, fmt.Errorf("%w: unavailable model source %s", storage.ErrMemoryConflict, e.DstID)
					}
					members = append(members, mb)
				}
			}
		}
		if len(members) == 0 {
			continue
		}
		result, err := summarise(llm, prompt, members)
		if err != nil {
			return nil, err
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("l5 merge: empty model response")
		}
		title, _ := result["title"].(string)
		body, _ := result["model"].(string)
		title = cmp.Or(title, "mental model")
		oldIDs := make([]any, 0, len(oldModels))
		for _, om := range oldModels {
			oldIDs = append(oldIDs, om.ID)
		}
		merged, vec, err := buildModel(embedder, title, body, workspace, "l5_merge", map[string]any{
			"n_members": len(members), "merged_from": oldIDs,
		})
		if err != nil {
			return nil, err
		}
		batch := storage.MemoryBatch{Writes: []storage.MemoryWrite{{Memory: merged, Vector: vec}}}
		if err := stageProvenance(store, &batch, merged, members, abstractsRelation); err != nil {
			return nil, err
		}
		for _, om := range oldModels {
			stageRetirement(&batch, om, merged.ID)
		}
		if err := store.ApplyMemoryBatch(batch); err != nil {
			return nil, err
		}
		report.MergedModels++
		report.Clusters = append(report.Clusters, map[string]any{
			"model_id": merged.ID, "n_members": len(members), "action": "merged",
		})
	}
	return report, nil
}

// ExtractL5 clusters uncovered L2/L3 memories into mental models.
func ExtractL5(store storage.Store, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, llm providers.LLMProvider, prompt string) (*L5ExtractionReport, error) {
	ws := workspace
	ws = cmp.Or(ws, cfg.Workspace)
	report := &L5ExtractionReport{}
	if llm == nil {
		report.Skipped = true
		return report, nil
	}
	prompt = cmp.Or(prompt, l5DefaultPrompt)

	covered, err := coveredMemberIDs(store, ws)
	if err != nil {
		return nil, err
	}
	all, err := store.IterMemories(ws, "", "")
	if err != nil {
		return nil, err
	}
	var candidates []*schema.Memory
	for _, m := range all {
		key := [2]string{string(m.Layer), string(m.Type)}
		if schema.IsUsable(m) && !covered[m.ID] && abstractable[key] {
			candidates = append(candidates, m)
		}
	}

	byID := map[string]*schema.Memory{}
	ids := make([]string, 0, len(candidates))
	contents := make([]string, 0, len(candidates))
	for _, m := range candidates {
		byID[m.ID] = m
		ids = append(ids, m.ID)
		contents = append(contents, m.Content)
	}
	var vecs [][]float32
	if len(contents) > 0 {
		vecs, err = embedder.EmbedBatch(contents)
		if err != nil {
			return nil, err
		}
		if len(vecs) != len(contents) {
			return nil, fmt.Errorf("l5 extract: embedding batch returned %d vectors for %d memories", len(vecs), len(contents))
		}
	}
	components := connectedComponents(ids, vecs, cfg.System2.L5ClusterSimilarity)

	for _, comp := range components {
		if len(comp) < cfg.System2.L5MinClusterSize {
			continue
		}
		var members []*schema.Memory
		for _, mid := range comp {
			members = append(members, byID[mid])
		}
		result, err := summarise(llm, prompt, members)
		if err != nil {
			return nil, err
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("l5 extract: empty model response")
		}
		title, _ := result["title"].(string)
		body, _ := result["model"].(string)
		title = cmp.Or(title, "mental model")
		modelMem, vec, err := buildModel(embedder, title, body, ws, "l5_extract", map[string]any{"n_members": len(members)})
		if err != nil {
			return nil, err
		}
		batch := storage.MemoryBatch{Writes: []storage.MemoryWrite{{Memory: modelMem, Vector: vec}}}
		if err := stageProvenance(store, &batch, modelMem, members, abstractsRelation); err != nil {
			return nil, err
		}
		if err := store.ApplyMemoryBatch(batch); err != nil {
			return nil, err
		}
		report.NewModels++
		report.Clusters = append(report.Clusters, map[string]any{
			"model_id": modelMem.ID, "n_members": len(members), "action": "new",
		})
	}

	n := cfg.System2.L5MergeEveryNCycles
	if n > 0 {
		key := workspaceMetaKey("l5_merge_cycle_count", ws)
		raw, err := store.GetMeta(key)
		if err != nil {
			return nil, err
		}
		counter := 1
		if raw != "" {
			c, err := strconv.Atoi(raw)
			if err != nil || c < 0 {
				return nil, fmt.Errorf("l5: invalid workspace cycle count %q", raw)
			}
			counter = c + 1
		}
		if counter >= n {
			report, err = mergeL5(store, embedder, cfg, ws, llm, prompt, report)
			if err != nil {
				return nil, err
			}
			if err := store.SetMeta(key, "0"); err != nil {
				return nil, err
			}
			return report, nil
		}
		if err := store.SetMeta(key, strconv.Itoa(counter)); err != nil {
			return nil, err
		}
	}
	return report, nil
}
