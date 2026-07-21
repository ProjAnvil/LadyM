"""FastAPI + HTMX config editor (SPEC §2.9). Local-only; bind 127.0.0.1.

This module is the optional ``[web]`` boundary: it imports fastapi/starlette at
module top, which is safe because nothing on the offline path imports
``ladym.web`` — the CLI lazily imports :func:`build_app` only inside the
``ladym config`` command, and the test suite guards with ``importorskip``.
"""

from __future__ import annotations

from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import HTMLResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates

from ..config import Config

_STATIC = Path(__file__).parent / "static"
_TEMPLATES = Jinja2Templates(directory=str(Path(__file__).parent / "templates"))


def build_app(config_path: Path | None = None) -> FastAPI:
    """Build the local-only config editor app.

    ``config_path`` is forwarded to :meth:`Config.load` so each request resolves
    the live 4-layer config (defaults → global → project → ``config_path``);
    edits are written back to ``./ladym.toml`` by ``POST /save`` (Task 5.2).
    """
    app = FastAPI(title="LadyM config")
    app.mount("/static", StaticFiles(directory=str(_STATIC)), name="static")

    @app.get("/", response_class=HTMLResponse)
    def index(request: Request) -> HTMLResponse:
        cfg = Config.load(config_path=config_path)
        return _TEMPLATES.TemplateResponse(request, "config.html", {"cfg": cfg})

    return app
