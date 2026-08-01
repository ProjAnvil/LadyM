# ModelRouting(LLM + Embedding 注入)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让宿主 langchain/langgraph 程序把已构造好的 `BaseChatModel`(per-op)和 `Embeddings` 注入 Engine,绕过 ladyM 自己的 Config 凭据重建。

**Architecture:** 新建 `src/ladym/adapter.py` 统一三个 langchain→ladyM 桥接件(搬来的 `LangChainLLMProvider`+`_to_lc`、新增 `LangChainEmbeddingAdapter`、新增 `ModelRouting` dataclass)。Engine 加 `models=` 参数,`_make_agent(op)` 优先用注入模型,embedding 在 `__init__` 注入点接管 `self.provider`。循环 import(adapter↔providers.llm)用函数内懒 import 打断。

**Tech Stack:** Python ≥3.11、langchain_core(`BaseChatModel`/`Embeddings`,仅 TYPE_CHECKING 引用)、pydantic、pytest。

## Global Constraints

- **新模块**:`src/ladym/adapter.py`——`LangChainLLMProvider` + `_to_lc`(从 `providers/llm.py` 搬来)、`LangChainEmbeddingAdapter`(新)、`ModelRouting`(新)三件同居。
- **循环 import**:adapter 顶层 import `LLMProvider`(from `providers.llm`)和 `EmbeddingProvider`(from `storage.embeddings`);反向引用(`make_llm_provider`、`_make_agent`)用**函数体内懒 import**。
- **op 字段名**:`ModelRouting` 字段名 = `NAMED_OPS` 字符串(`consolidate`/`proceduralize`/`attention_gate`/`l5_mental_model`/`l6_forward_intent`),`_make_agent` 用 `getattr(routing, op, None)` 取值。
- **structured_method**:注入路径固定 `"function_calling"`(LangChainLLMProvider 默认值)。
- **向后兼容**:`providers/__init__.py` 继续 re-export `LangChainLLMProvider`(改从 adapter 取),保 `from ladym.providers import LangChainLLMProvider` 不破。
- **langchain 类型只在 `TYPE_CHECKING` 下引用**(字符串注解),避免核心硬依赖 langchain。
- **搬动影响面 4 处**:`providers/llm.py:71-104`(搬走 `_to_lc`+`LangChainLLMProvider`)、`providers/llm.py:132`(懒 import)、`providers/__init__.py:10`(re-export 改源)、`tests/unit/test_llm_providers.py:50`(import 拆分)。

**已在分支**:`feat/model-routing-injection`(rebase 到含 codeindex 的 main)。

---

### Task 1: 新建 `adapter.py` + 搬迁 `LangChainLLMProvider`(纯重构)

**Files:**
- Create: `src/ladym/adapter.py`
- Modify: `src/ladym/providers/llm.py:71-104`(删除 `_to_lc` + `LangChainLLMProvider`)、`:132`(`make_llm_provider` 懒 import)
- Modify: `src/ladym/providers/__init__.py:10`(re-export 改从 adapter 取)
- Modify: `tests/unit/test_llm_providers.py:50`(import 拆分)
- Test: 既有 `tests/unit/test_llm_providers.py`(验证搬迁后仍通过)+ 循环 import 守卫

**Interfaces:**
- Produces: `ladym.adapter.LangChainLLMProvider`(从 providers.llm 搬来,签名不变:`__init__(self, chat_model, structured_method="function_calling")`,属性 `_cm`/`_sm`)。

- [ ] **Step 1: 创建 `src/ladym/adapter.py`**

```python
"""Langchain → ladyM bridge layer.

Houses the adapters that wrap host-owned langchain objects (BaseChatModel,
Embeddings) into ladyM's own provider abstractions, plus ``ModelRouting`` —
the typed injection config. langchain types appear only under TYPE_CHECKING
so importing this module needs no langchain at runtime.
"""

from __future__ import annotations

from typing import TYPE_CHECKING

from .providers.llm import LLMProvider
from .storage.embeddings import EmbeddingProvider

if TYPE_CHECKING:
    from langchain_core.embeddings import Embeddings
    from langchain_core.language_models import BaseChatModel


def _to_lc(msg):
    """Convert a ladyM Message dict to a langchain message (moved from providers.llm)."""
    from langchain_core.messages import AIMessage, HumanMessage, SystemMessage

    if msg["role"] == "system":
        return SystemMessage(content=msg["content"])
    if msg["role"] == "assistant":
        return AIMessage(content=msg["content"])
    return HumanMessage(content=msg["content"])


class LangChainLLMProvider(LLMProvider):
    """Wrap a langchain ``BaseChatModel`` as a ladyM ``LLMProvider`` (moved from providers.llm)."""

    name = "langchain"

    def __init__(self, chat_model, structured_method: str = "function_calling"):
        self._cm = chat_model
        self._sm = structured_method

    def complete(self, messages, **params):
        return self._cm.invoke([_to_lc(m) for m in messages]).content

    def complete_structured(self, messages, schema, **params):
        runner = self._cm.with_structured_output(schema, method=self._sm)
        out = runner.invoke([_to_lc(m) for m in messages])
        return (
            out
            if isinstance(out, dict)
            else (out.model_dump() if hasattr(out, "model_dump") else dict(out))
        )
```

- [ ] **Step 2: 从 `providers/llm.py` 删除已搬走的代码**

删除 `src/ladym/providers/llm.py` 的第 71-104 行(整个 `_to_lc` 函数 + `LangChainLLMProvider` 类)。`Message`、`LLMProvider`、`FakeLLMProvider`、`make_llm_provider` **保留原处**。

- [ ] **Step 3: `make_llm_provider` 改懒 import**

`src/ladym/providers/llm.py:132` 的 `return LangChainLLMProvider(cm, structured_method)` 改为:

```python
    from ..adapter import LangChainLLMProvider  # lazy: breaks adapter↔providers.llm cycle
    return LangChainLLMProvider(cm, structured_method)
```

- [ ] **Step 4: `providers/__init__.py` re-export 改源**

`src/ladym/providers/__init__.py:10` 把 `LangChainLLMProvider` 从 `.llm` 的 import 块里移除,改为单独从 adapter 取(保向后兼容):

```python
from .agents import NAMED_OPS, AgentConfig, AgentRegistry, make_agent
from ..adapter import LangChainLLMProvider
from .llm import (
    FakeLLMProvider,
    LLMProvider,
    Message,
    make_llm_provider,
)
```
（`__all__` 列表里 `LangChainLLMProvider` 保留不变。）

- [ ] **Step 5: 更新测试 import**

`tests/unit/test_llm_providers.py:50` 拆分 import(LangChainLLMProvider 现在在 adapter):

```python
    from ladym.adapter import LangChainLLMProvider
    from ladym.providers.llm import make_llm_provider
```

- [ ] **Step 6: 跑既有 LLM provider 测试 + 循环 import 守卫**

Run: `uv run pytest tests/unit/test_llm_providers.py -v`
Expected: 全 PASS(行为不变,只是搬迁)。

再验证无循环 import:
Run: `uv run python -c "import ladym; from ladym.adapter import LangChainLLMProvider; from ladym.providers.llm import make_llm_provider; print('no cycle')"`
Expected: 打印 `no cycle`,无 ImportError。

- [ ] **Step 7: 跑全量测试确认无回归**

Run: `uv run pytest -q`
Expected: 290 passed(与搬迁前一致)。

- [ ] **Step 8: 提交**

```bash
git add src/ladym/adapter.py src/ladym/providers/llm.py src/ladym/providers/__init__.py tests/unit/test_llm_providers.py
git commit -m "$(cat <<'EOF'
refactor: move LangChainLLMProvider into new adapter module

Create src/ladym/adapter.py as the langchain→ladyM bridge layer. Move
LangChainLLMProvider + its _to_lc helper out of providers/llm.py.
Circular import (adapter↔providers.llm) broken via lazy in-function
import in make_llm_provider. providers/__init__ re-exports from the new
location for back-compat. Pure refactor — no behavior change.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `LangChainEmbeddingAdapter` + `ModelRouting`

**Files:**
- Modify: `src/ladym/adapter.py`(追加两个类)
- Test: `tests/unit/test_adapter.py`(新建)

**Interfaces:**
- Consumes: Task 1 的 `adapter.py` 模块、`EmbeddingProvider` 基类(`storage.embeddings`)。
- Produces: `LangChainEmbeddingAdapter(embeddings)` —— `__init__(self, embeddings)`,属性 `_lc`/`dim`(初始 None,首次 embed 探测);`embed(text)`/`embed_batch(texts)` 委托 langchain `Embeddings`。`ModelRouting` dataclass —— 6 个可选字段(5 个 op + embedding),默认 None。

- [ ] **Step 1: 写失败测试** —— 新建 `tests/unit/test_adapter.py`

```python
"""Tests for the langchain adapter classes + ModelRouting."""

from ladym.adapter import LangChainEmbeddingAdapter, ModelRouting


class FakeEmbeddings:
    """Duck-typed stand-in for langchain Embeddings — no langchain needed."""

    def embed_query(self, text: str) -> list[float]:
        return [1.0, 2.0, 3.0]

    def embed_documents(self, texts: list[str]) -> list[list[float]]:
        return [self.embed_query(t) for t in texts]


def test_embedding_adapter_embed_and_dim_probe():
    adapter = LangChainEmbeddingAdapter(FakeEmbeddings())
    assert adapter.dim is None
    vec = adapter.embed("hello")
    assert vec == [1.0, 2.0, 3.0]
    assert adapter.dim == 3  # probed on first embed


def test_embedding_adapter_embed_batch():
    adapter = LangChainEmbeddingAdapter(FakeEmbeddings())
    adapter.embed("warm")  # set dim
    out = adapter.embed_batch(["a", "b"])
    assert out == [[1.0, 2.0, 3.0], [1.0, 2.0, 3.0]]


def test_embedding_adapter_health_check():
    adapter = LangChainEmbeddingAdapter(FakeEmbeddings())
    ok, msg = adapter.health_check()
    assert ok is True
    assert "dim=3" in msg


def test_model_routing_defaults_none():
    r = ModelRouting()
    assert r.consolidate is None
    assert r.attention_gate is None
    assert r.proceduralize is None
    assert r.l5_mental_model is None
    assert r.l6_forward_intent is None
    assert r.embedding is None


def test_model_routing_fields_match_named_ops():
    """Field names must equal NAMED_OPS strings (getattr(op) resolves)."""
    from ladym.providers.agents import NAMED_OPS

    fields = {"consolidate", "proceduralize", "attention_gate",
              "l5_mental_model", "l6_forward_intent"}
    assert set(NAMED_OPS) == fields
```

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/unit/test_adapter.py -v`
Expected: FAIL —— `ImportError: cannot import name 'LangChainEmbeddingAdapter'` / `'ModelRouting'`(还不存在)。

- [ ] **Step 3: 追加两个类到 `src/ladym/adapter.py`**

在文件末尾(Task 1 的 `LangChainLLMProvider` 之后)追加:

```python
class LangChainEmbeddingAdapter(EmbeddingProvider):
    """Bridge a langchain ``Embeddings`` into ladyM's ``EmbeddingProvider``.

    ``dim`` starts ``None`` and is probed on the first :meth:`embed` call —
    same pattern as ``OllamaEmbedding``, so Engine's ``_ensure_provider_dim``
    handles it without special-casing.
    """

    def __init__(self, embeddings: "Embeddings"):
        self._lc = embeddings
        self.dim: int | None = None

    def embed(self, text: str) -> list[float]:
        vec = self._lc.embed_query(text)
        if self.dim is None:
            self.dim = len(vec)
        return vec

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        return self._lc.embed_documents(texts)


from dataclasses import dataclass


@dataclass
class ModelRouting:
    """Inject host-owned langchain models, bypassing ladyM's own LLM/embedding config.

    Unset fields fall back to Config / heuristic. Field names mirror ``NAMED_OPS``
    so ``getattr(routing, op, None)`` resolves each cognitive operation.
    """

    consolidate: "BaseChatModel | None" = None
    proceduralize: "BaseChatModel | None" = None
    attention_gate: "BaseChatModel | None" = None
    l5_mental_model: "BaseChatModel | None" = None
    l6_forward_intent: "BaseChatModel | None" = None
    embedding: "Embeddings | None" = None
```

（`from dataclasses import dataclass` 放在文件顶部 import 区更整洁——实施时挪上去。）

- [ ] **Step 4: 跑测试确认通过**

Run: `uv run pytest tests/unit/test_adapter.py -v`
Expected: 5 PASS。

- [ ] **Step 5: 提交**

```bash
git add src/ladym/adapter.py tests/unit/test_adapter.py
git commit -m "$(cat <<'EOF'
feat(adapter): add LangChainEmbeddingAdapter + ModelRouting

LangChainEmbeddingAdapter bridges langchain Embeddings into ladyM's
EmbeddingProvider (embed_query/embed_documents → embed/embed_batch),
probing dim on first call. ModelRouting is the typed per-op injection
config (5 LLM op fields + 1 embedding field, all default None).

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Engine 注入缝(`models=` + `_make_agent` + embedding 接管)

**Files:**
- Modify: `src/ladym/engine.py`(`__init__` 加 `models` 参数 + embedding 注入点;新增 `_make_agent`;改 `:114`/`:171` 两处调用点)
- Test: `tests/unit/test_engine_injection.py`(新建)

**Interfaces:**
- Consumes: Task 2 的 `ModelRouting`(`adapter.py`)、`LangChainLLMProvider`/`LangChainEmbeddingAdapter`(`adapter.py`)、`make_agent`(`providers`)、`make_provider`(`storage.embeddings`)。
- Produces: `Engine(config, *, models=ModelRouting(...))` —— 注入的 LLM per-op 经 `_make_agent(op)` 优先于 config;注入的 embedding 接管 `self.provider`。不传 `models` → 完全走老路(向后兼容)。

- [ ] **Step 1: 写失败测试** —— 新建 `tests/unit/test_engine_injection.py`

```python
"""Tests for Engine's ModelRouting injection (LLM per-op + embedding)."""

from ladym.adapter import LangChainEmbeddingAdapter, LangChainLLMProvider, ModelRouting
from ladym.config import Config
from ladym.engine import Engine
from ladym.storage.embeddings import HashingEmbedding


class FakeEmbeddings:
    def embed_query(self, text):
        return [0.5, 0.5]

    def embed_documents(self, texts):
        return [self.embed_query(t) for t in texts]


def test_injected_llm_wrapped_not_rebuilt(tmp_path):
    """_make_agent wraps the injected model instead of rebuilding from config."""
    sentinel = object()  # stands in for a BaseChatModel
    eng = Engine(Config.for_testing(tmp_path), models=ModelRouting(consolidate=sentinel))
    try:
        provider = eng._make_agent("consolidate")
        assert isinstance(provider, LangChainLLMProvider)
        assert provider._cm is sentinel  # the injected object, not a config rebuild
    finally:
        eng.close()


def test_injected_embedding_takes_over_provider(tmp_path):
    eng = Engine(Config.for_testing(tmp_path), models=ModelRouting(embedding=FakeEmbeddings()))
    try:
        assert isinstance(eng.provider, LangChainEmbeddingAdapter)
        assert eng.provider.dim == 2  # probed
        assert eng.provider.embed("x") == [0.5, 0.5]
    finally:
        eng.close()


def test_no_injection_falls_back_to_config(tmp_path):
    """Without models=, Engine uses config-driven providers (back-compat)."""
    eng = Engine(Config.for_testing(tmp_path))
    try:
        assert isinstance(eng.provider, HashingEmbedding)  # default offline embedding
        assert eng._make_agent("consolidate") is None  # config has no LLM → None
    finally:
        eng.close()


def test_uninjected_op_falls_back(tmp_path):
    """An op not set in ModelRouting falls back to make_agent(config, op)."""
    sentinel = object()
    eng = Engine(Config.for_testing(tmp_path), models=ModelRouting(consolidate=sentinel))
    try:
        # consolidate is injected; proceduralize is not → config path → None
        assert eng._make_agent("consolidate")._cm is sentinel
        assert eng._make_agent("proceduralize") is None
    finally:
        eng.close()
```

- [ ] **Step 2: 跑测试确认失败**

Run: `uv run pytest tests/unit/test_engine_injection.py -v`
Expected: FAIL —— `Engine.__init__() got an unexpected keyword argument 'models'`。

- [ ] **Step 3: Engine `__init__` 加 `models` 参数 + embedding 注入点**

`src/ladym/engine.py` 的 `__init__` 签名改为:

```python
def __init__(self, config: Config | None = None, *, config_obj: Config | None = None,
             models=None):
```

在方法体开头(`cfg = config or config_obj or Config()` 之后)加:

```python
    from .adapter import ModelRouting
    self._routing: ModelRouting = models if isinstance(models, ModelRouting) else ModelRouting()
```

把 `self.provider: EmbeddingProvider = make_provider(cfg)` 那行改为:

```python
    if self._routing.embedding is not None:
        from .adapter import LangChainEmbeddingAdapter
        self.provider: EmbeddingProvider = LangChainEmbeddingAdapter(self._routing.embedding)
    else:
        self.provider: EmbeddingProvider = make_provider(cfg)
```

（后续 `self._ensure_provider_dim()` / `self._enforce_embedding_dim()` 原样不动 —— adapter 的 dim=None 会被既有探测流程兜住。）

- [ ] **Step 4: 新增 `_make_agent` + 改两处调用点**

在 Engine 里(`_get_agent` 附近)新增:

```python
def _make_agent(self, op: str):
    """Build the LLM provider for one op — injected model wins over config."""
    model = getattr(self._routing, op, None)
    if model is not None:
        from .adapter import LangChainLLMProvider
        return LangChainLLMProvider(model)
    return make_agent(self.config, op)
```

改两处调用点:
- `engine.py:114`(在 `attach_llm_classifier` 里)`provider = make_agent(self.config, "consolidate")` → `provider = self._make_agent("consolidate")`
- `engine.py:171`(在 `_get_agent` 里)`agent = make_agent(self.config, op)` → `agent = self._make_agent(op)`

- [ ] **Step 5: 跑注入测试确认通过**

Run: `uv run pytest tests/unit/test_engine_injection.py -v`
Expected: 4 PASS。

- [ ] **Step 6: 跑全量测试确认无回归**

Run: `uv run pytest -q`
Expected: 全 PASS(既有 engine/agent/embedding 测试不受影响 —— 不传 models= 时完全走老路)。

- [ ] **Step 7: 提交**

```bash
git add src/ladym/engine.py tests/unit/test_engine_injection.py
git commit -m "$(cat <<'EOF'
feat(engine): inject host langchain models via ModelRouting

Engine accepts models=ModelRouting(...): per-op BaseChatModel fields are
wrapped by _make_agent (injected wins over config rebuild); an injected
Embeddings takes over self.provider via LangChainEmbeddingAdapter. Without
models=, behavior is unchanged (full back-compat).

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: 公开导出 + 文档

**Files:**
- Modify: `src/ladym/__init__.py`(导出 `ModelRouting`、`NAMED_OPS`)
- Modify: `src/ladym/engine.py`(`__init__` docstring 加 `models` 参数说明 + 示例)
- Modify: `README.md` + `README.zh-CN.md`(Integration 段加"注入宿主 langchain 模型")

**Interfaces:**
- 无新代码接口。产出:`from ladym import ModelRouting, NAMED_OPS` 可用;文档准确反映用法。

- [ ] **Step 1: `__init__.py` 导出**

`src/ladym/__init__.py` 加 import + `__all__` 条目:

```python
from .adapter import ModelRouting
from .providers.agents import NAMED_OPS
```
在 `__all__` 里加 `"ModelRouting"` 和 `"NAMED_OPS"`。

- [ ] **Step 2: Engine `__init__` docstring**

在 `Engine.__init__` 的 docstring(如有)或紧随签名处加参数说明:

```python
"""...existing docstring...

``models`` (:class:`~ladym.adapter.ModelRouting` | None): inject host-owned
langchain models — per-op ``BaseChatModel`` fields bypass Config's credential
rebuild, and ``embedding`` (a langchain ``Embeddings``) takes over the
embedding provider. Unset fields fall back to Config. Example::

    from ladym import Engine, Config, ModelRouting
    eng = Engine(Config(db_path="m.db"), models=ModelRouting(
        consolidate=my_chat_model, embedding=my_embeddings))
"""
```

- [ ] **Step 3: README Integration 段**

`README.md` 的 Integration 段(## Integration 之后)加一小节:

```markdown
### Injecting your own langchain models

If your app already configures langchain ``ChatOpenAI`` / ``OpenAIEmbeddings``
(with api_key, base_url, model), pass them straight to Engine via
``ModelRouting`` — no need to re-declare credentials in ladyM's config:

\`\`\`python
from ladym import Engine, Config, ModelRouting
from langchain_openai import ChatOpenAI, OpenAIEmbeddings

eng = Engine(Config(db_path="mem.db"), models=ModelRouting(
    consolidate=ChatOpenAI(model="gpt-4o", api_key=sk, base_url=url),
    attention_gate=ChatOpenAI(model="gpt-4o-mini", api_key=sk, base_url=url),
    embedding=OpenAIEmbeddings(model="text-embedding-3-small", api_key=sk),
))
\`\`\`

Each of the five cognitive ops (``consolidate``, ``proceduralize``,
``attention_gate``, ``l5_mental_model``, ``l6_forward_intent``) can take a
different model; unset ops fall back to Config.
```

`README.zh-CN.md` 做平行翻译(命令保持英文)。

- [ ] **Step 4: 验证导出可用**

Run: `uv run python -c "from ladym import Engine, Config, ModelRouting, NAMED_OPS; print(NAMED_OPS, ModelRouting())"`
Expected: 打印 op 元组 + 空 routing,无报错。

- [ ] **Step 5: 跑全量测试**

Run: `uv run pytest -q`
Expected: 全 PASS。

- [ ] **Step 6: 提交**

```bash
git add src/ladym/__init__.py src/ladym/engine.py README.md README.zh-CN.md
git commit -m "$(cat <<'EOF'
docs: export ModelRouting/NAMED_OPS + document model injection

ModelRouting and NAMED_OPS are now importable from ladym top-level.
Engine.__init__ docstring documents the models= parameter. Both READMEs
get an Integration subsection showing how to inject host langchain
ChatModel/Embeddings without re-declaring credentials.

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
)"
```

---

## 验收(对齐 spec 验证标准)

实现全部完成后手动核对:

1. 注入 `ModelRouting(consolidate=m)` → `eng._make_agent("consolidate")._cm is m`(包注入对象,非重建)。
2. 注入 `ModelRouting(embedding=emb)` → `eng.provider` 是 `LangChainEmbeddingAdapter`,dim 自动探测。
3. 不传 `models=` → 行为与改动前一致(LLM 走 `make_agent(config, op)`,embedding 走 `make_provider(config)`)。
4. `ModelRouting` 只填部分字段 → 未填的 op 回退。
5. `from ladym import ModelRouting, NAMED_OPS` 可用。
6. 全量 `pytest` 通过(含搬迁后的 test_llm_providers + 新增 test_adapter/test_engine_injection)。
