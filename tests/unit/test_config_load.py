"""Tests for Task 2.1 — TOML loading, 4-layer precedence, secret rejection, endpoint rename.

These pin the public contract for :class:`ladym.config.Config` loaders.

* `Config.from_file(path)` parses a TOML file, strips secret literals (with a warning),
  renames ``embedding_endpoint`` → ``embedding_base_url`` (with a deprecation warning),
  and populates both flat mirror fields and nested config dataclasses.
* `Config.load(config_path=None, *, cli_overrides=None)` resolves the 4-layer precedence:
  CLI > env > project file (``./ladym.toml``) > global file
  (``~/.ladym/config.toml``) > defaults.
"""

from __future__ import annotations

from pathlib import Path

from ladym.config import Config


def write(p: Path, body: str) -> None:
    p.write_text(body)


# ---- from_file: basic loading -------------------------------------------------


def test_from_file_loads_embedding_and_llm(tmp_path):
    f = tmp_path / "ladym.toml"
    write(f, """
embedding_provider = "openai"
embedding_base_url = "https://api.deepseek.com/v1"
[llm]
provider = "openai"
base_url = "https://api.deepseek.com/v1"
model = "deepseek-chat"
""")
    cfg = Config.from_file(f)
    assert cfg.embedding_provider == "openai"
    assert cfg.llm_provider == "openai"
    assert cfg.llm_base_url == "https://api.deepseek.com/v1"
    assert cfg.llm_model == "deepseek-chat"


def test_from_file_loads_nested_embedding_section(tmp_path):
    """The [embedding] table populates flat mirror fields too."""
    f = tmp_path / "ladym.toml"
    write(f, """
[embedding]
provider = "ollama"
base_url = "http://localhost:11434"
model = "nomic-embed-text"
timeout_s = 42.0
allow_dim_change = true
""")
    cfg = Config.from_file(f)
    assert cfg.embedding_provider == "ollama"
    assert cfg.embedding_base_url == "http://localhost:11434"
    assert cfg.embedding_model == "nomic-embed-text"
    assert cfg.embedding_timeout_s == 42.0
    assert cfg.embedding_allow_dim_change is True


def test_from_file_loads_nested_sections(tmp_path):
    """[activation], [recall], [consolidate], [code_index], [system2], [attention], [agents]."""
    f = tmp_path / "ladym.toml"
    write(f, """
[activation]
similarity = 2.5
[recall]
top_k_tier1 = 5
[system2]
enabled = true
interval_s = 99
[attention]
dedup_window_s = 7200.0
[agents.consolidate]
provider = "openai"
model = "gpt-4o"
""")
    cfg = Config.from_file(f)
    assert cfg.activation.similarity == 2.5
    assert cfg.recall.top_k_tier1 == 5
    assert cfg.system2.enabled is True
    assert cfg.system2.interval_s == 99
    assert cfg.attention.dedup_window_s == 7200.0
    assert cfg.agents_overrides["consolidate"]["provider"] == "openai"
    assert cfg.agents_overrides["consolidate"]["model"] == "gpt-4o"


# ---- secret rejection ---------------------------------------------------------


def test_secret_literal_is_rejected(tmp_path, capsys):
    f = tmp_path / "ladym.toml"
    write(f, '[llm]\napi_key = "sk-leaked"\nmodel = "m"\n')
    cfg = Config.from_file(f)
    captured = capsys.readouterr()
    assert "api_key" in captured.err.lower() or "warning" in captured.err.lower()
    # Not stored anywhere; both the flat and nested api_key_env stay at default "".
    assert getattr(cfg, "llm_api_key_env", "") == ""


def test_secret_literal_in_flat_form_rejected(tmp_path, capsys):
    """Top-level embedding_api_key = '...' is also a secret and must be dropped."""
    f = tmp_path / "ladym.toml"
    write(f, 'embedding_api_key = "sk-flat"\n')
    cfg = Config.from_file(f)
    captured = capsys.readouterr()
    assert "warning" in captured.err.lower()
    assert cfg.embedding_api_key_env == ""


def test_secret_literal_inside_nested_embedding_section(tmp_path, capsys):
    """[embedding].api_key is dropped too; base_url survives."""
    f = tmp_path / "ladym.toml"
    write(f, '[embedding]\napi_key = "sk-nested"\nbase_url = "http://x"\n')
    cfg = Config.from_file(f)
    captured = capsys.readouterr()
    assert "warning" in captured.err.lower()
    assert cfg.embedding_base_url == "http://x"
    assert cfg.embedding_api_key_env == ""


def test_parse_toml_safely_strips_secrets(capsys):
    """parse_toml_safely(text) returns the parsed dict minus secret literals."""
    from ladym.config import parse_toml_safely

    out = parse_toml_safely('model = "m"\napi_key = "sk"\n[llm]\ntoken = "t"\n')
    assert out == {"model": "m", "llm": {}}
    assert "warning" in capsys.readouterr().err.lower()


def test_api_key_env_survives_secret_stripping(tmp_path):
    """Regression guard for the Critical bug: ``api_key_env`` references the NAME
    of an env var (not a secret literal), so it must NOT be dropped by _is_secret.
    See NFR-5: operators wire keys via ``*_env`` instead of persisting secrets.
    """
    f = tmp_path / "ladym.toml"
    write(
        f,
        '[llm]\napi_key_env = "MY_LLM_KEY"\n'
        '[embedding]\napi_key_env = "MY_EMB_KEY"\n',
    )
    cfg = Config.from_file(f)
    assert cfg.llm_api_key_env == "MY_LLM_KEY"
    assert cfg.embedding_api_key_env == "MY_EMB_KEY"


def test_non_underscore_secret_variants_rejected(tmp_path, capsys):
    """``apikey``, ``access_token``, ``signing_key`` are all secret literals and
    must be rejected (warned + not applied). The old substring matcher missed
    the no-underscore forms because it only looked for 'api_key' (with one '_').
    """
    f = tmp_path / "ladym.toml"
    write(
        f,
        '[llm]\napikey = "x"\n'
        'access_token = "x"\n'
        'signing_key = "x"\n'
        'model = "kept"\n',
    )
    cfg = Config.from_file(f)
    err = capsys.readouterr().err.lower()
    # Each variant warned.
    assert "apikey" in err
    assert "access_token" in err
    assert "signing_key" in err
    # Non-secret field in the same section still applied.
    assert cfg.llm_model == "kept"
    # No secret value leaked into any attribute.
    assert getattr(cfg, "llm_apikey", None) in (None, "")
    assert getattr(cfg, "llm_access_token", None) in (None, "")
    assert getattr(cfg, "llm_signing_key", None) in (None, "")


def test_allowed_non_secret_keys_applied(tmp_path):
    """``model``, ``base_url``, and ``api_key_env`` are all legitimate non-secret
    keys and must be applied as-is.
    """
    f = tmp_path / "ladym.toml"
    write(
        f,
        '[llm]\nmodel = "m"\n'
        'base_url = "u"\n'
        'api_key_env = "V"\n',
    )
    cfg = Config.from_file(f)
    assert cfg.llm_model == "m"
    assert cfg.llm_base_url == "u"
    assert cfg.llm_api_key_env == "V"


def test_cli_overrides_strip_secret_literals(tmp_path, capsys, monkeypatch):
    """Defense-in-depth: secret literals in ``cli_overrides`` are stripped too,
    not silently applied. Non-secret CLI overrides still take effect."""
    # Hermetic: don't let a real ./ladym.toml or ~/.ladym/config.toml leak in.
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.delenv("LADYM_WORKSPACE", raising=False)
    f = tmp_path / "ladym.toml"
    write(f, 'workspace = "fromfile"\n')
    cfg = Config.load(
        config_path=f,
        cli_overrides={
            "workspace": "fromcli",
            "llm_api_key": "sk-cli-leak",
            "llm_model": "from-cli",
        },
    )
    err = capsys.readouterr().err.lower()
    # Non-secret override applied.
    assert cfg.workspace == "fromcli"
    assert cfg.llm_model == "from-cli"
    # Secret override warned + did not leak.
    assert "llm_api_key" in err
    assert getattr(cfg, "llm_api_key", None) in (None, "")



# ---- precedence ---------------------------------------------------------------


def test_precedence_cli_over_file_over_defaults(tmp_path, monkeypatch):
    """CLI > file > defaults."""
    # Ensure env does not leak into this test.
    monkeypatch.delenv("LADYM_WORKSPACE", raising=False)
    f = tmp_path / "ladym.toml"
    write(f, 'workspace = "fromfile"\n')
    cfg = Config.load(config_path=f, cli_overrides={"workspace": "fromcli"})
    assert cfg.workspace == "fromcli"


def test_env_over_file(tmp_path, monkeypatch):
    f = tmp_path / "ladym.toml"
    write(f, 'workspace = "fromfile"\n')
    monkeypatch.setenv("LADYM_WORKSPACE", "fromenv")
    cfg = Config.load(config_path=f)
    assert cfg.workspace == "fromenv"


def test_file_over_defaults(tmp_path, monkeypatch):
    """File > defaults (no env, no CLI)."""
    monkeypatch.delenv("LADYM_WORKSPACE", raising=False)
    f = tmp_path / "ladym.toml"
    write(f, 'workspace = "fromfile"\n')
    cfg = Config.load(config_path=f)
    assert cfg.workspace == "fromfile"


def test_precedence_explicit_over_project_over_global(tmp_path, monkeypatch):
    """config_path > ./ladym.toml > ~/.ladym/config.toml > defaults (deep merge).

    We point ``Path.home()`` and ``Path.cwd()`` at the tmp_path so the test is
    deterministic regardless of the developer's real home/cwd.
    """
    monkeypatch.delenv("LADYM_WORKSPACE", raising=False)

    home = tmp_path / "home"
    proj = tmp_path / "proj"
    home.mkdir()
    proj.mkdir()
    (home / ".ladym").mkdir()
    (home / ".ladym" / "config.toml").write_text(
        'workspace = "global"\nllm_model = "global-model"\n'
    )
    (proj / "ladym.toml").write_text('llm_model = "project-model"\n')
    explicit = proj / "explicit.toml"
    explicit.write_text('embedding_provider = "ollama"\n')

    monkeypatch.setattr(Path, "home", lambda: home)
    monkeypatch.chdir(proj)

    cfg = Config.load(config_path=explicit)
    # global-only field passes through
    assert cfg.workspace == "global"
    # project overrides global
    assert cfg.llm_model == "project-model"
    # explicit overrides defaults
    assert cfg.embedding_provider == "ollama"


def test_precedence_deep_merge_nested_dataclasses(tmp_path, monkeypatch):
    """Deep merge: project file's [activation].recency overlays global's [activation].similarity."""
    monkeypatch.delenv("LADYM_WORKSPACE", raising=False)

    home = tmp_path / "home"
    proj = tmp_path / "proj"
    home.mkdir()
    proj.mkdir()
    (home / ".ladym").mkdir()
    (home / ".ladym" / "config.toml").write_text(
        "[activation]\nsimilarity = 2.0\nrecency = 0.5\n"
    )
    (proj / "ladym.toml").write_text("[activation]\nrecency = 0.9\n")

    monkeypatch.setattr(Path, "home", lambda: home)
    monkeypatch.chdir(proj)

    cfg = Config.load()
    # global's similarity survives, project's recency overlays it
    assert cfg.activation.similarity == 2.0
    assert cfg.activation.recency == 0.9


# ---- rename + deprecation -----------------------------------------------------


def test_endpoint_renamed_to_base_url_with_deprecation(tmp_path, capsys):
    f = tmp_path / "ladym.toml"
    write(f, 'embedding_endpoint = "http://old"\n')
    cfg = Config.from_file(f)
    assert cfg.embedding_base_url == "http://old"
    assert "deprecat" in capsys.readouterr().err.lower()


# ---- defaults + offline back-compat ------------------------------------------


def test_config_defaults_offline():
    cfg = Config()
    assert cfg.embedding_provider == "hashing"
    assert cfg.llm_provider == "none"  # canonical offline token (Task 2.1)
    assert cfg.workspace == "default"
    assert cfg.prefer_sqlite_vec is True
    assert cfg.activation.similarity == 1.0
    assert cfg.recall.top_k_tier1 == 8


def test_for_testing_still_offline(tmp_path):
    cfg = Config.for_testing(tmp_path)
    assert cfg.embedding_provider == "hashing"
    assert cfg.llm_provider == "none"
    assert cfg.prefer_sqlite_vec is False
    assert cfg.workspace == "test"


# ---- agents_overrides deep merge ----------------------------------------------


def test_agents_overrides_deep_merge(tmp_path, monkeypatch):
    """Higher-precedence layer's per-op dict wins; lower layer's other ops survive."""
    monkeypatch.delenv("LADYM_WORKSPACE", raising=False)

    home = tmp_path / "home"
    proj = tmp_path / "proj"
    home.mkdir()
    proj.mkdir()
    (home / ".ladym").mkdir()
    (home / ".ladym" / "config.toml").write_text(
        "[agents.consolidate]\nprovider = 'openai'\nmodel = 'global-m'\n"
    )
    (proj / "ladym.toml").write_text(
        "[agents.consolidate]\nmodel = 'project-m'\n"
    )

    monkeypatch.setattr(Path, "home", lambda: home)
    monkeypatch.chdir(proj)

    cfg = Config.load()
    ov = cfg.agents_overrides["consolidate"]
    # Provider from global survives; model from project overrides.
    assert ov["provider"] == "openai"
    assert ov["model"] == "project-m"


def test_plaintext_api_key_allowed_when_flag_on(tmp_path):
    """DEV escape hatch: with allow_plaintext_secrets=true, a literal api_key survives."""
    f = tmp_path / "ladym.toml"
    f.write_text(
        'allow_plaintext_secrets = true\n'
        '[llm]\nprovider = "openai"\napi_key = "sk-test-123"\n'
    )
    cfg = Config.from_file(f)
    assert cfg.allow_plaintext_secrets is True
    assert cfg.llm_api_key == "sk-test-123"


def test_plaintext_api_key_still_rejected_when_flag_off(tmp_path, capsys):
    """Default stays secure: without the flag, a literal api_key is stripped + warned."""
    f = tmp_path / "ladym.toml"
    f.write_text('[llm]\nprovider = "openai"\napi_key = "sk-test-123"\n')
    cfg = Config.from_file(f)
    assert cfg.llm_api_key == ""  # stripped
    assert "secret literal" in capsys.readouterr().err.lower()
