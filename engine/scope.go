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

// OperationEvent is a structured L1 write: explicit resource and action slots
// layered on the legacy record_event fields (spec §6.1). ActionClass, when
// empty, is derived from Action via the controlled vocabulary.
type OperationEvent struct {
	Agent, Action, ActionClass string
	Resources                  []string
	Observation, Outcome       string
	Tags                       []string
	Metadata                   map[string]any
}

// RecordOperation records an episodic event with structured slots: the action
// is normalized onto the vocabulary (ActionClass overrides), each resource is
// resolved to a workspace L2 resource entity, and an operates_on edge with a
// deterministic projected id links the episode to every resource.
func (s *Scope) RecordOperation(ev OperationEvent) (*schema.Memory, error) {
	return s.recordOperation(ev, true)
}

// RecordEvent logs an L1 episodic event. Legacy shape: no resource slots, no
// vocabulary stamping, no edges — byte-identical to the pre-operation write
// path; internally it delegates to the same pipeline with slots disabled.
func (s *Scope) RecordEvent(agent, action, observation, outcome string, tags []string, metadata map[string]any) (*schema.Memory, error) {
	return s.recordOperation(OperationEvent{
		Agent:       agent,
		Action:      action,
		Observation: observation,
		Outcome:     outcome,
		Tags:        tags,
		Metadata:    metadata,
	}, false)
}

// recordOperation is the shared write path. slots controls whether the
// action_class/action_raw metadata keys are stamped (new API) or the output
// stays byte-identical to the legacy RecordEvent (old API).
func (s *Scope) recordOperation(ev OperationEvent, slots bool) (*schema.Memory, error) {
	meta := ev.Metadata
	if slots {
		meta = maps.Clone(meta)
		if meta == nil {
			meta = map[string]any{}
		}
		actionClass := ev.ActionClass
		if actionClass == "" {
			actionClass = operations.NormalizeAction(ev.Action, s.eng.Config)
		}
		if actionClass != "" {
			meta["action_class"] = actionClass
		}
		if ev.Action != "" {
			meta["action_raw"] = ev.Action
		}
	}

	var resources []*schema.Memory
	for _, name := range ev.Resources {
		res, _, err := operations.ResolveResource(s.eng.Store, s.eng.Provider, s.eng.Config, s.workspace, name)
		if err != nil {
			return nil, err
		}
		resources = append(resources, res)
	}

	ep, err := s.Episodic.Record(ev.Agent, ev.Action, ev.Observation, ev.Outcome, ev.Tags, meta)
	if err != nil {
		return nil, err
	}
	for _, res := range resources {
		e := schema.NewEdge(ep.ID, "operates_on", res.ID)
		e.ID = schema.ProjectedEdgeID(ep.ID, "operates_on", res.ID)
		if err := s.eng.Store.PutEdge(e); err != nil {
			return nil, err
		}
	}
	return ep, nil
}
