"""Core data models for LadyM.

All memory items — whether an episodic event, a consolidated fact, a code symbol, or a
procedural playbook — are represented by a single :class:`Memory` record. This unification is
what lets the recall pipeline treat "memory" and "codebase RAG" as one system (see
ARCHITECTURE.md §1).
"""

from __future__ import annotations

import time
import uuid
from enum import StrEnum
from typing import Any

from pydantic import BaseModel, Field


def _now() -> float:
    return time.time()


def _new_id() -> str:
    return uuid.uuid4().hex


class Layer(StrEnum):
    """The five memory layers, see ARCHITECTURE.md §1."""

    WORKING = "L0_working"        # in-process scratch
    EPISODIC = "L1_episodic"      # time-stamped events
    SEMANTIC = "L2_semantic"      # consolidated facts + code analysis
    PROCEDURAL = "L3_procedural"  # how-to playbooks
    ASSOCIATIVE = "L4_associative"  # graph edges (handled via Edge, not Memory rows)
    L5_MENTAL = "L5_mental"          # mental models (schema-only, post-MVP extraction)
    L6_PREDICTIVE = "L6_predictive"  # forward intent (schema-only, post-MVP extraction)


class MemoryType(StrEnum):
    """Sub-types within a layer. L2 is the most polymorphic."""

    NOTE = "note"               # free-form working/semantic note
    EVENT = "event"             # episodic (agent, action, observation, outcome)
    FACT = "fact"               # consolidated semantic fact
    CODE_FILE = "code_file"     # whole-file summary
    CODE_SYMBOL = "code_symbol"  # function/class/method/doc
    PLAYBOOK = "playbook"       # procedural steps
    SNIPPET = "snippet"         # verified reusable code
    MENTAL_MODEL = "mental_model"      # L5 mental model (schema-only, post-MVP)
    FORWARD_INTENT = "forward_intent"  # L6 forward intent (schema-only, post-MVP)


class Memory(BaseModel):
    """A single memory item. Maps to one row in the ``memories`` table."""

    id: str = Field(default_factory=_new_id)
    layer: Layer
    type: MemoryType
    content: str                                # canonical text (what gets embedded)
    summary: str = ""                           # short label for tier-1 retrieval
    tags: list[str] = Field(default_factory=list)
    metadata: dict[str, Any] = Field(default_factory=dict)
    source: str = ""                            # file path / agent name / url
    workspace: str = "default"                  # multi-workspace isolation

    created_at: float = Field(default_factory=_now)
    updated_at: float = Field(default_factory=_now)
    last_access_at: float = Field(default_factory=_now)
    access_count: int = 0
    activation: float = 0.0                     # cached activation score
    content_hash: str = ""                      # for dedup / incremental re-index

    # Vector is stored separately (BLOB) but kept on the model for in-memory flows.
    embedding: list[float] | None = None

    model_config = {"use_enum_values": True}

    def touch(self) -> None:
        """Mark this memory as accessed (called by the retriever)."""
        self.last_access_at = _now()
        self.access_count += 1


class Edge(BaseModel):
    """A Zettelkasten-style link between two memories (L4 associative)."""

    id: str = Field(default_factory=_new_id)
    src_id: str
    relation: str                               # e.g. "calls", "related_to", "contradicts"
    dst_id: str
    weight: float = 1.0
    valid_from: float = Field(default_factory=_now)   # Zep-style temporal validity
    valid_to: float | None = None                     # None = still valid
    metadata: dict[str, Any] = Field(default_factory=dict)


class CodeSymbol(BaseModel):
    """Structured projection of a code-bearing Memory (type=CODE_SYMBOL)."""

    memory_id: str                              # FK -> Memory.id
    file_path: str
    symbol_kind: str                            # function | class | method | module
    qualified_name: str                         # module.Class.method
    signature: str = ""
    docstring: str = ""
    line_start: int = 0
    line_end: int = 0
    language: str = ""


class CodeRef(BaseModel):
    """A cross-reference between two symbols (calls / imports / defines)."""

    src_symbol: str                             # qualified_name
    dst_symbol: str
    ref_kind: str = "calls"


class RecallResult(BaseModel):
    """One hit returned by :func:`recall`."""

    memory: Memory
    score: float                                # final activation score
    tier: int = 1                              # which retrieval tier produced it (1 or 2)
    via: list[str] = Field(default_factory=list)  # graph hops / backtrack path


class RecallResponse(BaseModel):
    """The full response from a recall call."""

    query: str
    results: list[RecallResult]
    tier_reached: int                          # deepest tier that ran
    reflected_sufficient: bool                 # did reflect() say "enough"?
    elapsed_ms: float


class Stats(BaseModel):
    """Aggregate stats for the ``stats`` tool / command."""

    total_memories: int
    by_layer: dict[str, int]
    by_type: dict[str, int]
    edges: int
    code_symbols: int
    workspaces: list[str]
    db_path: str
    avg_tokens_per_memory: float = 0.0
