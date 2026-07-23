"""FastAPI + HTMX config editor (SPEC §2.9). Local-only; bind 127.0.0.1.

This module is the optional ``[web]`` boundary: it imports fastapi/starlette at
module top, which is safe because nothing on the offline path imports
``ladym.web`` — the CLI lazily imports :func:`build_app` only inside the
``ladym config`` command, and the test suite guards with ``importorskip``.
"""

from __future__ import annotations

import pathlib
import time
from typing import get_type_hints

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates

from ..config import _NESTED_DATACLASS_SECTIONS, Config, _is_secret
from ..errors import ConfigError
from ..secrets import get_store

_STATIC = pathlib.Path(__file__).parent / "static"
_TEMPLATES = Jinja2Templates(directory=str(pathlib.Path(__file__).parent / "templates"))

# Resolved once at import; used to cast form strings to declared field types.
_CONFIG_HINTS = get_type_hints(Config)
_NESTED_HINTS = {name: get_type_hints(cls) for name, cls in _NESTED_DATACLASS_SECTIONS.items()}


# ---------------------------------------------------------------------------
# Form -> Config application (typed; rejects secret literals)
# ---------------------------------------------------------------------------


def _cast(value: object, typ: object) -> object:
    """Cast a form string to the declared field type (best-effort)."""
    if typ is bool:
        return str(value).strip().lower() in ("1", "true", "yes", "on")
    if typ is int:
        return int(float(str(value)))  # tolerate "64.0" from a numeric input
    if typ is float:
        return float(str(value))
    if typ is pathlib.Path:
        return pathlib.Path(str(value))
    return value


def _apply_form(cfg: Config, payload: dict) -> None:
    """Apply a flat HTML form payload to ``cfg`` in place.

    HTML forms submit every field as a string, so this casts to each field's
    declared type (otherwise ``embedding_query_cache_size`` would land as the
    string ``"64"`` and break :func:`make_provider`). ``activation_*`` keys are
    reshaped into the ``[activation]`` table the loader expects. Secret literals
    are dropped via the authoritative ``config._is_secret`` (which exempts
    ``*_env`` references, so ``embedding_api_key_env`` survives); empty values
    are skipped so a blank field can't clobber an existing value.
    """
    flat: dict[str, object] = {}
    nested: dict[str, dict[str, object]] = {}
    for key, raw in payload.items():
        if _is_secret(key) or raw in ("", None):
            continue
        prefix, sep, rest = key.partition("_")
        if sep and prefix in _NESTED_HINTS and rest in _NESTED_HINTS[prefix]:
            nested.setdefault(prefix, {})[rest] = _cast(raw, _NESTED_HINTS[prefix][rest])
            continue
        if key in _CONFIG_HINTS and hasattr(cfg, key):
            flat[key] = _cast(raw, _CONFIG_HINTS[key])

    for key, val in flat.items():
        setattr(cfg, key, val)
    for section, fields in nested.items():
        target = getattr(cfg, section)
        for sk, sv in fields.items():
            setattr(target, sk, sv)


# ---------------------------------------------------------------------------
# Config -> typed TOML serialization (table form; round-trips via from_file)
# ---------------------------------------------------------------------------


def _toml_scalar(value: object) -> str:
    # bool check must precede int (bool is an int subclass).
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, float):
        return repr(value)
    text = str(value).replace("\\", "\\\\").replace('"', '\\"')
    return f'"{text}"'


def _write_toml(path: pathlib.Path, cfg: Config) -> None:
    """Serialize the editor-managed fields to a typed ``ladym.toml``.

    Table form (``[embedding]``/``[llm]``/``[activation]``) matches how operators
    hand-write the file and round-trips through :meth:`Config.from_file`. Because
    file precedence is below env (``LADYM_*``), persisting an env-derived
    ``db_path``/``workspace`` here is harmless — the env value still wins.
    """
    lines = [
        f"db_path = {_toml_scalar(cfg.db_path)}",
        f"workspace = {_toml_scalar(cfg.workspace)}",
        "",
        "[embedding]",
        f"provider = {_toml_scalar(cfg.embedding_provider)}",
        f"base_url = {_toml_scalar(cfg.embedding_base_url)}",
        f"model = {_toml_scalar(cfg.embedding_model)}",
        f"api_key_env = {_toml_scalar(cfg.embedding_api_key_env)}",
        f"fallback = {_toml_scalar(cfg.embedding_fallback)}",
        f"query_cache_size = {_toml_scalar(cfg.embedding_query_cache_size)}",
        "",
        "[llm]",
        f"provider = {_toml_scalar(cfg.llm_provider)}",
        f"base_url = {_toml_scalar(cfg.llm_base_url)}",
        f"model = {_toml_scalar(cfg.llm_model)}",
        f"api_key_env = {_toml_scalar(cfg.llm_api_key_env)}",
        f"structured_method = {_toml_scalar(cfg.llm_structured_method)}",
        "",
        "[activation]",
        f"similarity = {_toml_scalar(cfg.activation.similarity)}",
        f"recency = {_toml_scalar(cfg.activation.recency)}",
        f"frequency = {_toml_scalar(cfg.activation.frequency)}",
    ]
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


# ---------------------------------------------------------------------------
# HTMX probe fragments for the "test" buttons
# ---------------------------------------------------------------------------


def _probe(fn) -> HTMLResponse:
    """Time a ``(ok, msg)``-returning callable and format it as an HTMX fragment."""
    t0 = time.perf_counter()
    try:
        ok, msg = fn()
    except Exception as e:  # noqa: BLE001 — surface any provider error inline
        ok, msg = False, f"{type(e).__name__}: {e}"
    dt = (time.perf_counter() - t0) * 1000
    mark = "✓" if ok else "✗"
    return HTMLResponse(f"<small>{mark} {msg} · {dt:.0f}ms</small>")


def _embedding_probe(cfg: Config) -> tuple[bool, str]:
    from ..storage.embeddings import make_provider

    return make_provider(cfg).health_check()


def _llm_probe(cfg: Config) -> tuple[bool, str]:
    import os

    from ..providers.llm import make_llm_provider

    provider = make_llm_provider(
        provider=cfg.llm_provider,
        base_url=cfg.llm_base_url,
        model=cfg.llm_model,
        api_key=os.environ.get(cfg.llm_api_key_env, "") if cfg.llm_api_key_env else "",
        structured_method=cfg.llm_structured_method,
    )
    if provider is None:
        return True, "none (heuristic mode)"
    out = provider.complete([{"role": "user", "content": "ping"}])
    return True, repr(out[:20])


# ---------------------------------------------------------------------------
# App factory
# ---------------------------------------------------------------------------


def build_app(config_path: pathlib.Path | None = None) -> FastAPI:
    """Build the local-only config editor app.

    ``config_path`` is forwarded to :meth:`Config.load` so each request resolves
    the live 4-layer config (defaults -> global -> project -> ``config_path``);
    ``POST /save`` writes the edited fields back to ``./ladym.toml``.
    """
    app = FastAPI(title="LadyM config")
    app.mount("/static", StaticFiles(directory=str(_STATIC)), name="static")

    @app.get("/", response_class=HTMLResponse)
    def index(request: Request) -> HTMLResponse:
        cfg = Config.load(config_path=config_path)
        return _TEMPLATES.TemplateResponse(
            request,
            "config.html",
            {"cfg": cfg, "master_key_set": get_store().has_master_key()},
        )

    @app.post("/save", response_class=HTMLResponse)
    async def save(request: Request) -> HTMLResponse:
        form = await request.form()
        cfg = Config.load(config_path=config_path)
        _apply_form(cfg, dict(form))
        _write_toml(pathlib.Path("ladym.toml"), cfg)
        return HTMLResponse("<p>Saved to ./ladym.toml</p>")

    @app.post("/reset", response_class=HTMLResponse)
    def reset(request: Request) -> HTMLResponse:
        return index(request)

    @app.post("/test/embedding", response_class=HTMLResponse)
    async def test_embedding(request: Request) -> HTMLResponse:
        cfg = Config()
        _apply_form(cfg, dict(await request.form()))
        return _probe(lambda: _embedding_probe(cfg))

    @app.post("/test/llm", response_class=HTMLResponse)
    async def test_llm(request: Request) -> HTMLResponse:
        cfg = Config()
        _apply_form(cfg, dict(await request.form()))
        return _probe(lambda: _llm_probe(cfg))

    @app.get("/stats", response_class=HTMLResponse)
    def stats() -> HTMLResponse:
        from ..engine import Engine

        cfg = Config.load(config_path=config_path)
        eng = Engine(cfg)
        try:
            s = eng.stats()
        finally:
            eng.close()
        rows = "".join(
            f"<tr><td>{layer}</td><td>{n}</td></tr>" for layer, n in s.by_layer.items()
        )
        return HTMLResponse(
            "<table>"
            f"<tr><th>total</th><td>{s.total_memories}</td></tr>"
            f"<tr><th>avg tokens/mem</th><td>{s.avg_tokens_per_memory:.1f}</td></tr>"
            f"{rows}"
            "</table>"
        )

    # ------------------------------------------------------------------
    # Secret store API (Task 9, spec §6) — shares ~/.ladyM with the CLI.
    # Values are NEVER returned: GET lists names only, so the response body
    # can't leak a secret through the browser or chat/logs.
    # ------------------------------------------------------------------

    @app.get("/api/secrets")
    def api_secrets() -> dict:
        s = get_store()
        return {"master_key_set": s.has_master_key(), "names": s.list_names()}

    @app.post("/api/secrets")
    async def api_secrets_set(request: Request) -> dict:
        payload = await request.json()
        name = payload.get("name")
        value = payload.get("value")
        # Validate before touching the store: a missing/empty name or value is a
        # client error (4xx), not a server bug. Direct [] access would KeyError
        # into a FastAPI 500 here.
        if not name or value is None or value == "":
            raise HTTPException(
                status_code=400, detail="name and value are required"
            )
        s = get_store()
        try:
            s.set(name, value)
        except ConfigError as e:
            # require_master_key() — no master key set yet
            raise HTTPException(status_code=400, detail=str(e)) from e
        return {"ok": True}

    @app.delete("/api/secrets/{name}")
    def api_secrets_rm(name: str) -> dict:
        get_store().remove(name)
        return {"ok": True}

    @app.post("/api/master-key")
    async def api_master_key(request: Request) -> dict:
        payload = await request.json()
        s = get_store()
        try:
            if payload.get("reset"):
                s.reset_master_key(payload.get("key"))
            else:
                s.set_master_key(payload.get("key"))
        except ConfigError as e:
            # e.g. set_master_key refused because secrets.enc already has entries
            raise HTTPException(status_code=400, detail=str(e)) from e
        return {"ok": True, "master_key_set": True}

    return app
