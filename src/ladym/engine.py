"""The Engine — single orchestrator and entry point for SDK / CLI / MCP.

Wires the store, embedding provider, the five layers, and the cognitive operations into one
object. All front-ends call the same Engine so behaviour is identical everywhere.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from .config import Config
from .layers.associative import AssociativeMemory
from .layers.episodic import EpisodicMemory
from .layers.procedural import ProceduralMemory
from .layers.semantic import SemanticMemory
from .layers.working import WorkingMemory
from .operations.consolidate import (
    ConsolidationReport,
    LLMClassifier,
    consolidate,
)
from .operations.decay import DecayReport, decay
from .operations.proceduralize import ProceduralizeReport, proceduralize
from .operations.recall import recall
from .schema import (
    Edge,
    Layer,
    Memory,
    MemoryType,
    RecallResponse,
    Stats,
)
from .storage.embeddings import EmbeddingProvider, make_provider
from .storage.store import SQLiteStore


class Engine:
    """Top-level LadyM orchestrator. Owns the store + provider + operations."""

    def __init__(self, config: Config | None = None, *, config_obj: Config | None = None):
        # ``config_obj`` is accepted for naming clarity; ``config`` wins if both passed.
        cfg = config or config_obj or Config()
        self.config = cfg
        self.provider: EmbeddingProvider = make_provider(cfg)
        # SQLiteVec is preferred in production; tests pass prefer_sqlite_vec=False to keep
        # the index in-memory for determinism. Embeddings are always persisted as a BLOB so
        # either path survives a reopen.
        self.store = SQLiteStore(
            cfg.db_path, dim=self.provider.dim, prefer_sqlite_vec=cfg.prefer_sqlite_vec
        )
        # convenience: monkeypatch hook so recall can find neighbour counts on the store
        if not hasattr(self.store, "associative_neighbour_counts"):
            self.store.associative_neighbour_counts = self._associative_neighbour_counts  # type: ignore[attr-defined]

        self.working = WorkingMemory(workspace=cfg.workspace)
        self.episodic = EpisodicMemory(self.store, self.provider, workspace=cfg.workspace)
        self.semantic = SemanticMemory(self.store, self.provider, workspace=cfg.workspace)
        self.procedural = ProceduralMemory(self.store, self.provider, workspace=cfg.workspace)
        self.associative = AssociativeMemory(self.store)

        self._llm_classify: LLMClassifier | None = None

    # ----- wiring helpers -----

    def attach_llm_classifier(self, fn: LLMClassifier) -> None:
        """Wire in an LLM-backed ADD/UPDATE/DELETE/NOOP classifier for consolidation."""
        self._llm_classify = fn

    def close(self) -> None:
        self.store.close()

    def __enter__(self) -> Engine:
        return self

    def __exit__(self, *exc: Any) -> None:
        self.close()

    # ----- write path -----

    def remember(
        self,
        content: str,
        *,
        layer: Layer = Layer.SEMANTIC,
        type_: MemoryType = MemoryType.FACT,
        tags: list[str] | None = None,
        metadata: dict[str, Any] | None = None,
        source: str = "",
        summary: str = "",
    ) -> Memory:
        """Generic write. Routes to the right layer based on ``layer``/``type_``."""
        if layer == Layer.WORKING:
            return self.working.push(
                content, tags=tags, metadata=metadata, source=source
            )
        if layer == Layer.EPISODIC:
            return self.episodic.record(
                agent=source or "user",
                action=summary or content[:80],
                observation=content,
                tags=tags,
                metadata=metadata,
            )
        if layer == Layer.PROCEDURAL:
            if type_ == MemoryType.SNIPPET:
                return self.procedural.put_snippet(summary or "snippet", content, tags=tags)
            return self.procedural.put_playbook(
                summary or content[:80], content.split("\n"), tags=tags
            )
        # default: semantic
        return self.semantic.put_fact(
            content, summary=summary, tags=tags, metadata=metadata, source=source
        )

    def record_event(
        self, *, agent: str, action: str, observation: str = "",
        outcome: str = "", tags: list[str] | None = None,
        metadata: dict[str, Any] | None = None,
    ) -> Memory:
        return self.episodic.record(
            agent=agent, action=action, observation=observation,
            outcome=outcome, tags=tags, metadata=metadata,
        )

    def link(self, src_id: str, dst_id: str, relation: str = "related_to",
             **kw: Any) -> Edge:
        return self.associative.link(src_id, dst_id, relation, **kw)

    # ----- read path -----

    def recall(
        self,
        query: str,
        *,
        workspace: str | None = None,
        top_k: int | None = None,
        layers: list[Layer] | None = None,
        types: list[MemoryType] | None = None,
        min_similarity: float = 0.0,
    ) -> RecallResponse:
        return recall(
            self.store,
            self.provider,
            query,
            cfg=self.config,
            workspace=workspace,
            top_k=top_k,
            layers=layers,
            types=types,
            min_similarity=min_similarity,
        )

    def search_code(
        self, query: str, *, top_k: int = 10, workspace: str | None = None,
    ) -> RecallResponse:
        """Code-only shortcut — restricts layers/types to L2 code items."""
        return self.recall(
            query,
            workspace=workspace,
            top_k=top_k,
            layers=[Layer.SEMANTIC],
            types=[MemoryType.CODE_SYMBOL, MemoryType.CODE_FILE],
            min_similarity=0.01,
        )

    # ----- cognitive operations -----

    def consolidate(self, *, workspace: str | None = None,
                    since: float | None = None) -> ConsolidationReport:
        return consolidate(
            self.store,
            self.provider,
            cfg=self.config,
            workspace=workspace,
            llm_classify=self._llm_classify,
            since=since,
        )

    def proceduralize(self, *, workspace: str | None = None,
                      min_cluster_size: int = 3) -> ProceduralizeReport:
        return proceduralize(
            self.store, self.provider, cfg=self.config,
            workspace=workspace, min_cluster_size=min_cluster_size,
        )

    def decay(self, *, workspace: str | None = None, dry_run: bool = False,
              max_age_s: float | None = None,
              activation_floor: float = 0.05) -> DecayReport:
        return decay(
            self.store,
            workspace=workspace,
            weights=self.config.activation,
            max_age_s=max_age_s if max_age_s is not None else 30 * 24 * 3600.0,
            activation_floor=activation_floor,
            dry_run=dry_run,
        )

    def index_code(self, root: str | Path, *, force: bool = False,
                   workspace: str | None = None,
                   languages: list[str] | None = None):
        from .code.indexer import index_codebase
        return index_codebase(
            Path(root), self.store, self.provider,
            cfg=self.config, workspace=workspace, force=force, language_filter=languages,
        )

    def forget(self, memory_id: str) -> None:
        self.store.delete_memory(memory_id)

    # ----- introspection -----

    def stats(self) -> Stats:
        counts = self.store.count(workspace=self.config.workspace)
        by_layer: dict[str, int] = {}
        by_type: dict[str, int] = {}
        for k, n in counts.items():
            layer, _, type_ = k.partition("/")
            by_layer[layer] = by_layer.get(layer, 0) + n
            by_type[type_] = by_type.get(type_, 0) + n
        n_code_syms = self.store.conn.execute(
            "SELECT COUNT(*) FROM code_symbols"
        ).fetchone()[0]
        return Stats(
            total_memories=sum(by_layer.values()),
            by_layer=by_layer,
            by_type=by_type,
            edges=self.store.count_edges(),
            code_symbols=n_code_syms,
            workspaces=self.store.workspaces(),
            db_path=str(self.config.db_path),
        )

    # ----- private -----

    def _associative_neighbour_counts(self) -> dict[str, int]:
        return self.associative.neighbor_counts()
