"""LadyM CLI — typer-based front-end to the Engine."""

from __future__ import annotations

import json
import logging
import sys
from pathlib import Path

import typer
from rich.console import Console
from rich.table import Table

from .config import Config
from .engine import Engine
from .errors import ConfigError
from .secrets import SecretStore, get_store

app = typer.Typer(
    name="ladym",
    help=(
        "LadyM — Layered Agent DYnamic Memory: brain-inspired memory for LLM agents & codebase RAG."
    ),
    no_args_is_help=True,
)
console = Console()
logger = logging.getLogger("ladym.system2")

# Set by the callback when ``--config`` / ``--debug`` are passed; honoured by
# ``_engine`` and ``main`` respectively.
_config_path: str | None = None
_debug: bool = False


@app.callback()
def _main(
    config: str | None = typer.Option(  # noqa: B008 - typer idiom
        None, "--config", help="Path to a ladym.toml to load on top of defaults/env."
    ),
    debug: bool = typer.Option(  # noqa: B008 - typer idiom
        False, "--debug", help="Show full Python tracebacks on error."
    ),
) -> None:
    """LadyM — global options live here (parsed BEFORE the subcommand)."""
    global _config_path, _debug
    _config_path = config
    _debug = debug


def _load_config(db: str | None, workspace: str | None) -> Config:
    """Resolve a Config via ``Config.load`` (4-layer precedence from Task 2.1).

    ``--db`` / ``--workspace`` per-command flags become CLI overrides (highest
    precedence); ``--config`` (global) becomes the ``config_path`` argument.
    """
    overrides: dict = {}
    if db:
        overrides["db_path"] = Path(db)
    if workspace:
        overrides["workspace"] = workspace
    return Config.load(
        config_path=Path(_config_path) if _config_path else None,
        cli_overrides=overrides or None,
    )


def _engine(db: str | None, workspace: str | None) -> Engine:
    """Build an Engine from the resolved Config (see :func:`_load_config`)."""
    return Engine(_load_config(db, workspace))


@app.command()
def remember(
    content: str = typer.Argument(..., help="The fact/note to store."),
    db: str | None = typer.Option(None, "--db", help="Path to ladym.db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
    tags: str | None = typer.Option(None, "--tags", help="Comma-separated tags"),
    source: str = typer.Option("cli", "--source"),
):
    """Write a semantic memory (fact).

    Routes through ``eng.remember`` (not ``semantic.put_fact`` directly) so the
    attention gate (SPEC §2.7 / C5) applies: noise / recent-duplicate
    content is dropped before any long-term write. The gate's drop returns an
    unpersisted ``Memory`` whose ``id`` is a non-existent fake UUID — we surface
    the drop in the output WITHOUT printing that id (it would mislead users into
    calling ``forget``/``link`` on a memory that was never stored).
    """
    eng = _engine(db, workspace)
    try:
        tag_list = [t.strip() for t in tags.split(",")] if tags else []
        m = eng.remember(content, tags=tag_list, source=source)
        if m.metadata.get("gated") == "dropped":
            console.print(
                f"[yellow]dropped[/yellow] reason={m.metadata.get('reason')} "
                f"(gated; not persisted)"
            )
        else:
            console.print(
                f"[green]remembered[/green] id={m.id} hash={m.content_hash[:8]}"
            )
    finally:
        eng.close()


@app.command()
def record(
    agent: str = typer.Option(..., "--agent", help="Who/what performed the action."),
    action: str = typer.Option(..., "--action", help="What was done."),
    observation: str = typer.Option("", "--observation", help="What was seen/learned."),
    outcome: str = typer.Option("", "--outcome", help="Result of the action."),
    tags: str = typer.Option(None, "--tags", help="Comma-separated tags"),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Record an L1 episodic event (feeds System2 consolidation + L5/L6 extraction)."""
    eng = _engine(db, workspace)
    try:
        tag_list = [t.strip() for t in tags.split(",")] if tags else []
        m = eng.record_event(
            agent=agent, action=action, observation=observation,
            outcome=outcome, tags=tag_list,
        )
        console.print(f"[green]recorded[/green] id={m.id} layer={m.layer} type={m.type}")
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
    root: Path = typer.Argument(..., help="Directory to index."),  # noqa: B008 - typer idiom
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
    force: bool = typer.Option(False, "--force", help="Re-index even if unchanged."),
    languages: str | None = typer.Option(
        None, "--languages", help="Comma-separated, e.g. python,go"
    ),
):
    """Index a codebase into L2 semantic memory."""
    eng = _engine(db, workspace)
    try:
        langs = [lang.strip() for lang in languages.split(",")] if languages else None
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
    cfg = _load_config(db, workspace)
    from .mcp.server import build_server

    server = build_server(cfg)
    # MCP stdio: stdout must carry ONLY JSON-RPC frames — diagnostics go to stderr.
    Console(stderr=True).print(
        f"[bold]LadyM MCP server[/bold] starting (db={cfg.db_path}, ws={cfg.workspace})"
    )
    server.run()


@app.command()
def worker(
    once: bool = typer.Option(False, "--once", help="Run one cycle and exit."),
    interval: int = typer.Option(300, "--interval", help="Seconds between cycles."),
    db: str | None = typer.Option(None, "--db"),
    workspace: str | None = typer.Option(None, "--workspace", "-w"),
):
    """Run System2 consolidation cycles in the background (SPEC §2.8).

    Opens the store in WAL mode so concurrent readers (other CLI invocations,
    the MCP server, an in-process ``start_system2`` thread) can ``recall``
    while this worker writes — without sqlite locking. WAL must be enabled
    BEFORE the Engine is constructed (``enable_wal`` is read by
    ``SQLiteStore.__init__``), so we build the Config inline rather than via
    ``_engine`` (which doesn't set WAL).

    Resilience: in ``--interval`` loop mode each cycle is wrapped in
    ``try/except Exception``; failures are logged and the loop continues (the
    CLI worker is user-supervised — there is no bounded stop, in contrast to
    ``Engine.start_system2``'s background thread). In ``--once`` mode
    exceptions propagate so the user sees the error and the process exits
    non-zero.
    """
    import time

    cfg = _load_config(db, workspace)
    cfg.enable_wal = True
    eng = Engine(cfg)
    from .operations.system2 import run_system2_cycle

    try:
        while True:
            if once:
                # Single cycle; let exceptions propagate so the user sees the
                # error and the process exits non-zero.
                run_system2_cycle(eng, workspace=workspace)
                break
            try:
                run_system2_cycle(eng, workspace=workspace)
            except Exception:
                logger.exception("system2 CLI worker cycle failed; continuing")
            time.sleep(interval)
    finally:
        eng.close()


config_app = typer.Typer(
    name="config",
    help="Manage ladym.toml (web editor) and the encrypted secret store.",
    no_args_is_help=False,
)


@config_app.callback(invoke_without_command=True)
def config_main(
    ctx: typer.Context,
    port: int = typer.Option(8765, "--port"),
    no_browser: bool = typer.Option(False, "--no-browser"),
):
    """With no subcommand: launch the local web config editor (needs web extra).

    Serves a FastAPI + HTMX form on 127.0.0.1 that edits embedding/llm/activation
    and writes the result to ./ladym.toml. Imports are lazy so the rest of the
    CLI works without the extra; importing fastapi is checked explicitly so the
    guard fires even if ladym.web.app is already cached in the process.
    """
    if ctx.invoked_subcommand is not None:
        return
    try:
        import fastapi  # noqa: F401 — explicit dependency guard (order-independent)
        import uvicorn
        from .web.app import build_app
    except ImportError:
        console.print(
            "[red]web extra not installed[/red] — install with: pip install 'ladym\\[web]'"
        )
        raise typer.Exit(1) from None
    cfg_path = Path(_config_path) if _config_path else None
    app_obj = build_app(config_path=cfg_path)
    if not no_browser:
        import threading
        import webbrowser

        threading.Timer(1.0, lambda: webbrowser.open(f"http://127.0.0.1:{port}/")).start()
    console.print(f"[bold]LadyM config[/bold] on http://127.0.0.1:{port}/")
    uvicorn.run(app_obj, host="127.0.0.1", port=port, log_level="warning")


def _store() -> SecretStore:
    return get_store()


@config_app.command("set")
def config_set(
    key: str = typer.Argument(..., help="KEY_NAME (same value as api_key_env in ladym.toml)."),
    value: str = typer.Argument(..., help="Secret value (plaintext; encrypted at rest)."),
):
    """Store KEY=VALUE in the encrypted secret store."""
    _store().set(key, value)
    console.print(f"[green]stored[/green] {key}")


@config_app.command("set-master-key")
def config_set_master_key(
    key: str | None = typer.Argument(
        None, help="Master key string; omit to generate a strong random key."
    ),
):
    """Initialize the master key (required before storing any secret)."""
    store = _store()
    store.set_master_key(key)
    if key is None:
        console.print(
            "[green]generated[/green] a random master key at "
            f"{store._master} — back it up; losing it makes secrets unrecoverable."
        )
    else:
        console.print("[green]master key set[/green]")


@config_app.command("reset-master-key")
def config_reset_master_key(
    key: str | None = typer.Argument(None, help="New master key; omit for random."),
):
    """Re-encrypt every secret under a new master key."""
    _store().reset_master_key(key)
    console.print("[green]master key reset[/green]; all secrets re-encrypted")


@config_app.command("list")
def config_list():
    """List stored KEY_NAMEs (values are never printed)."""
    names = _store().list_names()
    if not names:
        console.print("[yellow]no secrets stored[/yellow]")
        return
    for n in names:
        console.print(n)


@config_app.command("rm")
def config_rm(
    key: str = typer.Argument(...),
):
    """Remove a stored secret."""
    if _store().remove(key):
        console.print(f"[green]removed[/green] {key}")
    else:
        console.print(f"[yellow]no such key[/yellow] {key}")
        raise typer.Exit(1)


app.add_typer(config_app)


def main() -> None:
    """Entry point: run the Typer app, converting ConfigError (and other
    provider errors, when not --debug) into a one-line message + exit 1."""
    try:
        app()
    except ConfigError as e:
        if _debug:
            raise
        console.print(f"[red]ladym:[/red] {e}")
        sys.exit(1)
    except Exception as e:  # noqa: BLE001 — top-level friendly handler
        if _debug:
            raise
        console.print(f"[red]ladym:[/red] {type(e).__name__}: {e}")
        sys.exit(1)


if __name__ == "__main__":  # pragma: no cover
    main()
