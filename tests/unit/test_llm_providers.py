"""Tests for ladym.providers.llm — LLMProvider ABC + FakeLLMProvider."""

from pydantic import BaseModel

from ladym.providers.llm import FakeLLMProvider, LLMProvider, Message


class Decision(BaseModel):
    action: str
    content: str | None = None


def test_fake_complete_returns_scripted_text():
    p = FakeLLMProvider(complete_fn=lambda msgs: "pong")
    assert p.complete([Message(role="user", content="ping")]) == "pong"


def test_fake_structured_returns_dict():
    p = FakeLLMProvider(
        structured_fn=lambda msgs, schema: {"action": "ADD", "content": None}
    )
    out = p.complete_structured([Message(role="user", content="x")], Decision)
    assert out == {"action": "ADD", "content": None}


def test_fake_raises_when_no_script():
    import pytest

    p = FakeLLMProvider()
    with pytest.raises(NotImplementedError):
        p.complete([Message(role="user", content="x")])


def test_llmprovider_is_abstract():
    import pytest

    with pytest.raises(TypeError):
        LLMProvider()  # type: ignore[abstract]


def test_make_llm_provider_none_returns_none():
    from ladym.providers.llm import make_llm_provider
    assert make_llm_provider(provider="none", base_url="", model="",
                             api_key="", structured_method="function_calling") is None


def test_langchain_provider_built_when_importable():
    import pytest

    from ladym.providers.llm import LangChainLLMProvider, make_llm_provider
    pytest.importorskip("langchain_openai")
    p = make_llm_provider(provider="openai", base_url="https://example.test/v1",
                          model="m", api_key="k", structured_method="function_calling",
                          max_tokens=8, temperature=0.0, timeout_s=5)
    assert isinstance(p, LangChainLLMProvider)
