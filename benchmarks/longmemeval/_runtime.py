"""Shared runtime helpers — load ladyM config and build per-instance Engines.

Centralised so every run module picks up the user's *configured* providers (embedding +
LLM) instead of the offline ``Config()`` defaults. The previous per-module
``_default_engine_factory`` copies each built ``Config(...)`` directly and silently fell
back to ``llm_provider="none"`` / ``embedding_provider="hashing"``, ignoring ``ladym.toml``.

``Config.load()`` resolves the 4-layer precedence
(defaults → ``~/.ladym/config.toml`` → ``./ladym.toml`` → ``config_path`` → env); the
Secret Store then supplies keys non-interactively when the LLM/embedding providers are
built. ``db_path``/``workspace`` are overridden per-instance on top of the loaded config,
so each LongMemEval instance stays isolated while still using the configured providers.
"""
from __future__ import annotations

from pathlib import Path


def load_cfg():
    """Return a Config loaded from ``ladym.toml`` + Secret Store (NOT bare ``Config()``)."""
    from ladym import Config
    return Config.load()


def make_engine(db_path, workspace):
    """Build a per-instance Engine using the user's configured embedding/LLM providers."""
    from ladym import Engine
    cfg = load_cfg()
    cfg.db_path = Path(db_path)
    cfg.workspace = workspace
    return Engine(cfg)
