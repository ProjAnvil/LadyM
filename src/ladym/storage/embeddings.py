"""Pluggable embedding providers.

The default :class:`HashingEmbedding` is fully deterministic and dependency-free, which keeps
the test suite hermetic and lets LadyM run with zero network and zero model downloads.
Heavier providers (sentence-transformers, OpenAI) are loaded lazily behind the same ABC.
"""

from __future__ import annotations

import hashlib
import math
import re
from abc import ABC, abstractmethod

from ..config import Config

_TOKEN_RE = re.compile(r"[A-Za-z0-9_]+|[\.\,\;\:\(\)\[\]\{\}]")


def tokenize(text: str) -> list[str]:
    """Lightweight tokenizer: words + punctuation as separate tokens.

    Also splits camelCase and snake_case so ``getUserName`` and ``get_user_name`` tokenize
    similarly — important for code retrieval.
    """
    out: list[str] = []
    for raw in _TOKEN_RE.findall(text or ""):
        # split camelCase / PascalCase
        parts = re.findall(r"[A-Z]+(?=[A-Z][a-z])|[A-Z]?[a-z]+|[A-Z]+|\d+", raw)
        if parts:
            out.extend(p.lower() for p in parts)
        else:
            out.append(raw.lower())
    return out


class EmbeddingProvider(ABC):
    """The contract every provider implements."""

    dim: int

    @abstractmethod
    def embed(self, text: str) -> list[float]:
        ...

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [self.embed(t) for t in texts]

    def health_check(self) -> "tuple[bool, str]":
        """One-shot probe for the web UI 'test embedding' button."""
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:  # noqa: BLE001
            return False, f"{type(e).__name__}: {e}"


class HashingEmbedding(EmbeddingProvider):
    """Deterministic, offline, dependency-free embedding via feature hashing.

    Each token is hashed into ``dim`` buckets with a sign derived from a second hash, then
    L2-normalised. Cosine similarity therefore reflects token overlap (with shingle-like
    locality from the unigram + bigram features). Not as good as a real model, but
    reproducible and free — perfect for tests, CI, and small private repos.
    """

    def __init__(self, dim: int = 256):
        self.dim = dim

    def _features(self, text: str) -> list[str]:
        toks = tokenize(text)
        feats = list(toks)
        feats.extend(f"{a}_{b}" for a, b in zip(toks, toks[1:], strict=False))  # bigrams
        return feats

    def embed(self, text: str) -> list[float]:
        vec = [0.0] * self.dim
        for feat in self._features(text):
            h = hashlib.blake2b(feat.encode(), digest_size=8).digest()
            bucket = int.from_bytes(h[:4], "little") % self.dim
            sign = 1.0 if (h[4] & 1) == 0 else -1.0
            vec[bucket] += sign
        # L2 normalise (cosine is what the retriever uses)
        norm = math.sqrt(sum(v * v for v in vec)) or 1.0
        return [v / norm for v in vec]


class SentenceTransformerEmbedding(EmbeddingProvider):
    """Local quality embeddings via sentence-transformers (lazy import)."""

    def __init__(self, model: str = "sentence-transformers/all-MiniLM-L6-v2"):
        try:
            from sentence_transformers import SentenceTransformer  # type: ignore
        except ImportError as e:  # pragma: no cover - optional dep
            raise ImportError(
                "sentence-transformers is not installed. "
                "Install with: pip install 'ladym[local]'"
            ) from e
        self._model = SentenceTransformer(model)
        self.dim = int(self._model.get_sentence_embedding_dimension())

    def embed(self, text: str) -> list[float]:
        return self._model.encode(text, normalize_embeddings=True).tolist()  # type: ignore[no-any-return]

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return [v.tolist() for v in self._model.encode(texts, normalize_embeddings=True)]  # type: ignore[no-any-return]


class OpenAIEmbedding(EmbeddingProvider):
    """Hosted embeddings via the OpenAI API (lazy import)."""

    def __init__(self, model: str = "text-embedding-3-small", dim: int = 1536):
        try:
            from openai import OpenAI  # type: ignore
        except ImportError as e:  # pragma: no cover - optional dep
            raise ImportError(
                "openai is not installed. Install with: pip install 'ladym[openai]'"
            ) from e
        self._client = OpenAI()
        self._model = model
        self.dim = dim

    def embed(self, text: str) -> list[float]:
        resp = self._client.embeddings.create(model=self._model, input=text)
        return resp.data[0].embedding  # type: ignore[no-any-return]

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        resp = self._client.embeddings.create(model=self._model, input=texts)
        return [d.embedding for d in sorted(resp.data, key=lambda x: x.index)]  # type: ignore[no-any-return]


def cosine_similarity(a: list[float], b: list[float]) -> float:
    """Cosine of two vectors that are already (or assumed) normalized."""
    if len(a) != len(b):
        return 0.0
    dot = sum(x * y for x, y in zip(a, b, strict=False))
    na = math.sqrt(sum(x * x for x in a))
    nb = math.sqrt(sum(y * y for y in b))
    if na == 0.0 or nb == 0.0:
        return 0.0
    return dot / (na * nb)


def make_provider(config: Config) -> EmbeddingProvider:
    """Factory that resolves the configured provider."""
    name = config.embedding_provider.lower()
    if name == "hashing":
        return HashingEmbedding(dim=config.embedding_dim)
    if name in ("st", "sentence-transformer", "sentence_transformers"):
        return SentenceTransformerEmbedding(config.embedding_model or None)  # type: ignore[arg-type]
    if name == "openai":
        return OpenAIEmbedding(config.embedding_model or "text-embedding-3-small")
    raise ValueError(f"unknown embedding provider: {name}")
