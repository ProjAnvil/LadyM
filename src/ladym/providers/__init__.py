"""External and pluggable providers for LadyM.

Re-exports the LLM provider abstraction (Task 1.6/1.7) and the per-operation agent
layer (Task 1.8) so callers can ``from ladym.providers import ...`` in one line.
"""

from .agents import NAMED_OPS, AgentConfig, AgentRegistry, make_agent
from .llm import (
    FakeLLMProvider,
    LangChainLLMProvider,
    LLMProvider,
    Message,
    make_llm_provider,
)

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
