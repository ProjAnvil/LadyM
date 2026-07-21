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
