package storage

import "context"

type contextualPoster interface {
	PostContext(context.Context, string, any, map[string]string) (any, error)
}
type scopedPoster struct {
	ctx    context.Context
	poster contextualPoster
}

func (p scopedPoster) Post(url string, payload any, headers map[string]string) (any, error) {
	return p.poster.PostContext(p.ctx, url, payload, headers)
}

func (o *OpenAIEmbedding) EmbedContext(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, ok := o.httpClient().(contextualPoster)
	if !ok {
		return nil, ErrEmbeddingContextUnsupported
	}
	view := *o
	view.client = scopedPoster{ctx: ctx, poster: client}
	return view.Embed(text)
}
func (o *OpenAIEmbedding) EmbedBatchContext(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, ok := o.httpClient().(contextualPoster)
	if !ok {
		return nil, ErrEmbeddingContextUnsupported
	}
	view := *o
	view.client = scopedPoster{ctx: ctx, poster: client}
	return view.EmbedBatch(texts)
}
func (o *OllamaEmbedding) EmbedContext(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, ok := o.httpClient().(contextualPoster)
	if !ok {
		return nil, ErrEmbeddingContextUnsupported
	}
	view := *o
	view.client = scopedPoster{ctx: ctx, poster: client}
	return view.Embed(text)
}
func (o *OllamaEmbedding) EmbedBatchContext(ctx context.Context, texts []string) ([][]float32, error) {
	var out [][]float32
	for _, text := range texts {
		v, err := o.EmbedContext(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (h *HashingEmbedding) EmbedContext(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vec, err := h.Embed(text)
	if err != nil {
		return nil, err
	}
	return vec, ctx.Err()
}
func (h *HashingEmbedding) EmbedBatchContext(ctx context.Context, texts []string) ([][]float32, error) {
	var out [][]float32
	for _, text := range texts {
		vec, err := h.EmbedContext(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	return out, nil
}

func (c *CachedEmbedding) EmbedContext(ctx context.Context, text string) ([]float32, error) {
	// Contextual maintenance does not mutate the legacy query LRU.
	return EmbedContext(ctx, c.inner, text)
}

func (c *CachedEmbedding) EmbedBatchContext(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p, ok := c.inner.(ContextEmbeddingProvider); ok {
		return p.EmbedBatchContext(ctx, texts)
	}
	return nil, ErrEmbeddingContextUnsupported
}
