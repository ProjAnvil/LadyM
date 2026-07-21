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
