"""Python SDK for the LadyM HTTP data-plane (`ladym serve --http`).

Zero runtime dependencies (stdlib urllib only). The wire format mirrors
client/golang; non-2xx responses raise LadymError carrying the HTTP status.
"""

from __future__ import annotations

import base64
import json
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Callable, Optional

__all__ = [
    "Client",
    "LadymError",
    "RememberResult",
    "RecallResponse",
    "RecallResult",
    "Memory",
    "RecordEventResult",
    "ConsolidateResult",
    "Stats",
    "MemoryList",
    "User",
]

# Transport takes (method, path, query, body) and returns (status, parsed JSON
# body as a dict). Injectable for tests and custom HTTP stacks.
Transport = Callable[[str, str, dict, Optional[dict]], "tuple[int, dict]"]


class LadymError(Exception):
    """A non-2xx response from the server. Message is the server's
    {"error": ...} field, falling back to the trimmed body."""

    def __init__(self, status: int, message: str):
        super().__init__(f"ladym server returned http {status}: {message}")
        self.status = status
        self.message = message


@dataclass
class Memory:
    """One memory as returned in recall results / memory lists."""

    id: str = ""
    layer: str = ""
    type: str = ""
    summary: str = ""
    content: str = ""
    source: str = ""
    tags: list = field(default_factory=list)
    workspace: str = ""
    metadata: Optional[dict] = None
    created_at: float = 0.0
    updated_at: float = 0.0

    @classmethod
    def from_json(cls, d: dict) -> "Memory":
        known = {f for f in cls.__dataclass_fields__}
        return cls(**{k: v for k, v in d.items() if k in known})


@dataclass
class RememberResult:
    """The /api/remember response. On an attention-gate drop nothing is
    persisted: gated == "dropped" and reason explains why."""

    id: str = ""
    hash: str = ""
    gated: str = ""
    reason: str = ""

    @property
    def dropped(self) -> bool:
        return self.gated == "dropped"


@dataclass
class RecallResult:
    score: float = 0.0
    tier: int = 0
    via: list = field(default_factory=list)
    memory: Memory = field(default_factory=Memory)


@dataclass
class RecallResponse:
    query: str = ""
    tier_reached: int = 0
    reflected_sufficient: bool = False
    elapsed_ms: float = 0.0
    results: list = field(default_factory=list)  # list[RecallResult]


@dataclass
class RecordEventResult:
    id: str = ""
    layer: str = ""
    type: str = ""


@dataclass
class ConsolidateResult:
    kept_episodes: int = 0
    promoted_to_semantic: int = 0
    skipped_consolidated: int = 0
    actions: dict = field(default_factory=dict)


@dataclass
class Stats:
    total_memories: int = 0
    by_layer: dict = field(default_factory=dict)
    by_type: dict = field(default_factory=dict)
    edges: int = 0
    code_symbols: int = 0
    workspaces: list = field(default_factory=list)
    db_path: str = ""
    avg_tokens_per_memory: float = 0.0


@dataclass
class MemoryList:
    """One page of GET /api/memories; total is the filtered count before
    pagination."""

    memories: list = field(default_factory=list)  # list[Memory]
    total: int = 0


@dataclass
class User:
    """A users-table account (the password hash never leaves the server)."""

    username: str = ""
    workspace: str = ""
    admin: bool = False
    created_at: float = 0.0


class Client:
    """Client for one `ladym serve --http` data-plane.

    base_url is scheme://host:port (trailing slash stripped). username=None
    sends no Authorization header (no-auth deployments); a username with an
    empty password matches a passwordless server account. timeout applies to
    the default urllib transport (seconds).
    """

    def __init__(
        self,
        base_url: str,
        username: Optional[str] = None,
        password: Optional[str] = None,
        timeout: float = 30.0,
        transport: Optional[Transport] = None,
    ):
        self._base = base_url.rstrip("/")
        self._user = username
        self._password = password or ""
        self._timeout = timeout
        self._transport = transport or self._urllib_transport

    # ---- transport ----

    def _urllib_transport(self, method: str, path: str, query: dict, body: Optional[dict]) -> "tuple[int, dict]":
        url = self._base + path
        if query:
            url += "?" + urllib.parse.urlencode(query)
        data = None
        headers = {}
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        if self._user is not None:
            cred = base64.b64encode(f"{self._user}:{self._password}".encode()).decode()
            headers["Authorization"] = f"Basic {cred}"
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self._timeout) as resp:
                raw = resp.read()
                return resp.status, _parse_json(raw)
        except urllib.error.HTTPError as e:
            return e.code, _parse_json(e.read())

    def _do(self, method: str, path: str, query: Optional[dict] = None, body: Optional[dict] = None) -> dict:
        status, payload = self._transport(method, path, query or {}, body)
        if status < 200 or status >= 300:
            msg = payload.get("error") if isinstance(payload, dict) else None
            raise LadymError(status, msg or f"(empty error body)")
        return payload

    @staticmethod
    def _drop_none(d: dict) -> dict:
        return {k: v for k, v in d.items() if v is not None}

    # ---- endpoints (one per /api/* tool, mirroring client/golang) ----

    def ping(self) -> None:
        """Probe /healthz (auth-exempt); returns None when healthy."""
        self._do("GET", "/healthz")

    def login(self) -> User:
        """Verify the client's own credentials; stateless, no session."""
        out = self._do("POST", "/api/login",
                       body={"username": self._user or "", "password": self._password})
        return User(**{k: v for k, v in out.items() if k in User.__dataclass_fields__})

    def remember(
        self,
        content: str,
        tags: Optional[list] = None,
        workspace: Optional[str] = None,
        source: Optional[str] = None,
    ) -> RememberResult:
        """Write a semantic fact (an empty source defaults to "http" server-side)."""
        body = {"content": content, "source": source or "", "tags": tags, "workspace": workspace or ""}
        out = self._do("POST", "/api/remember", body=body)
        return RememberResult(
            id=out.get("id") or "", hash=out.get("hash") or "",
            gated=out.get("gated") or "", reason=out.get("reason") or "",
        )

    def recall(
        self,
        query: str,
        top_k: int = 0,
        workspace: Optional[str] = None,
        code_only: bool = False,
        layers: Optional[list] = None,
        types: Optional[list] = None,
        min_similarity: float = 0.0,
    ) -> RecallResponse:
        """Query memories. top_k=0 lets the server default (8); code_only
        restricts to code items.

        layers/types/min_similarity are MCP-only knobs: the HTTP data-plane
        does not expose them, so passing any non-default value raises
        ValueError instead of silently ignoring it.
        """
        if layers or types or min_similarity:
            raise ValueError("layers/types/min_similarity are not supported by the HTTP data-plane")
        out = self._do("POST", "/api/recall", body={
            "query": query, "top_k": top_k, "workspace": workspace or "", "code_only": code_only,
        })
        results = [
            RecallResult(
                score=r.get("score", 0.0), tier=r.get("tier", 0),
                via=r.get("via") or [], memory=Memory.from_json(r.get("memory") or {}),
            )
            for r in out.get("results") or []
        ]
        return RecallResponse(
            query=out.get("query", ""), tier_reached=out.get("tier_reached", 0),
            reflected_sufficient=out.get("reflected_sufficient", False),
            elapsed_ms=out.get("elapsed_ms", 0.0), results=results,
        )

    def record_event(
        self,
        agent: str,
        action: str,
        observation: str = "",
        outcome: str = "",
        tags: Optional[list] = None,
        workspace: Optional[str] = None,
    ) -> RecordEventResult:
        """Write an L1 episodic event."""
        out = self._do("POST", "/api/record_event", body={
            "agent": agent, "action": action, "observation": observation,
            "outcome": outcome, "tags": tags, "workspace": workspace or "",
        })
        return RecordEventResult(id=out.get("id", ""), layer=out.get("layer", ""), type=out.get("type", ""))

    def consolidate(self, workspace: Optional[str] = None, since: float = 0.0) -> ConsolidateResult:
        """Run one System2 consolidation cycle; since (unix seconds) bounds the
        pass, 0 processes the whole pending backlog."""
        out = self._do("POST", "/api/consolidate", body={"workspace": workspace or "", "since": since})
        return ConsolidateResult(
            kept_episodes=out.get("kept_episodes", 0),
            promoted_to_semantic=out.get("promoted_to_semantic", 0),
            skipped_consolidated=out.get("skipped_consolidated", 0),
            actions=out.get("actions") or {},
        )

    def stats(self, workspace: Optional[str] = None) -> Stats:
        """Aggregate statistics (workspace-scoped when given / forced)."""
        out = self._do("POST", "/api/stats", body={"workspace": workspace or ""})
        return Stats(**{k: v for k, v in out.items() if k in Stats.__dataclass_fields__})

    def link(self, src: str, dst: str, relation: str = "related_to") -> str:
        """Create an associative edge src -[relation]-> dst; returns the edge id."""
        out = self._do("POST", "/api/link", body={"src": src, "dst": dst, "relation": relation})
        return out.get("id", "")

    def forget(self, memory_id: str) -> None:
        """Delete a memory by id (no-op when missing, MCP semantics)."""
        self._do("POST", "/api/forget", body={"memory_id": memory_id})

    # ---- console CRUD (api/crud.go) ----

    def list_memories(
        self,
        workspace: Optional[str] = None,
        layer: Optional[str] = None,
        type: Optional[str] = None,
        limit: int = 0,
        offset: int = 0,
    ) -> MemoryList:
        """List memories with workspace/layer/type filters; zero limit/offset
        fall back to the server defaults (limit 50)."""
        query = {}
        if workspace:
            query["workspace"] = workspace
        if layer:
            query["layer"] = layer
        if type:
            query["type"] = type
        if limit > 0:
            query["limit"] = str(limit)
        if offset > 0:
            query["offset"] = str(offset)
        out = self._do("GET", "/api/memories", query=query)
        return MemoryList(
            memories=[Memory.from_json(m) for m in out.get("memories") or []],
            total=out.get("total", 0),
        )

    def update_memory(
        self,
        memory_id: str,
        content: Optional[str] = None,
        summary: Optional[str] = None,
        tags: Optional[list] = None,
    ) -> dict:
        """Patch content/summary/tags of one memory (unset fields are left
        unchanged); a content change re-embeds server-side. Returns the
        updated memory."""
        body = self._drop_none({"content": content, "summary": summary, "tags": tags})
        out = self._do("PUT", "/api/memories/" + urllib.parse.quote(memory_id, safe=""), body=body)
        return out.get("memory") or {}

    def delete_memory(self, memory_id: str) -> None:
        """Delete one memory by id; a missing id raises LadymError(404)."""
        self._do("DELETE", "/api/memories/" + urllib.parse.quote(memory_id, safe=""))


def _parse_json(raw: bytes) -> dict:
    try:
        v = json.loads(raw) if raw.strip() else {}
    except ValueError:
        # Non-JSON error page (proxy etc.): keep the trimmed text as the
        # message so LadymError can surface it.
        return {"error": raw.decode(errors="replace").strip()}
    return v if isinstance(v, dict) else {}
