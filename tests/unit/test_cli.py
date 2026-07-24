"""CLI smoke tests using typer's CliRunner."""

import os
from pathlib import Path

import pytest
from typer.testing import CliRunner

from ladym.cli import app

runner = CliRunner()


@pytest.fixture(autouse=True)
def _isolate_config(monkeypatch, tmp_path):
    """Hermetic: ignore any project/global ``ladym.toml`` and ``LADYM_*`` env during CLI
    tests, so a developer's local ``./ladym.toml`` (e.g. an ollama config) can't change
    which provider these tests run against."""
    monkeypatch.chdir(tmp_path)
    monkeypatch.setenv("HOME", str(tmp_path))
    for k in list(os.environ):
        if k.startswith("LADYM_"):
            monkeypatch.delenv(k, raising=False)
    yield


@pytest.fixture
def db_arg(tmp_path):
    return str(tmp_path / "cli.ladym.db")


def test_cli_remember_and_recall(db_arg):
    r = runner.invoke(
        app,
        [
            "remember",
            "auth uses JWT tokens with 24h expiry",
            "--db",
            db_arg,
            "--tags",
            "auth,security",
        ],
    )
    assert r.exit_code == 0, r.output
    assert "remembered" in r.output

    r = runner.invoke(
        app,
        [
            "recall",
            "how does authentication work",
            "--db",
            db_arg,
            "--top-k",
            "5",
        ],
    )
    assert r.exit_code == 0, r.output
    assert "auth" in r.output.lower()


def test_cli_recall_no_matches(db_arg):
    r = runner.invoke(app, ["recall", "completely unknown topic xyzzy", "--db", db_arg])
    assert r.exit_code == 0
    # either "no memories matched" or empty table
    assert "no memories matched" in r.output or "recall" in r.output


def test_cli_stats_empty_db(db_arg):
    r = runner.invoke(app, ["stats", "--db", db_arg])
    assert r.exit_code == 0
    assert "total memories" in r.output


def test_cli_index_and_search_code(db_arg):
    fixture = Path(__file__).resolve().parent.parent / "fixtures" / "sample_repo"
    r = runner.invoke(app, ["index", str(fixture), "--db", db_arg])
    assert r.exit_code == 0, r.output
    assert "indexed" in r.output

    r = runner.invoke(
        app,
        [
            "recall",
            "verify password",
            "--db",
            db_arg,
            "--code",
            "--json",
        ],
    )
    assert r.exit_code == 0, r.output
    # JSON output should contain verify_password
    assert "verify_password" in r.output or "password" in r.output.lower()


def test_cli_consolidate_smoke(db_arg):
    # write an episode via SDK, then consolidate via CLI
    from ladym.sdk import open_engine

    with open_engine(db_path=db_arg) as eng:
        eng.episodic.record(agent="bot", action="x", observation="discovered jwt key")
    r = runner.invoke(app, ["consolidate", "--db", db_arg])
    assert r.exit_code == 0, r.output
    assert "consolidated" in r.output


def test_cli_remember_then_forget(db_arg):
    from ladym.sdk import open_engine

    with open_engine(db_path=db_arg) as eng:
        m = eng.semantic.put_fact("ephemeral fact")
        mid = m.id
    r = runner.invoke(app, ["forget", mid, "--db", db_arg])
    assert r.exit_code == 0
    assert "forgot" in r.output


def test_cli_remember_then_link(db_arg):
    from ladym.sdk import open_engine

    with open_engine(db_path=db_arg) as eng:
        a = eng.semantic.put_fact("A").id
        b = eng.semantic.put_fact("B").id
    r = runner.invoke(app, ["link", a, b, "--relation", "depends_on", "--db", db_arg])
    assert r.exit_code == 0
    assert "linked" in r.output


def test_cli_workspace_isolation(db_arg):
    runner.invoke(
        app,
        [
            "remember",
            "ws-one fact",
            "--db",
            db_arg,
            "--workspace",
            "ws1",
        ],
    )
    r = runner.invoke(
        app,
        [
            "recall",
            "ws-one fact",
            "--db",
            db_arg,
            "--workspace",
            "ws2",
        ],
    )
    # querying a different workspace must not surface ws1's memory
    assert "ws-one" not in r.output or "no memories matched" in r.output


def test_cli_recall_with_config_file(tmp_path, monkeypatch):
    """A global ``--config <path>`` flag makes commands source their engine via
    ``Config.load`` (Task 2.2).

    The flag is a typer *callback* option, so typer parses it BEFORE the subcommand.
    """
    # Isolate from any operator config on this machine.
    monkeypatch.setenv("HOME", str(tmp_path))
    monkeypatch.chdir(tmp_path)
    f = tmp_path / "ladym.toml"
    f.write_text(f'db_path = "{tmp_path}/c.db"\nworkspace = "w"\nembedding_provider = "hashing"\n')
    res = runner.invoke(app, ["--config", str(f), "recall", "anything"])
    assert res.exit_code == 0, res.output


def test_config_command_missing_web_extra_errors(monkeypatch):
    """``ladym config`` errors clearly when the [web] extra isn't installed.

    Simulates fastapi being absent (sets ``sys.modules['fastapi'] = None``) so the
    command's explicit dependency guard fires regardless of whether other tests
    have already cached ``ladym.web.app``.
    """
    import sys

    monkeypatch.setitem(sys.modules, "fastapi", None)
    res = runner.invoke(app, ["config", "--no-browser"])
    assert res.exit_code != 0
    assert "ladym[web]" in res.output


def test_cli_remember_drop_noise(db_arg):
    """``ladym remember`` with pure-noise content is dropped by the heuristic
    prefix — exit 0, output surfaces the drop reason, and the memory is NOT
    persisted (the dropped Memory carries a non-existent fake id that we must
    not leak as ``id=``)."""
    from ladym.sdk import open_engine

    r = runner.invoke(
        app,
        ["remember", "lol ok test asdf foo", "--db", db_arg, "--workspace", "wsdrop"],
    )
    assert r.exit_code == 0, r.output
    assert "dropped" in r.output
    assert "reason=noise" in r.output
    # Red line: the non-persistent fake id must NOT be printed.
    assert "id=" not in r.output

    # The dropped content must not have been persisted (count returns a
    # {layer/type: n} dict; an empty workspace is {}).
    with open_engine(db_path=db_arg, workspace="wsdrop") as eng:
        assert eng.store.count(workspace="wsdrop") == {}
        assert not any(m.content == "lol ok test asdf foo" for m in eng.store.iter_memories(workspace="wsdrop"))


def test_cli_remember_pass_persists(db_arg):
    """``ladym remember "<long fact>"`` clears the gate: output surfaces
    ``remembered id=``, and the memory is actually in the store."""
    from ladym.sdk import open_engine

    content = "a reasonably long fact about the system"
    r = runner.invoke(app, ["remember", content, "--db", db_arg])
    assert r.exit_code == 0, r.output
    assert "remembered" in r.output
    assert "id=" in r.output
    assert "hash=" in r.output  # gate-passed output includes hash (see cli.py:87)
    assert "dropped" not in r.output

    with open_engine(db_path=db_arg) as eng:
        assert any(m.content == content for m in eng.store.iter_memories(workspace="default"))


def test_cli_record_creates_l1_episodic_event(db_arg):
    """``ladym record`` writes an L1 episodic EVENT (not an L2 fact like ``remember``)."""
    from ladym.sdk import open_engine

    r = runner.invoke(
        app,
        [
            "record",
            "--agent", "claude",
            "--action", "fixed login bug",
            "--observation", "rotated jwt secret",
            "--outcome", "success",
            "--tags", "auth,bug",
            "--db", db_arg,
        ],
    )
    assert r.exit_code == 0, r.output
    assert "recorded" in r.output
    assert "L1_episodic" in r.output
    assert "event" in r.output

    # Reopen the store and assert the L1 row exists with the right shape.
    with open_engine(db_path=db_arg) as eng:
        episodic = list(eng.store.iter_memories(layer="L1_episodic"))
        assert len(episodic) == 1
        m = episodic[0]
        assert m.type == "event"
        assert m.metadata.get("agent") == "claude"
        assert m.metadata.get("action") == "fixed login bug"
        assert m.metadata.get("observation") == "rotated jwt secret"
        assert m.metadata.get("outcome") == "success"
        assert "auth" in m.tags and "bug" in m.tags


def test_cli_record_feeds_system2_l5_l6(db_arg):
    """Payoff: ``ladym record`` ×3 seeds enough episodes for the worker's L5/L6
    extractors to fire (instead of being gated off for lack of episodes).

    Adapts ``test_cycle_populates_l5_l6_when_agent_configured`` to use the CLI
    ``record`` command as the seeding surface.
    """
    from ladym.operations import system2 as system2_module
    from ladym.operations.l5 import L5ExtractionReport
    from ladym.operations.l6 import L6PredictionReport
    from ladym.providers import FakeLLMProvider
    from ladym.schema import Layer
    from ladym.sdk import open_engine

    # Seed 3 distinct episodes via the new CLI command.
    for obs in ("auth uses JWT", "cache uses redis", "logs ship to loki"):
        r = runner.invoke(
            app,
            [
                "record",
                "--agent", "bot",
                "--action", "found",
                "--observation", obs,
                "--db", db_arg,
            ],
        )
        assert r.exit_code == 0, r.output

    # 3 distinct L2 facts for L5 to cluster — decoupled from how consolidate
    # merges the episodes above, so the resulting cluster size is deterministic.
    with open_engine(db_path=db_arg) as eng:
        for c in (
            "the api authenticates with jwt",
            "the cache is backed by redis",
            "logs go to loki",
        ):
            eng.semantic.put_fact(c)

        # inject fake agents straight into the lazy cache so _get_agent returns them
        eng._agents["l5_mental_model"] = FakeLLMProvider(
            structured_fn=lambda msgs, schema: {
                "title": "Infra",
                "model": "service infrastructure",
            }
        )
        eng._agents["l6_forward_intent"] = FakeLLMProvider(
            structured_fn=lambda msgs, schema: {
                "intents": [{"intent": "rotate keys", "confidence": 0.8}]
            }
        )
        # force every candidate into one cluster regardless of cosine sign
        eng.config.system2.l5_cluster_similarity = -1.0
        eng.config.system2.l5_min_cluster_size = 2

        report = system2_module.run_system2_cycle(eng)

        # The gate (min_episodes_to_run default 3) must have cleared because of
        # the CLI ``record`` writes, so L5/L6 actually ran.
        assert isinstance(report.l5, L5ExtractionReport)
        assert isinstance(report.l6, L6PredictionReport)
        assert report.l5.new_models >= 1, "L5 extractor did not fire on CLI-seeded episodes"
        assert report.l6.predictions >= 1, "L6 predictor did not fire on CLI-seeded episodes"
        assert any(
            m.layer == Layer.L5_MENTAL.value
            for m in eng.store.iter_memories()
        )
        assert any(
            m.layer == Layer.L6_PREDICTIVE.value
            for m in eng.store.iter_memories()
        )


# --- Task 5: top-level friendly errors + --debug (entry point = ladym.cli:main) ---
#
# The friendly handler lives in ``ladym.cli.main()``, which wraps ``app()`` and
# is wired as the ``[project.scripts]`` entry point. Typer's ``CliRunner`` calls
# the typer app directly and bypasses ``main()``, so to exercise the production
# entry we invoke ``main()`` ourselves:
#   - friendly path: in-process — capture stdout, catch SystemExit(1).
#   - --debug path: subprocess — the re-raised exception must reach Python's
#     top level so typer's excepthook prints the traceback to stderr (this
#     cannot be observed in-process without re-implementing the excepthook).


def _run_main_in_process(argv):
    """Call ladym.cli.main(argv) in this process; capture stdout.

    Returns (exit_code_or_None, stdout_str, exc_type_or_None). When ``main``
    raises SystemExit (friendly path) we report its code; any other exception
    is reported via ``exc_type`` (the --debug path re-raises).
    """
    import io
    import sys as _sys

    from ladym import cli as cli_mod

    old_argv = _sys.argv
    old_stdout = _sys.stdout
    _sys.argv = ["ladym", *argv]
    captured = io.StringIO()
    _sys.stdout = captured
    exit_code = None
    exc_type = None
    try:
        cli_mod.main()
    except SystemExit as e:
        exit_code = e.code
    except BaseException as e:  # re-raised by --debug
        exc_type = type(e)
    finally:
        _sys.stdout = old_stdout
        _sys.argv = old_argv
    # restore _debug after each run (the callback mutates the module global)
    cli_mod._debug = False
    return exit_code, captured.getvalue(), exc_type


def test_config_error_surfaces_friendly_not_traceback(tmp_path, monkeypatch):
    # provider=openai but no key anywhere → make_agent raises ConfigError on
    # remember → main() prints a one-line message + sys.exit(1), no traceback.
    db = str(tmp_path / "x.ladym.db")
    monkeypatch.setenv("LADYM_LLM_PROVIDER", "openai")
    monkeypatch.setenv("LADYM_LLM_API_KEY_ENV", "NO_SUCH_KEY")
    monkeypatch.delenv("NO_SUCH_KEY", raising=False)

    exit_code, stdout, exc_type = _run_main_in_process(
        ["remember", "x", "--db", db]
    )
    assert exit_code == 1
    assert exc_type is None  # friendly path: no exception escapes
    assert "NO_SUCH_KEY" in stdout
    assert "set-master-key" in stdout
    assert "Traceback (most recent call last)" not in stdout


def test_debug_shows_traceback(tmp_path, monkeypatch):
    # --debug makes main() re-raise → typer's excepthook prints the full
    # traceback to stderr (observed via a real subprocess, since the excepthook
    # only fires at the process top level).
    db = str(tmp_path / "x.ladym.db")
    env = {
        **os.environ,
        "HOME": str(tmp_path),
        # wipe any operator LADYM_* config from this machine
        **{k: "" for k in list(os.environ) if k.startswith("LADYM_")},
        "LADYM_LLM_PROVIDER": "openai",
        "LADYM_LLM_API_KEY_ENV": "NO_SUCH_KEY",
    }
    env.pop("NO_SUCH_KEY", None)
    import subprocess
    import sys

    repo_src = str(Path(__file__).resolve().parents[2] / "src")
    env["PYTHONPATH"] = repo_src + os.pathsep + env.get("PYTHONPATH", "")
    py = sys.executable
    proc = subprocess.run(
        [
            py,
            "-c",
            "import sys; sys.argv=['ladym','--debug','remember','x',"
            f"'--db','{db}']; from ladym.cli import main; main()",
        ],
        cwd=str(tmp_path),
        env=env,
        capture_output=True,
        text=True,
    )
    assert proc.returncode == 1, proc.stderr
    combined = proc.stdout + proc.stderr
    assert "Traceback" in combined


# --- Task 6: config command group (set/set-master-key/reset-master-key/list/rm) ---


def test_config_set_requires_master_key(tmp_path, monkeypatch):
    # _isolate_config is autouse → HOME=tmp_path → empty ~/.ladyM
    # ``runner.invoke(app, ...)`` calls the typer app directly and bypasses
    # ``main()`` (where the friendly ConfigError→one-line message handler lives,
    # mirroring ``test_config_error_surfaces_friendly_not_traceback``). So we
    # exercise the production entry via ``_run_main_in_process`` to observe the
    # real stdout users see.
    exit_code, stdout, exc_type = _run_main_in_process(
        ["config", "set", "DEEPSEEK_API_KEY", "sk-x"]
    )
    assert exit_code == 1
    assert exc_type is None  # friendly path: no exception escapes
    assert "set-master-key" in stdout
    assert "Traceback (most recent call last)" not in stdout


def test_config_set_and_list_roundtrip(tmp_path, monkeypatch):
    assert runner.invoke(app, ["config", "set-master-key", "pass"]).exit_code == 0
    assert runner.invoke(app, ["config", "set", "K1", "v1"]).exit_code == 0
    r = runner.invoke(app, ["config", "list"])
    assert "K1" in r.output
    assert "v1" not in r.output  # value not printed


def test_config_reset_master_key_reencrypts(tmp_path, monkeypatch):
    runner.invoke(app, ["config", "set-master-key", "old"])
    runner.invoke(app, ["config", "set", "K", "secret"])
    assert runner.invoke(app, ["config", "reset-master-key", "new"]).exit_code == 0
    # value still resolvable after re-encryption (verified via get in a py call below)
    from ladym.secrets import SecretStore
    s = SecretStore(dir=tmp_path / ".ladyM")
    assert s.get("K") == "secret"


def test_config_rm(tmp_path, monkeypatch):
    runner.invoke(app, ["config", "set-master-key", "p"])
    runner.invoke(app, ["config", "set", "K", "v"])
    assert runner.invoke(app, ["config", "rm", "K"]).exit_code == 0
    r = runner.invoke(app, ["config", "list"])
    assert "K" not in r.output
