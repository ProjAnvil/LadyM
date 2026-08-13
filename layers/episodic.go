package layers

import (
	"strings"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// EpisodicMemory is L1 — time-stamped events.
type EpisodicMemory struct {
	Store     *storage.SQLiteStore
	Embedder  storage.EmbeddingProvider
	Workspace string
}

// NewEpisodicMemory builds an EpisodicMemory.
func NewEpisodicMemory(store *storage.SQLiteStore, embedder storage.EmbeddingProvider, workspace string) *EpisodicMemory {
	if workspace == "" {
		workspace = "default"
	}
	return &EpisodicMemory{Store: store, Embedder: embedder, Workspace: workspace}
}

// Record appends an episodic event. Content is a rendered sentence for embedding.
func (e *EpisodicMemory) Record(agent, action, observation, outcome string, tags []string, metadata map[string]any) (*schema.Memory, error) {
	parts := []string{"agent=" + agent, "action=" + action}
	if observation != "" {
		parts = append(parts, "observation="+observation)
	}
	if outcome != "" {
		parts = append(parts, "outcome="+outcome)
	}
	content := strings.Join(parts, " | ")
	if metadata == nil {
		metadata = map[string]any{}
	}
	meta := map[string]any{}
	for k, v := range metadata {
		meta[k] = v
	}
	meta["agent"] = agent
	meta["action"] = action
	if observation != "" {
		meta["observation"] = observation
	}
	if outcome != "" {
		meta["outcome"] = outcome
	}
	if tags == nil {
		tags = []string{}
	}

	m := schema.NewMemory(schema.LayerEpisodic, schema.TypeEvent)
	m.Content = content
	m.Summary = agent + ": " + action
	m.Tags = tags
	m.Metadata = meta
	m.Source = agent
	m.Workspace = e.Workspace

	vec, err := e.Embedder.Embed(content)
	if err != nil {
		return nil, err
	}
	if err := e.Store.PutMemory(m, vec); err != nil {
		return nil, err
	}
	return m, nil
}

// Recent returns the most recent episodes.
func (e *EpisodicMemory) Recent(limit int) ([]*schema.Memory, error) {
	return e.Store.IterMemories(e.Workspace, string(schema.LayerEpisodic), "")
}
