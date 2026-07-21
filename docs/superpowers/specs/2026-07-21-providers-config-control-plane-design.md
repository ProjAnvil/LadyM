# LadyM — Providers, Config & Control Plane (SPEC)

> Status: design approved 2026-07-21. This spec turns three new requirements + six
> already-decided enhancements into an implementation contract. Target reader: an engineer
> who was **not** in the design conversation and must implement from this document alone.
>
> Companion: `docs/superpowers/plans/2026-07-21-providers-config-control-plane.md` (phased implementation plan, produced after this spec).

---

## 0. Context (read this first)

LadyM is a five-layer (L0–L4), brain-inspired memory framework for LLM agents and codebase
RAG. It is local-first (one SQLite file via `sqlite-vec`), sync, and ships four front-ends
(MCP / CLI / Skill / Python SDK) all calling one `Engine`. 103 tests pass fully offline on the
deterministic `HashingEmbedding`. Read `ARCHITECTURE.md` for the cognitive-science provenance
and `README.md` for usage before touching code.

**This spec adds three things on top of the existing engine:**

1. **External embedding providers** — open the embedding layer to any HTTP / Ollama / callable
   service, with `base_url` support so OpenAI-/Anthropic-compatible third parties (DeepSeek,
   Kimi/Moonshot, Zhipu, Together, vLLM, …) drop in by changing one URL.
2. **Per-operation configurable agents** — a unified `LLMProvider` abstraction (implemented on
   LangChain) and an agent registry so each cognitive operation picks its own model / prompt /
   endpoint, with sensible defaults.
3. **`ladym config` web UI** — a local browser page to edit every config field, with
   "test connection" buttons, saving to TOML.

**And integrates six already-decided enhancements** (do not re-litigate, see §2.7): supersedes
pointer chain, attention gate, System1/System2 dual path, L5 Mental Models + L6 Forward Intent
layers, TOML config files, and a memory-density stat.

---

## 1. Goals & user stories

### Requirement 1 — External embedding providers

> *As an operator, I want to point LadyM at any embedding service — hosted, self-hosted
> (Ollama), or a custom function — by editing config, without writing Python.*

**Stories**

- US-1.1 — Configure an HTTP embedding service via `provider="http"`, `base_url`,
  `model`, and optional auth, and have `index_code` / `remember` / `recall` all use it.
- US-1.2 — Configure Ollama embeddings via `provider="ollama"`, `base_url="http://localhost:11434"`,
  `model="nomic-embed-text"`.
- US-1.3 — Use an OpenAI-compatible third party (e.g. Zhipu) by setting `provider="openai"`,
  `base_url="https://open.bigmodel.cn/api/paas/v4"`.
- US-1.4 — Register a custom Python callable as an embedding provider at runtime
  (`embedding.register_callable("mine", fn)`).
- US-1.5 — Fail loudly when the provider is unreachable (default), OR opt into a safe
  fallback (`embedding.fallback`), but **never silently mix vector dimensions**.

**Acceptance criteria**

- AC-1.1 — `make_provider` resolves `hashing | st | openai | ollama | http | callable`; unknown
  names raise `ValueError`.
- AC-1.2 — `OllamaEmbedding`, `HttpEmbedding`, `CallableEmbedding` all implement
  `embed`, `embed_batch`, `health_check`.
- AC-1.3 — Dimension is **auto-probed** on first use against an empty DB and persisted in a new
  `meta` table; reopening a DB whose config disagrees raises `EmbeddingDimensionMismatch`.
- AC-1.4 — On provider failure: default = raise `EmbeddingProviderError`; if
  `fallback != "none"` **and** `fallback.dim == primary.dim` → degrade + warn; else raise.
- AC-1.5 — `embedding_dim` in config is treated as read-only advice; the probed value wins.
- AC-1.6 — External providers expose `base_url`; the OpenAI/ST/Ollama providers all honour it.

### Requirement 2 — Per-operation configurable agents

> *As an operator, I want each cognitive operation to use its own LLM strength — a cheap model
> for classification, a strong model for mental-model extraction — and to keep some operations
> purely heuristic.*

**Stories**

- US-2.1 — Define a global `[llm]` default; every operation inherits it unless overridden.
- US-2.2 — Override any of the five named operations
  (`consolidate`, `proceduralize`, `attention_gate`, `l5_mental_model`, `l6_forward_intent`)
  with a different `provider / base_url / model / api_key_env / params`.
- US-2.3 — Point any operation at a third-party endpoint via `base_url`.
- US-2.4 — Run fully offline with `provider="none"` everywhere — all operations fall back to
  pure heuristics, no network, no LangChain import.

**Acceptance criteria**

- AC-2.1 — `LLMProvider` ABC exposes `complete(messages) -> str` and
  `complete_structured(messages, schema) -> dict`; concrete providers are built via LangChain.
- AC-2.2 — `make_agent(cfg, op)` returns a callable bound to an `AgentConfig`; replaces the
  bare `attach_llm_classifier` hook (which is kept as a thin shim for back-compat).
- AC-2.3 — With no `[llm]` configured, `make_llm_provider` returns `None` and no LLM is
  constructed or imported.
- AC-2.4 — `complete_structured` returns a dict matching the supplied pydantic schema for
  OpenAI / Anthropic / Ollama backends (tool-calling where supported, JSON-mode + parse
  fallback otherwise), controlled by `structured_method`.

### Requirement 3 — `ladym config` web UI

> *As an operator, I want to open a local web page, edit every config field, test that my
> embedding/LLM endpoints actually respond, and save to TOML — without memorising field names.*

**Stories**

- US-3.1 — `ladym config` starts a local server and opens the browser at it.
- US-3.2 — Every field in `Config` is editable in a form grouped by section.
- US-3.3 — "Test embedding" / "Test LLM" buttons invoke the provider once and show ✓/✗ + latency.
- US-3.4 — "Save" writes the merged config to `./ladym.toml` (or a chosen path); "Reset"
  reloads from disk.
- US-3.5 — API keys typed into the form are **rejected** with a warning (keys are env-only).

**Acceptance criteria**

- AC-3.1 — `ladym config` is provided by an optional `[web]` extra; missing extra prints a
  clear install hint and exits non-zero.
- AC-3.2 — Server binds `127.0.0.1` only (no deployment surface).
- AC-3.3 — Static assets (HTMX, Pico.css) are vendored; the page works with **zero network**.
- AC-3.4 — The `/test/*` endpoints return HTML fragments for HTMX partial refresh.

---

## 2. Functional design

### 2.1 Architecture (how new pieces attach to `Engine`)

```
              Config.load()  ← CLI > env > ./ladym.toml > ~/.ladym/config.toml > defaults
                     │
   ┌─────────────────Engine─────────────────────────────────────────────┐
   │ EmbeddingProvider  hashing(default) | st | openai | ollama | http | callable
   │     · 3-layer resilience: hard-fail default / dim-guarded fallback / auto-probe+persist
   │     · query LRU cache (default off)
   │ LLMProvider (LangChain)  none | openai | anthropic | ollama | http   — write-path only
   │     · complete() / complete_structured(schema)
   │ AgentRegistry  make_agent(op) → AgentConfig(provider,base_url,model,params) bound callable
   │     · 5 ops: consolidate | proceduralize | attention_gate | l5_mental_model | l6_forward_intent
   │ AttentionGate        remember() pre-filter [PASS|REWRITE|DROP], L1/L2/L3 only
   │ Layers +L5_mental(mental_model) +L6_predictive(forward_intent)
   │ supersedes chain      UPDATE/DELETE → mark + L4 edge, never physical delete in consolidate
   │ System2 worker        run_system2_cycle(engine) → ladym worker [--once|--interval], WAL
   └─────────────────────────────────────────────────────────────────────┘
```

**Read path is unchanged** except a supersedes filter: `recall()` still calls only
`embedder.embed(query)`; `reflect()` stays heuristic; **no LLM is ever invoked in the read
path** (NFR-3).

### 2.2 Configuration

#### 2.2.1 Precedence (high → low)

**CLI args > env vars > `./ladym.toml` (project) > `~/.ladym/config.toml` (global) > code
dataclass defaults.**

- Project file overrides global (matches the workspace-local philosophy of `ladym.db`).
- Env still overrides files (existing `LADYM_DB/LADYM_WORKSPACE/LADYM_EMBEDDING` kept; extended
  to `LADYM_LLM_PROVIDER/LADYM_LLM_BASE_URL/LADYM_LLM_MODEL/LADYM_*_API_KEY`).
- CLI args override everything for one invocation without writing to disk.
- **Secrets are env-only.** Any `api_key` / `*_key` literal appearing in a TOML file is parsed
  with a warning and **ignored**; keys are referenced by `api_key_env` (an env-var name).

#### 2.2.2 Loading

- New `Config.load(config_path: Path | None = None) -> Config` merges the four layers. An
  explicit `config_path` (e.g. `--config`) replaces the auto-discovered project file.
- `Config.from_file(path) -> Config` parses one TOML file (Python 3.11 `tomllib`).
- `Engine(Config())` behaviour is unchanged (back-compat, NFR-4): with no files and no env it
  reproduces today's defaults.

#### 2.2.3 Full TOML example

```toml
# ./ladym.toml
db_path        = "ladym.db"
workspace      = "default"
prefer_sqlite_vec = true

[embedding]
provider        = "openai"                 # hashing|st|openai|ollama|http|callable
base_url        = "https://api.openai.com/v1"
model           = "text-embedding-3-small"
api_key_env     = "LADYM_OPENAI_API_KEY"   # name of env var; never a literal key
fallback        = "none"                   # none|hashing|<provider name>
query_cache_size = 0                       # LRU capacity; 0 = off
timeout_s       = 10
allow_dim_change = false                   # true = wipe+re-embed on dim mismatch (explicit, expensive)

[llm]                                      # global default; 5 agents inherit
provider        = "none"                   # none|openai|anthropic|ollama|http
base_url        = ""                       # third-party OpenAI/Anthropic-compatible endpoint
model           = "gpt-4o-mini"
api_key_env     = ""
max_tokens      = 1024
temperature     = 0.2
structured_method = "function_calling"     # function_calling|json_mode
timeout_s       = 30

[agents.consolidate]                      # override only what differs from [llm]
provider = "openai"; base_url = "https://api.deepseek.com/v1"
model = "deepseek-chat"; api_key_env = "DEEPSEEK_API_KEY"

[agents.l5_mental_model]                  # strong model via another vendor
provider = "openai"; base_url = "https://api.moonshot.cn/v1"
model = "moonshot-v1-32k"; api_key_env = "MOONSHOT_API_KEY"

[agents.attention_gate]                   # force heuristic, spend nothing
provider = "none"

# proceduralize / l6_forward_intent absent ⇒ inherit [llm]

[activation]
similarity = 1.0; recency = 0.3; frequency = 0.2; graph = 0.15; type_boost = 0.25
recency_half_life_s = 604800.0

[recall]
top_k_tier1 = 8; top_k_tier2 = 20; graph_hops = 2
reflection_min_hits = 2; reflection_min_coverage = 0.5; enable_tier2 = true

[consolidate]
min_episodes_to_trigger = 3; dedup_similarity_threshold = 0.85

[code_index]
max_body_lines_per_symbol = 40; respect_gitignore = true
extra_ignore_globs = ["**/.venv/**", "**/node_modules/**", "**/__pycache__/**"]
# languages omitted ⇒ all supported

[system2]
enabled = false                            # in-process worker thread (opt-in)
interval_s = 300
min_episodes_to_run = 3                    # threshold gate: skip LLM steps below this

[attention]                                # heuristic-mode knobs (provider="none")
min_chars = 8; dedup_window_s = 3600
noise_words = []                           # extend the built-in list
```

#### 2.2.4 Field reference

| Section.field | Type | Default | Env | Notes |
|---|---|---|---|---|
| `db_path` | path | `./ladym.db` | `LADYM_DB` | |
| `workspace` | str | `default` | `LADYM_WORKSPACE` | |
| `prefer_sqlite_vec` | bool | `true` | — | |
| `embedding.provider` | enum | `hashing` | `LADYM_EMBEDDING` | hashing\|st\|openai\|ollama\|http\|callable |
| `embedding.base_url` | str | `""` | `LADYM_EMBEDDING_BASE_URL` | endpoint; replaces old `endpoint` |
| `embedding.model` | str | `""` | `LADYM_EMBEDDING_MODEL` | |
| `embedding.api_key_env` | str | `""` | — | name of env var holding the key |
| `embedding.fallback` | str | `none` | — | `none`\|`hashing`\|provider name |
| `embedding.query_cache_size` | int | `0` | — | LRU; 0 disables |
| `embedding.timeout_s` | float | `10` | — | per request |
| `embedding.allow_dim_change` | bool | `false` | — | re-embed on mismatch |
| `llm.provider` | enum | `none` | `LADYM_LLM_PROVIDER` | none\|openai\|anthropic\|ollama\|http |
| `llm.base_url` | str | `""` | `LADYM_LLM_BASE_URL` | third-party endpoint |
| `llm.model` | str | `gpt-4o-mini` | `LADYM_LLM_MODEL` | |
| `llm.api_key_env` | str | `""` | — | env var name |
| `llm.max_tokens` | int | `1024` | — | |
| `llm.temperature` | float | `0.2` | — | |
| `llm.structured_method` | enum | `function_calling` | — | function_calling\|json_mode |
| `llm.timeout_s` | float | `30` | — | |
| `agents.<op>.*` | — | inherit `[llm]` | — | op ∈ {consolidate, proceduralize, attention_gate, l5_mental_model, l6_forward_intent} |
| `activation.*` | float | see §2.2.3 | — | ACT-R weights |
| `recall.*` | mixed | see §2.2.3 | — | two-tier knobs |
| `consolidate.*` | mixed | see §2.2.3 | — | |
| `code_index.*` | mixed | see §2.2.3 | — | |
| `system2.enabled` | bool | `false` | — | opt-in in-process thread |
| `system2.interval_s` | int | `300` | — | |
| `system2.min_episodes_to_run` | int | `3` | — | threshold gate |
| `attention.min_chars` | int | `8` | — | heuristic gate only |
| `attention.dedup_window_s` | float | `3600` | — | heuristic gate only |
| `attention.noise_words` | list | `[]` | — | extends built-in |

### 2.3 EmbeddingProvider contract

The existing ABC is extended with `health_check`; `embed`/`embed_batch` are unchanged.

```python
class EmbeddingProvider(ABC):
    dim: int
    def embed(self, text: str) -> list[float]: ...
    def embed_batch(self, texts: list[str]) -> list[list[float]]: ...
    def health_check(self) -> tuple[bool, str]:
        """One-shot probe for the web UI 'test embedding' button. Returns (ok, message)."""
```

New concrete providers:

```python
class OllamaEmbedding(EmbeddingProvider):
    # POST {base_url}/api/embeddings {"model":..,"prompt":..} → {"embedding":[...]}
    def __init__(self, base_url, model, timeout_s=10): ...

class HttpEmbedding(EmbeddingProvider):
    # Generic POST {base_url} with a configurable request/response JSONPath
    def __init__(self, base_url, model, request_template, response_path,
                 headers=None, timeout_s=10): ...

class CallableEmbedding(EmbeddingProvider):
    # Wraps a user-supplied (text|list[text]) -> vector|list[vector]
    def __init__(self, fn, dim): ...
```

**Dimension auto-probe & persistence**

- On `Engine` init with an **empty** DB, call `provider.embed("dimensionality probe")` once,
  store `dim` into a new `meta(key TEXT PRIMARY KEY, value TEXT)` table
  (`meta.embedding_dim`, `meta.embedding_provider`, `meta.embedding_model`).
- On reopen, compare probed `dim` from `meta` with the live provider's `dim`:
  - match → continue;
  - mismatch with `allow_dim_change=false` → raise `EmbeddingDimensionMismatch(stored, configured)`;
  - mismatch with `allow_dim_change=true` → wipe all `memories.embedding` blobs + vector index,
    re-embed everything (expensive, explicit), update `meta`.

**Failure handling**

```python
class EmbeddingProviderError(RuntimeError): ...
```

- Provider raises → if `fallback == "none"`: re-raise `EmbeddingProviderError`.
- Else: probe `fallback`'s `dim`; if `== primary.dim` → switch to fallback, log warning,
  continue; else → raise (never silently mix dims).
- Batches (`embed_batch`) are atomic: one item fails ⇒ the whole call raises (retry policy is
  the System2 worker's concern, not the provider's).

**Query cache** — an optional LRU keyed on `hash(query_text)` wrapping `embed` only (not
`embed_batch`); capacity `embedding.query_cache_size`, default 0 (off).

### 2.4 LLMProvider contract (implemented on LangChain)

```python
class Message(TypedDict):
    role: str        # "system" | "user" | "assistant"
    content: str

class LLMProvider(ABC):
    name: str
    def complete(self, messages: list[Message], **params) -> str: ...
    def complete_structured(self, messages: list[Message],
                            schema: type[BaseModel], **params) -> dict: ...
    def close(self) -> None: ...
```

**One concrete implementation wraps a LangChain `BaseChatModel`**; the factory selects the chat
class by `provider` and threads `base_url` through, so third-party OpenAI-/Anthropic-compatible
endpoints work by URL swap:

```python
class LangChainLLMProvider(LLMProvider):
    def __init__(self, chat_model: BaseChatModel, structured_method: str = "function_calling"):
        self._cm = chat_model
        self._sm = structured_method
    def complete(self, messages, **p) -> str:
        return self._cm.invoke([_to_langchain(m) for m in messages]).content
    def complete_structured(self, messages, schema, **p) -> dict:
        runner = self._cm.with_structured_output(schema, method=self._sm)
        return runner.invoke([_to_langchain(m) for m in messages])   # dict matching schema
    def close(self): ...

def make_llm_provider(*, provider, base_url, model, api_key, structured_method,
                      max_tokens, temperature, timeout_s) -> LLMProvider | None:
    if provider == "none":
        return None                                  # caller uses heuristics
    if provider == "openai":
        from langchain_openai import ChatOpenAI
        cm = ChatOpenAI(base_url=base_url, model=model, api_key=api_key,
                        max_tokens=max_tokens, temperature=temperature, timeout=timeout_s)
    elif provider == "anthropic":
        from langchain_anthropic import ChatAnthropic
        cm = ChatAnthropic(base_url=base_url, model=model, api_key=api_key,
                           max_tokens=max_tokens, temperature=temperature, timeout=timeout_s)
    elif provider == "ollama":
        from langchain_ollama import ChatOllama
        cm = ChatOllama(base_url=base_url, model=model, temperature=temperature)
    elif provider == "http":
        from langchain_openai import ChatOpenAI      # OpenAI-shaped generic HTTP
        cm = ChatOpenAI(base_url=base_url, model=model, api_key=api_key, ...)
    return LangChainLLMProvider(cm, structured_method)
```

**Offline safety (NFR-2):** LangChain is imported **only** inside `make_llm_provider` for a real
backend. `provider="none"` and the test `FakeLLMProvider` never import LangChain. The 103
offline tests are unaffected; LangChain is an optional extra, not a core dep.

**Structured output:** `complete_structured` uses `.with_structured_output(schema, method=…)`.
For OpenAI-compatible third parties that lack function-calling, set
`structured_method = "json_mode"`. Ollama falls back to prompt + strict-JSON parse with one
retry inside the provider.

### 2.5 Agent registry & defaults

```python
@dataclass
class AgentConfig:
    op: str
    provider: str = "none"            # inherits [llm] if unset at load time
    base_url: str = ""
    model: str = ""
    api_key_env: str = ""
    prompt_template: str = ""          # operation-specific default if empty
    max_tokens: int = 1024
    temperature: float = 0.2
    structured_method: str = "function_calling"

def make_agent(cfg: Config, op: str) -> Agent:
    """Bind an AgentConfig to a provider, returning an operation-specific callable."""
```

**Default agent table (when `[llm]` is configured):**

| op | role | output | sensible default model | prompt template source |
|---|---|---|---|---|
| `consolidate` | ADD/UPDATE/DELETE/NOOP classifier | structured | cheap (haiku-class / 4o-mini) | `prompts/consolidate.txt` |
| `proceduralize` | cluster → playbook summary | text | cheap | `prompts/proceduralize.txt` |
| `attention_gate` | is-this-worth-storing | structured (pass/rewrite/drop) | cheap **or none** (heuristic) | `prompts/attention_gate.txt` |
| `l5_mental_model` | abstract mental-model extraction | text | strong (sonnet-class) | `prompts/l5.txt` |
| `l6_forward_intent` | next-intent prediction | text | strong | `prompts/l6.txt` |

With `[llm] provider="none"` (default), **all five** run their pure-heuristic path and no
`LLMProvider` is constructed. `Engine.attach_llm_classifier` is preserved as a one-line shim
around `make_agent(cfg, "consolidate")` so existing call sites and tests keep working (NFR-4).

### 2.6 supersedes pointer chain

Fixes the UPDATE-overwrites-history defect. Consolidation's `UPDATE`/`DELETE` no longer
overwrite/delete in place:

- On `UPDATE`: the old fact is **not** mutated in place. Instead a **new** memory is created
  holding the merged content (the candidate's richer text), and the old one is retired against
  it: `old.metadata["superseded_by"] = new.id`, `old.metadata["superseded_at"] = now`, plus an
  L4 edge `Edge(src_id=old.id, relation="supersedes", dst_id=new.id, valid_from=now)`. This
  changes today's mutate-in-place behaviour deliberately — the point is to keep history.
- On `DELETE`: the existing (stale) fact is **retired**, not removed. Set
  `old.metadata["superseded"] = True`, `old.metadata["superseded_at"] = now`, and close its
  edges (`valid_to = now`). There is no successor id (`superseded_by` is unset) — DELETE means
  "this fact is wrong, retire it", not "this fact is replaced by that one".
- `recall` tier-1 **filters out** any memory that is retired — i.e.
  `metadata.superseded_by` is set (UPDATE chain) **or** `metadata.superseded` is true (DELETE
  retirement) — so only the live version(s) surface in normal retrieval.
- `recall` tier-2 graph expansion **follows `supersedes` edges**, so a "how did this fact
  evolve?" query can traverse the full chain.
- New helper `latest_in_chain(store, mem_id)` walks `supersedes` edges to the head.
- **Physical deletion** remains available via `forget()` (explicit) and `decay()` (activation
  floor) — these are the only paths that actually delete rows.

### 2.7 Attention gate

```python
@dataclass
class GateDecision:
    action: str          # "pass" | "rewrite" | "drop"
    content: str | None  # rewritten content when action == "rewrite"
    reason: str

def attention_gate(content: str, *, engine: "Engine", layer: Layer) -> GateDecision: ...
```

- **Heuristic mode (default, `provider="none"`):** drop if `len(content) < attention.min_chars`,
  or content hashes-equal to a recent L1 within `dedup_window_s`, or it matches the noise-word
  list. Otherwise pass. Zero deps.
- **LLM mode:** `complete_structured` returns `{action, content?, reason}`.
- Applies **only to L1/L2/L3 long-term writes**; L0 working memory is never gated (scratch).
- `Engine.remember()` calls the gate before persisting. On `drop`: **do not persist**, but
  **return a `Memory`** with `metadata={"gated": "dropped", "reason": ...}` so the return-type
  contract is preserved (NFR-4). On `rewrite`: persist the rewritten content, tag
  `metadata={"gated": "rewritten", "original": original}`.

### 2.8 System1 / System2 + L5 / L6

**New layers (schema only needs enum additions; `layer` is a TEXT column):**

- `Layer.L5_MENTAL = "L5_mental"` + `MemoryType.MENTAL_MODEL = "mental_model"`
- `Layer.L6_PREDICTIVE = "L6_predictive"` + `MemoryType.FORWARD_INTENT = "forward_intent"`

**System2 cycle** — a pure, sync, side-effectful-on-DB function:

```python
def run_system2_cycle(engine: "Engine", *, workspace: str | None = None) -> System2Report:
    # threshold gate: if new L1 < cfg.system2.min_episodes_to_run, skip LLM steps
    report = System2Report()
    report.consolidate = engine.consolidate(workspace=workspace)
    report.proceduralize = engine.proceduralize(workspace=workspace)
    if enough_new_episodes:
        # L5/L6 extractors are registered in post-MVP; until then these are no-ops
        # guarded by hasattr, so the MVP cycle is consolidate + proceduralize + decay.
        if hasattr(engine, "extract_mental_models"):
            report.l5 = engine.extract_mental_models(workspace=workspace)   # agents.l5_mental_model
        if hasattr(engine, "predict_forward_intents"):
            report.l6 = engine.predict_forward_intents(workspace=workspace) # agents.l6_forward_intent
    report.decay = engine.decay(workspace=workspace)
    return report
```

- **L5 extraction:** cluster L2 facts + L3 playbooks (embedding similarity), summarise each
  cluster into one abstract mental model via `agents.l5_mental_model`; store as
  `L5_mental/mental_model`, link members with `relation="abstracts"`.
- **L6 prediction:** from recent L1 episodes, predict likely next intents via
  `agents.l6_forward_intent`; store as `L6_predictive/forward_intent` with `valid_to` expiry.

**Lifecycle (decoupled from `Engine`):**

- **Primary:** `ladym worker [--once] [--interval 300] [--workspace w]` runs a loop calling
  `run_system2_cycle`. The `--once` mode lets cron/launchd drive it.
- **Opt-in in-process:** `engine.start_system2(interval_s=…)` spawns a daemon thread that opens
  its **own** sqlite connection (never shares the Engine's main connection) and calls the same
  cycle. Off by default (`system2.enabled=false`).
- **Concurrency:** the store opens with `PRAGMA journal_mode=WAL` so the worker writes while
  readers recall, without blocking.

### 2.9 `ladym config` web UI

**Stack:** FastAPI + HTMX + Pico.css, behind an optional `[web]` extra
(`fastapi`, `uvicorn[standard]`, `jinja2`, `python-multipart`). Static assets
(`htmx.min.js`, `pico.min.css`) are vendored under `src/ladym/web/static/` so the page works
fully offline.

**Command:** `ladym config [--port 8765] [--no-browser] [--config PATH]` — lazy-imports the web
module; binds `127.0.0.1`; opens the browser unless `--no-browser`.

**Routes:**

| Route | Method | Behaviour |
|---|---|---|
| `/` | GET | render the full config form, grouped by section |
| `/save` | POST | merge form values, refuse literal keys, write `./ladym.toml` (or `--config` path) |
| `/reset` | POST | reload config from disk |
| `/test/embedding` | POST (HTMX) | build the embedding provider from submitted fields, call `health_check()` + probe `dim`, return an HTML fragment (✓/✗ + dim + latency) |
| `/test/llm` | POST (HTMX) | build the LLM provider, call `complete("ping")`, return ✓/✗ + latency fragment |
| `/stats` | GET (HTMX) | render `engine.stats()` for the configured DB |

**Wireframe (text):**

```
┌─ LadyM config ──────────────────────────────  [Save] [Reset] ─┐
│ [Embedding] [LLM] [Agents] [Activation] [Recall] [Consolidate]│  ← section anchors
│ [CodeIndex] [System2] [Attention]                              │
├────────────────────────────────────────────────────────────────┤
│ Embedding                              [ test embedding ]      │
│   provider   [ openai ▾ ]              ✓ ok · dim 1536 · 142ms │
│   base_url   [ https://api.openai.com/v1 ]                     │
│   model      [ text-embedding-3-small ]                        │
│   api_key_env[ LADYM_OPENAI_API_KEY ]                          │
│   fallback   [ none ▾ ]   query_cache [ 0 ]                    │
│ ─────────────────────────────────────────────────────────────  │
│ LLM  (global default)                  [ test llm ]            │
│   provider [ none ▾ ]  base_url [ … ]  model [ … ] …           │
│ ─────────────────────────────────────────────────────────────  │
│ Agents (per-operation overrides)                               │
│   consolidate    provider[…▾] base_url[…] model[…] …           │
│   l5_mental_model provider[…▾] base_url[…] model[…] …          │
│   attention_gate provider[ none ▾ ]                            │
│   …                                                            │
└────────────────────────────────────────────────────────────────┘
```

### 2.10 Memory-density stat

`Stats` gains `avg_tokens_per_memory: float`. Token count is approximated cheaply via the
existing `tokenize()` length (sum of token counts / count of memories), no new dep. Surfaced in
`ladym stats` and the web `/stats` fragment.

---

## 3. Non-functional requirements (hard constraints)

> **NFR-1 — Read-path latency (revised).** Read-path cost = *embedding latency* +
> *engine overhead*. **Engine overhead (activation rerank + sqlite + reflection gate) is
> < 10 ms at p95** at 200 memories, regardless of provider. The hashing path keeps
> **overall p95 < 10 ms** (tests/CI unaffected). The external path's p95 ≈ provider RTT;
> **target < 300 ms**, hard cap configurable. Validated by `tests/perf/test_read_path_budget.py`.

> **NFR-2 — Progressive degradation.** Every LLM/embedding dependency has a pure-code fallback
> when unconfigured. Zero-config, offline, testable. The existing 103 tests stay green and
> offline throughout; LangChain is an optional extra never imported in the offline path.

> **NFR-3 — LLM only on the write path.** `consolidate / proceduralize / L5 / L6 / attention
> gate` LLM calls are write-path. `recall` / `search_code` **never** invoke a generative LLM.
> `reflect()` is heuristic-only forever (the previously-documented "pluggable LLM judge" option
> is removed; `ARCHITECTURE.md` §3 wording is amended). Enforced by a guard test that greps the
> recall module for LLM imports.

> **NFR-4 — API stability.** `Engine` / SDK / CLI / MCP public signatures only gain fields;
> semantics are unchanged. `Engine(Config())` still works; new fields all have defaults;
> `attach_llm_classifier` is kept as a shim. Old code runs unmodified.

> **NFR-5 — Config safety.** Secrets never live in TOML (env-only). Dimension mismatches never
> corrupt the index. No silent quality downgrade across providers.

---

## 4. Testing strategy

- **Regression-protection phase first (see PLAN).** Before any new code, run the full 103 tests
  on a clean checkout, snapshot results, and add `Config` back-compat assertions.
- **External embedding providers** — providers accept an injected `http_client` (an `httpx.Client`
  or a `FakeHTTPClient` stub). Unit tests script responses; no network. `make_provider` is
  tested via `hashing` only.
- **LLM providers** — a `FakeLLMProvider` (scriptable `complete`/`complete_structured`) replaces
  real calls. The existing `attach_llm_classifier` test pattern is preserved.
- **LangChain path** — covered by an *integration-marked* test that constructs a real
  `LangChainLLMProvider` only when `langchain` is importable, skipped otherwise; never runs in
  the default offline suite.
- **supersedes chain** — full-chain traversal, tier-1 filtering, `latest_in_chain`, no physical
  delete on UPDATE/DELETE.
- **attention gate** — heuristic drop/rewrite/dedup + LLM mode via `FakeLLMProvider`.
- **WAL concurrency** — integration test: a `ladym worker --once` writing while a reader
  recalls, asserting no blocking and consistent reads.
- **Config precedence & secret rejection** — a test matrix over the four layers + a test that a
  literal `api_key` in TOML is ignored with a warning.
- **Dimension guard** — empty-DB probe persists `meta.embedding_dim`; reopen with a mismatched
  provider raises `EmbeddingDimensionMismatch`; `allow_dim_change=true` re-embeds.
- **Performance guard** — `tests/perf/test_read_path_budget.py` asserts engine-overhead p95
  < 10 ms @ 200 memories on the hashing provider.
- **NFR-3 guard** — `tests/test_read_path_no_llm.py` asserts `recall.py` (and its imports) bring
  in no LLM provider code.

---

## 5. Backward compatibility

- `Engine(Config())`, `Config()`, `Config.for_testing()`, all four front-ends: unchanged.
- New `Config` fields all default to current behaviour (e.g. `embedding.provider="hashing"`,
  `llm.provider="none"`, `system2.enabled=false`).
- `embedding.endpoint` → renamed `embedding.base_url`; the loader accepts the old key with a
  deprecation warning for one release.
- `Engine.attach_llm_classifier` retained as a shim.
- SQLite schema gains one new table (`meta`); existing DBs get it via the idempotent migration
  pattern already used for the `embedding` column. No data migration needed for supersedes
  (uses existing `metadata` JSON + `edges` table) or L5/L6 (TEXT `layer` column).

---

## 6. MVP boundary

**Shipped in MVP (delivered value without the rest):**
- Embedding provider abstraction + `ollama`/`http`/`callable` providers + `base_url`
- Dimension auto-probe/persist + mismatch guard + dim-guarded fallback
- `LLMProvider` (LangChain) + `AgentRegistry` + 5 named agents
- TOML config loading + 4-layer precedence + secret rejection
- `attention_gate` + supersedes chain
- `ladym worker [--once|--interval]` + `run_system2_cycle` + WAL
- Memory-density stat
- Regression-protection phase

**Deferred (post-MVP, schema-ready):**
- `ladym config` web UI (§2.9)
- L5/L6 extraction logic (schema + agent slots exist; extraction functions come later)
- In-process System2 thread (`system2.enabled`)
- Query-embedding cache (default off; flip on once measured)

---

## 7. Glossary & references

- **HyMem** (two-tier retrieval, reflection gate, cognitive economy) —
  [arXiv 2602.13933](https://arxiv.org/html/2602.13933v2). *Note: this spec removes the LLM
  reflection-judge option to honour NFR-3.*
- **mem0** (ADD/UPDATE/DELETE/NOOP, dual store) — [arXiv 2504.19413](https://arxiv.org/html/2504.19413v1).
- **Anthropic — Effective Context Engineering** (LLM off the read path) —
  [anthropic.com/engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents).
- **LangChain** structured output — [python.langchain.com](https://python.langchain.com/docs/concepts/structured_outputs/).
- **Ollama** embeddings API — [ollama.com](https://github.com/ollama/ollama/blob/main/docs/api.md).
- **sqlite WAL** mode — [sqlite.org/wal](https://www.sqlite.org/wal.html).
- **TOML** parsing — Python 3.11 `tomllib` ([docs](https://docs.python.org/3/library/tomllib.html)).
- Project context: `ARCHITECTURE.md`, `README.md`, `src/ladym/{config,engine}.py`,
  `src/ladym/storage/embeddings.py`.
