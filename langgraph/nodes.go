// Path B — LangGraph graph nodes for automatic memory injection, mirroring
// the Python langgraph/nodes.py on the main branch. Wire the returned
// NodeFuncs into the host's
// own StateGraph (graph.AddNode) over a state with a "messages" channel
// (channels.MessagesReducer, []messages.Message).
package langgraph

import (
	"strings"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
	lcgraph "github.com/projanvil/langchain-golang/langgraph/graph"
	lgruntime "github.com/projanvil/langchain-golang/langgraph/runtime"
)

// WorkspaceFunc resolves the ladyM workspace for a node run, mirroring
// Python's per-request isolation via config["configurable"]["user_id"].
// A nil WorkspaceFunc uses the engine's default workspace.
type WorkspaceFunc func(rt lgruntime.Runtime) string

func resolveWorkspace(eng *engine.Engine, wsFn WorkspaceFunc, rt lgruntime.Runtime) string {
	if wsFn != nil {
		if ws := wsFn(rt); ws != "" {
			return ws
		}
	}
	return eng.Config.Workspace
}

// stateMessages extracts the reduced messages list from graph state,
// tolerating both []messages.Message and []messages.MessageUpdate values.
func stateMessages(state map[string]any) []lcmessages.Message {
	switch v := state["messages"].(type) {
	case []lcmessages.Message:
		return v
	case []lcmessages.MessageUpdate:
		out := make([]lcmessages.Message, 0, len(v))
		for _, u := range v {
			if m, ok := u.(lcmessages.Message); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// CreateRecallNode returns a graph node that recalls against the latest
// message and prepends a SystemMessage to state["messages"], mirroring
// Python's create_recall_node. Emits no update when there are no hits.
func CreateRecallNode(eng *engine.Engine, topK int, prefix string, wsFn WorkspaceFunc) lcgraph.NodeFunc {
	if topK <= 0 {
		topK = 6
	}
	if prefix == "" {
		prefix = "Relevant long-term memory:"
	}
	return func(rt lgruntime.Runtime, state map[string]any) (any, error) {
		msgs := stateMessages(state)
		if len(msgs) == 0 {
			return map[string]any{}, nil
		}
		query := lcmessages.Text(msgs[len(msgs)-1])
		resp, err := eng.Recall(query, resolveWorkspace(eng, wsFn, rt), topK, nil, nil, 0)
		if err != nil {
			return nil, err
		}
		if len(resp.Results) == 0 {
			return map[string]any{}, nil
		}
		lines := make([]string, 0, len(resp.Results))
		for _, r := range resp.Results {
			summary := r.Memory.Summary
			if summary == "" {
				summary = r.Memory.Content
			}
			lines = append(lines, "- "+summary)
		}
		return map[string]any{
			"messages": []lcmessages.Message{lcmessages.System(prefix + "\n" + strings.Join(lines, "\n"))},
		}, nil
	}
}

// CreateRetainNode returns a graph node that stores the latest human+AI turn
// into long-term memory (subject to ladyM's attention gate), mirroring
// Python's create_retain_node. For per-request multi-user isolation (a
// resolved workspace different from the engine default) a short-lived
// workspace-bound Engine is built per call and closed afterwards.
func CreateRetainNode(eng *engine.Engine, wsFn WorkspaceFunc) lcgraph.NodeFunc {
	return func(rt lgruntime.Runtime, state map[string]any) (any, error) {
		msgs := stateMessages(state)
		var human, ai *lcmessages.Message
		for i := len(msgs) - 1; i >= 0 && (human == nil || ai == nil); i-- {
			m := msgs[i]
			switch m.Role {
			case lcmessages.RoleHuman:
				if human == nil {
					human = &m
				}
			case lcmessages.RoleAI:
				if ai == nil {
					ai = &m
				}
			}
		}
		if human == nil || ai == nil {
			return map[string]any{}, nil
		}
		content := "Q: " + lcmessages.Text(*human) + "\nA: " + lcmessages.Text(*ai)

		local := eng
		ws := resolveWorkspace(eng, wsFn, rt)
		if ws != eng.Config.Workspace {
			cfg := *eng.Config // shallow copy, retarget workspace
			cfg.Workspace = ws
			shortLived, err := engine.New(&cfg)
			if err != nil {
				return nil, err
			}
			defer shortLived.Close()
			local = shortLived
		}
		if _, err := local.Remember(content, schema.LayerSemantic, schema.TypeFact, nil, nil, "langgraph-node", ""); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	}
}
