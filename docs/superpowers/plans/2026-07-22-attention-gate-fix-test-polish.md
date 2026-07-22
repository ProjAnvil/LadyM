# attention-gate-fix Test Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 收紧 gate-fix（`feat/attention-gate-fix` 分支）final review 标记的 3 个非阻塞测试 Minor——让 drop/pass 测试断言更精确、覆盖更全。

**Architecture:** 纯测试改动（只动 `tests/unit/test_cli.py` + `tests/unit/test_mcp_server.py`），不改任何实现代码。gate-fix 实现已 final review（opus）Approved 且全套 224 passed；这些 polish 让测试更自描述、更严格。3 项 Minor 归入 2 个 task：Task 1 收紧现有断言（`hash=` 断言 + `iter_memories` workspace 过滤），Task 2 补 MCP drop 矩阵的两个缺失用例（noise + recent-duplicate）。

**Tech Stack:** pytest, typer CliRunner, FastMCP tool registry, ladym SDK (`open_engine`)。

## Global Constraints

- **分支**：在 `feat/attention-gate-fix`（tip = `c48ef90`）上工作。该分支已 final review Approved。开工前 `git checkout feat/attention-gate-fix` 确认在其上。
- **只改测试**：仅 `tests/unit/test_cli.py` + `tests/unit/test_mcp_server.py`。**不要动** `src/`、`scenarios/`、`eng.remember`、`attention_gate`——实现已对，这是测试收紧。
- **预期 pass**：实现已正确，新/改断言应直接 pass。若新断言失败，说明断言写错或发现了实现 bug——**停下来调查**，不要弱化断言绕过。
- **API 事实**（直接用，勿臆测）：
  - `store.iter_memories(*, workspace=None, layer=None, type_=None)`（`src/ladym/storage/store.py:273`）支持 `workspace` 参数。
  - CLI pass 输出格式：`remembered id=<32hex> hash=<8hex>`（`src/ladym/cli.py:87`）——含 `hash=`。
  - MCP drop 返回：`{"id":null,"hash":null,"gated":"dropped","reason":<..>}`（`src/ladym/mcp/server.py:104-107`）；pass 返回 `{"id":..,"hash":..}`（无 `gated` 键）。
  - `episodic.record(agent, action, observation)`（不传 outcome）渲染 content = `agent={a} | action={b} | observation={c}`（见 `tests/unit/test_attention_gate.py:50-57` 的验证模式）。
  - heuristic gate（`llm_provider=none`，`Config.for_testing` 默认）：too short（`< min_chars=8`）→ noise（全 token 命中 `{lol,ok,test,asdf,foo,bar,todo}`）→ recent duplicate（`dedup_window_s=3600s` 内同 L1 content hash）。
- **commit**：英文 conventional commits + `Co-Authored-By: Claude <noreply@anthropic.com>`。显式路径 `git add tests/unit/...`（不要 `-A`/`.`）。
- **验证命令**：`uv run pytest tests/unit/test_cli.py tests/unit/test_mcp_server.py tests/unit/test_attention_gate.py -q`。注意 `test_mcp_index_code` 偶发受 test-ordering 影响 flaky（单独 `uv run pytest tests/unit/test_mcp_server.py -q` 重跑即 pass）——非本 plan 范围，遇到重跑确认即可。

---

### Task 1: 收紧 drop/pass 断言（`hash=` + workspace 过滤）

**Files:**
- Modify: `tests/unit/test_cli.py`（`test_cli_remember_drop_too_short` ~L187-208、`test_cli_remember_pass_persists` ~L211-224）
- Modify: `tests/unit/test_mcp_server.py`（`test_mcp_remember_drop_too_short` ~L119-131、`test_mcp_remember_pass_persists` ~L134-144）

**Why:** final review 标 (a) `test_cli_remember_pass_persists` 没断言 `hash=`；(b) 两文件的 `iter_memories()` 未传 workspace（扫全库，断言不够 self-documenting，共享 db 下可能误报）。收紧后精确锁定目标 workspace、并验证 pass 输出完整。

- [ ] **Step 1: `test_cli_remember_pass_persists` 加 `hash=` 断言**

在 `tests/unit/test_cli.py` 的 `test_cli_remember_pass_persists`，把：
```python
    assert "remembered" in r.output
    assert "id=" in r.output
    assert "dropped" not in r.output
```
改为：
```python
    assert "remembered" in r.output
    assert "id=" in r.output
    assert "hash=" in r.output  # pass 输出含 hash（cli.py:87）
    assert "dropped" not in r.output
```

- [ ] **Step 2: test_cli 的 `iter_memories` 加 workspace 过滤**

在 `tests/unit/test_cli.py`：

`test_cli_remember_drop_too_short` 内，把：
```python
        assert not any(m.content == "hi" for m in eng.store.iter_memories())
```
改为：
```python
        assert not any(m.content == "hi" for m in eng.store.iter_memories(workspace="wsdrop"))
```

`test_cli_remember_pass_persists` 内，把：
```python
        assert any(m.content == content for m in eng.store.iter_memories())
```
改为：
```python
        assert any(m.content == content for m in eng.store.iter_memories(workspace="default"))
```
（CLI `remember` 无 `-w` → workspace 默认 `"default"`；`_isolate_config` 清了 `LADYM_*` env，故 `open_engine(db_path=db_arg)` 也是 `"default"`。）

- [ ] **Step 3: test_mcp 的 `iter_memories` 加 workspace 过滤**

在 `tests/unit/test_mcp_server.py`：

`test_mcp_remember_drop_too_short` 内，把：
```python
    assert not any(m.content == "hi" for m in eng.store.iter_memories())
```
改为：
```python
    assert not any(m.content == "hi" for m in eng.store.iter_memories(workspace="wsdrop"))
```

`test_mcp_remember_pass_persists` 内，把：
```python
    assert any(m.content == content for m in eng.store.iter_memories())
```
改为：
```python
    assert any(m.content == content for m in eng.store.iter_memories(workspace="wspass"))
```

- [ ] **Step 4: 跑测试确认 pass**

Run: `uv run pytest tests/unit/test_cli.py tests/unit/test_mcp_server.py tests/unit/test_attention_gate.py -q`
Expected: 全 pass（实现已对）。若 `test_mcp_index_code` 偶发 fail（ordering flaky），`uv run pytest tests/unit/test_mcp_server.py -q` 重跑确认 pass。

- [ ] **Step 5: commit**

```bash
git add tests/unit/test_cli.py tests/unit/test_mcp_server.py
git commit -m "test: tighten gate-fix drop/pass assertions (hash= + workspace scoping)" \
  -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 补 MCP drop 矩阵的 noise + recent-duplicate 用例

**Files:**
- Modify: `tests/unit/test_mcp_server.py`（在 `test_mcp_remember_pass_persists` 之后追加两个测试函数）

**Why:** gate-fix Task 2 review 标"只覆盖 too short reason"。noise + recent-duplicate 两条 gate 分支虽由 `test_attention_gate.py` 在 SDK 层覆盖，但 MCP tool 路径未测——补全让 MCP drop 矩阵三路径（too short / noise / recent duplicate）都有 MCP-level 测试。

**Interfaces:**
- Consumes: `server_with_engine` fixture（已存在，`test_mcp_server.py:30-36`）；MCP tools `remember` / `record_event`（经 `_tools(server)` 字典访问）。
- gate 行为：heuristic 模式（`Config.for_testing` → `llm_provider=none`）下，noise content → drop `reason="noise"`；与近期 L1 episodic 同 content hash → drop `reason="recent duplicate"`。

- [ ] **Step 1: 加 `test_mcp_remember_drop_noise`**

在 `tests/unit/test_mcp_server.py` 末尾（`test_mcp_remember_pass_persists` 之后）追加：
```python
def test_mcp_remember_drop_noise(server_with_engine):
    """remember() of content composed entirely of noise tokens is dropped as noise."""
    _, tools, eng = server_with_engine
    out = json.loads(tools["remember"]("lol ok test asdf foo", workspace="wsnoise"))
    assert out["gated"] == "dropped"
    assert out["reason"] == "noise"
    assert out["id"] is None
    assert out["hash"] is None
    # The noise content must not have been persisted in any workspace.
    assert not any(
        m.content == "lol ok test asdf foo"
        for m in eng.store.iter_memories(workspace="wsnoise")
    )
```

- [ ] **Step 2: 加 `test_mcp_remember_drop_recent_duplicate`**

紧接上文追加：
```python
def test_mcp_remember_drop_recent_duplicate(server_with_engine):
    """remember() of content identical to a recent L1 episodic event is dropped
    as a recent duplicate (same content hash within dedup_window_s)."""
    _, tools, eng = server_with_engine
    # Seed an L1 episodic event in wsdup; record_event(agent, action, observation)
    # renders content as "agent=.. | action=.. | observation=.." (no outcome appended).
    tools["record_event"](
        agent="x", action="y", observation="exact dup content", workspace="wsdup"
    )
    dup = "agent=x | action=y | observation=exact dup content"
    out = json.loads(tools["remember"](dup, workspace="wsdup"))
    assert out["gated"] == "dropped"
    assert out["reason"] == "recent duplicate"
    assert out["id"] is None
    assert out["hash"] is None
    # The dropped remember must NOT have persisted an L2 fact (only the L1 event exists).
    assert list(eng.store.iter_memories(workspace="wsdup", layer="L2_semantic")) == []
```

- [ ] **Step 3: 跑测试确认 pass**

Run: `uv run pytest tests/unit/test_mcp_server.py -q`
Expected: 全 pass（含两个新测试）。若 `recent duplicate` 没触发（`reason` 不符），核对 `record_event` 渲染的 content 串是否与 `dup` 完全一致——参照 `tests/unit/test_attention_gate.py:50-57` 的模式（record 无 outcome → content 到 observation）。

- [ ] **Step 4: 跑相关子集 + commit**

Run: `uv run pytest tests/unit/test_mcp_server.py tests/unit/test_cli.py tests/unit/test_attention_gate.py -q`
Expected: 全 pass。
```bash
git add tests/unit/test_mcp_server.py
git commit -m "test: cover noise + recent-duplicate drop reasons in MCP remember" \
  -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review（plan 作者已核对）

- **Spec 覆盖**：3 项 Minor（`hash=` 断言 / workspace 过滤 / noise+duplicate 用例）→ Task 1 覆盖前两项，Task 2 覆盖第三项。无遗漏。
- **Placeholder**：无 TBD/TODO，每步有精确代码与命令。
- **类型一致**：`iter_memories(workspace=)` 签名已核对（store.py:273）；`record_event` content 渲染模式已核对（test_attention_gate.py:50-57）。
- **范围**：纯测试改动，不碰实现；2 个 task 各自独立可测、可 review，无同函数并发冲突。
