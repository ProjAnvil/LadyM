# VENDORED from https://github.com/xiaowu0162/LongMemEval @ 9e0b455f4ef0e2ab8f2e582289761153549043fc
# Copied 2026-08-02 — pinned for reproducible scoring. Do NOT edit except to update pin.
# Upstream license: MIT. Original paths:
#   evaluate_qa.py, print_qa_metrics.py, print_retrieval_metrics.py -> src/evaluation/
#   eval_utils.py -> src/retrieval/
import sys
import json
import numpy as np


# NOTE (vendoring edit, 2026-08-02): wrapped the upstream CLI body in
# `if __name__ == "__main__":` so the module is importable without argv
# side effects (matches the pattern evaluate_qa.py already uses upstream).
if __name__ == '__main__':
    if len(sys.argv) != 2:
        print('Usage: python print_retrieval_metrics.py in_file')
        exit()

    in_file = sys.argv[1]
    in_data = [json.loads(line) for line in open(in_file).readlines()]
    in_data = [x for x in in_data if '_abs' not in x['question_id']]

    task2type = {
        'single_hop': 'single_needle',
        'assistant_previnfo': 'single_needle',
        'two_hop': 'multi_session_synthesis',
        'multi_session_synthesis': 'multi_session_synthesis',
        'knowledge_update': 'knowledge_update',
        'temp_reasoning_explicit': 'temporal_reasoning',
        'temp_reasoning_implicit': 'temporal_reasoning',
        'implicit_preference_v2': 'implicit_preference_v2'
    }
    type2acc = {t: [] for t in set(list(task2type.values()))}

    all_metrics = []
    for entry in in_data:
        all_metrics.append(entry['retrieval_results']['metrics'])

    sess_metric_names = ['recall_all@5', 'ndcg_any@5', 'recall_all@10', 'ndcg_any@10']
    print('Session-level metrics:')
    try:
        print(', '.join(['\t{} = {}'.format(name, round(np.mean([x['session'][name] for x in all_metrics]), 4)) for name in sess_metric_names]))
    except:
        pass

    turn_metric_names = ['recall_all@5', 'ndcg_any@5', 'recall_all@10', 'ndcg_any@10', 'recall_all@50', 'ndcg_any@50']
    print('Turn-level metrics:')
    try:
        print(', '.join(['\t{} = {}'.format(name, round(np.mean([x['turn'][name] for x in all_metrics]), 4)) for name in turn_metric_names]))
    except:
        pass

