package operations

import (
	"sort"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

const l6Layer = schema.LayerL6Predictive
const l6Watermark = "l6_last_episode_ts"

const l6DefaultPrompt = `You are a forward-prediction engine for a brain-inspired agent memory system. You are given a
list of recent episodic events (what the agent just did or observed). Predict the most likely
NEXT intents the agent or user will pursue.

Reply ONLY with JSON matching exactly this schema:
  {"intents": [{"intent": "<a concrete next action or goal>", "confidence": <0.0-1.0>, "horizon_s": <seconds this prediction stays plausible>}]}

Rules:
- Return between 1 and 5 intents, most likely first.
- "intent" must be a concrete next step, not a vague topic.
- You may omit "horizon_s" on an intent only if you cannot estimate it; the system defaults it.
`

// L6PredictionReport reports the outcome of L6 forward-intent prediction.
type L6PredictionReport struct {
	Predictions        int
	ExpiredRetired     int
	EpisodesSeen       int
	WatermarkUpdatedTo float64
	Details            []map[string]any
	Skipped            bool
}

// PredictL6 predicts next intents from recent episodes, with TTL expiry.
func PredictL6(store storage.Store, embedder storage.EmbeddingProvider, cfg *config.Config, workspace string, llm providers.LLMProvider, prompt string) (*L6PredictionReport, error) {
	ws := workspace
	if ws == "" {
		ws = cfg.Workspace
	}
	report := &L6PredictionReport{}
	if llm == nil {
		report.Skipped = true
		return report, nil
	}
	if prompt == "" {
		prompt = l6DefaultPrompt
	}
	now := schema.Now()

	// 1. expire sweep
	l6s, err := store.IterMemories(ws, string(l6Layer), "")
	if err != nil {
		return nil, err
	}
	for _, m := range l6s {
		if IsRetired(m) {
			continue
		}
		// Python: float(m.metadata.get("valid_to", 0)) — missing counts as 0
		// (expired); a present but non-numeric value raises and is skipped.
		validTo, ok := m.MetaFloat("valid_to")
		if !ok {
			if _, exists := m.Metadata["valid_to"]; exists {
				continue
			}
			validTo = 0
		}
		if now > validTo {
			if err := Retire(store, m, ""); err != nil {
				return nil, err
			}
			report.ExpiredRetired++
		}
	}

	// 2. window
	rawWatermark, err := store.GetMeta(l6Watermark)
	if err != nil {
		return nil, err
	}
	watermark := 0.0
	if rawWatermark != "" {
		watermark = parseFloatOrZero(rawWatermark)
	}
	episodes, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	sort.SliceStable(episodes, func(a, b int) bool { return episodes[a].CreatedAt < episodes[b].CreatedAt })
	var recent []*schema.Memory
	for _, e := range episodes {
		if e.CreatedAt > watermark {
			recent = append(recent, e)
		}
	}
	if len(recent) > cfg.System2.L6MaxEpisodes {
		recent = recent[:cfg.System2.L6MaxEpisodes]
	}
	report.EpisodesSeen = len(recent)
	if len(recent) == 0 {
		return report, nil
	}

	// 3. predict
	lines := make([]string, 0, len(recent))
	for _, e := range recent {
		lines = append(lines, "- "+e.Content)
	}
	corpus := strings.Join(lines, "\n")
	msgs := []providers.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Predict likely next intents from these recent episodes:\n" + corpus},
	}
	out, err := llm.CompleteStructured(msgs, providers.JSONSchema{
		"type": "object",
		"properties": map[string]any{
			"intents": map[string]any{
				"type":        "array",
				"description": "Predicted next intents",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"intent":     map[string]any{"type": "string", "description": "Predicted intent text"},
						"confidence": map[string]any{"type": "number", "description": "0..1 confidence, default 0.5"},
						"horizon_s":  map[string]any{"type": "number", "description": "Seconds the prediction stays valid; omit for the configured default"},
					},
					"required": []string{"intent"},
				},
			},
		},
		"required": []string{"intents"},
	})
	if err != nil {
		return nil, err
	}
	defaultHorizon := cfg.System2.L6HorizonS

	intents, _ := out["intents"].([]any)
	for _, it := range intents {
		obj, ok := it.(map[string]any)
		if !ok {
			continue
		}
		intentText, _ := obj["intent"].(string)
		intentText = strings.TrimSpace(intentText)
		if intentText == "" {
			continue
		}
		confidence := 0.5
		if c, ok := obj["confidence"].(float64); ok {
			confidence = c
		}
		horizon := defaultHorizon
		if h, ok := obj["horizon_s"].(float64); ok {
			horizon = h
		}
		validTo := now + horizon
		m := schema.NewMemory(l6Layer, schema.TypeForwardIntent)
		m.Content = intentText
		m.Summary = truncateStr(intentText, 80)
		m.Tags = []string{"predicted"}
		m.Metadata = map[string]any{"confidence": confidence, "horizon_s": horizon, "valid_to": validTo}
		m.Source = "l6_predict"
		m.Workspace = ws
		vec, err := embedder.Embed(intentText)
		if err != nil {
			return nil, err
		}
		if err := store.PutMemory(m, vec); err != nil {
			return nil, err
		}
		report.Predictions++
		report.Details = append(report.Details, map[string]any{
			"intent": intentText, "confidence": confidence, "valid_to": validTo,
		})
	}

	newest := recent[len(recent)-1].CreatedAt
	if err := store.SetMeta(l6Watermark, floatString(newest)); err != nil {
		return nil, err
	}
	report.WatermarkUpdatedTo = newest
	return report, nil
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
