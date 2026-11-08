"""Offline Engine observations, not fabricated QA or billing measurements."""

import math

import pytest

from longmemeval.learning_eval import run_learning_eval


def test_four_real_engine_variants():
    report = run_learning_eval()
    assert report["measurement"] == "offline_engine_fixture"
    assert report["read_elapsed_unit"] == "seconds"
    assert report["generative_calls"] == 0
    rows = report["variants"]
    assert [r["variant"] for r in rows] == [
        "stateless", "direct_source", "r1", "r2_topic",
    ]
    for row in rows:
        assert set(row) == {
            "variant", "source_coverage", "stale_usage", "read_elapsed",
            "context_units", "unit",
        }
        assert row["unit"] == "bytes"
        assert math.isfinite(row["read_elapsed"]) and row["read_elapsed"] >= 0
        assert isinstance(row["context_units"], int) and row["context_units"] >= 0
        assert 0 <= row["source_coverage"] <= 1
        assert row["stale_usage"] == 0
    assert rows[0]["source_coverage"] == 0
    assert rows[1]["source_coverage"] == rows[2]["source_coverage"] == rows[3]["source_coverage"] == 1
    assert all(r["context_units"] > 0 for r in rows)
    assert "accuracy" not in report and "token_savings" not in report


@pytest.mark.parametrize("edition,backend", [
    ("enterprise", "sqlite"), ("personal", "unknown"), ("unknown", "sqlite"),
])
def test_invalid_edition_backend_is_rejected(edition, backend):
    with pytest.raises(ValueError):
        run_learning_eval(edition=edition, backend=backend)


def test_postgres_requires_explicit_credentials(monkeypatch):
    monkeypatch.delenv("LADYM_TEST_PG_DSN", raising=False)
    with pytest.raises(ValueError, match="LADYM_TEST_PG_DSN"):
        run_learning_eval(backend="postgres")


def test_live_qa_requires_explicit_model_budget_limit(tmp_path, monkeypatch):
    from longmemeval.config import BenchConfig
    from longmemeval.run_qa import run_qa

    monkeypatch.setenv("OPENAI_API_KEY", "not-authority-to-call")
    cfg = BenchConfig(base_dir=tmp_path)
    with pytest.raises(ValueError, match="model.*budget.*limit"):
        run_qa([], cfg)
    assert not cfg.results_dir.exists()


def test_live_judge_requires_explicit_budget_limit(tmp_path):
    from longmemeval.config import BenchConfig
    from longmemeval.evaluate import run_qa_metrics

    cfg = BenchConfig(base_dir=tmp_path)
    with pytest.raises(ValueError, match="model.*budget.*limit"):
        run_qa_metrics(cfg, tmp_path / "data.json", "gpt-4o")


@pytest.mark.parametrize("command", ["qa", "evaluate"])
def test_live_cli_rejects_implicit_network(command):
    from longmemeval.__main__ import parse_args

    with pytest.raises(SystemExit):
        parse_args([command])


def test_live_cli_exposes_explicit_wall_budget():
    from longmemeval.__main__ import parse_args

    args = parse_args(["qa", "--model", "chosen-model", "--budget-seconds", "10", "--limit", "2"])
    assert args.model == "chosen-model" and args.budget_seconds == 10 and args.limit == 2


@pytest.mark.parametrize("budget", [0, -1, float("nan"), float("inf")])
def test_live_budget_must_be_positive_finite(budget):
    from longmemeval.config import BenchConfig

    with pytest.raises(ValueError):
        BenchConfig(limit=1, live_budget_s=budget).require_live("model")


def test_live_judge_refuses_hypotheses_above_limit(tmp_path):
    from longmemeval.config import BenchConfig
    from longmemeval.evaluate import run_qa_metrics

    cfg = BenchConfig(base_dir=tmp_path, limit=1, live_budget_s=10)
    cfg.results_dir.mkdir(parents=True)
    (cfg.results_dir / "hypothesis.jsonl").write_text(
        '{"question_id":"a","hypothesis":"x"}\n{"question_id":"b","hypothesis":"y"}\n'
    )
    with pytest.raises(ValueError, match="question limit"):
        run_qa_metrics(cfg, tmp_path / "data.json", "gpt-4o")


def test_live_answer_deadline_prevents_another_provider_request(monkeypatch):
    import sys
    from types import SimpleNamespace
    from longmemeval.config import BenchConfig
    from longmemeval import run_qa

    times = iter([100, 101, 111])
    monkeypatch.setattr(run_qa.time, "monotonic", lambda: next(times))
    result = SimpleNamespace(choices=[SimpleNamespace(message=SimpleNamespace(content="fixture"))])
    transport = SimpleNamespace(chat=SimpleNamespace(completions=SimpleNamespace(create=lambda **kw: result)))
    monkeypatch.setitem(sys.modules, "openai", SimpleNamespace(OpenAI=lambda **kw: transport))
    answer = run_qa._default_answer_llm(BenchConfig(limit=2, live_model="explicit-model", live_budget_s=10))
    assert answer("system", "user") == "fixture"
    with pytest.raises(TimeoutError, match="wall budget"):
        answer("system", "user")
