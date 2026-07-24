"""LadyM — a brain-inspired memory framework for LLM agents and codebase RAG.

Public surface:

    from ladym import Engine, Config, Layer, MemoryType
    eng = Engine(Config(db_path="ladym.db"))
    eng.index_code("./src")
    eng.remember("auth uses JWT with 24h expiry")
    hits = eng.recall("how does authentication work")

See ARCHITECTURE.md for the design and README.md for usage.
"""

from __future__ import annotations

from .config import (
    ActivationWeights,
    CodeIndexConfig,
    Config,
    ConsolidateConfig,
    RecallConfig,
)
from .engine import Engine
from .schema import (
    CodeSymbol,
    Edge,
    Layer,
    Memory,
    MemoryType,
    RecallResponse,
    RecallResult,
    Stats,
)

__all__ = [
    "ActivationWeights",
    "CodeIndexConfig",
    "Config",
    "ConsolidateConfig",
    "Engine",
    "RecallConfig",
    "Layer",
    "Memory",
    "MemoryType",
    "Edge",
    "CodeSymbol",
    "RecallResponse",
    "RecallResult",
    "Stats",
]

__version__ = "0.1.0"
