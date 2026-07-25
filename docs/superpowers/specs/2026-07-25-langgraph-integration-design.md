# LangGraph 集成(Tools + Nodes 双路径)— 设计

- **日期**: 2026-07-25
- **状态**: 草案,待 review
- **相关**: 无直接前置(新功能);复用 `Engine` 公开 API(`src/ladym/engine.py`)与 `RecallResponse`/`Memory` schema(`src/ladym/schema.py`)。背景调研见会话记录(web search: LangGraph 2026 三种长期记忆集成模式)。

## 背景与动机

外部开发者希望把 ladyM 作为 LangGraph agent 的 memory layer。当前现状(grep `langgraph|BaseStore|@tool|StoreAdapter` 全仓库确认):

1. **ladyM 无任何 langgraph/langchain 集成代码** —— 一处 `@tool` / `BaseStore` 子类 / langgraph adapter 都没有。
2. LangChain 目前是**反向消费**:`[llm]` extra 里的 `LangChainLLMProvider`(`src/ladym/providers/llm.py:84`)把 langchain 的 `ChatOpenAI/ChatAnthropic/ChatOllama` 包成 ladyM **内部**的 `LLMProvider`。也就是 ladyM 用 langchain 跑模型,而非把自己暴露给 langchain/langgraph。
3. 现有对外出口只有 SDK / Engine / MCP / CLI / Web,缺 langgraph 这条路径。

2026 年 LangGraph 长期记忆有三种官方集成模式([Vectorize/Hindsight 2026-03](https://hindsight.vectorize.io/blog/2026/03/24/langgraph-longterm-memory)、[LangChain 官方](https://docs.langchain.com/oss/python/concepts/memory)):

| 模式 | 工作方式 | LLM 控制时机 | 自动运行 | 迁移成本 |
|------|---------|-------------|---------|---------|
| **Tools** | 写成 langchain `BaseTool`,LLM 自主调用 | ✅ | ❌ | 中 |
| **Nodes** | 图节点 `recall→agent→retain` 每轮跑 | ❌ | ✅ | 低 |
| BaseStore | 子类化 `BaseStore` 替换后端 | ❌ | ✅ | 最低 |

本设计提供 **Tools + Nodes 两条等价路径,让开发者按 agent 形态自选**,明确**不做 BaseStore 模式**(见非目标)。

## 目标

- 提供两条路径,行为一致(同一 Engine、同一 workspace 解析、同一 attention gate 语义)。
- 复用 ladyM 全部增值语义:7 层分层、attention gate(drop/rewrite/pass)、多策略召回(向量+BM25+图+时间衰减)、workspace 多租户隔离、`search_code` 代码检索。
- **零侵入 core**:`engine.py` / `schema.py` / 现有 extras 不改一行。
- 可选 extra:未安装 `[langgraph]` 的用户 `import ladym` 完全不受影响。
- 多用户:运行时通过 `RunnableConfig["configurable"]["user_id"]` 注入,映射到 ladyM `workspace`(与 Zep/Hindsight 等 langgraph 生态惯例一致)。

## 非目标(YAGNI 边界)

- **不做 BaseStore 模式**:`BaseStore` 只有 `namespace+key+value(dict)` 三件套,把 ladyM 塞进去会把 7 层分层 / attention gate / consolidate / supersedes 全部拍平成"带向量的 KV",降级使用,浪费大半架构。除非未来出现强烈的"复用现有 store 接口"约束,否则不做。
- 不替用户编译完整 graph —— 只给**可组装的零件**(tool 列表 / 节点函数),用户自己拼 `StateGraph`,保持灵活。
- 不内置 LLM 选择 —— 用户自己传 `llm`(langgraph 的 `create_react_agent` 或自定义 agent 节点),ladyM 只管 memory。
- 不加 LangSmith / tracing / LangGraph Platform 集成。
- 不做同步/异步双实现 —— MVP 只做同步(`invoke`);异步(`ainvoke`)留待后续(见风险)。

## 架构

遵循现有 `providers/`、`mcp/` 的范式(一个 `__init__.py` re-export + 子模块):

```
src/ladym/langgraph/
├── __init__.py      # 公开 API re-export
├── _runtime.py      # 共享:workspace 解析 + Engine 生命周期
├── tools.py         # 路径 A:create_ladym_tools(engine) -> list[BaseTool]
└── nodes.py         # 路径 B:create_recall_node / create_retain_node
```

### 1. 共享运行时(`_runtime.py`)

两个路径共用的逻辑集中在此,避免重复:

| 函数 | 行为 |
|------|------|
| `resolve_workspace(config, engine) -> str` | **路径 B 用**:从 `RunnableConfig["configurable"]["user_id"]` 取 workspace;缺省回退 `engine.config.workspace`。路径 A 不经过此处(工厂时绑定,见 §2) |
| `resolve_engine(engine_or_config_or_path) -> Engine` | 接受已构造的 `Engine`(原样返回)、`Config`(惰性建单例)、`str\|Path`(`Config(db_path=...)`)、`None`(用默认 `Config()`)。工厂函数用它在内部维护一个惰性单例 |

**懒加载约定**:本模块**不在 `ladym/__init__.py` 顶层 import `langgraph`/`langchain_core`**。所有 langgraph 相关 import 放在 `_runtime.py` / `tools.py` / `nodes.py` 的函数内部或模块顶部(子模块自身被 import 时才触发)。`ladym.langgraph.__init__` 也用惰性 import(函数包装)或直接 re-export 子模块符号——后者会在 `import ladym.langgraph` 时触发 langgraph import,这是可接受的(用户显式 import 子包即表示装了 extra)。

### 2. 路径 A — Tools(`tools.py`)

```python
def create_ladym_tools(
    engine: Engine | Config | str | Path | None = None,
    *,
    workspace: str | None = None,     # 工厂时绑定;None → engine.config.workspace
    default_top_k: int = 8,
) -> list[BaseTool]:
    """Return langchain tools wrapping ladyM recall/remember/search_code.
    ``workspace`` is fixed at factory time (NOT resolved per-request) — this avoids
    langchain 1.x tool-runtime injection complexity. For per-request multi-user
    isolation, use path B (nodes) instead."""
```

产出的 tool 列表:

| Tool | 签名 | 行为 | 返回格式 |
|------|------|------|---------|
| `recall_memory` | `(query: str, top_k: int = 8) -> str` | `eng.recall(query, workspace=<resolved>, top_k=top_k)` | 每条 `[layer\|type\|score] summary_or_content` 换行;无命中返回 `"(no hits)"` |
| `remember_fact` | `(content: str, tags: list[str] \| None = None) -> str` | `eng.remember(content, tags=tags, source="langgraph-tool")`,**attention gate 仍是最终裁决者**(可 drop/rewrite) | `stored id=... gate=pass\|dropped\|rewritten` |
| `search_code` | `(query: str, top_k: int = 8) -> str` | `eng.search_code(query, workspace=<resolved>, top_k=top_k)` | 每条 `summary :: qualified_name` 换行 |

实现用 `@tool` 装饰闭包函数(捕获 engine + workspace),docstring + 类型注解构成 tool schema。**workspace 在工厂调用时绑定**(默认 `engine.config.workspace`),tool 内部不再解析 runtime config —— 这避开了 langchain 1.x 里 tool 内访问运行时上下文的 API 变动(1.x 推荐 `ToolRuntime` + `context_schema`,会给用户 graph 加约束;旧 `config: RunnableConfig` 裸参数又有"暴露进 LLM tool schema"风险)。每个 tool 调用 `eng.recall/remember/search_code(query, workspace=<bound>, ...)`。**运行时动态多用户请走路径 B**。

**用法**:
```python
from ladym.langgraph import create_ladym_tools
from ladym import Engine, Config
from langgraph.prebuilt import create_react_agent

tools = create_ladym_tools(Engine(Config(db_path="ladym.db")))
agent = create_react_agent(llm, tools, checkpointer=checkpointer)
agent.invoke({"messages": [...]}, config={"configurable": {"user_id": "u-456"}})
```

也兼容纯 LangChain:`llm.bind_tools(tools)`。

### 3. 路径 B — Nodes(`nodes.py`)

```python
def create_recall_node(
    engine, *, top_k: int = 6, prefix: str = "Relevant long-term memory:",
) -> Callable[[dict, RunnableConfig], dict]:
    """Returns a graph node that recalls against the latest user message and
    prepends a SystemMessage to state['messages']."""

def create_retain_node(
    engine, *,
) -> Callable[[dict, RunnableConfig], dict]:
    """Returns a graph node that stores the last human+ai turn to long-term memory,
    subject to ladyM's attention gate."""
```

| 节点 | 行为 |
|------|------|
| **recall** | 取 `state["messages"][-1].content` 作 query;`eng.recall(query, workspace=<resolved>, top_k)`;有命中 → 拼 `SystemMessage(prefix + "\n" + 每条 summary)` prepend 到 `messages`;无命中 → 返回 `{}`(不改 state) |
| **retain** | 从 `state["messages"]` 反向找最后一条 human + 最后一条 ai;合并成 `"Q: {human}\nA: {ai}"` → `eng.remember(..., source="langgraph-node")`;attention gate 裁决(drop/rewrite/pass);返回 `{}` |

**用法**:
```python
from ladym.langgraph import create_recall_node, create_retain_node
from ladym import Engine, Config
from langgraph.graph import StateGraph, START, END, MessagesState

eng = Engine(Config(db_path="ladym.db"))
builder = StateGraph(MessagesState)
builder.add_node("recall", create_recall_node(eng))
builder.add_node("agent",  agent_node)            # 用户自己的 LLM 节点
builder.add_node("retain", create_retain_node(eng))
builder.add_edge(START, "recall")
builder.add_edge("recall", "agent")
builder.add_edge("agent", "retain")
builder.add_edge("retain", END)
graph = builder.compile(checkpointer=checkpointer)
```

### 数据流

**路径 A(Tools)**:用户消息 → LLM 自主决定调 `recall_memory` → 拿到记忆 → 生成回答 → LLM 自主决定调 `remember_fact` 存关键事实。LLM 全程控制时机。

**路径 B(Nodes)**:`START → recall`(自动注入相关记忆为 SystemMessage)→ `agent`(LLM 带记忆生成)→ `retain`(自动存本轮,attention gate 把关)→ `END`。每轮必有记忆上下文,LLM 不操心存取。

两条路径最终都落到**同一个 Engine / 同一个 workspace**,记忆互通。

## 关键设计决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| Engine 生命周期 | 工厂接收 `Engine`(推荐)为主;`Config`/`db_path`/`None` 为便捷重载(内部惰性单例) | 长跑进程复用连接;上手门槛低 |
| 多用户隔离(路径 B) | 节点 `config["configurable"]["user_id"]` → `workspace`;缺省回退 `engine.config.workspace` | langgraph 节点 `(state, config)` API 稳定;与 ladyM 多 workspace 一致 |
| workspace 绑定(路径 A) | 工厂调用时固定(默认 `engine.config.workspace`) | 避开 langchain 1.x `ToolRuntime`/`context_schema` 约束;动态多租户用路径 B |
| recall 注入方式 | 拼一条 `SystemMessage` prepend | langgraph nodes 模式标准做法 |
| retain 触发 | 取最后 human + 最后 ai 合并;attention gate 裁决 | 复用 ladyM 语义,不重复造判断 |
| Tools 粒度 | 3 个:`recall_memory`/`remember_fact`/`search_code`,暴露 `top_k`/`tags` | 覆盖读/写/代码,结果里显示 layer/type 让 LLM 感知分层 |
| workspace 不暴露为 tool 参数 | 路径 A 工厂绑定 / 路径 B 从 config 解析 | LLM 不该能决定读写谁的 workspace(安全) |
| 依赖 | 新增 `[langgraph]` extra:`langgraph>=1.0`;复用 `[llm]` 的 `langchain-core>=0.3` | 与现有 optional-dependencies 风格一致;1.0 是 langgraph 稳定里程碑 |
| 懒加载 | `import langgraph` 不出现在 `ladym/__init__.py` 顶层 | 未装 extra 的用户不受影响 |

## 依赖与打包

`pyproject.toml` 新增:

```toml
[project.optional-dependencies]
langgraph = ["langgraph>=1.0", "langchain-core>=0.3"]  # langchain-core 复用 [llm] 已声明版本
all = ["ladym[web,llm,local,mcp,openai,anthropic,langgraph]"]  # 加入 aggregate
```

(注:`langchain-core` 已在 `[llm]` 里;此处重复声明是为让 `[langgraph]` 单独可装而不强制拉 `[llm]` 的 chat-model 依赖。hatchling/uv 会自动去重。)

## 测试策略

沿用现有 `tests/conftest.py` 的 `Config.for_testing()` + `HashingEmbedding(dim=256)` + `tmp_db` fixture,**全程离线**(不触网、不需要真 LLM)。

| 测试文件 | 覆盖 |
|---------|------|
| `tests/unit/langgraph/__init__.py` | 包标记 |
| `tests/unit/langgraph/test_runtime.py` | `resolve_workspace`(有/无 user_id)、`resolve_engine`(各输入形态) |
| `tests/unit/langgraph/test_tools.py` | `create_ladym_tools` 返回 3 个 tool;各 tool 调用落到 Engine 且返回格式正确;`workspace` 从 config 解析(非参数);`remember_fact` 被 gate drop 时返回 `gate=dropped` |
| `tests/unit/langgraph/test_nodes.py` | recall 节点无命中返回 `{}`、有命中返回含 SystemMessage 的 dict;retain 节点取最后 human+ai 且经 gate |
| `tests/integration/test_langgraph_flow.py` | 端到端:用 `FakeLLMProvider` 跑一个最小 StateGraph(路径 B)和一个 `create_react_agent`(路径 A,若 prebuilt 可用),断言记忆被写入且下轮可 recall |

**skip 规则**:所有 langgraph 测试文件顶部 `pytest.importorskip("langgraph")`,未装 extra 时自动 skip(不阻断主测试套件)。

## 文档计划

- 新增 `docs/langgraph-integration.md`:完整 quickstart(两路径各一个可跑示例 + 多用户 + 注意事项)。
- `README.md` / `README.zh-CN.md` 各加一节 "Use with LangGraph",链到上面的 doc。
- `pyproject.toml` 的 `keywords` 可加 `"langgraph"`。

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| langgraph 1.x API 变动 | 关键 import 集中在 `_runtime.py`;pin `>=1.0`;测试覆盖 |
| Engine 跨节点共享的线程安全 | `SQLiteStore` 已支持 WAL(见 `start_system2` 范式),单 Engine 多节点共享读安全;文档说明多 worker 应每 worker 一个 Engine |
| 用户忘传 `user_id` 导致多用户串数据 | 文档强调;`resolve_workspace` 缺省回退到 `engine.config.workspace`(非空),不会崩,但会在 doc 里警示 |
| `@tool` 闭包捕获 engine 后无法替换 | 文厂函数文档说明:重建 tool 列表即可换 engine;长跑场景建议直接传 Engine |
| 异步(`ainvoke`)暂不支持 | MVP 明确只做同步;异步节点签名(`async def`)留后续版本,文档标注 |
| langchain 1.x tool 内 runtime 访问 API 变动(`ToolRuntime` 取代 `InjectedState`/裸 `config`) | 路径 A 工厂时绑定 workspace,tool 内不碰 runtime config,彻底回避;路径 B 用稳定的节点 `(state, config)` 签名 |

## 备选方案(已否决)

- **BaseStore 模式**:见非目标。会拍平 ladyM 分层语义、绕过 attention gate、丢 consolidate/supersedes。仅当未来出现"必须复用现有 store 接口且不改图"的硬约束时才重新评估。
- **替用户编译完整 graph**(提供 `build_memory_agent()` 一把梭):否决,因为 agent 形态千差万别(ReAct / 自定义 / 多 agent),把图结构写死反而限制灵活性。提供零件更符合 ladyM 作为"memory layer"的定位。

## 实现顺序(供 writing-plans 展开)

1. `pyproject.toml` 加 `[langgraph]` extra + `all` 聚合。
2. `src/ladym/langgraph/_runtime.py`(workspace/engine 解析)。
3. `src/ladym/langgraph/tools.py`(路径 A)+ `__init__.py` re-export。
4. `src/ladym/langgraph/nodes.py`(路径 B)。
5. 测试(unit + integration)。
6. 文档(`docs/langgraph-integration.md` + README 章节)。
7. 本地跑 `pytest tests/unit/langgraph tests/integration/test_langgraph_flow.py` + `ruff check`。
