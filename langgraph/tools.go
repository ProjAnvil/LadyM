// Package langgraph integrates ladyM as a memory layer for langchain-golang
// LangGraph hosts — the Go port of the Python langgraph module (main branch).
//
// Two paths, mirroring the Python module:
//
//   - Path A (Tools): CreateTools returns LangChain tools wrapping ladyM
//     long-term memory; the LLM decides when to recall / remember
//     (ReAct-style, e.g. via agents.CreateAgent).
//   - Path B (Nodes): CreateRecallNode / CreateRetainNode return graph
//     NodeFuncs for automatic per-turn memory injection, wired into the
//     host's own StateGraph.
package langgraph

import (
	"context"
	"fmt"
	"strings"

	"github.com/ProjAnvil/LadyM/engine"
	"github.com/ProjAnvil/LadyM/schema"
	lctools "github.com/projanvil/langchain-golang/core/tools"
)

// CreateTools builds LangChain tools backed by ladyM memory:
// [recall_memory, remember_fact, search_code]. Each tool closes over the
// given Engine; workspace overrides the engine's default workspace (empty
// keeps the engine default). Mirrors Python's create_ladym_tools.
func CreateTools(eng *engine.Engine, workspace string, defaultTopK int) ([]lctools.Tool, error) {
	if defaultTopK <= 0 {
		defaultTopK = 8
	}
	ws := workspace
	if ws == "" {
		ws = eng.Config.Workspace
	}

	type recallArgs struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k,omitempty"`
	}
	type rememberArgs struct {
		Content string   `json:"content"`
		Tags    []string `json:"tags,omitempty"`
	}

	recallMemory, err := lctools.FromFunc("recall_memory",
		"Retrieve relevant long-term memories (facts, decisions, code, playbooks).\n\n"+
			"Call BEFORE answering when prior context, past decisions, or how-to "+
			"knowledge may be relevant. One line per hit, or \"(no hits)\".",
		func(ctx context.Context, in recallArgs) (lctools.Result, error) {
			topK := in.TopK
			if topK <= 0 {
				topK = defaultTopK
			}
			resp, err := eng.Recall(in.Query, ws, topK, nil, nil, 0)
			if err != nil {
				return lctools.Result{}, err
			}
			if len(resp.Results) == 0 {
				return lctools.Result{Content: "(no hits)"}, nil
			}
			lines := make([]string, 0, len(resp.Results))
			for _, r := range resp.Results {
				summary := r.Memory.Summary
				if summary == "" {
					summary = r.Memory.Content
				}
				lines = append(lines, fmt.Sprintf("[%s|%s|%.2f] %s", r.Memory.Layer, r.Memory.Type, r.Score, summary))
			}
			return lctools.Result{Content: strings.Join(lines, "\n")}, nil
		})
	if err != nil {
		return nil, err
	}

	rememberFact, err := lctools.FromFunc("remember_fact",
		"Persist a durable fact worth recalling later (user preference, key "+
			"decision, verified answer). Do NOT store ephemeral/transactional state.\n\n"+
			"Subject to ladyM's attention gate: low-value or duplicate content may "+
			"be dropped or rewritten automatically.",
		func(ctx context.Context, in rememberArgs) (lctools.Result, error) {
			m, err := eng.Remember(in.Content, schema.LayerSemantic, schema.TypeFact, in.Tags, nil, "langgraph-tool", "")
			if err != nil {
				return lctools.Result{}, err
			}
			gate, _ := m.Metadata["gated"].(string)
			if gate == "" {
				gate = "pass"
			}
			return lctools.Result{Content: fmt.Sprintf("stored id=%s gate=%s", m.ID, gate)}, nil
		})
	if err != nil {
		return nil, err
	}

	searchCode, err := lctools.FromFunc("search_code",
		"Search indexed source symbols/files. Use for 'how does X work in the "+
			"codebase' against previously index_code-indexed source.",
		func(ctx context.Context, in recallArgs) (lctools.Result, error) {
			topK := in.TopK
			if topK <= 0 {
				topK = defaultTopK
			}
			resp, err := eng.SearchCode(in.Query, topK, ws)
			if err != nil {
				return lctools.Result{}, err
			}
			if len(resp.Results) == 0 {
				return lctools.Result{Content: "(no hits)"}, nil
			}
			lines := make([]string, 0, len(resp.Results))
			for _, r := range resp.Results {
				qn, _ := r.Memory.Metadata["qualified_name"].(string)
				lines = append(lines, fmt.Sprintf("%s :: %s", r.Memory.Summary, qn))
			}
			return lctools.Result{Content: strings.Join(lines, "\n")}, nil
		})
	if err != nil {
		return nil, err
	}

	return []lctools.Tool{recallMemory, rememberFact, searchCode}, nil
}
