package storage

import (
	"fmt"

	"github.com/ProjAnvil/LadyM/schema"
)

func StoredEmbeddings(store Store, provider EmbeddingProvider, memories []*schema.Memory) ([][]float32, error) {
	fingerprint, err := store.GetMeta("embedding_fingerprint")
	if err != nil {
		return nil, err
	}
	if identified, ok := provider.(interface {
		Identity() schema.EmbeddingIdentity
	}); ok && fingerprint != "" && fingerprint != identified.Identity().Fingerprint() {
		return nil, &EmbeddingFingerprintMismatch{Stored: fingerprint, Configured: identified.Identity().Fingerprint()}
	}
	out := make([][]float32, len(memories))
	var texts []string
	var positions []int
	for i, m := range memories {
		if m.Type == schema.TypeResource && m.MetaString("resource_identity") == "path" {
			continue
		}
		vec, err := store.GetEmbedding(m.ID)
		if err != nil {
			return nil, err
		}
		if len(vec) > 0 && m.ContentHash == schema.ContentHash(m.Content) {
			out[i] = vec
		} else {
			texts = append(texts, m.Content)
			positions = append(positions, i)
		}
	}
	if len(texts) == 0 {
		return out, nil
	}
	vectors, err := provider.EmbedBatch(texts)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("stored embedding fallback: %d vectors for %d inputs", len(vectors), len(texts))
	}
	for i, position := range positions {
		out[position] = vectors[i]
	}
	return out, nil
}
