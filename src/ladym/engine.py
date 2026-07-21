"""The Engine — single orchestrator and entry point for SDK / CLI / MCP.

Wires the store, embedding provider, the five layers, and the cognitive operations into one
object. All front-ends call the same Engine so behaviour is identical everywhere.
"""

from __future__ import annotations

import logging
import threading
from pathlib import Path
from typing import Any

from .config import Config
from .layers.associative import AssociativeMemory
from .layers.episodic import EpisodicMemory
from .layers.procedural import ProceduralMemory
from .layers.semantic import SemanticMemory
from .layers.working import WorkingMemory
from .operations.consolidate import (
    Action,
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
from .storage.embeddings import (
    EmbeddingProvider,
    EmbeddingProviderError,
    _assert_dim_matches,
    make_provider,
)
from .storage.store import SQLiteStore

logger = logging.getLogger("ladym.system2")


class Engine:
    """Top-level LadyM orchestrator. Owns the store + provider + operations."""

    def __init__(self, config: Config | None = None, *, config_obj: Config | None = None):
        # ``config_obj`` is accepted for naming clarity; ``config`` wins if both passed.
        cfg = config or config_obj or Config()
        self.config = cfg
        self.provider: EmbeddingProvider = make_provider(cfg)
        # Some providers (e.g. OllamaEmbedding) keep ``dim=None`` until the first embed()
        # call so construction doesn't hit the network and ``health_check`` can run without
        # a working endpoint. But SQLiteStore needs a concrete dim, and the dim-probe below
        # would persist the string "None" otherwise. Probe once, eagerly, before the store
        # is constructed.
        self._ensure_provider_dim()
        # SQLiteVec is preferred in production; tests pass prefer_sqlite_vec=False to keep
        # the index in-memory for determinism. Embeddings are always persisted as a BLOB so
        # either path survives a reopen.
        self.store = SQLiteStore(
            cfg.db_path, dim=self.provider.dim,
            prefer_sqlite_vec=cfg.prefer_sqlite_vec, enable_wal=cfg.enable_wal,
        )
        # convenience: monkeypatch hook so recall can find neighbour counts on the store
        if not hasattr(self.store, "associative_neighbour_counts"):
            self.store.associative_neighbour_counts = self._associative_neighbour_counts  # type: ignore[attr-defined]
        # Persist/probe the embedding dimension so a reopened DB cannot silently mix vectors
        # of different dims. On empty DB we record the probe; on mismatch we either re-embed
        # (if allowed) or refuse to start.
        self._enforce_embedding_dim()

        self.working = WorkingMemory(workspace=cfg.workspace)
        self.episodic = EpisodicMemory(self.store, self.provider, workspace=cfg.workspace)
        self.semantic = SemanticMemory(self.store, self.provider, workspace=cfg.workspace)
        self.procedural = ProceduralMemory(self.store, self.provider, workspace=cfg.workspace)
        self.associative = AssociativeMemory(self.store)

        self._llm_classify: LLMClassifier | None = None
        # Auto-wire the consolidate agent from config. With no LLM configured (offline
        # default) ``make_agent`` returns None and this is a clean no-op, keeping the
        # offline test suite green. With ``[agents.consolidate]`` (or ``[llm]`` globals)
        # configured, the engine gets a working classifier with no extra glue.
        self.attach_llm_classifier()

        # Per-operation LLM agent map. ``make_agent`` returns ``None`` for ops whose
        # provider resolves to ``"none"`` (the offline default), so in the offline
        # baseline every value is ``None`` and the heuristic code paths stay active.
        # The attention_gate entry controls whether ``Engine.remember`` consults an LLM
        # before writing to L1/L2/L3 (see operations.attention).
        from .providers import make_agent

        self._agents: dict = {
            "attention_gate": make_agent(cfg, "attention_gate"),
        }

    # ----- wiring helpers -----

    def attach_llm_classifier(self, fn: LLMClassifier | None = None) -> None:
        """Wire an LLM classifier for consolidation.

        Two modes (NFR-4 back-compat):

        * ``fn`` supplied — stored verbatim. Existing callers/tests that pass a callable
          keep working unchanged.
        * ``fn`` is ``None`` — build the ``consolidate`` agent from config via
          :func:`ladym.providers.make_agent`. Returns silently with ``_llm_classify``
          set to ``None`` when no LLM is configured (offline / heuristic mode).
        """
        if fn is not None:
            self._llm_classify = fn
            return
        from .providers import make_agent

        provider = make_agent(self.config, "consolidate")
        if provider is None:
            self._llm_classify = None
            return

        def _classify(candidate: str, similar: list[str]):
            from pydantic import BaseModel

            class _Decision(BaseModel):
                action: str
                new_text: str | None = None

            msgs = [
                {"role": "system", "content": _consolidate_prompt()},
                {
                    "role": "user",
                    "content": f"candidate: {candidate}\nsimilar: {similar}",
                },
            ]
            d = provider.complete_structured(msgs, _Decision)
            action_val = d["action"] if isinstance(d, dict) else d.action
            new_text = d.get("new_text") if isinstance(d, dict) else d.new_text
            return Action(action_val), new_text

        self._llm_classify = _classify

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
        from .operations.attention import attention_gate

        gate = attention_gate(content, engine=self, layer=layer)
        if gate.action == "drop":
            # Do NOT persist: return an unpersisted Memory tagged so callers can tell
            # the write was filtered (preserves the Memory return-type contract, NFR-4).
            return Memory(
                layer=layer,
                type=type_,
                content=content,
                summary=summary,
                tags=tags or [],
                metadata={
                    **(metadata or {}),
                    "gated": "dropped",
                    "reason": gate.reason,
                },
                source=source,
                workspace=self.config.workspace,
            )
        if gate.action == "rewrite" and gate.content:
            # SPEC §2.7: on rewrite, persist the original content under metadata["original"]
            # so the pre-rewrite text is recoverable for audit / undo.
            metadata = {
                **(metadata or {}),
                "gated": "rewritten",
                "original": content,
            }
            content = gate.content

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

    # ----- background System2 worker (opt-in, in-process) -----

    def start_system2(
        self,
        *,
        interval_s: int | None = None,
        workspace: str | None = None,
    ) -> threading.Event:
        """Launch a daemon thread running System2 cycles in the background.

        Returns a :class:`threading.Event`; ``set()`` it to ask the worker to
        stop after its current cycle. The worker runs in its OWN Engine (its
        own ``SQLiteStore`` / sqlite connection), opened in WAL mode so it can
        write while the caller's Engine reads without locking. WAL is enabled
        BEFORE the worker Engine is constructed (``enable_wal`` is consumed by
        ``SQLiteStore.__init__``); setting it post-construction is a no-op.

        Resilience: each cycle's exceptions are logged via
        ``logger.exception``; after
        ``config.system2.max_consecutive_errors`` consecutive failures the
        worker logs ``critical`` and stops (so a stale index / misconfigured
        LLM surfaces visibly instead of looping silently). A successful cycle
        resets the counter.
        """
        import copy

        stop = threading.Event()
        interval = interval_s if interval_s is not None else self.config.system2.interval_s
        max_errs = self.config.system2.max_consecutive_errors

        # Fresh Config so the worker's mutations can't leak back to self.config.
        worker_cfg = copy.copy(self.config)
        worker_cfg.enable_wal = True

        def _loop() -> None:
            from .operations import system2 as _sys2

            worker_eng = Engine(worker_cfg)
            consecutive_errs = 0
            try:
                while not stop.is_set():
                    # Worker must stay alive across a single bad cycle; log and
                    # try again next tick. Call through the module so tests can
                    # monkeypatch run_system2_cycle. After ``max_errs`` in a row
                    # we log critical and stop the thread.
                    try:
                        _sys2.run_system2_cycle(worker_eng, workspace=workspace)
                        consecutive_errs = 0
                    except Exception:
                        consecutive_errs += 1
                        logger.exception(
                            "system2 cycle failed (%d/%d consecutive)",
                            consecutive_errs,
                            max_errs,
                        )
                        if consecutive_errs >= max_errs:
                            logger.critical(
                                "system2 worker stopping after %d consecutive "
                                "failures (config: system2.max_consecutive_errors)",
                                consecutive_errs,
                            )
                            break
                    stop.wait(interval)
            finally:
                worker_eng.close()

        threading.Thread(target=_loop, daemon=True).start()
        return stop

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
        # token estimate via the existing tokenizer
        from .storage.embeddings import tokenize

        mems = list(self.store.iter_memories(workspace=self.config.workspace))
        total_tokens = sum(len(tokenize(m.content)) for m in mems)
        avg = (total_tokens / len(mems)) if mems else 0.0
        return Stats(
            total_memories=sum(by_layer.values()),
            by_layer=by_layer,
            by_type=by_type,
            edges=self.store.count_edges(),
            code_symbols=n_code_syms,
            workspaces=self.store.workspaces(),
            db_path=str(self.config.db_path),
            avg_tokens_per_memory=avg,
        )

    # ----- private -----

    def _associative_neighbour_counts(self) -> dict[str, int]:
        return self.associative.neighbor_counts()

    # ----- embedding-dim lifecycle -----

    def _ensure_provider_dim(self) -> None:
        """Force a concrete ``dim`` onto providers that defer it (e.g. ``OllamaEmbedding``).

        Providers may keep ``self.dim is None`` until the first :meth:`embed` call so that
        construction does not require a working endpoint (needed for ``health_check``). But
        the store and the dim-probe both need a concrete int. We probe once here, before
        either is constructed. A failing probe surfaces as
        :class:`EmbeddingProviderError` so callers can format a useful diagnostic.
        """
        if getattr(self.provider, "dim", None) is None:
            try:
                self.provider.embed("dimensionality probe")
            except Exception as e:  # noqa: BLE001
                raise EmbeddingProviderError(
                    f"cannot determine embedding dimension for provider "
                    f"{self.config.embedding_provider!r}: {e}"
                ) from e

    def _enforce_embedding_dim(self) -> None:
        """Persist the live provider's dim on an empty DB; refuse (or re-embed) on mismatch.

        Called once during ``__init__`` after the store is open. Reads the ``embedding_dim``
        key from the ``meta`` table (Task 1.3); when absent (fresh DB) we just record the
        current dim. When present and different, the behaviour depends on
        ``Config.embedding_allow_dim_change``: ``True`` wipes & re-embeds every memory;
        ``False`` (default) raises :class:`EmbeddingDimensionMismatch` so the operator decides.
        """
        stored = self.store.get_meta("embedding_dim")
        actual = self.provider.dim
        if stored is None:
            # fresh DB: probe & persist
            self.store.set_meta("embedding_dim", str(actual))
            self.store.set_meta("embedding_provider", self.config.embedding_provider)
            return
        if int(stored) != actual:
            if self.config.embedding_allow_dim_change:
                # The store was constructed with the new dim, but its ``vec_memories``
                # virtual table (and self.store.dim for sqlite-vec) still reflects the old
                # dim because the table already existed. Drop & rebuild before re-embedding.
                self.store.rebuild_vector_index(actual)
                self._reembed_all()
                self.store.set_meta("embedding_dim", str(actual))
                self.store.set_meta("embedding_provider", self.config.embedding_provider)
            else:
                _assert_dim_matches(stored=int(stored), configured=actual)

    def _reembed_all(self) -> None:
        """Re-embed every persisted memory with the current provider.

        Used when the operator opts into a dim change (``embedding_allow_dim_change=True``).
        ``put_memory`` rewrites both the embedding BLOB and the in-memory / sqlite-vec index,
        so a single pass is sufficient. The store was already constructed with the new dim,
        so the index accepts the new vectors directly.
        """
        # Snapshot first: ``put_memory`` mutates the rows we are iterating over.
        for m in list(self.store.iter_memories()):
            vec = self.provider.embed(m.content)
            self.store.put_memory(m, vector=vec)


def _consolidate_prompt() -> str:
    """System prompt for the LLM-backed consolidation classifier.

    Keeps the agent focused on the four candidate-fact decisions (ADD/UPDATE/DELETE/NOOP)
    that mirror mem0's Algorithm 1 and the offline heuristic in
    :mod:`ladym.operations.consolidate`.
    """
    return (
        "You classify a candidate fact against similar existing facts. "
        "Reply with JSON {action, new_text}. action in ADD|UPDATE|DELETE|NOOP. "
        "ADD=brand new; UPDATE=refines an existing one (set new_text); "
        "DELETE=existing is now wrong; NOOP=duplicate."
    )
