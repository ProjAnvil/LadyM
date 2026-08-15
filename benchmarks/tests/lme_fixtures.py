"""Synthetic LongMemEval-shaped instance for offline unit tests.

Matches the real schema exactly so ingest/metrics code is identical to production.
Two sessions; session 2 has a knowledge-update (supersedes session 1's value).
Gold: answer_session_ids=['sess_1']; evidence turn marked has_answer.
"""
import copy


def make_mini_instance() -> dict:
    return {
        "question_id": "mini_1",
        "question_type": "single-session-user",
        "question": "What is Alice's favorite color?",
        "answer": "blue",
        "question_date": "2024-02-02",
        "haystack_session_ids": ["sess_1", "sess_2"],
        "haystack_dates": ["2024-01-01", "2024-02-01"],
        "haystack_sessions": [
            [  # sess_1
                {"role": "user", "content": "I love the color blue.", "has_answer": True},
                {"role": "assistant", "content": "Noted, blue it is."},
            ],
            [  # sess_2 (knowledge update: now green)
                {"role": "user", "content": "Actually I changed my mind, green is nicer."},
                {"role": "assistant", "content": "Got it, green now."},
            ],
        ],
        "answer_session_ids": ["sess_1"],
    }


def make_mini_dataset() -> list[dict]:
    """Two-instance list incl. an abstention question."""
    base = make_mini_instance()
    abstention = copy.deepcopy(base)
    abstention["question_id"] = "mini_2_abs"
    abstention["question"] = "What is Alice's shoe size?"
    abstention["answer"] = "The question is unanswerable."
    abstention["answer_session_ids"] = []
    abstention["question_type"] = "single-session-user"
    return [base, abstention]
