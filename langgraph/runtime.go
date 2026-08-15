// Shared runtime helpers for the LangGraph integration (paths A and B),
// mirroring Python's langgraph/_runtime.py on the main branch. Keeps Engine
// lifecycle and workspace resolution in one place so the two paths stay
// consistent and have a single seam against Engine.
package langgraph

import (
	"fmt"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/engine"
	lgruntime "github.com/projanvil/langchain-golang/langgraph/runtime"
)

// ResolveEngine returns a ready-to-use Engine from flexible factory input,
// mirroring Python's resolve_engine:
//
//   - *engine.Engine — returned as-is (caller owns lifecycle); workspace is
//     IGNORED — the engine's own Config.Workspace wins. Pass a *config.Config
//     or db path instead if you need a different workspace.
//   - *config.Config — new Engine from a shallow copy; a non-empty workspace
//     overrides cfg.Workspace.
//   - string — treated as a db path over config.Default(); workspace honored.
//   - nil — config.Default() (offline hashing embedding); workspace honored.
//
// Any other type is an error.
func ResolveEngine(src any, workspace string) (*engine.Engine, error) {
	switch v := src.(type) {
	case *engine.Engine:
		return v, nil
	case *config.Config:
		cfg := *v // shallow copy, retarget workspace
		if workspace != "" {
			cfg.Workspace = workspace
		}
		return engine.New(&cfg)
	case string:
		cfg := config.Default()
		cfg.DBPath = v
		if workspace != "" {
			cfg.Workspace = workspace
		}
		return engine.New(cfg)
	case nil:
		cfg := config.Default()
		if workspace != "" {
			cfg.Workspace = workspace
		}
		return engine.New(cfg)
	default:
		return nil, fmt.Errorf("langgraph: unsupported engine source %T (want *engine.Engine, *config.Config, db path string, or nil)", src)
	}
}

// WorkspaceFromUserID returns a WorkspaceFunc that resolves the ladyM
// workspace from the run-scoped context's "user_id" field, mirroring
// Python's resolve_workspace reading config["configurable"]["user_id"].
// An empty result falls back to the engine's default workspace.
func WorkspaceFromUserID() WorkspaceFunc {
	return func(rt lgruntime.Runtime) string {
		if v, ok := lgruntime.ValueFromRuntime(rt, "user_id"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
}
