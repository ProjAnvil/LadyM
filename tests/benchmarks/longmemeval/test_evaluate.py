"""evaluate.py tests: orchestrate vendored upstream scripts → scores.md.

Monkeypatches subprocess.run so we validate orchestration, NOT real subprocess behavior.
"""
from benchmarks.longmemeval.config import BenchConfig
from benchmarks.longmemeval import evaluate


def test_retrieval_block_parses_upstream_stdout(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    (cfg.results_dir / "retrieval.jsonl").write_text(
        '{"question_id":"x","retrieval_results":{"metrics":{}}}\n'
    )
    fake_stdout = (
        "Session-level metrics:\n\trecall_all@5 = 0.8\n"
        "Turn-level metrics:\n\trecall_all@50 = 0.6\n"
    )

    def fake_run(cmd, **kw):
        class R:
            stdout = fake_stdout
            returncode = 0
        return R()

    monkeypatch.setattr(evaluate.subprocess, "run", fake_run)
    block = evaluate.run_retrieval_metrics(cfg)
    assert "recall_all@5 = 0.8" in block
    assert "Session-level" in block


def test_scores_md_written(tmp_path, monkeypatch):
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    (cfg.results_dir / "retrieval.jsonl").write_text("\n")
    (cfg.results_dir / "hypothesis.jsonl").write_text(
        '{"question_id":"x","hypothesis":"y"}\n'
    )

    def fake_run(cmd, **kw):
        class R:
            stdout = "Overall Accuracy: 0.75"
            returncode = 0
        return R()

    monkeypatch.setattr(evaluate.subprocess, "run", fake_run)
    evaluate.evaluate(cfg, tmp_path / "data.json", judge_model="gpt-4o")
    md = (cfg.results_dir / "scores.md").read_text()
    assert "Overall Accuracy: 0.75" in md


def test_eval_log_path_matches_vendored_short_name(tmp_path, monkeypatch):
    """evaluate_qa.py writes <hyp>.eval-results-<argv[1]>; for judge='gpt-4o'
    the suffix is '-gpt-4o' (NOT '-gpt4o'). This test pins the corrected path.
    """
    cfg = BenchConfig(difficulty="oracle", variant="raw", base_dir=tmp_path)
    cfg.results_dir.mkdir(parents=True, exist_ok=True)
    (cfg.results_dir / "hypothesis.jsonl").write_text(
        '{"question_id":"x","hypothesis":"y"}\n'
    )
    seen = []

    def fake_run(cmd, **kw):
        seen.append(cmd)
        class R:
            stdout = "Overall Accuracy: 0.75"
            returncode = 0
        return R()

    monkeypatch.setattr(evaluate.subprocess, "run", fake_run)
    evaluate.evaluate(cfg, tmp_path / "data.json", judge_model="gpt-4o")
    # 2 calls: evaluate_qa.py then print_qa_metrics.py
    assert len(seen) == 2
    # The print_qa_metrics.py call receives the eval-log path as argv[1]
    print_qa_cmd = seen[1]
    eval_log_arg = print_qa_cmd[2]
    # Vendored writes <hyp>.eval-results-<metric_model_short> where short == argv[1]
    # of evaluate_qa.py == "gpt-4o" (NOT "gpt4o")
    assert eval_log_arg.endswith("hypothesis.jsonl.eval-results-gpt-4o")
