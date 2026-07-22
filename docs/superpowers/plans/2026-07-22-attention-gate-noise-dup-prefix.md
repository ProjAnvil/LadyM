# Attention Gate: noise/dup 前置 + LLM 语义层 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 attention gate 从"heuristic 或 LLM 互斥两模式"改成"noise + recent-duplicate 作确定性前置层,通过后才交 LLM 做语义判断(too short 规则移除)",让 S09 scenario 在 `provider=none` 和配了 LLM 两种部署下都能配置无关地通过。

**Architecture:** gate 函数分两层——(1) heuristic 前置:noise 词表 + recent-duplicate hash,确定性 drop,跑在 LLM 之前;(2) 语义层:配了 LLM 就 `complete_structured` 判 pass/rewrite/drop,否则 pass。`too short`(`len < min_chars`)规则删除——"hi" 这类短内容交给 LLM 判断(none 模式直接 pass)。`min_chars` config 字段保留(兼容),但 gate 不再使用。

**Tech Stack:** Python 3.12、pytest、typer(CLI)、ladym MCP server(stdio)、`FakeLLMProvider`(hermetic 测试 double)。

**Spec:** `docs/superpowers/specs/2026-07-22-attention-gate-noise-dup-prefix-design.md`

## Global Constraints

- **提交**:英文 conventional commits,结尾 `Co-Authored-By: Claude <noreply@anthropy.com>`;显式路径 `git add <files>`,**不用** `-A`/`.`。
- **验证基线**:`uv run pytest tests/unit/test_attention_gate.py tests/unit/test_cli.py tests/unit/test_mcp_server.py -q` 全绿。(`test_mcp_index_code` 偶发受 test-ordering flaky,单独重跑即 pass——非本 plan 范围。)
- **`min_chars` 字段保留**:`config.py:144` 的 `min_chars: int = 8` **不动**,`test_config_load.py:75,85`(测 toml 加载 `min_chars=16`)**不动**。gate 只是不再读它。
- **运行环境**:本地 ollama(11434)在跑;CLI 调用加 `LADYM_LLM_PROVIDER=none` 前缀可离线跑(单测用 `Config.for_testing` 默认 none,不需要)。

---

## File Structure

| 文件 | 责任 | 本次动作 |
|------|------|----------|
| `src/ladym/operations/attention.py` | gate 函数 + `_llm_gate` + module docstring | 重构(Task 1) |
| `tests/unit/test_attention_gate.py` | gate 单元测试 | 改 3 处 + 加 1 处(Task 1) |
| `tests/unit/test_cli.py` | CLI 路径测试 | 改 `test_cli_remember_drop_too_short`(Task 2) |
| `tests/unit/test_mcp_server.py` | MCP 路径测试 | 改 `test_mcp_remember_drop_too_short`(Task 3) |
| `docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md` | SPEC §2.7 | 改 §2.7 措辞 + :267 表(Task 4) |
| `scenarios/S09-attention-gate.md` | e2e 剧本 | 重写 drop 矩阵(Task 4) |
| `scenarios/_conventions.md` | scenario 约定 | 更新 §8.1(Task 4) |

---

## Task 1: 重构 attention_gate 核心

**Files:**
- Modify: `src/ladym/operations/attention.py:13-26`(docstring)、`:60-102`(`attention_gate`)、`:105-131`(`_llm_gate`)
- Test: `tests/unit/test_attention_gate.py`

**Interfaces:**
- Consumes: `engine.config.attention.noise_words` / `dedup_window_s`、`engine._get_agent("attention_gate")`、`engine.store.conn`、`Layer`、`_BUILTIN_NOISE`、`_hash`、`GateDecision`(均已存在,签名不变)
- Produces: `attention_gate(content, *, engine, layer) -> GateDecision`(签名不变);行为变化——不再因 `len < min_chars` drop,"hi" 在无 LLM 时 pass;noise/dup 在 LLM 调用前判定。

- [ ] **Step 1: 把 `test_gate_drops_too_short` 改成验证 "hi" 在 none 模式 pass**

打开 `tests/unit/test_attention_gate.py`,把 `:33-36` 的:

```python
def test_gate_drops_too_short(engine):
    d = attention_gate("hi", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "drop"
```

替换为:

```python
def test_gate_passes_short_when_no_llm(engine):
    # too-short content is no longer a heuristic drop; with no LLM agent wired
    # (Config.for_testing default), "hi" clears the heuristic prefix and passes.
    d = attention_gate("hi", engine=engine, layer=Layer.SEMANTIC)
    assert d.action == "pass"
```

- [ ] **Step 2: 运行,确认 FAIL**

Run: `uv run pytest tests/unit/test_attention_gate.py::test_gate_passes_short_when_no_llm -q`
Expected: FAIL — 当前 gate 对 "hi" 返回 `drop`("too short")。这证明测试有效。

- [ ] **Step 3: 重构 `attention_gate`——移除 too short,noise+dup 前置到 LLM 之前**

打开 `src/ladym/operations/attention.py`,把 `:60-102` 的整个 `attention_gate` 函数体替换为:

```python
def attention_gate(content: str, *, engine, layer: Layer) -> GateDecision:
    """Apply the attention gate to ``content`` destined for ``layer``.

    Two layers:

    1. **Heuristic prefix** (always runs, before any LLM call): deterministic
       hard rules that need no semantics — content composed entirely of the
       noise vocabulary, or a hash-exact duplicate of a recent L1 episodic event
       (within ``dedup_window_s``). Both ``drop``.
    2. **Semantic layer**: content that clears the prefix is delegated to the LLM
       agent bound to ``attention_gate`` if one is configured (``pass`` /
       ``rewrite`` / ``drop``); otherwise it ``pass``es.

    Short content like ``"hi"`` reaches the semantic layer — with no LLM it
    passes; with an LLM the prompt judges whether it is worth keeping. L0
    working memory is never gated (ephemeral scratch).
    """
    cfg = engine.config
    if layer == Layer.WORKING:
        return GateDecision(action="pass", reason="working memory never gated")

    # ----- heuristic prefix: deterministic hard rules (run before the LLM) -----
    stripped = content.strip()

    # noise: every token is in the noise vocabulary.
    tokens = {w.lower() for w in stripped.split()}
    noise = _BUILTIN_NOISE | set(cfg.attention.noise_words)
    if tokens and tokens <= noise:
        return GateDecision(action="drop", reason="noise")

    # recent-duplicate: same content hash inside the dedup window against L1 events.
    # SPEC §2.7: keep the scan O(recent_rows) rather than O(all_episodes) by pushing the
    # time-window cut into SQL; hash-equality is then checked in Python (cheap, and stays
    # independent of the store's content_hash column which may be empty for legacy rows).
    now = time.time()
    window = cfg.attention.dedup_window_s
    needle = _hash(content)
    since = now - window
    cur = engine.store.conn.execute(
        "SELECT content FROM memories "
        "WHERE workspace = ? AND layer = ? AND created_at >= ?",
        (cfg.workspace, Layer.EPISODIC.value, since),
    )
    for row in cur:
        if _hash(row["content"]) == needle:
            return GateDecision(action="drop", reason="recent duplicate")

    # ----- semantic layer: delegate to the LLM if configured, else pass -----
    agent = engine._get_agent("attention_gate")
    if agent is not None:
        return _llm_gate(agent, content)
    return GateDecision(action="pass")
```

关键变化:删除了原 `:76-78` 的 `stripped = content.strip()` + `if len(stripped) < cfg.attention.min_chars: return ... "too short"`;noise / recent-duplicate 块移到 `_get_agent` / `_llm_gate` **之前**;`agent is not None` 分支和末尾 `return pass` 移到最后。

- [ ] **Step 4: 运行新测试,确认 PASS**

Run: `uv run pytest tests/unit/test_attention_gate.py::test_gate_passes_short_when_no_llm -q`
Expected: PASS。

- [ ] **Step 5: 改 `test_remember_drop_returns_unpersisted_memory`——不再用 "hi",改用 noise 内容触发 drop**

把 `:69-73` 的:

```python
def test_remember_drop_returns_unpersisted_memory(engine):
    m = engine.remember("hi")  # too short -> drop
    assert m.metadata.get("gated") == "dropped"
    assert m.metadata.get("reason") == "too short"
    assert engine.store.get_memory(m.id) is None  # not persisted
```

替换为:

```python
def test_remember_drop_returns_unpersisted_memory(engine):
    # pure-noise content is dropped by the heuristic prefix.
    m = engine.remember("lol ok test asdf foo")  # noise -> drop
    assert m.metadata.get("gated") == "dropped"
    assert m.metadata.get("reason") == "noise"
    assert engine.store.get_memory(m.id) is None  # not persisted
```

- [ ] **Step 6: 改 `test_remember_working_layer_skips_gate`——用会被 drop 的内容证明 L0 bypass**

把 `:82-90` 的:

```python
def test_remember_working_layer_skips_gate(tmp_path):
    """L0 working memory is never gated even for short content."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        # "hi" would be dropped on L1/L2/L3 but L0 bypasses the gate entirely.
        m = e.remember("hi", layer=Layer.WORKING)
        assert m.metadata.get("gated") != "dropped"
    finally:
        e.close()
```

替换为:

```python
def test_remember_working_layer_skips_gate(tmp_path):
    """L0 working memory is never gated even for content the gate would drop."""
    e = Engine(Config.for_testing(tmp_path))
    try:
        # noise content would be dropped on L1/L2/L3 but L0 bypasses the gate entirely.
        m = e.remember("lol ok test asdf foo", layer=Layer.WORKING)
        assert m.metadata.get("gated") != "dropped"
    finally:
        e.close()
```

(原测试用 "hi",但 "hi" 现在在 L1/L2/L3 也 pass,无法证明 bypass;改用 noise 内容——它在 L1/L2/L3 会被 drop,在 L0 不 drop。)

- [ ] **Step 7: 加 `test_llm_gate_receives_short_content`——证明 "hi" 过 heuristic 后到达 LLM**

在 LLM mode 测试区(`test_remember_llm_pass` 之后,`test_gate_decision_dataclass_defaults` 之前)插入:

```python
def test_llm_gate_receives_short_content(tmp_path):
    """Short content ('hi') clears the heuristic prefix and reaches the LLM gate.

    This is the design's point: 'hi' is no longer a deterministic drop — with
    an LLM wired it is delegated to the semantic layer.
    """
    e = Engine(Config.for_testing(tmp_path))
    try:
        called: list[list[Message]] = []
        e._agents["attention_gate"] = _fake_gate(
            lambda msgs, schema: called.append(msgs) or {
                "action": "pass",
                "content": None,
                "reason": "worth keeping",
            }
        )
        d = attention_gate("hi", engine=e, layer=Layer.SEMANTIC)
        assert called  # the LLM really was invoked
        assert d.action == "pass"
    finally:
        e.close()
```

- [ ] **Step 8: 重写 `_llm_gate` 的 system prompt**

把 `src/ladym/operations/attention.py:114-125` 的 `msgs = [...]` 块替换为:

```python
    msgs = [
        {
            "role": "system",
            "content": (
                "You are the attention gate. Decide if the user content is worth "
                "storing as a long-term memory.\n"
                "- pass: content with information value — facts, decisions, events, "
                "preferences, code knowledge, etc.\n"
                "- drop: content with no information value — greetings (hi/hey), "
                "acknowledgements (ok/sure), small talk, fragments, pure emotion, etc.\n"
                "- rewrite: content has value but is poorly worded; return the cleaned-up "
                "text in `content`.\n"
                "Reply JSON {action, content?, reason}. action in pass|rewrite|drop."
            ),
        },
        {"role": "user", "content": content},
    ]
```

(原 prompt 只有 `"Decide if the user content is worth storing long-term. Reply JSON..."`,没编码任何判断原则;新 prompt 用自然语言描述 pass/drop/rewrite 的语义标准。)

- [ ] **Step 9: 运行整个 gate 测试文件,确认全绿**

Run: `uv run pytest tests/unit/test_attention_gate.py -q`
Expected: 13 passed(原 12 + 新增 `test_llm_gate_receives_short_content`)。

- [ ] **Step 10: commit**

```bash
git add src/ladym/operations/attention.py tests/unit/test_attention_gate.py
git commit -m "refactor(attention): noise/dup heuristic prefix, drop too-short rule" -m "Move noise + recent-duplicate ahead of the LLM call as a deterministic heuristic layer. Remove the too-short (min_chars) rule: short content like 'hi' now reaches the semantic layer (passes with no LLM, judged by the LLM prompt when configured). Rewrite the LLM system prompt with concrete pass/drop/rewrite criteria." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: CLI 测试适配

**Files:**
- Modify: `tests/unit/test_cli.py:187-208`(`test_cli_remember_drop_too_short`)

**Interfaces:**
- Consumes: `runner`、`app`、`db_arg` fixture、`ladym.sdk.open_engine`(均已存在)
- Produces: 无(仅测试)

- [ ] **Step 1: 把 `test_cli_remember_drop_too_short` 改成用 noise 内容测 drop**

打开 `tests/unit/test_cli.py`,把 `:187-208` 的整个函数替换为:

```python
def test_cli_remember_drop_noise(db_arg):
    """``ladym remember`` with pure-noise content is dropped by the heuristic
    prefix — exit 0, output surfaces the drop reason, and the memory is NOT
    persisted (the dropped Memory carries a non-existent fake id that we must
    not leak as ``id=``)."""
    from ladym.sdk import open_engine

    r = runner.invoke(
        app,
        ["remember", "lol ok test asdf foo", "--db", db_arg, "--workspace", "wsdrop"],
    )
    assert r.exit_code == 0, r.output
    assert "dropped" in r.output
    assert "reason=noise" in r.output
    # Red line: the non-persistent fake id must NOT be printed.
    assert "id=" not in r.output

    # The dropped content must not have been persisted (count returns a
    # {layer/type: n} dict; an empty workspace is {}).
    with open_engine(db_path=db_arg, workspace="wsdrop") as eng:
        assert eng.store.count(workspace="wsdrop") == {}
        assert not any(m.content == "lol ok test asdf foo" for m in eng.store.iter_memories(workspace="wsdrop"))
```

(改名 `test_cli_remember_drop_too_short` → `test_cli_remember_drop_noise`;内容从 `"hi"` 换成 `"lol ok test asdf foo"`;断言 `reason=too short` → `reason=noise`。仍证明 CLI 写路径 gate 接入。)

- [ ] **Step 2: 运行,确认 PASS**

Run: `uv run pytest tests/unit/test_cli.py::test_cli_remember_drop_noise tests/unit/test_cli.py::test_cli_remember_pass_persists -q`
Expected: 2 passed。

- [ ] **Step 3: 运行整个 test_cli.py,确认无回归**

Run: `uv run pytest tests/unit/test_cli.py -q`
Expected: 全绿(原数不变,函数改名)。

- [ ] **Step 4: commit**

```bash
git add tests/unit/test_cli.py
git commit -m "test(cli): switch drop test from too-short to noise content" -m "The too-short rule is gone; prove the CLI write-path gate still drops by using pure-noise content (reason=noise) instead of 'hi'." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 3: MCP 测试适配

**Files:**
- Modify: `tests/unit/test_mcp_server.py:119-131`(`test_mcp_remember_drop_too_short`)

**Interfaces:**
- Consumes: `server_with_engine` fixture、`json`(均已存在)
- Produces: 无(仅测试)

- [ ] **Step 1: 把 `test_mcp_remember_drop_too_short` 改成用 noise 内容测 drop**

打开 `tests/unit/test_mcp_server.py`,把 `:119-131` 的整个函数替换为:

```python
def test_mcp_remember_drop_noise(server_with_engine):
    """``remember`` with pure-noise content is dropped by the heuristic prefix —
    the response carries ``{"gated":"dropped","reason":"noise"}`` with null
    id/hash (never the non-persistent fake id), and nothing is persisted."""
    _, tools, eng = server_with_engine
    out = json.loads(tools["remember"]("lol ok test asdf foo", workspace="wsdrop"))
    assert out["gated"] == "dropped"
    assert out["reason"] == "noise"
    assert out["id"] is None
    assert out["hash"] is None

    # The dropped content must not have been persisted in any workspace.
    assert not any(m.content == "lol ok test asdf foo" for m in eng.store.iter_memories(workspace="wsdrop"))
```

- [ ] **Step 2: 运行,确认 PASS**

Run: `uv run pytest tests/unit/test_mcp_server.py::test_mcp_remember_drop_noise tests/unit/test_mcp_server.py::test_mcp_remember_pass_persists -q`
Expected: 2 passed。

- [ ] **Step 3: 运行整个 test_mcp_server.py,确认无回归**

Run: `uv run pytest tests/unit/test_mcp_server.py -q`
Expected: 全绿(`test_mcp_index_code` 若 flaky,单独重跑即 pass——非本 plan 引入)。

- [ ] **Step 4: commit**

```bash
git add tests/unit/test_mcp_server.py
git commit -m "test(mcp): switch drop test from too-short to noise content" -m "The too-short rule is gone; prove the MCP write-path gate still drops by using pure-noise content (reason=noise) instead of 'hi'." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 4: 文档同步(SPEC §2.7 + S09 + _conventions)

**Files:**
- Modify: `docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md:456-459`(§2.7)、`:267`(config 表 min_chars 行)
- Modify: `scenarios/S09-attention-gate.md`
- Modify: `scenarios/_conventions.md`(§8.1)

**Interfaces:** 无(纯文档)。

- [ ] **Step 1: 改 SPEC §2.7 的两模式描述**

打开 `docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md`,把 `:456-459` 的:

```
- **Heuristic mode (default, `provider="none"`):** drop if `len(content) < attention.min_chars`,
  or content hashes-equal to a recent L1 within `dedup_window_s`, or it matches the noise-word
  list. Otherwise pass. Zero deps.
- **LLM mode:** `complete_structured` returns `{action, content?, reason}`.
```

替换为:

```
- **Heuristic prefix (always runs, before any LLM call):** drop if content matches the
  noise-word list, or hashes-equal to a recent L1 within `dedup_window_s`. These are
  deterministic hard facts needing no semantics. Zero deps.
- **Semantic layer:** content that clears the prefix is delegated to the LLM
  (`complete_structured` → `{action, content?, reason}`) when `agents.attention_gate`
  resolves to a provider; otherwise it passes. Short content like `"hi"` reaches this layer
  (passes with no LLM; the LLM prompt judges it when configured).
```

- [ ] **Step 2: 改 SPEC config 表的 min_chars 行**

同文件 `:267` 附近,把:

```
| `attention.min_chars` | int | `8` | — | heuristic gate only |
```

替换为:

```
| `attention.min_chars` | int | `8` | — | retained for compat; gate no longer uses it (too-short rule removed) |
```

- [ ] **Step 3: 重写 S09 的 drop 矩阵**

打开 `scenarios/S09-attention-gate.md`。改动要点(逐处编辑):

1. **标题行下方的说明引用块**:把"gate 已于 2026-07-22 修复……MCP/CLI `remember` 现路由经 `eng.remember`"保留,但追加一句说明本次重构:
   > **2026-07-22 重构**:gate 改为 noise+dup 确定性前置 + LLM 语义层(详见 `docs/superpowers/specs/2026-07-22-attention-gate-noise-dup-prefix-design.md`)。`too short` 规则移除——"hi" 不再被确定性 drop。**drop 矩阵现在两种配置都成立(noise/dup 前置确定性 drop);"hi" 改为演示 LLM 增值。**

2. **Given**:删除"跑前 `export LADYM_LLM_PROVIDER=none` 强制 heuristic"那句及"embedding 非本地时一并 `export LADYM_EMBEDDING=hashing`"——本剧本不再需要关 LLM。改为:
   > gate 两层:heuristic 前置(noise 词表 / recent-duplicate hash,确定性 drop)+ 语义层(配了 LLM 交 LLM 判,否则 pass)。见 `src/ladym/operations/attention.py`。

3. **When 步骤 1**:`mcp__ladym__remember(content="hi", workspace="scn-s09")` → 预期改为:
   > **provider=none**:`{"id":..,"hash":..}`(pass,持久化);**配了 LLM**:交 LLM 判(软断言:gate 被调用、返回结构化结果,drop/pass 均可接受——演示 LLM 对短内容的语义判断)。

4. **When 步骤 2**(noise):`mcp__ladym__remember(content="lol ok test asdf foo", ...)` → 预期不变(drop `reason="noise"`),但强调"两种配置都确定性 drop"。

5. **When 步骤 3**(recent-duplicate):预期不变(drop `reason="recent duplicate"`),强调"两种配置都确定性 drop"。

6. **Then 断言**:
   - [硬] noise(步骤2)两路径返回 `gated=="dropped"`、`reason=="noise"`、id null。
   - [硬] recent-duplicate(步骤3)两路径返回 `gated=="dropped"`、`reason=="recent duplicate"`、id null。
   - [硬] "hi"(步骤1):provider=none 时 pass(持久化);配 LLM 时 gate 被调用(用返回含 `gated` 或持久化与否判定,不强求 drop/pass)。
   - [软] 给一句:noise/dup 是确定性硬过滤(配置无关);"hi" 的判断体现了 LLM 的语义增值(none 时 pass、LLM 时按语义)。

- [ ] **Step 4: 更新 `_conventions.md §8.1`**

打开 `scenarios/_conventions.md`,在 `§8.1` 末尾追加一段:

```markdown
### 8.1a gate 两层结构(2026-07-22 重构)
- **heuristic 前置**(确定性,LLM 调用前):noise(纯噪声词表)、recent-duplicate(近期 L1 同 content hash)→ drop。
- **语义层**:配了 LLM 交 LLM 判(pass/rewrite/drop);否则 pass。
- `too short` 规则已移除——"hi" 这类短内容不再被确定性 drop(none 模式 pass;LLM 模式交 prompt 判)。
- **对剧本的影响**:noise/dup 断言两种配置都成立(不再需要 `export LADYM_LLM_PROVIDER=none`);"hi" 类断言改为配置相关软断言。详见 S09。
```

- [ ] **Step 5: commit**

```bash
git add docs/superpowers/specs/2026-07-21-providers-config-control-plane-design.md scenarios/S09-attention-gate.md scenarios/_conventions.md
git commit -m "docs: sync SPEC §2.7, S09, _conventions to noise/dup-prefix gate" -m "SPEC §2.7: two-mode → heuristic-prefix + semantic-layer; mark min_chars retained-but-unused. S09: drop matrix now config-agnostic (noise/dup); 'hi' becomes an LLM-value demo. _conventions §8.1a: document the two-layer structure." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## 完成验证

- [ ] **全量回归**: `uv run pytest tests/unit/test_attention_gate.py tests/unit/test_cli.py tests/unit/test_mcp_server.py -q` → 全绿。
- [ ] **手测(可选,真实 LLM 配置)**:重启 MCP server 加载新代码后,`mcp__ladym__remember("hi", workspace="scn-s09")` 应进入 LLM 判定(不再确定性 drop);`remember("lol ok test asdf foo", ...)` 应 drop `reason=noise`(heuristic 前置)。
