package layers

import (
	"fmt"
	"strings"

	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// ProceduralMemory is L3 — reusable playbooks and verified snippets.
type ProceduralMemory struct {
	Store     *storage.SQLiteStore
	Embedder  storage.EmbeddingProvider
	Workspace string
}

// NewProceduralMemory builds a ProceduralMemory.
func NewProceduralMemory(store *storage.SQLiteStore, embedder storage.EmbeddingProvider, workspace string) *ProceduralMemory {
	if workspace == "" {
		workspace = "default"
	}
	return &ProceduralMemory{Store: store, Embedder: embedder, Workspace: workspace}
}

// PlaybookContent is the canonical content string for a playbook — the single
// source of truth shared by PutPlaybook and proceduralize. Matches Python
// “name + "\n" + "\n".join(steps)“: empty steps keeps the trailing newline;
// non-empty steps end without one.
func PlaybookContent(name string, steps []string) string {
	var sb strings.Builder
	sb.WriteString(name)
	sb.WriteString("\n")
	for i, s := range steps {
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "%d. %s", i+1, s)
	}
	return sb.String()
}

// PutPlaybook writes a playbook.
func (p *ProceduralMemory) PutPlaybook(name string, steps []string, preconditions []string, expectedOutcome string, tags []string) (*schema.Memory, error) {
	if preconditions == nil {
		preconditions = []string{}
	}
	if tags == nil {
		tags = []string{}
	}
	body := map[string]any{
		"name":             name,
		"preconditions":    preconditions,
		"steps":            steps,
		"expected_outcome": expectedOutcome,
	}
	content := PlaybookContent(name, steps)
	m := schema.NewMemory(schema.LayerProcedural, schema.TypePlaybook)
	m.Content = content
	m.Summary = name
	m.Tags = append(append([]string{}, tags...), "playbook")
	m.Metadata = body
	m.Source = "proceduralize"
	m.Workspace = p.Workspace
	m.ContentHash = schema.ContentHash(content)

	vec, err := p.Embedder.Embed(content)
	if err != nil {
		return nil, err
	}
	if err := p.Store.PutMemory(m, vec); err != nil {
		return nil, err
	}
	return m, nil
}

// PutSnippet writes a verified code snippet.
func (p *ProceduralMemory) PutSnippet(title, code, language string, tags []string) (*schema.Memory, error) {
	if language == "" {
		language = "python"
	}
	if tags == nil {
		tags = []string{}
	}
	content := fmt.Sprintf("%s\n```%s\n%s\n```", title, language, code)
	m := schema.NewMemory(schema.LayerProcedural, schema.TypeSnippet)
	m.Content = content
	m.Summary = title
	m.Tags = append(append([]string{}, tags...), "snippet", language)
	m.Metadata = map[string]any{"language": language, "code": code, "title": title}
	m.Workspace = p.Workspace

	vec, err := p.Embedder.Embed(content)
	if err != nil {
		return nil, err
	}
	if err := p.Store.PutMemory(m, vec); err != nil {
		return nil, err
	}
	return m, nil
}
