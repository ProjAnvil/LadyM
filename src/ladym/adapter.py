"""Langchain → ladyM bridge layer.

Houses the adapters that wrap host-owned langchain objects (BaseChatModel,
Embeddings) into ladyM's own provider abstractions, plus ``ModelRouting`` —
the typed injection config. langchain types appear only under TYPE_CHECKING
so importing this module needs no langchain at runtime.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING

from .providers.llm import LLMProvider
from .storage.embeddings import EmbeddingProvider

if TYPE_CHECKING:
    from langchain_core.embeddings import Embeddings
    from langchain_core.language_models import BaseChatModel


def _to_lc(msg):
    """Convert a ladyM Message dict to a langchain message (moved from providers.llm)."""
    from langchain_core.messages import AIMessage, HumanMessage, SystemMessage

    if msg["role"] == "system":
        return SystemMessage(content=msg["content"])
    if msg["role"] == "assistant":
        return AIMessage(content=msg["content"])
    return HumanMessage(content=msg["content"])


class LangChainLLMProvider(LLMProvider):
    """Wrap a langchain ``BaseChatModel`` as a ladyM ``LLMProvider`` (moved from providers.llm)."""

    name = "langchain"

    def __init__(self, chat_model, structured_method: str = "function_calling"):
        self._cm = chat_model
        self._sm = structured_method

    def complete(self, messages, **params):
        return self._cm.invoke([_to_lc(m) for m in messages]).content

    def complete_structured(self, messages, schema, **params):
        runner = self._cm.with_structured_output(schema, method=self._sm)
        out = runner.invoke([_to_lc(m) for m in messages])
        return (
            out
            if isinstance(out, dict)
            else (out.model_dump() if hasattr(out, "model_dump") else dict(out))
        )


class LangChainEmbeddingAdapter(EmbeddingProvider):
    """Bridge a langchain ``Embeddings`` into ladyM's ``EmbeddingProvider``.

    ``dim`` starts ``None`` and is probed on the first :meth:`embed` call —
    same pattern as ``OllamaEmbedding``, so Engine's ``_ensure_provider_dim``
    handles it without special-casing.
    """

    def __init__(self, embeddings: Embeddings):
        self._lc = embeddings
        self.dim: int | None = None

    def embed(self, text: str) -> list[float]:
        vec = self._lc.embed_query(text)
        if self.dim is None:
            self.dim = len(vec)
        return vec

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return self._lc.embed_documents(texts)


@dataclass
class ModelRouting:
    """Inject host-owned langchain models, bypassing ladyM's own LLM/embedding config.

    Unset fields fall back to Config / heuristic. Field names mirror ``NAMED_OPS``
    so ``getattr(routing, op, None)`` resolves each cognitive operation.
    """

    consolidate: BaseChatModel | None = None
    proceduralize: BaseChatModel | None = None
    attention_gate: BaseChatModel | None = None
    l5_mental_model: BaseChatModel | None = None
    l6_forward_intent: BaseChatModel | None = None
    embedding: Embeddings | None = None
