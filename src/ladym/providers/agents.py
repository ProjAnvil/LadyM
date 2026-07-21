"""Per-operation agent configuration + factory.

Each cognitive operation (consolidate, proceduralize, attention_gate, l5_mental_model,
l6_forward_intent) can be bound to its own LLM provider/model, or fall back to the
``[llm]`` globals declared on :class:`Config`. ``make_agent`` returns ``None`` for the
heuristic (offline) mode so the engine can run with zero configuration.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

from ..config import Config
from .llm import LLMProvider, make_llm_provider

NAMED_OPS = (
    "consolidate",
    "proceduralize",
    "attention_gate",
    "l5_mental_model",
    "l6_forward_intent",
)


@dataclass
class AgentConfig:
    """Resolved per-operation LLM configuration.

    ``provider == "none"`` (or ``None``) means heuristic mode — no LLM call is made.
    """

    op: str
    provider: str = "none"
    base_url: str = ""
    model: str = ""
    api_key_env: str = ""
    prompt_template: str = ""
    max_tokens: int = 1024
    temperature: float = 0.2
    structured_method: str = "function_calling"
    timeout_s: float = 30.0
    api_key: str = ""  # plaintext key (when allow_plaintext_secrets=true); else ""


class AgentRegistry:
    """Builds :class:`AgentConfig` by layering per-op overrides on top of ``[llm]`` globals."""

    def __init__(self, cfg: Config):
        self._cfg = cfg

    def get(self, op: str) -> AgentConfig:
        if op not in NAMED_OPS:
            raise ValueError(f"unknown op {op!r}; expected one of {NAMED_OPS}")
        overrides = getattr(self._cfg, "agents_overrides", {}).get(op, {})
        # The offline default is the string ``"none"`` (Task 2.1 normalised
        # ``Config.llm_provider``'s default). Legacy callers may still pass
        # ``None``; the ``or "none"`` normalisation below keeps them working
        # so ``AgentConfig`` always carries a comparable provider token.
        provider = overrides.get("provider", self._cfg.llm_provider) or "none"
        return AgentConfig(
            op=op,
            provider=provider,
            base_url=overrides.get("base_url", self._cfg.llm_base_url),
            model=overrides.get("model", self._cfg.llm_model),
            api_key=overrides.get("api_key", getattr(self._cfg, "llm_api_key", "")),
            api_key_env=overrides.get(
                "api_key_env", getattr(self._cfg, "llm_api_key_env", "")
            ),
            prompt_template=overrides.get("prompt_template", ""),
            max_tokens=overrides.get("max_tokens", self._cfg.llm_max_tokens),
            temperature=overrides.get("temperature", self._cfg.llm_temperature),
            structured_method=overrides.get(
                "structured_method",
                getattr(self._cfg, "llm_structured_method", "function_calling"),
            ),
            timeout_s=overrides.get(
                "timeout_s",
                getattr(self._cfg, "llm_timeout_s", 30.0),
            ),
        )


def make_agent(cfg: Config, op: str) -> LLMProvider | None:
    """Build (or skip) the LLM provider bound to one operation.

    Returns ``None`` for heuristic mode (``provider`` not configured / set to ``"none"``),
    so callers can treat a ``None`` return as "no LLM — use the deterministic path".
    """
    ac = AgentRegistry(cfg).get(op)
    if ac.provider == "none":
        return None
    # Plaintext key (allow_plaintext_secrets=true) wins; else env-var lookup.
    api_key = ac.api_key or (os.environ.get(ac.api_key_env, "") if ac.api_key_env else "")
    return make_llm_provider(
        provider=ac.provider,
        base_url=ac.base_url,
        model=ac.model,
        api_key=api_key,
        structured_method=ac.structured_method,
        max_tokens=ac.max_tokens,
        temperature=ac.temperature,
        timeout_s=ac.timeout_s,
    )
