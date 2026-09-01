"""Hermes CLI shim (directory-plugin form).

Hermes discovers provider CLIs by looking for ``cli.py`` in the plugin
directory. The implementation lives in ``src/ladym_hermes/cli.py``.
"""

try:
    from .src.ladym_hermes.cli import register_cli
except ImportError:
    try:
        from src.ladym_hermes.cli import register_cli
    except ImportError:
        from ladym_hermes.cli import register_cli

__all__ = ["register_cli"]
