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
    if len(sys.argv) != 3:
        print('Usage: python print_qa_metrics.py in_file ref_file')
        exit()

    in_file = sys.argv[1]
    ref_file = sys.argv[2]
    in_data = [json.loads(line) for line in open(in_file).readlines()]
    ref_data = json.load(open(ref_file))
    ref_data = {x['question_id']: x for x in ref_data}

    all_acc, task_acc, abstention_acc = [], [], []
    type2acc = {t: [] for t in ['single-session-user', 'single-session-preference', 'single-session-assistant', 'multi-session', 'temporal-reasoning', 'knowledge-update']}
    for entry in in_data:
        ref_entry = ref_data[entry['question_id']]
        assert entry['autoeval_label']['model'] == 'gpt-4o-2024-08-06'
        type2acc[ref_entry['question_type']].append(1 if entry['autoeval_label']['label'] else 0)
        if '_abs' in entry['question_id']:
            abstention_acc.append(1 if entry['autoeval_label']['label'] else 0)

    print('\nEvaluation results by task:')
    for k, v in type2acc.items():
        print('\t{}: {} ({})'.format(k, round(np.mean(v), 4), len(v)))
        all_acc += v
        task_acc.append(np.mean(v))

    print('\nTask-averaged Accuracy:', round(np.mean(task_acc), 4))
    print('Overall Accuracy:', round(np.mean(all_acc), 4))
    print('Abstention Accuracy:', round(np.mean(abstention_acc), 4), f'({len(abstention_acc)})')
