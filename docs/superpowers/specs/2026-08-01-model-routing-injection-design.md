# ModelRouting:注入宿主 langchain 模型(LLM + Embedding)— 设计

- **日期**: 2026-08-01
- **状态**: 草案,待 review
- **相关**: 无直接前置;改的是 `Engine` 构造器与 LLM/embedding provider 解析缝。复用既有 `LangChainLLMProvider`(`src/ladym/providers/llm.py`)与 `NAMED_OPS`(`src/ladym/providers/agents.py:19`)。与 `[[codeindex-extra]]` 特性独立,从 `main` 分出。

## 背景与动机

当 ladyM 作为依赖**内嵌进宿主的 langchain/langgraph 程序**时,宿主已经构造好了 langchain 的 `ChatOpenAI`(带 api_key / base_url / model)甚至 `OpenAIEmbeddings`。但 ladyM 现在逼宿主**在 Config / secret-store / env 里再声明一遍同样的 key 和端点**,然后内部用 `make_llm_provider` **重建**一个等价模型。

当前现状(Read / grep 确认):

1. **Engine 无任何模型注入点** —— `Engine.__init__(self, config=None, *, config_obj=None)` 只吃 `Config`。
2. **LLM 懒构建,两处调用 `make_agent`**:`engine.py:114`(`"consolidate"`)和 `engine.py:171`(`_get_agent(op)`)。`make_agent(config, op)` → `AgentRegistry(cfg).get(op)` → 从 Config 字段(`provider`/`base_url`/`model`/`api_key`)→ `make_llm_provider` 重建 langchain `ChatOpenAI`/`ChatAnthropic`/`ChatOllama` → 包进 `LangChainLLMProvider(cm)`。
3. **桥已存在但没入口** —— `LangChainLLMProvider(chat_model)` 接受任意 langchain `BaseChatModel`,只需一个"把宿主 model 喂进去"的入口。
4. **Embedding 无桥** —— 存储层说 ladyM 自己的 `EmbeddingProvider`(`embed()`/`embed_batch()`),与 langchain `Embeddings`(`embed_query()`/`embed_documents()`)方法名不同,缺一个适配类。
5. **宿主要 per-op 不同模型** —— ladyM 有 5 个认知操作(`NAMED_OPS`),宿主希望给它们分别指定不同模型(如 gate 用便宜的、consolidate 用强的),且**不想用纯字符串 key 的 dict**(易拼错、无补全、难维护)。

### 解决思路

提供一个**类型化的 `ModelRouting` dataclass**:每个认知操作是一个命名字段(LLM),加一个 embedding 字段。宿主构造时把已有的 langchain 模型填进去,Engine 内部自动包装、绕过 Config 重建。未填的字段回退到 Config / heuristic。

## 设计

### 总览

```python
from ladym import Engine, Config, ModelRouting
from langchain_openai import ChatOpenAI, OpenAIEmbeddings

eng = Engine(
    Config(db_path="mem.db"),
    models=ModelRouting(
        consolidate=ChatOpenAI(model="gpt-4o", api_key=sk, base_url=url),
        attention_gate=ChatOpenAI(model="gpt-4o-mini", api_key=sk, base_url=url),
        embedding=OpenAIEmbeddings(model="text-embedding-3-small", api_key=sk, base_url=url),
    ),
)
# proceduralize / l6 未填 → 回退 Config;provider/endpoint/key 零重复声明
```

### Part A —— `ModelRouting` dataclass

新建 `src/ladym/routing.py`(顶层模块,匹配其用户可见的公开 API 角色),导出:

```python
from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from langchain_core.language_models import BaseChatModel
    from langchain_core.embeddings import Embeddings

@dataclass
class ModelRouting:
    """注入宿主已有的 langchain 模型,绕过 ladyM 自己的 LLM/embedding 配置。

    未设置的字段 → 回退到 Config / heuristic。字段名与 ``NAMED_OPS`` 一一对应。
    """
    consolidate:       "BaseChatModel | None" = None
    attention_gate:    "BaseChatModel | None" = None
    proceduralize:     "BaseChatModel | None" = None
    l5_mental_model:   "BaseChatModel | None" = None
    l6_forward_intent: "BaseChatModel | None" = None
    embedding:         "Embeddings | None" = None
```

- 字段名 = `NAMED_OPS` 字符串(`"consolidate"` 等),`_make_agent` 用 `getattr(routing, op, None)` 取值 —— **类型安全,字段名即 op 名,无游离 string key**。
- langchain 类型只在 `TYPE_CHECKING` 下引用(字符串注解),避免 ladyM 核心硬依赖 langchain —— 与现有 `LangChainLLMProvider` 的 import 策略一致。

在 `ladym/__init__.py` 导出 `ModelRouting`。

### Part B —— LLM 注入缝

Engine 构造器加 keyword-only `models` 参数:

```python
def __init__(self, config=None, *, config_obj=None, models: ModelRouting | None = None):
    ...
    self._routing = models or ModelRouting()
```

新增私有 `_make_agent(op)`,两个调用点(`:114`、`:171`)改调它:

```python
def _make_agent(self, op):
    """Build the LLM provider for one op — injected model wins over config."""
    model = getattr(self._routing, op, None)
    if model is not None:
        from .providers.llm import LangChainLLMProvider
        return LangChainLLMProvider(model)        # 桥已存在,一行包好
    return make_agent(self.config, op)             # 未注入 → 走老路
```

- `engine.py:114` `make_agent(self.config, "consolidate")` → `self._make_agent("consolidate")`
- `engine.py:171` `make_agent(self.config, op)` → `self._make_agent(op)`

注入路径用 `structured_method` 默认值 `"function_calling"`(OpenAI/Anthropic/Ollama 通用)。ImportError 处理不动 —— 注入路径不抛(宿主既给了 langchain model,langchain 必已装),config 路径的 try/except 原样保留。

### Part C —— Embedding 注入 + 适配器

**新增 `LangChainEmbeddingAdapter`**(放 `src/ladym/storage/embeddings.py`,与 `EmbeddingProvider`/`make_provider` 同居),类比 `LangChainLLMProvider`:

```python
class LangChainEmbeddingAdapter(EmbeddingProvider):
    """Bridge a langchain ``Embeddings`` into ladyM's ``EmbeddingProvider``."""

    def __init__(self, embeddings):
        self._lc = embeddings
        self.dim: int | None = None        # _ensure_provider_dim() 会探测

    def embed(self, text: str) -> list[float]:
        vec = self._lc.embed_query(text)
        if self.dim is None:
            self.dim = len(vec)            # 同 OllamaEmbedding 模式
        return vec

    def embed_batch(self, texts):
        return self._lc.embed_documents(texts)

    def health_check(self):
        try:
            v = self.embed("dimensionality probe")
            return True, f"ok dim={len(v)}"
        except Exception as e:
            return False, f"{type(e).__name__}: {e}"
```

**Engine 构造器注入点**(`__init__` 里 `self.provider = make_provider(cfg)` 处):

```python
if self._routing.embedding is not None:
    from .storage.embeddings import LangChainEmbeddingAdapter
    self.provider = LangChainEmbeddingAdapter(self._routing.embedding)
else:
    self.provider = make_provider(cfg)
# 后续 _ensure_provider_dim() / _enforce_embedding_dim() 原样跑 —— adapter 的
# dim=None 会被既有探测流程兜住(同 OllamaEmbedding)。
```

### Part D —— op 名单暴露

`NAMED_OPS` 已在 `providers/agents.py:19` 定义。在 `ladym/__init__.py` 重新导出,供宿主引用(也可直接用 `ModelRouting` 字段名):

```python
from ladym import NAMED_OPS
# == ("consolidate", "proceduralize", "attention_gate", "l5_mental_model", "l6_forward_intent")
```

## 范围与非目标

- ✅ **本次做**:`ModelRouting` dataclass;Engine `models=` 参数;LLM per-op 注入(`_make_agent`);`LangChainEmbeddingAdapter` + embedding 注入;`NAMED_OPS`/`ModelRouting` 导出;测试;README/docstring 文档。
- ⏸️ **非目标**:
  - per-op `structured_method` 自定义 —— 注入路径固定 `"function_calling"`(覆盖 99% 场景);需要 json_mode 再放开。
  - 每次 API 调用时动态换 model(方案②)—— 本次只做构造时一次性绑定。
  - ladyM 存储层改用 langchain `Embeddings` 替代自有 `EmbeddingProvider` —— 不做(Adapter 保核心 langchain-free)。

## 验证标准

1. 注入 `ModelRouting` → `eng._make_agent("consolidate")._cm is injected_model`(包的是注入对象,非 config 重建)。
2. 注入单 embedding → `eng.provider` 是 `LangChainEmbeddingAdapter`,`eng.provider.embed("x")` 返回 langchain embedding 的向量,dim 自动探测。
3. 不传 `models=` → 行为与改动前完全一致(LLM 走 `make_agent(config, op)`,embedding 走 `make_provider(config)`)—— 向后兼容。
4. `ModelRouting` 只填部分字段 → 未填的 op 回退 Config / heuristic。
5. 全量 `pytest` 通过(含既有 engine / embedding / agent 测试无回归)。
