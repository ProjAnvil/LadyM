"""LadyM MCP server — exposes the Engine as a set of MCP tools.

Run with::

    ladym serve                          # via CLI (recommended)
    python -m ladym.mcp.server           # directly

Any MCP-aware agent (Claude Code, Cursor, …) can then call the tools below instead of
grepping/reading files on every turn.

Tools:
    recall(query, top_k?, code_only?)         → ranked memories
    remember(content, tags?, source?)         → write a fact
    search_code(query, top_k?)                → code-only search
    index_code(root, force?, languages?)      → index/re-index a repo
    consolidate()                             → L1 → L2
    stats()                                   → counts
    link(src, dst, relation?)                 → associative edge
    forget(memory_id)                         → delete
"""

from __future__ import annotations

import json
from typing import Any

from ..config import Config
from ..engine import Engine


def _to_payload(memory) -> dict[str, Any]:  # type: ignore[no-untyped-def]
    return {
        "id": memory.id,
        "layer": memory.layer,
        "type": memory.type,
        "summary": memory.summary,
        "content": memory.content,
        "source": memory.source,
        "tags": memory.tags,
        "score": getattr(memory, "score", None),
    }


def _recall_result_payload(result) -> dict[str, Any]:  # type: ignore[no-untyped-def]
    return {
        "score": result.score,
        "tier": result.tier,
        "via": result.via,
        "memory": _to_payload(result.memory),
    }


def build_server(config: Config | None = None, *, engine: Engine | None = None):
    """Construct the FastMCP server. Imports the MCP SDK lazily so the rest of LadyM works
    without the ``mcp`` extra installed."""
    try:
        from mcp.server.fastmcp import FastMCP  # type: ignore
    except ImportError as e:  # pragma: no cover - optional dep
        raise ImportError(
            "mcp SDK is not installed. Install with: pip install 'ladym[mcp]'"
        ) from e

    owns_engine = engine is None
    eng: Engine = engine or Engine(config or Config())

    server = FastMCP("ladym")

    @server.tool()
    def recall(query: str, top_k: int = 8, code_only: bool = False,
               workspace: str | None = None) -> str:
        """Recall memories matching a natural-language query.

        Use ``code_only=True`` to restrict to codebase analysis (symbols + file summaries).
        Returns ranked results with tier (1=lightweight, 2=deep) and activation score.
        """
        if code_only:
            resp = eng.search_code(query, top_k=top_k, workspace=workspace)
        else:
            resp = eng.recall(query, top_k=top_k, workspace=workspace)
        return json.dumps({
            "query": resp.query,
            "tier_reached": resp.tier_reached,
            "reflected_sufficient": resp.reflected_sufficient,
            "elapsed_ms": resp.elapsed_ms,
            "results": [_recall_result_payload(r) for r in resp.results],
        })

    @server.tool()
    def remember(content: str, tags: list[str] | None = None,
                 source: str = "", workspace: str | None = None) -> str:
        """Write a semantic fact / note that future recall can retrieve."""
        ws = workspace or eng.config.workspace
        eng.semantic.workspace = ws
        m = eng.semantic.put_fact(content, tags=tags or [], source=source or "mcp")
        return json.dumps({"id": m.id, "hash": m.content_hash})

    @server.tool()
    def search_code(query: str, top_k: int = 10, workspace: str | None = None) -> str:
        """Search indexed code symbols + file summaries by keyword."""
        resp = eng.search_code(query, top_k=top_k, workspace=workspace)
        return json.dumps({
            "results": [_recall_result_payload(r) for r in resp.results],
            "elapsed_ms": resp.elapsed_ms,
        })

    @server.tool()
    def index_code(root: str, force: bool = False,
                   languages: list[str] | None = None,
                   workspace: str | None = None) -> str:
        """Index (or re-index) a codebase at ``root``. Incremental by default; pass
        ``force=True`` to rebuild from scratch. ``languages`` filters by language id."""
        report = eng.index_code(root, force=force, languages=languages, workspace=workspace)
        return json.dumps({
            "files_seen": report.files_seen,
            "files_indexed": report.files_indexed,
            "files_skipped_unchanged": report.files_skipped_unchanged,
            "symbols_written": report.symbols_written,
            "refs_written": report.refs_written,
            "elapsed_ms": report.elapsed_ms,
            "errors": report.errors[:20],
        })

    @server.tool()
    def consolidate(workspace: str | None = None) -> str:
        """Promote episodic events into consolidated semantic facts (L1 → L2)."""
        report = eng.consolidate(workspace=workspace)
        return json.dumps({
            "kept_episodes": report.kept_episodes,
            "promoted_to_semantic": report.promoted_to_semantic,
            "actions": report.actions,
        })

    @server.tool()
    def stats(workspace: str | None = None) -> str:
        """Return memory-store statistics."""
        s = eng.stats()
        return s.model_dump_json()

    @server.tool()
    def link(src: str, dst: str, relation: str = "related_to") -> str:
        """Create an associative edge between two memory ids (Zettelkasten link)."""
        edge = eng.associative.link(src, dst, relation)
        return json.dumps({"id": edge.id, "src": edge.src_id, "dst": edge.dst_id,
                           "relation": edge.relation})

    @server.tool()
    def forget(memory_id: str) -> str:
        """Delete a single memory by id."""
        eng.forget(memory_id)
        return json.dumps({"forgotten": memory_id})

    # attach cleanup hook so the engine is closed when the server stops
    server._ladym_owns_engine = owns_engine  # type: ignore[attr-defined]
    server._ladym_engine = eng  # type: ignore[attr-defined]
    return server


def main() -> None:  # pragma: no cover - thin runner
    import argparse

    parser = argparse.ArgumentParser(description="LadyM MCP server")
    parser.add_argument("--db", default=None)
    parser.add_argument("--workspace", default=None)
    args = parser.parse_args()

    cfg = Config()
    if args.db:
        cfg.db_path = __import__("pathlib").Path(args.db)
    if args.workspace:
        cfg.workspace = args.workspace

    server = build_server(cfg)
    server.run()


if __name__ == "__main__":  # pragma: no cover
    main()
