package storage

// CachedEmbedding wraps an inner EmbeddingProvider with an LRU cache for
// Embed() calls. EmbedBatch always delegates straight through.
type CachedEmbedding struct {
	inner EmbeddingProvider
	size  int
	order []string
	idx   map[string]int
	cache map[string][]float32
}

// NewCachedEmbedding wraps inner with an LRU cache of the given size.
func NewCachedEmbedding(inner EmbeddingProvider, size int) *CachedEmbedding {
	return &CachedEmbedding{
		inner: inner,
		size:  size,
		idx:   map[string]int{},
		cache: map[string][]float32{},
	}
}

func (c *CachedEmbedding) Dim() int { return c.inner.Dim() }

func (c *CachedEmbedding) Embed(text string) ([]float32, error) {
	if v, ok := c.cache[text]; ok {
		c.touch(text)
		return append([]float32{}, v...), nil
	}
	v, err := c.inner.Embed(text)
	if err != nil {
		return nil, err
	}
	c.cache[text] = v
	c.order = append(c.order, text)
	c.idx[text] = len(c.order) - 1
	if len(c.order) > c.size {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.cache, oldest)
		delete(c.idx, oldest)
		for i, k := range c.order {
			c.idx[k] = i
		}
	}
	return append([]float32{}, v...), nil
}

func (c *CachedEmbedding) touch(text string) {
	i, ok := c.idx[text]
	if !ok {
		return
	}
	c.order = append(append(append([]string{}, c.order[:i]...), c.order[i+1:]...), text)
	for j, k := range c.order {
		c.idx[k] = j
	}
}

func (c *CachedEmbedding) EmbedBatch(texts []string) ([][]float32, error) {
	return c.inner.EmbedBatch(texts)
}

func (c *CachedEmbedding) HealthCheck() (bool, string) { return c.inner.HealthCheck() }
