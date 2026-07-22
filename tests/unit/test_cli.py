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


def test_cli_remember_drop_too_short(db_arg):
    """``ladym remember "hi"`` is below the attention gate's min_chars, so it
    must be dropped — exit 0, output surfaces the drop reason, and the memory
    is NOT persisted (the dropped Memory carries a non-existent fake id that we
    must not leak as ``id=``)."""
    from ladym.sdk import open_engine

    r = runner.invoke(
        app,
        ["remember", "hi", "--db", db_arg, "--workspace", "wsdrop"],
    )
    assert r.exit_code == 0, r.output
    assert "dropped" in r.output
    assert "reason=too short" in r.output
    # Red line: the non-persistent fake id must NOT be printed.
    assert "id=" not in r.output

    # The dropped content must not have been persisted (count returns a
    # {layer/type: n} dict; an empty workspace is {}).
    with open_engine(db_path=db_arg, workspace="wsdrop") as eng:
        assert eng.store.count(workspace="wsdrop") == {}
        assert not any(m.content == "hi" for m in eng.store.iter_memories(workspace="wsdrop"))


def test_cli_remember_pass_persists(db_arg):
    """``ladym remember "<long fact>"`` clears the gate: output surfaces
    ``remembered id=``, and the memory is actually in the store."""
    from ladym.sdk import open_engine

    content = "a reasonably long fact about the system"
    r = runner.invoke(app, ["remember", content, "--db", db_arg])
    assert r.exit_code == 0, r.output
    assert "remembered" in r.output
    assert "id=" in r.output
    assert "hash=" in r.output  # pass 输出含 hash（cli.py:87）
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
