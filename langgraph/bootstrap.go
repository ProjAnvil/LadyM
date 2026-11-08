package langgraph

import (
	"github.com/ProjAnvil/LadyM/engine"
	lcmessages "github.com/projanvil/langchain-golang/core/messages"
	lcgraph "github.com/projanvil/langchain-golang/langgraph/graph"
	lgruntime "github.com/projanvil/langchain-golang/langgraph/runtime"
)

func CreateBootstrapNode(eng *engine.Engine, req engine.BootstrapRequest, counter engine.TokenCounter, wsFn WorkspaceFunc) lcgraph.NodeFunc {
	return func(rt lgruntime.Runtime, state map[string]any) (any, error) {
		result, err := eng.Scope(resolveWorkspace(eng, wsFn, rt)).Bootstrap(req, counter)
		if err != nil {
			return nil, err
		}
		if result.Content == "" {
			return map[string]any{}, nil
		}
		return map[string]any{"messages": []lcmessages.Message{lcmessages.System(result.Content)}}, nil
	}
}
