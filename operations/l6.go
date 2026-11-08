package operations

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/providers"
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

const l6Layer = schema.LayerL6Predictive
const l6Watermark = "l6_last_episode_ts"

type episodeCursor struct {
	Timestamp float64 `json:"timestamp"`
	ID        string  `json:"id"`
}

func workspaceMetaKey(operation, workspace string) string {
	return operation + ":" + schema.ContentHash(workspace)
}

func compareEpisodeCursor(a, b episodeCursor) int {
	return cmp.Or(cmp.Compare(a.Timestamp, b.Timestamp), cmp.Compare(a.ID, b.ID))
}

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
	ws = cmp.Or(ws, cfg.Workspace)
	report := &L6PredictionReport{}
	if llm == nil {
		report.Skipped = true
		return report, nil
	}
	prompt = cmp.Or(prompt, l6DefaultPrompt)
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
	cursorKey := workspaceMetaKey("l6_episode_cursor", ws)
	rawWatermark, err := store.GetMeta(cursorKey)
	if err != nil {
		return nil, err
	}
	cursor := episodeCursor{}
	if rawWatermark != "" {
		if err := json.Unmarshal([]byte(rawWatermark), &cursor); err != nil {
			return nil, fmt.Errorf("l6: invalid workspace cursor: %w", err)
		}
	}
	episodes, err := store.IterMemories(ws, string(schema.LayerEpisodic), "")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(episodes, func(a, b *schema.Memory) int {
		return compareEpisodeCursor(episodeCursor{a.CreatedAt, a.ID}, episodeCursor{b.CreatedAt, b.ID})
	})
	var recent []*schema.Memory
	for _, e := range episodes {
		if schema.IsUsable(e) && compareEpisodeCursor(episodeCursor{e.CreatedAt, e.ID}, cursor) > 0 {
			recent = append(recent, e)
		}
	}
	if cfg.System2.L6MaxEpisodes < 0 {
		return nil, fmt.Errorf("l6: max episodes must be non-negative")
	}
	if cap := cmp.Or(cfg.System2.L6MaxEpisodes, 20); len(recent) > cap {
		recent = recent[:cap]
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

	last := recent[len(recent)-1]
	encoded, err := json.Marshal(episodeCursor{last.CreatedAt, last.ID})
	if err != nil {
		return nil, err
	}
	if err := store.SetMeta(cursorKey, string(encoded)); err != nil {
		return nil, err
	}
	report.WatermarkUpdatedTo = last.CreatedAt
	return report, nil
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
