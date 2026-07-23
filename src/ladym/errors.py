"""Centralized ladym exceptions."""


class ConfigError(RuntimeError):
    """Raised when runtime configuration makes an operation impossible.

    Typical cause: an LLM/embedding provider is configured (``provider !=
    "none"``) but its API key is missing. The message MUST be actionable and
    one-line — CLI/MCP surface it verbatim instead of dumping a traceback.

    This is **fail-fast, NOT a fallback**: we refuse to silently degrade to
    heuristic/offline mode when the user explicitly asked for a provider.
    See ``user-design-pref-no-fallback``.
    """
