from benchmarks.longmemeval.metrics import recall_all, ndcg, build_metric_dict


def test_recall_all_all_gold_present():
    assert recall_all(["a", "b", "c"], {"a", "b"}, k=3) == 1.0


def test_recall_all_missing_one():
    assert recall_all(["a", "x", "c"], {"a", "b"}, k=3) == 0.0


def test_recall_all_respects_k():
    # gold at position beyond k -> 0
    assert recall_all(["x", "y", "a"], {"a"}, k=2) == 0.0
    assert recall_all(["x", "y", "a"], {"a"}, k=3) == 1.0


def test_ndcg_perfect_ranking():
    # both gold first -> ndcg 1.0
    assert ndcg(["a", "b", "x"], {"a", "b"}, k=2) == 1.0


def test_ndcg_worst_ranking():
    # gold at the very end of top-k -> low but > 0
    val = ndcg(["x", "y", "a"], {"a"}, k=3)
    assert 0.0 < val < 1.0


def test_build_metric_dict_schema():
    turns = ["sess_1_0", "sess_2_0", "x_0"] * 20  # enough for @50
    d = build_metric_dict(
        recalled_turn_doc_ids=turns,
        gold_turn_doc_ids={"sess_1_0"},
        gold_session_ids={"sess_1"},
        recalled_session_ids_ordered=["sess_1", "sess_2", "x"],
    )
    assert set(d.keys()) == {"session", "turn"}
    assert set(d["session"]) == {"recall_all@5", "ndcg_any@5", "recall_all@10", "ndcg_any@10"}
    assert set(d["turn"]) == {"recall_all@5", "ndcg_any@5", "recall_all@10",
                              "ndcg_any@10", "recall_all@50", "ndcg_any@50"}
    # sess_1 is gold and ranked first -> recall_all@5 == 1.0
    assert d["session"]["recall_all@5"] == 1.0
