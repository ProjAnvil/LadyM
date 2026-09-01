"""ladyM memory provider plugin for Hermes Agent (directory-plugin shim).

Hermes loads this directory as a plugin (see plugin.yaml) and calls
:func:`register` with the plugin context. The implementation lives in the
pip-installable package under ``src/ladym_hermes``; this shim re-exports it
for every loading style:

- as a package (``import hermes`` with the parent dir on sys.path),
- as a top-level module with this directory on sys.path,
- with ``src/`` on sys.path or the wheel installed (``ladym_hermes``).
"""

try:
    from .src.ladym_hermes import LadymMemoryProvider, register
except ImportError:
    try:
        from src.ladym_hermes import LadymMemoryProvider, register
    except ImportError:
        from ladym_hermes import LadymMemoryProvider, register

__all__ = ["LadymMemoryProvider", "register"]
