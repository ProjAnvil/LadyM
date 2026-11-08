package storage

import (
	"cmp"
	"errors"
	"strings"

	"github.com/ProjAnvil/LadyM/config"
	"github.com/ProjAnvil/LadyM/schema"
)

func (h *HashingEmbedding) Identity() schema.EmbeddingIdentity {
	return schema.EmbeddingIdentity{Provider: "hashing", Model: "blake2b-feature-v1", Dim: h.Dim(), Tokenizer: CJKTokenizerVersion()}
}

func (c *CachedEmbedding) Identity() schema.EmbeddingIdentity {
	if provider, ok := c.inner.(interface {
		Identity() schema.EmbeddingIdentity
	}); ok {
		return provider.Identity()
	}
	return schema.EmbeddingIdentity{}
}

func (o *OpenAIEmbedding) Identity() schema.EmbeddingIdentity {
	return schema.EmbeddingIdentity{Provider: "openai", Model: o.model, Dim: o.Dim(), Tokenizer: "provider-managed-v1"}
}

func (o *OllamaEmbedding) Identity() schema.EmbeddingIdentity {
	return schema.EmbeddingIdentity{Provider: "ollama", Model: o.model, Dim: o.Dim(), Tokenizer: "provider-managed-v1"}
}

// ProviderEmbeddingIdentity never guesses that an anonymous injected provider
// is equivalent merely because its dimension matches stored vectors.
func ProviderEmbeddingIdentity(cfg *config.Config, provider EmbeddingProvider, injected bool) (schema.EmbeddingIdentity, error) {
	if identified, ok := provider.(interface {
		Identity() schema.EmbeddingIdentity
	}); ok {
		identity := identified.Identity()
		if err := identity.Validate(); err == nil {
			if identity.Dim != provider.Dim() {
				return schema.EmbeddingIdentity{}, errors.New("embedding identity dimension differs from provider")
			}
			return identity, nil
		}
	}
	if injected {
		return schema.EmbeddingIdentity{}, errors.New("injected embedding provider requires explicit Identity()")
	}
	name := strings.ToLower(cfg.EmbeddingProvider)
	model := cfg.EmbeddingModel
	switch name {
	case "hashing":
		identity := NewHashingEmbedding(provider.Dim()).Identity()
		return identity, identity.Validate()
	case "openai":
		model = cmp.Or(model, "text-embedding-3-small")
	case "ollama":
		model = cmp.Or(model, "nomic-embed-text")
	}
	identity := schema.EmbeddingIdentity{Provider: name, Model: model, Dim: provider.Dim(), Tokenizer: "provider-managed-v1"}
	return identity, identity.Validate()
}
