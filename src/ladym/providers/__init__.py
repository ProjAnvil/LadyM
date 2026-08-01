"""External and pluggable providers for LadyM.

Re-exports the LLM provider abstraction (Task 1.6/1.7) and the per-operation agent
layer (Task 1.8) so callers can ``from ladym.providers import ...`` in one line.
"""

from .agents import NAMED_OPS, AgentConfig, AgentRegistry, make_agent
from .llm import (
    FakeLLMProvider,
    LLMProvider,
    Message,
    make_llm_provider,
)


def __getattr__(name):
    """Lazy re-export to avoid adapter↔providers.llm circular import.

    ``LangChainLLMProvider`` now lives in ``ladym.adapter``. adapter.py imports
    ``LLMProvider`` from ``providers.llm`` at module top level (needed for class
    inheritance), so a top-level ``from ..adapter import LangChainLLMProvider``
    here would cycle: adapter → providers/__init__ → adapter (partially loaded).
    PEP 562 ``__getattr__`` defers the re-export until first attribute access,
    by which point both modules are fully initialized.
    """
    if name == "LangChainLLMProvider":
        from ..adapter import LangChainLLMProvider

        return LangChainLLMProvider
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")

__all__ = [
    "AgentConfig",
    "AgentRegistry",
    "NAMED_OPS",
    "make_agent",
    "FakeLLMProvider",
    "LangChainLLMProvider",
    "LLMProvider",
    "Message",
    "make_llm_provider",
]
