package engine

import (
	"cmp"
	"maps"

	"github.com/ProjAnvil/LadyM/layers"
	"github.com/ProjAnvil/LadyM/operations"
	"github.com/ProjAnvil/LadyM/schema"
)

// Scope is a workspace-bound view of an Engine: it shares the engine's Store,
// Provider, Config, and lazily-built LLM agents, and holds layer handles bound
// to one workspace. Construction does no I/O, so concurrent front-ends (HTTP,
// langgraph nodes) can build one per request instead of mutating the shared
// engine's workspace fields.
type Scope struct {
	eng       *Engine
	workspace string

	Working    *layers.WorkingMemory
	Episodic   *layers.EpisodicMemory
	Semantic   *layers.SemanticMemory
	Procedural *layers.ProceduralMemory
}

// Scope returns a view of the engine bound to workspace ("" = the engine's
// default workspace).
func (e *Engine) Scope(workspace string) *Scope {
	ws := workspace
	ws = cmp.Or(ws, e.Config.Workspace)
	working := e.Working
	if ws != e.Config.Workspace {
		// Non-default workspaces get their own L0 buffer so concurrent
		// requests to different workspaces never share it.
		v, _ := e.working.LoadOrStore(ws, layers.NewWorkingMemory(64, ws))
		working = v.(*layers.WorkingMemory)
	}
	return &Scope{
		eng:        e,
		workspace:  ws,
		Working:    working,
		Episodic:   layers.NewEpisodicMemory(e.Store, e.Provider, ws),
		Semantic:   layers.NewSemanticMemory(e.Store, e.Provider, ws),
		Procedural: layers.NewProceduralMemory(e.Store, e.Provider, ws),
	}
}

// Workspace returns the scope's workspace.
func (s *Scope) Workspace() string { return s.workspace }

// Remember is the generic write, routing to the right layer. It returns an
// unpersisted Memory tagged gated=dedropped when the attention gate drops the
// content.
func (s *Scope) Remember(content string, layer schema.Layer, type_ schema.MemoryType, tags []string, metadata map[string]any, source, summary string) (*schema.Memory, error) {
	gate, err := operations.AttentionGate(content, s.eng.Config, s.eng.Store, s.eng.getAgent, layer)
	if err != nil {
		return nil, err
	}
	if gate.Action == "drop" {
		meta := map[string]any{}
		maps.Copy(meta, metadata)
		meta["gated"] = "dropped"
		meta["reason"] = gate.Reason
		m := schema.NewMemory(layer, type_)
		m.Content = content
		m.Summary = summary
		m.Tags = tags
		m.Metadata = meta
		m.Source = source
		m.Workspace = s.workspace
		return m, nil
	}
	if gate.Action == "rewrite" && gate.Content != "" {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["gated"] = "rewritten"
		metadata["original"] = content
		content = gate.Content
	}

	switch layer {
	case schema.LayerWorking:
		return s.Working.Push(content, tags, metadata, source), nil
	case schema.LayerEpisodic:
		agent := source
		agent = cmp.Or(agent, "user")
		action := summary
		action = cmp.Or(action, truncate80(content))
		return s.Episodic.Record(agent, action, content, "", tags, metadata)
	case schema.LayerProcedural:
		if type_ == schema.TypeSnippet {
			title := summary
			title = cmp.Or(title, "snippet")
			return s.Procedural.PutSnippet(title, content, "python", tags)
		}
		name := summary
		name = cmp.Or(name, truncate80(content))
		return s.Procedural.PutPlaybook(name, splitLines(content), nil, "", tags)
	default:
		return s.Semantic.PutFact(content, summary, tags, metadata, source)
	}
}

// RecordEvent logs an L1 episodic event.
func (s *Scope) RecordEvent(agent, action, observation, outcome string, tags []string, metadata map[string]any) (*schema.Memory, error) {
	return s.Episodic.Record(agent, action, observation, outcome, tags, metadata)
}
