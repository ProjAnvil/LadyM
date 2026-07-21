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
from collections.abc import Callable

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

    def health_check(self) -> tuple[bool, str]:
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
    """Hosted embeddings via the OpenAI API (lazy import).

    ``base_url`` lets the same class target any OpenAI-compatible third party (vLLM, Together,
    Groq, local LM Studio, …) per SPEC AC-1.6. Defaults to ``None`` → OpenAI's own endpoint.
    """

    def __init__(self, model: str = "text-embedding-3-small", dim: int = 1536,
                 base_url: str | None = None):
        try:
            from openai import OpenAI  # type: ignore
        except ImportError as e:  # pragma: no cover - optional dep
            raise ImportError(
                "openai is not installed. Install with: pip install 'ladym[openai]'"
            ) from e
        self._client = OpenAI(base_url=base_url) if base_url else OpenAI()
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


class EmbeddingProviderError(RuntimeError):
    """Base class for provider-side errors (dim mismatch, transport failure, …)."""


class EmbeddingDimensionMismatch(EmbeddingProviderError):
    """Raised when a reopened DB holds vectors of a different dim than the live provider.

    Carries the two dims so callers (CLI, web UI) can format their own message.
    """

    def __init__(self, stored: int, configured: int):
        super().__init__(
            f"embedding dim mismatch: DB has {stored}-dim vectors but provider returns "
            f"{configured}-dim. Set embedding.allow_dim_change=true to wipe and re-embed."
        )
        self.stored = stored
        self.configured = configured


def _assert_dim_matches(*, stored: int, configured: int) -> None:
    """Pure helper — raises :class:`EmbeddingDimensionMismatch` if dims differ."""
    if stored != configured:
        raise EmbeddingDimensionMismatch(stored, configured)


# Registry for user-supplied callable embeddings (referenced by name via
# ``Config.embedding_model``). Populated via :func:`register_callable`.
_callable_registry: dict[str, Callable[[str], list[float]]] = {}


def register_callable(name: str, fn: Callable[[str], list[float]]) -> None:
    """Register a Python function as a named embedding provider.

    ``Config(embedding_provider="callable", embedding_model=name)`` then resolves to a
    :class:`~ladym.providers.embeddings_http.CallableEmbedding` wrapping ``fn``.
    """
    _callable_registry[name] = fn


def make_provider(config: Config) -> EmbeddingProvider:
    """Factory that resolves the configured provider.

    Routes between the offline :class:`HashingEmbedding`, heavy local models
    (sentence-transformers), hosted (OpenAI, including OpenAI-compatible third parties via
    ``base_url``), and the external HTTP providers added in Task 1.2 (Ollama, generic HTTP,
    user callables). Optionally wraps the result in :class:`CachedEmbedding` when
    ``config.embedding_query_cache_size > 0``.
    """
    name = config.embedding_provider.lower()
    if name == "hashing":
        provider: EmbeddingProvider = HashingEmbedding(dim=config.embedding_dim)
    elif name in ("st", "sentence-transformer", "sentence_transformers"):
        provider = SentenceTransformerEmbedding(config.embedding_model or None)  # type: ignore[arg-type]
    elif name == "openai":
        provider = OpenAIEmbedding(
            config.embedding_model or "text-embedding-3-small",
            base_url=config.embedding_base_url or None,
        )
    elif name == "ollama":
        from ..providers.embeddings_http import OllamaEmbedding
        provider = OllamaEmbedding(
            config.embedding_base_url or "http://localhost:11434",
            config.embedding_model or "nomic-embed-text",
            timeout_s=config.embedding_timeout_s,
        )
    elif name == "http":
        from ..providers.embeddings_http import HttpEmbedding
        provider = HttpEmbedding(
            config.embedding_base_url,
            request_template=config.embedding_http_request,
            response_path=config.embedding_http_response_path,
            dim=config.embedding_dim,
            model=config.embedding_model,
            timeout_s=config.embedding_timeout_s,
        )
    elif name == "callable":
        fn = _callable_registry.get(config.embedding_model)
        if fn is None:
            raise ValueError(
                f"no callable embedding registered under {config.embedding_model!r} "
                "(call ladym.storage.embeddings.register_callable first)"
            )
        from ..providers.embeddings_http import CallableEmbedding
        provider = CallableEmbedding(fn, dim=config.embedding_dim)
    else:
        raise ValueError(f"unknown embedding provider: {name}")

    if config.embedding_query_cache_size > 0:
        from ..providers.query_cache import CachedEmbedding
        provider = CachedEmbedding(provider, config.embedding_query_cache_size)
    return provider
