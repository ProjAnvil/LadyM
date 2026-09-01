"""ladyM memory provider plugin for Hermes Agent (pip-installable package).

This is the importable package form of the plugin; the repository directory
``integrations/hermes/`` doubles as a Hermes directory plugin whose root
``__init__.py`` shims to this package.
"""

from .provider import LadymMemoryProvider

__all__ = ["LadymMemoryProvider", "register"]


def register(ctx) -> None:
    """Hermes plugin entry point: register the ladyM memory provider."""
    ctx.register_memory_provider(LadymMemoryProvider())
