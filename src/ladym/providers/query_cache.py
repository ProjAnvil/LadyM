"""Optional LRU cache for single-query embeddings."""

from __future__ import annotations

from collections import OrderedDict

from ..storage.embeddings import EmbeddingProvider


class CachedEmbedding(EmbeddingProvider):
    """Wraps an inner :class:`EmbeddingProvider` with an LRU cache for ``embed()`` calls.

    ``embed_batch`` always delegates straight through (bypasses the cache) because the
    overhead of de-duplicating a batch list usually isn't worth it for the typical single-
    query path this cache is designed to accelerate.
    """

    def __init__(self, inner: EmbeddingProvider, size: int):
        self._inner = inner
        self._size = size
        self._cache: OrderedDict[str, list[float]] = OrderedDict()
        self.dim = inner.dim

    def embed(self, text: str) -> list[float]:
        if text in self._cache:
            self._cache.move_to_end(text)
            return self._cache[text]
        v = self._inner.embed(text)
        self._cache[text] = v
        if len(self._cache) > self._size:
            self._cache.popitem(last=False)
        return v

    def embed_batch(self, texts):
        return self._inner.embed_batch(texts)  # batches bypass the cache

    def health_check(self):
        return self._inner.health_check()
