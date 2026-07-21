"""LadyM CLI — typer-based front-end to the Engine."""

from __future__ import annotations

import json
from pathlib import Path

import typer
from rich.console import Console
from rich.table import Table

from .config import Config
from .engine import Engine

app = typer.Typer(
    name="ladym",
    help="LadyM — Layered Agent DYnamic Memory: brain-inspired memory for LLM agents & codebase RAG.",
    no_args_is_help=True,
)
console = Console()


def _engine(db: str | None, workspace: str | None) -> Engine:
    cfg = Config()
    if db:
        cfg.db_path = Path(db)
    if workspace:
        cfg.workspace = workspace
    return Engine(cfg)


@app.command()
def remember(
    content: str = typer.Argument(..., help="The fact/note to store."),
    db: str | None = typer.Option(None, "--db", help="Path to ladym.db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
    tags: str | None = typer.Option(None, "--tags", help="Comma-separated tags"),
    source: str = typer.Option("cli", "--source"),
):
    """Write a semantic memory (fact)."""
    eng = _engine(db, workspace)
    try:
        tag_list = [t.strip() for t in tags.split(",")] if tags else []
        m = eng.semantic.put_fact(content, tags=tag_list, source=source)
        console.print(f"[green]remembered[/green] id={m.id} hash={m.content_hash[:8]}")
    finally:
        eng.close()


@app.command()
def recall(
    query: str = typer.Argument(..., help="Natural-language query."),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
    top_k: int = typer.Option(8, "--top-k", "-n"),
    code_only: bool = typer.Option(False, "--code", help="Restrict to code items."),
    json_out: bool = typer.Option(False, "--json", help="Emit JSON."),
):
    """Recall memories matching the query."""
    eng = _engine(db, workspace)
    try:
        resp = eng.search_code(query, top_k=top_k) if code_only else eng.recall(query, top_k=top_k)
        if json_out:
            payload = {
                "query": resp.query,
                "tier_reached": resp.tier_reached,
                "reflected_sufficient": resp.reflected_sufficient,
                "elapsed_ms": resp.elapsed_ms,
                "results": [
                    {
                        "id": r.memory.id,
                        "layer": r.memory.layer,
                        "type": r.memory.type,
                        "score": r.score,
                        "tier": r.tier,
                        "summary": r.memory.summary,
                        "content": r.memory.content[:400],
                        "source": r.memory.source,
                    }
                    for r in resp.results
                ],
            }
            console.print_json(json.dumps(payload))
            return
        if not resp.results:
            console.print("[yellow]no memories matched[/yellow]")
            return
        table = Table(title=f"recall: {query}  (tier {resp.tier_reached}, {resp.elapsed_ms:.1f}ms)")
        table.add_column("score", justify="right")
        table.add_column("layer")
        table.add_column("type")
        table.add_column("summary")
        table.add_column("source")
        for r in resp.results:
            table.add_row(
                f"{r.score:.3f}",
                r.memory.layer,
                r.memory.type,
                r.memory.summary[:60],
                r.memory.source[:30],
            )
        console.print(table)
    finally:
        eng.close()


@app.command()
def index(
    root: Path = typer.Argument(..., help="Directory to index."),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
    force: bool = typer.Option(False, "--force", help="Re-index even if unchanged."),
    languages: str | None = typer.Option(None, "--languages", help="Comma-separated, e.g. python,go"),
):
    """Index a codebase into L2 semantic memory."""
    eng = _engine(db, workspace)
    try:
        langs = [l.strip() for l in languages.split(",")] if languages else None
        report = eng.index_code(root, force=force, languages=langs)
        console.print(
            f"[green]indexed[/green] {report.files_indexed}/{report.files_seen} files "
            f"({report.symbols_written} symbols, {report.refs_written} refs) "
            f"in {report.elapsed_ms:.0f}ms"
        )
        if report.files_skipped_unchanged:
            console.print(f"  [dim]skipped unchanged: {report.files_skipped_unchanged}[/dim]")
        if report.errors:
            console.print(f"  [red]errors: {len(report.errors)}[/red]")
            for e in report.errors[:5]:
                console.print(f"    {e}")
    finally:
        eng.close()


@app.command(name="consolidate")
def consolidate_cmd(
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Promote episodic events into semantic facts."""
    eng = _engine(db, workspace)
    try:
        report = eng.consolidate()
        console.print(
            f"consolidated {report.kept_episodes} episodes: "
            f"ADD={report.actions['ADD']} UPDATE={report.actions['UPDATE']} "
            f"DELETE={report.actions['DELETE']} NOOP={report.actions['NOOP']}"
        )
    finally:
        eng.close()


@app.command()
def stats(
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Show memory statistics."""
    eng = _engine(db, workspace)
    try:
        s = eng.stats()
        console.print(f"[bold]LadyM stats[/bold]  db={s.db_path}")
        console.print(f"  total memories: {s.total_memories}")
        console.print(f"  edges: {s.edges}    code symbols: {s.code_symbols}")
        console.print(f"  workspaces: {', '.join(s.workspaces) or '(none)'}")
        if s.by_layer:
            t = Table("layer", "count")
            for k, v in sorted(s.by_layer.items()):
                t.add_row(k, str(v))
            console.print(t)
    finally:
        eng.close()


@app.command()
def forget(
    memory_id: str = typer.Argument(..., help="Memory id to delete."),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Delete a single memory by id."""
    eng = _engine(db, workspace)
    try:
        eng.forget(memory_id)
        console.print(f"[green]forgot[/green] {memory_id}")
    finally:
        eng.close()


@app.command()
def link(
    src: str = typer.Argument(...),
    dst: str = typer.Argument(...),
    relation: str = typer.Option("related_to", "--relation", "-r"),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Create an associative edge between two memories."""
    eng = _engine(db, workspace)
    try:
        edge = eng.associative.link(src, dst, relation)
        console.print(f"[green]linked[/green] {src} -[{relation}]-> {dst} (id={edge.id})")
    finally:
        eng.close()


@app.command()
def serve(
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Run the LadyM MCP server over stdio (for MCP-aware agents)."""
    cfg = Config()
    if db:
        cfg.db_path = Path(db)
    if workspace:
        cfg.workspace = workspace
    from .mcp.server import build_server
    server = build_server(cfg)
    console.print(f"[bold]LadyM MCP server[/bold] starting (db={cfg.db_path}, ws={cfg.workspace})")
    server.run()


if __name__ == "__main__":  # pragma: no cover
    app()
