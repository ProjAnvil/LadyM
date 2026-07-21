"""Runtime configuration for LadyM.

All values have sensible defaults so the engine works out-of-the-box with no env vars and no
network. Anything that needs a key/model is an opt-in override.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path


def _default_db_path() -> Path:
    # workspace-local so each project gets its own memory by default
    env = os.environ.get("LADYM_DB")
    if env:
        return Path(env)
    return Path.cwd() / "ladym.db"


@dataclass
class ActivationWeights:
    """Weights for the ACT-R-inspired activation function (ARCHITECTURE.md §4).

    All defaults are evidence-grounded starting points; tune per workload.
    """

    similarity: float = 1.0
    recency: float = 0.3
    frequency: float = 0.2
    graph: float = 0.15
    type_boost: float = 0.25
    recency_half_life_s: float = 7 * 24 * 3600.0  # one week


@dataclass
class RecallConfig:
    """Two-tier retrieval knobs (ARCHITECTURE.md §3)."""

    top_k_tier1: int = 8
    top_k_tier2: int = 20
    graph_hops: int = 2
    reflection_min_hits: int = 2
    reflection_min_coverage: float = 0.5  # fraction of query terms covered
    enable_tier2: bool = True


@dataclass
class ConsolidateConfig:
    """Knobs for L1→L2 consolidation."""

    min_episodes_to_trigger: int = 3
    dedup_similarity_threshold: float = 0.85


@dataclass
class CodeIndexConfig:
    """Knobs for codebase indexing."""

    max_body_lines_per_symbol: int = 40
    respect_gitignore: bool = True
    extra_ignore_globs: list[str] = field(
        default_factory=lambda: ["**/.venv/**", "**/node_modules/**", "**/__pycache__/**"]
    )
    languages: list[str] | None = None  # None = all supported


@dataclass
class Config:
    db_path: Path = field(default_factory=_default_db_path)
    workspace: str = field(default_factory=lambda: os.environ.get("LADYM_WORKSPACE", "default"))
    embedding_provider: str = field(
        default_factory=lambda: os.environ.get("LADYM_EMBEDDING", "hashing")
    )
    embedding_model: str = field(
        default_factory=lambda: os.environ.get("LADYM_EMBEDDING_MODEL", "")
    )
    embedding_dim: int = 256                  # for the hashing provider; overridden by others
    llm_provider: str | None = None           # None = no LLM (offline mode)
    llm_model: str = "gpt-4o-mini"
    activation: ActivationWeights = field(default_factory=ActivationWeights)
    recall: RecallConfig = field(default_factory=RecallConfig)
    consolidate: ConsolidateConfig = field(default_factory=ConsolidateConfig)
    code_index: CodeIndexConfig = field(default_factory=CodeIndexConfig)
    prefer_sqlite_vec: bool = True

    @classmethod
    def for_testing(cls, tmp_path: Path) -> Config:
        """A Config that points at a temp db and uses the offline hashing embedding.

        Uses the in-memory vector index (``prefer_sqlite_vec=False``) for deterministic,
        extension-free test runs; the store still persists embeddings to a BLOB column so a
        reopened engine can answer recall queries.
        """
        return cls(
            db_path=tmp_path / "test.ladym.db",
            workspace="test",
            embedding_provider="hashing",
            llm_provider=None,
            prefer_sqlite_vec=False,
        )
