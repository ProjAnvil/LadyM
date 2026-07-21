"""LLM provider abstraction. Concrete providers are built on LangChain (Task 1.7),
but the ABC and the offline test double live here and import nothing heavy."""

from __future__ import annotations

from abc import ABC, abstractmethod
from collections.abc import Callable
from typing import Any, TypedDict

from pydantic import BaseModel


class Message(TypedDict):
    """A single message in an LLM conversation."""

    role: str  # "system" | "user" | "assistant"
    content: str


class LLMProvider(ABC):
    """Abstract base for LLM providers.

    Concrete implementations supply ``complete`` and ``complete_structured``.
    """

    name: str = "abstract"

    @abstractmethod
    def complete(self, messages: list[Message], **params: Any) -> str: ...

    @abstractmethod
    def complete_structured(
        self, messages: list[Message], schema: type[BaseModel], **params: Any
    ) -> dict: ...

    def close(self) -> None:  # noqa: B027
        """Release any held resources (default: no-op)."""


class FakeLLMProvider(LLMProvider):
    """Scriptable test double for LLMProvider.

    Pass ``complete_fn`` / ``structured_fn`` callables to script responses.
    Calling a method whose fn is ``None`` raises ``NotImplementedError``.
    """

    name = "fake"

    def __init__(
        self,
        *,
        complete_fn: Callable[[list[Message]], str] | None = None,
        structured_fn: Callable[[list[Message], type[BaseModel]], dict] | None = None,
    ) -> None:
        self._complete = complete_fn
        self._structured = structured_fn

    def complete(self, messages: list[Message], **params: Any) -> str:
        if self._complete is None:
            raise NotImplementedError("FakeLLMProvider has no complete_fn scripted")
        return self._complete(messages)

    def complete_structured(
        self, messages: list[Message], schema: type[BaseModel], **params: Any
    ) -> dict:
        if self._structured is None:
            raise NotImplementedError("FakeLLMProvider has no structured_fn scripted")
        return self._structured(messages, schema)


def _to_lc(msg: Message):
    from langchain_core.messages import (  # local import keeps ABC light
        AIMessage,
        HumanMessage,
        SystemMessage,
    )
    if msg["role"] == "system":
        return SystemMessage(content=msg["content"])
    if msg["role"] == "assistant":
        return AIMessage(content=msg["content"])
    return HumanMessage(content=msg["content"])


class LangChainLLMProvider(LLMProvider):
    name = "langchain"

    def __init__(self, chat_model, structured_method: str = "function_calling"):
        self._cm = chat_model
        self._sm = structured_method

    def complete(self, messages, **params):
        return self._cm.invoke([_to_lc(m) for m in messages]).content

    def complete_structured(self, messages, schema, **params):
        runner = self._cm.with_structured_output(schema, method=self._sm)
        out = runner.invoke([_to_lc(m) for m in messages])
        # pydantic v2 deprecates ``.dict()`` in favour of ``.model_dump()``; the
        # ``isinstance(out, dict)`` short-circuit stays first because some
        # langchain structured-output paths already return a plain dict.
        return (
            out
            if isinstance(out, dict)
            else (out.model_dump() if hasattr(out, "model_dump") else dict(out))
        )


def make_llm_provider(*, provider: str, base_url: str, model: str, api_key: str,
                      structured_method: str = "function_calling",
                      max_tokens: int = 1024, temperature: float = 0.2,
                      timeout_s: float = 30.0) -> LLMProvider | None:
    provider = (provider or "none").lower()
    if provider == "none":
        return None
    try:
        if provider == "openai" or provider == "http":
            from langchain_openai import ChatOpenAI
            cm = ChatOpenAI(base_url=base_url or None, model=model, api_key=api_key or None,
                            max_tokens=max_tokens, temperature=temperature, timeout=timeout_s)
        elif provider == "anthropic":
            from langchain_anthropic import ChatAnthropic
            cm = ChatAnthropic(base_url=base_url or None, model=model, api_key=api_key or None,
                               max_tokens=max_tokens, temperature=temperature, timeout=timeout_s)
        elif provider == "ollama":
            from langchain_ollama import ChatOllama
            cm = ChatOllama(base_url=base_url or None, model=model, temperature=temperature)
        else:
            raise ValueError(f"unknown llm provider: {provider}")
    except ImportError as e:  # pragma: no cover
        raise ImportError(
            f"LLM provider {provider!r} needs langchain extras: pip install 'ladym[llm]'"
        ) from e
    return LangChainLLMProvider(cm, structured_method)
