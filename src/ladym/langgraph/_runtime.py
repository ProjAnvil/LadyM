"""Shared runtime helpers for the LangGraph integration (paths A and B).

Keeps Engine lifecycle and (for the nodes path) workspace resolution in one
place so the two paths stay consistent and have a single seam against Engine.
"""
from __future__ import annotations

import copy
from pathlib import Path
from typing import Any

from ..config import Config
from ..engine import Engine


def resolve_engine(
    engine: Engine | Config | str | Path | None,
    *,
    workspace: str | None = None,
) -> Engine:
    """Return a ready-to-use Engine from flexible factory input.

    * ``Engine`` -> returned as-is (caller owns lifecycle); ``workspace`` is
      IGNORED — the engine's own ``config.workspace`` wins. Pass a ``Config``
      or path instead if you need a different workspace.
    * ``Config`` -> new Engine from a copy; ``workspace`` overrides ``cfg.workspace``.
    * ``str`` / ``Path`` -> ``Config(db_path=...)``; ``workspace`` honored.
    * ``None`` -> default ``Config()`` (offline hashing embedding); ``workspace`` honored.
    """
    if isinstance(engine, Engine):
        return engine
    if isinstance(engine, Config):
        cfg = copy.copy(engine)
    elif isinstance(engine, (str, Path)):
        cfg = Config(db_path=Path(engine))
    else:
        cfg = Config()
    if workspace:
        cfg.workspace = workspace
    return Engine(cfg)


def resolve_workspace(config: Any, engine: Engine) -> str:
    """Resolve the ladyM workspace for a LangGraph node invocation (path B).

    Reads ``config["configurable"]["user_id"]``; falls back to
    ``engine.config.workspace``. Typed ``Any`` so this module has no hard
    langchain dependency.
    """
    try:
        configurable = (config or {}).get("configurable", {}) or {}
    except AttributeError:
        return engine.config.workspace
    user_id = configurable.get("user_id")
    return str(user_id) if user_id else engine.config.workspace
