"""NFR-3 guard: the read path (recall.py) must not depend on any LLM provider."""

import pathlib

import ladym.operations.recall as recall_mod


def test_recall_imports_no_llm():
    src = pathlib.Path(recall_mod.__file__).read_text()
    assert "langchain" not in src
    assert "providers.llm" not in src
    assert "complete_structured" not in src
    assert "make_llm_provider" not in src
