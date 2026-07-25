# LadyM × LangGraph

Use LadyM as the long-term memory layer for [LangGraph](https://www.langchain.com/langgraph) /
LangChain agents. Install the optional extra:

```bash
pip install 'ladym[langgraph]'
```

Two equivalent paths — pick by agent shape, not by fashion.

## Which path should I pick?

| | Tools (path A) | Nodes (path B) |
|---|---|---|
| Who decides when to recall/remember | The LLM (ReAct-style) | Automatic, every turn |
| Workspace | Bound at factory time | Per-request, via `config["configurable"]["user_id"]` |
| Best for | Tool-calling agents, `create_react_agent` | Chat assistants that always want memory context |
| Pure LangChain (no graph) | Yes | No (needs LangGraph) |

Rule of thumb: if your agent already reasons about tools → **A**. If you want memory injected
into every reply without prompting → **B**.

## Path A — Tools

`create_ladym_tools(engine)` returns three LangChain `BaseTool`s — `recall_memory`,
`remember_fact`, `search_code` — that close over one Engine + workspace. Pass them to any
tool-calling agent.

```python
from ladym import Engine, Config
from ladym.langgraph import create_ladym_tools
from langgraph.prebuilt import create_react_agent
from langchain_openai import ChatOpenAI

eng = Engine(Config(db_path="ladym.db"))
tools = create_ladym_tools(eng)

agent = create_react_agent(ChatOpenAI(model="gpt-4o-mini"), tools)
agent.invoke({"messages": [{"role": "user", "content": "what did we decide about auth?"}]})
```

Works with plain LangChain too: `llm.bind_tools(tools)`.

**Workspace is bound at factory time** (defaults to `engine.config.workspace`). For
per-request multi-user isolation, use path B.

## Path B — Nodes

`create_recall_node(engine)` and `create_retain_node(engine)` return LangGraph graph nodes.
Wire them around your own agent node:

```python
from ladym import Engine, Config
from ladym.langgraph import create_recall_node, create_retain_node
from langgraph.graph import START, END, StateGraph, MessagesState
from langgraph.checkpoint.memory import InMemorySaver

eng = Engine(Config(db_path="ladym.db"))

def agent_node(state):
    # your LLM call here — recalled memory is already in state["messages"] as a SystemMessage
    ...

builder = StateGraph(MessagesState)
builder.add_node("recall", create_recall_node(eng))
builder.add_node("agent", agent_node)
builder.add_node("retain", create_retain_node(eng))
builder.add_edge(START, "recall")
builder.add_edge("recall", "agent")
builder.add_edge("agent", "retain")
builder.add_edge("retain", END)
graph = builder.compile(checkpointer=InMemorySaver())

# per-user isolation: user_id -> ladyM workspace
graph.invoke(
    {"messages": [{"role": "user", "content": "hi"}]},
    config={"configurable": {"thread_id": "t1", "user_id": "user-456"}},
)
```

Each turn: the `recall` node retrieves relevant memories and prepends them as a `SystemMessage`;
after your agent replies, the `retain` node stores the turn. Both go through LadyM's attention
gate, so low-value or duplicate content is dropped/rewritten automatically.

## Index your codebase first

`search_code` (path A) and recall against code memories only return hits if you've indexed the
source. Do it once (incremental — re-runs skip unchanged files):

```python
eng = Engine(Config(db_path="ladym.db"))
eng.index_code("./src")
```

## How the attention gate interacts

Every write — `remember_fact`, the `retain` node, or `Engine.remember` directly — passes through
LadyM's attention gate first. The gate decides one of:

- **pass** — stored as-is.
- **rewrite** — a cleaner form is stored; the original is kept in `metadata["original"]`.
- **drop** — not persisted (noise, or a hash-duplicate of a recent episodic event).

The path-A `remember_fact` tool reports the outcome in its return string
(`gate=pass|dropped|rewritten`), so the LLM can see when its "remember this" was filtered.
