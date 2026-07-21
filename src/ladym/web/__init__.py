"""Optional FastAPI + HTMX config editor (the ``[web]`` extra).

Intentionally imports nothing here: :mod:`ladym.web.app` pulls in fastapi at
module top, so it must only be imported when the ``[web]`` extra is installed.
The offline path never touches this package.
"""
