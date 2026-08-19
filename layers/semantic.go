package layers

import (
	"github.com/ProjAnvil/LadyM/schema"
	"github.com/ProjAnvil/LadyM/storage"
)

// SemanticMemory is L2 — consolidated facts and codebase analysis.
type SemanticMemory struct {
	Store     storage.Store
	Embedder  storage.EmbeddingProvider
	Workspace string
}

// NewSemanticMemory builds a SemanticMemory.
func NewSemanticMemory(store storage.Store, embedder storage.EmbeddingProvider, workspace string) *SemanticMemory {
	if workspace == "" {
		workspace = "default"
	}
	return &SemanticMemory{Store: store, Embedder: embedder, Workspace: workspace}
}

// PutFact writes a semantic fact directly.
func (s *SemanticMemory) PutFact(content, summary string, tags []string, metadata map[string]any, source string) (*schema.Memory, error) {
	if summary == "" {
		summary = truncate(content, 80)
	}
	if tags == nil {
		tags = []string{}
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeFact)
	m.Content = content
	m.Summary = summary
	m.Tags = tags
	m.Metadata = metadata
	m.Source = source
	m.Workspace = s.Workspace
	m.ContentHash = schema.ContentHash(content)

	vec, err := s.Embedder.Embed(content)
	if err != nil {
		return nil, err
	}
	if err := s.Store.PutMemory(m, vec); err != nil {
		return nil, err
	}
	return m, nil
}

// PutCodeFile writes a whole-file summary as L2 code memory.
func (s *SemanticMemory) PutCodeFile(filePath, summary, language string) (*schema.Memory, error) {
	tags := []string{"code"}
	if language != "" {
		tags = append(tags, language)
	}
	m := schema.NewMemory(schema.LayerSemantic, schema.TypeCodeFile)
	m.Content = filePath + ": " + summary
	m.Summary = truncate(summary, 120)
	m.Tags = tags
	m.Metadata = map[string]any{"file_path": filePath, "language": language}
	m.Source = filePath
	m.Workspace = s.Workspace
	m.ContentHash = schema.ContentHash(filePath + "|" + summary)

	vec, err := s.Embedder.Embed(m.Content)
	if err != nil {
		return nil, err
	}
	if err := s.Store.PutMemory(m, vec); err != nil {
		return nil, err
	}
	return m, nil
}

// FindByHash returns the memory with the given content hash.
func (s *SemanticMemory) FindByHash(h string) (*schema.Memory, error) {
	return s.Store.FindByHash(h, s.Workspace)
}
