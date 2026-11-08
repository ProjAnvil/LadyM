package engine

import (
	"context"

	"github.com/ProjAnvil/LadyM/providers"
)

// WithContext scopes database I/O while sharing L0 buffers and host routing.
func (e *Engine) WithContext(ctx context.Context) *Engine {
	root := e.workingRoot
	if root == nil {
		root = e
	}
	return &Engine{
		Config: e.Config, Provider: e.Provider, Store: e.Store.WithContext(ctx),
		Working: root.Working, workingRoot: root,
		routing: e.routing, agents: map[string]providers.LLMProvider{},
	}
}
