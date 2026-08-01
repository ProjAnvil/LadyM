"""Langchain → ladyM bridge layer.

Houses the adapters that wrap host-owned langchain objects (BaseChatModel,
Embeddings) into ladyM's own provider abstractions, plus ``ModelRouting`` —
the typed injection config. langchain types appear only under TYPE_CHECKING
so importing this module needs no langchain at runtime.
"""

from __future__ import annotations

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
