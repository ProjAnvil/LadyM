"""Runtime configuration for LadyM.

All values have sensible defaults so the engine works out-of-the-box with no env vars and no
network. Anything that needs a key/model is an opt-in override.

Configuration sources (highest precedence first):

1. ``cli_overrides`` passed to :meth:`Config.load` (e.g. from Typer flags).
2. Environment variables (``LADYM_*``).
3. The project file ``./ladym.toml``.
4. The global file ``~/.ladym/config.toml``.
5. The dataclass defaults (offline: hashing embedding, ``llm_provider == "none"``).

Secret literals (``api_key``/``*_key``/``token``/``secret``/``password``) found in TOML are
**rejected with a warning and dropped** — operators must use ``<name>_env`` indirection
(e.g. ``llm.api_key_env = "MY_LLM_KEY"``) so secrets never land on disk.
"""

from __future__ import annotations

import os
import sys
import tomllib
from dataclasses import dataclass, field, fields
from pathlib import Path


def _default_db_path() -> Path:
    # workspace-local so each project gets its own memory by default
    env = os.environ.get("LADYM_DB")
    if env:
        return Path(env)
    return Path.cwd() / "ladym.db"


# ---------------------------------------------------------------------------
# Nested config dataclasses
# ---------------------------------------------------------------------------


@dataclass
class ActivationWeights:
    """Weights for the ACT-R-inspired activation function (ARCHITECTURE.md §4).

    All defaults are evidence-grounded starting points; tune per workload.
    """

    similarity: float = 1.0
    recency: float = 0.3
    frequency: float = 0.2
    graph: float = 0.15
    type_boost: float = 0.25
    recency_half_life_s: float = 7 * 24 * 3600.0  # one week


@dataclass
class RecallConfig:
    """Two-tier retrieval knobs (ARCHITECTURE.md §3)."""

    top_k_tier1: int = 8
    top_k_tier2: int = 20
    graph_hops: int = 2
    reflection_min_hits: int = 2
    reflection_min_coverage: float = 0.5  # fraction of query terms covered
    enable_tier2: bool = True


@dataclass
class ConsolidateConfig:
    """Knobs for L1→L2 consolidation."""

    min_episodes_to_trigger: int = 3
    dedup_similarity_threshold: float = 0.85


@dataclass
class CodeIndexConfig:
    """Knobs for codebase indexing."""

    max_body_lines_per_symbol: int = 40
    respect_gitignore: bool = True
    extra_ignore_globs: list[str] = field(
        default_factory=lambda: ["**/.venv/**", "**/node_modules/**", "**/__pycache__/**"]
    )
    languages: list[str] | None = None  # None = all supported


@dataclass
class EmbeddingConfig:
    """Nested mirror of the ``embedding_*`` flat fields (populated by the loader)."""

    provider: str = "hashing"
    base_url: str = ""
    model: str = ""
    api_key_env: str = ""
    fallback: str = "none"
    query_cache_size: int = 0
    timeout_s: float = 10.0
    allow_dim_change: bool = False
    http_request: str = '{"input": "{text}"}'
    http_response_path: str = "data"


@dataclass
class LLMConfig:
    """Nested mirror of the ``llm_*`` flat fields (populated by the loader)."""

    provider: str = "none"
    base_url: str = ""
    model: str = "gpt-4o-mini"
    api_key_env: str = ""
    max_tokens: int = 1024
    temperature: float = 0.2
    structured_method: str = "function_calling"
    timeout_s: float = 30.0


@dataclass
class System2Config:
    """Background reflection cycle knobs."""

    enabled: bool = False
    interval_s: int = 300
    min_episodes_to_run: int = 3


@dataclass
class AttentionConfig:
    """Pre-write attention gate knobs."""

    min_chars: int = 8
    dedup_window_s: float = 3600.0
    noise_words: list[str] = field(default_factory=list)


# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------


@dataclass
class Config:
    """Runtime configuration for LadyM.

    The flat mirror fields (``embedding_provider``, ``llm_provider``, …) are the source of
    truth for :func:`ladym.storage.embeddings.make_provider` /
    :func:`ladym.providers.agents.AgentRegistry` — keep their names stable. The nested
    dataclasses (``embedding``/``llm``/``system2``/``attention``/…) are a convenience mirror
    populated by the loader.
    """

    db_path: Path = field(default_factory=_default_db_path)
    workspace: str = field(
        default_factory=lambda: os.environ.get("LADYM_WORKSPACE", "default")
    )
    prefer_sqlite_vec: bool = True
    enable_wal: bool = False

    # embedding (flat — source of truth for make_provider)
    embedding_provider: str = field(
        default_factory=lambda: os.environ.get("LADYM_EMBEDDING", "hashing")
    )
    embedding_model: str = field(
        default_factory=lambda: os.environ.get("LADYM_EMBEDDING_MODEL", "")
    )
    embedding_dim: int = 256  # for the hashing provider; overridden by others
    embedding_base_url: str = ""
    embedding_api_key_env: str = ""
    embedding_fallback: str = "none"
    embedding_query_cache_size: int = 0  # LRU cache for embed(); 0 = off
    embedding_timeout_s: float = 10.0
    embedding_allow_dim_change: bool = False
    embedding_http_request: str = '{"input": "{text}"}'
    embedding_http_response_path: str = "data"
    embedding: EmbeddingConfig = field(default_factory=EmbeddingConfig)

    # llm (flat — source of truth for make_agent / AgentRegistry)
    llm_provider: str = "none"  # "none" = heuristic / offline mode
    llm_base_url: str = ""
    llm_model: str = "gpt-4o-mini"
    llm_api_key_env: str = ""
    llm_max_tokens: int = 1024
    llm_temperature: float = 0.2
    llm_structured_method: str = "function_calling"
    llm_timeout_s: float = 30.0
    llm: LLMConfig = field(default_factory=LLMConfig)

    agents_overrides: dict = field(default_factory=dict)
    activation: ActivationWeights = field(default_factory=ActivationWeights)
    recall: RecallConfig = field(default_factory=RecallConfig)
    consolidate: ConsolidateConfig = field(default_factory=ConsolidateConfig)
    code_index: CodeIndexConfig = field(default_factory=CodeIndexConfig)
    system2: System2Config = field(default_factory=System2Config)
    attention: AttentionConfig = field(default_factory=AttentionConfig)

    @classmethod
    def for_testing(cls, tmp_path: Path) -> Config:
        """A Config that points at a temp db and uses the offline hashing embedding.

        Uses the in-memory vector index (``prefer_sqlite_vec=False``) for deterministic,
        extension-free test runs; the store still persists embeddings to a BLOB column so a
        reopened engine can answer recall queries.
        """
        return cls(
            db_path=tmp_path / "test.ladym.db",
            workspace="test",
            embedding_provider="hashing",
            llm_provider="none",
            prefer_sqlite_vec=False,
        )

    # ----- loaders -----

    @classmethod
    def from_file(cls, path: Path) -> Config:
        """Build a Config from a single TOML file (defaults + file, no env/CLI).

        Strips secret literals (with a stderr warning), renames the deprecated
        ``embedding_endpoint`` → ``embedding_base_url`` (with a deprecation warning),
        and populates both the flat mirror fields and the nested dataclasses.
        """
        path = Path(path)
        with open(path, "rb") as fh:
            raw = tomllib.load(fh)
        data = _strip_secrets(raw, path)
        data = _rename_deprecated(data, path)
        cfg = cls()
        _apply_toml(cfg, data)
        _sync_nested(cfg)
        return cfg

    @classmethod
    def load(
        cls,
        config_path: Path | None = None,
        *,
        cli_overrides: dict | None = None,
    ) -> Config:
        """Resolve a Config through the 4-layer precedence.

        Layers, lowest precedence first: defaults → ``~/.ladym/config.toml`` →
        ``./ladym.toml`` → ``config_path``. Then env vars overlay, then ``cli_overrides``
        (highest). Each TOML layer is parsed, stripped of secrets, deep-merged over the
        accumulated dict, then applied to a fresh Config in one pass — so a higher layer's
        field-by-field overlay of a nested table works correctly.
        """
        layers: list[Path] = []
        global_path = Path.home() / ".ladym" / "config.toml"
        if global_path.exists():
            layers.append(global_path)
        project_path = Path.cwd() / "ladym.toml"
        if project_path.exists():
            layers.append(project_path)
        if config_path is not None:
            layers.append(Path(config_path))

        merged: dict = {}
        for p in layers:
            with open(p, "rb") as fh:
                raw = tomllib.load(fh)
            raw = _strip_secrets(raw, p)
            raw = _rename_deprecated(raw, p)
            merged = _deep_merge_toml(merged, raw)

        cfg = cls()
        _apply_toml(cfg, merged)
        _sync_nested(cfg)

        _apply_env(cfg)
        _sync_nested(cfg)

        if cli_overrides:
            _apply_dict(cfg, cli_overrides)
            _sync_nested(cfg)

        return cfg


# ---------------------------------------------------------------------------
# Helpers — secret handling
# ---------------------------------------------------------------------------

_SECRET_KEYS = ("api_key", "secret", "token", "password")


def _is_secret(key: str) -> bool:
    k = key.lower()
    return any(s in k for s in _SECRET_KEYS) or k.endswith("_key")


def _strip_secrets(data: dict, path: Path) -> dict:
    """Recursively drop secret literals from a TOML-derived dict, warning on each."""
    cleaned: dict = {}
    for k, v in data.items():
        if isinstance(v, dict):
            cleaned[k] = _strip_secrets(v, path)
        elif _is_secret(k):
            print(
                f"WARNING: ignoring secret literal {k!r} in {path}; "
                f"use <name>_env = \"VARNAME\" instead",
                file=sys.stderr,
            )
        else:
            cleaned[k] = v
    return cleaned


def parse_toml_safely(text: str, *, source: str = "<string>") -> dict:
    """Parse TOML text, stripping secret literals (with a stderr warning per drop).

    Exposed for callers that want the same safe-parse semantics as the file loaders
    without having to write to disk first.
    """
    raw = tomllib.loads(text)
    return _strip_secrets(raw, Path(source))


# ---------------------------------------------------------------------------
# Helpers — deprecation rename
# ---------------------------------------------------------------------------


def _rename_deprecated(data: dict, path: Path) -> dict:
    """Rewrite the legacy ``embedding_endpoint`` key to ``embedding_base_url``.

    Emits a deprecation warning to stderr if the old key is present.
    """
    if "embedding_endpoint" in data:
        print(
            f"WARNING: 'embedding_endpoint' in {path} is deprecated; "
            f"use 'embedding_base_url' instead",
            file=sys.stderr,
        )
        # The new name wins if both are set (caller fixed half the migration).
        if "embedding_base_url" not in data:
            data["embedding_base_url"] = data.pop("embedding_endpoint")
        else:
            data.pop("embedding_endpoint")
    # Also handle the rename inside an [embedding] table.
    emb = data.get("embedding")
    if isinstance(emb, dict) and "endpoint" in emb:
        print(
            f"WARNING: 'endpoint' in [embedding] of {path} is deprecated; "
            f"use 'base_url' instead",
            file=sys.stderr,
        )
        if "base_url" not in emb:
            emb["base_url"] = emb.pop("endpoint")
        else:
            emb.pop("endpoint")
    return data


# ---------------------------------------------------------------------------
# Helpers — deep merge
# ---------------------------------------------------------------------------


def _deep_merge_toml(base: dict, overlay: dict) -> dict:
    """Deep-merge ``overlay`` on top of ``base``; returns a new dict.

    * Scalars in ``overlay`` replace scalars in ``base``.
    * A ``[agents.<op>]`` nested table is merged key-by-key so a higher layer can override
      a single field (e.g. ``model``) without dropping a lower layer's ``provider``.
    * Other dicts recurse.
    """
    out = dict(base)
    for k, v in overlay.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge_toml(out[k], v)
        else:
            out[k] = v
    return out


# ---------------------------------------------------------------------------
# Helpers — applying a parsed dict to a Config
# ---------------------------------------------------------------------------

# Map flat TOML keys (top-level and inside [embedding]/[llm] tables) to Config attributes.
# The flat fields are the source of truth; nested dataclasses are synced via _sync_nested.
_FLAT_KEYS: dict[str, str] = {
    "db_path": "db_path",
    "workspace": "workspace",
    "prefer_sqlite_vec": "prefer_sqlite_vec",
    "enable_wal": "enable_wal",
    "embedding_provider": "embedding_provider",
    "embedding_model": "embedding_model",
    "embedding_dim": "embedding_dim",
    "embedding_base_url": "embedding_base_url",
    "embedding_api_key_env": "embedding_api_key_env",
    "embedding_fallback": "embedding_fallback",
    "embedding_query_cache_size": "embedding_query_cache_size",
    "embedding_timeout_s": "embedding_timeout_s",
    "embedding_allow_dim_change": "embedding_allow_dim_change",
    "embedding_http_request": "embedding_http_request",
    "embedding_http_response_path": "embedding_http_response_path",
    "llm_provider": "llm_provider",
    "llm_base_url": "llm_base_url",
    "llm_model": "llm_model",
    "llm_api_key_env": "llm_api_key_env",
    "llm_max_tokens": "llm_max_tokens",
    "llm_temperature": "llm_temperature",
    "llm_structured_method": "llm_structured_method",
    "llm_timeout_s": "llm_timeout_s",
}

# [embedding.<k>] → flat field (same suffix in both).
_EMBEDDING_TABLE_KEYS = {
    "provider",
    "base_url",
    "model",
    "api_key_env",
    "fallback",
    "query_cache_size",
    "timeout_s",
    "allow_dim_change",
    "http_request",
    "http_response_path",
}

# [llm.<k>] → flat field.
_LLM_TABLE_KEYS = {
    "provider",
    "base_url",
    "model",
    "api_key_env",
    "max_tokens",
    "temperature",
    "structured_method",
    "timeout_s",
}

# Nested dataclasses on Config that take their own field-by-field overlay.
_NESTED_DATACLASS_SECTIONS = {
    "activation": ActivationWeights,
    "recall": RecallConfig,
    "consolidate": ConsolidateConfig,
    "code_index": CodeIndexConfig,
    "system2": System2Config,
    "attention": AttentionConfig,
}


def _apply_toml(cfg: Config, data: dict) -> None:
    """Apply a parsed, secret-stripped TOML dict to a Config in place.

    Handles top-level flat keys, the ``[embedding]``/``[llm]`` tables (populating the flat
    mirror fields), ``[agents.<op>]`` per-op overrides, and the nested dataclass sections.
    """
    for k, v in data.items():
        if k == "embedding" and isinstance(v, dict):
            for ek, ev in v.items():
                if ek in _EMBEDDING_TABLE_KEYS:
                    setattr(cfg, f"embedding_{ek}", ev)
                elif _is_secret(ek):
                    # already stripped by _strip_secrets; defensive no-op
                    continue
                else:
                    print(
                        f"WARNING: ignoring unknown embedding key {ek!r}",
                        file=sys.stderr,
                    )
        elif k == "llm" and isinstance(v, dict):
            for lk, lv in v.items():
                if lk in _LLM_TABLE_KEYS:
                    setattr(cfg, f"llm_{lk}", lv)
                elif _is_secret(lk):
                    continue
                else:
                    print(
                        f"WARNING: ignoring unknown llm key {lk!r}",
                        file=sys.stderr,
                    )
        elif k == "agents" and isinstance(v, dict):
            # Each [agents.<op>] is a dict of per-op overrides; merge op-by-op.
            merged = dict(cfg.agents_overrides)
            for op, overrides in v.items():
                if not isinstance(overrides, dict):
                    print(
                        f"WARNING: ignoring non-table agents.{op}",
                        file=sys.stderr,
                    )
                    continue
                merged[op] = {**merged.get(op, {}), **overrides}
            cfg.agents_overrides = merged
        elif k in _NESTED_DATACLASS_SECTIONS and isinstance(v, dict):
            section = getattr(cfg, k)
            section_cls = _NESTED_DATACLASS_SECTIONS[k]
            valid_keys = {f.name for f in fields(section_cls)}
            for nk, nv in v.items():
                if nk in valid_keys:
                    setattr(section, nk, nv)
                else:
                    print(
                        f"WARNING: ignoring unknown {k}.{nk}",
                        file=sys.stderr,
                    )
        elif k in _FLAT_KEYS:
            setattr(cfg, _FLAT_KEYS[k], v)
        else:
            print(f"WARNING: ignoring unknown config key {k!r}", file=sys.stderr)


def _apply_dict(cfg: Config, d: dict) -> None:
    """Apply a CLI-style dict (same shape as the TOML dict) to a Config in place."""
    _apply_toml(cfg, d)


def _sync_nested(cfg: Config) -> None:
    """Rebuild the nested dataclasses (``embedding``/``llm``) from the flat fields.

    Keeps the nested views consistent with the flat source-of-truth fields after any
    mutation (env/CLI overlay, direct attribute writes, …).
    """
    cfg.embedding = EmbeddingConfig(
        provider=cfg.embedding_provider,
        base_url=cfg.embedding_base_url,
        model=cfg.embedding_model,
        api_key_env=cfg.embedding_api_key_env,
        fallback=cfg.embedding_fallback,
        query_cache_size=cfg.embedding_query_cache_size,
        timeout_s=cfg.embedding_timeout_s,
        allow_dim_change=cfg.embedding_allow_dim_change,
        http_request=cfg.embedding_http_request,
        http_response_path=cfg.embedding_http_response_path,
    )
    cfg.llm = LLMConfig(
        provider=cfg.llm_provider,
        base_url=cfg.llm_base_url,
        model=cfg.llm_model,
        api_key_env=cfg.llm_api_key_env,
        max_tokens=cfg.llm_max_tokens,
        temperature=cfg.llm_temperature,
        structured_method=cfg.llm_structured_method,
        timeout_s=cfg.llm_timeout_s,
    )


# ---------------------------------------------------------------------------
# Helpers — env overlay
# ---------------------------------------------------------------------------


def _to_bool(v: str) -> bool:
    return v.strip().lower() in {"1", "true", "yes", "on"}


_ENV_MAP: dict[str, tuple[str, object]] = {
    "LADYM_DB": ("db_path", Path),
    "LADYM_WORKSPACE": ("workspace", str),
    "LADYM_EMBEDDING": ("embedding_provider", str),
    "LADYM_EMBEDDING_MODEL": ("embedding_model", str),
    "LADYM_EMBEDDING_BASE_URL": ("embedding_base_url", str),
    "LADYM_EMBEDDING_TIMEOUT_S": ("embedding_timeout_s", float),
    "LADYM_LLM_PROVIDER": ("llm_provider", str),
    "LADYM_LLM_BASE_URL": ("llm_base_url", str),
    "LADYM_LLM_MODEL": ("llm_model", str),
    "LADYM_LLM_MAX_TOKENS": ("llm_max_tokens", int),
    "LADYM_LLM_TEMPERATURE": ("llm_temperature", float),
    "LADYM_PREFER_SQLITE_VEC": ("prefer_sqlite_vec", _to_bool),
    "LADYM_ENABLE_WAL": ("enable_wal", _to_bool),
}


def _apply_env(cfg: Config) -> None:
    for env, (attr, cast) in _ENV_MAP.items():
        val = os.environ.get(env)
        if val:
            setattr(cfg, attr, cast(val))  # type: ignore[arg-type]


__all__ = [
    "ActivationWeights",
    "AttentionConfig",
    "CodeIndexConfig",
    "Config",
    "ConsolidateConfig",
    "EmbeddingConfig",
    "LLMConfig",
    "RecallConfig",
    "System2Config",
    "parse_toml_safely",
]
