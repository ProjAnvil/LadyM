# S09 — attention gate:agent 路径绕过(仅 SDK 生效)

| 覆盖层 | gate | 路径 | MCP+CLI(+SDK 对照) | 需LLM | 否 |

> **校准结论(2026-07-22 试跑):** SPEC C5 称 `remember` 走 gate,但 MCP/CLI 的 `remember` 直接调 `semantic.put_fact`、**不经 `eng.remember`**,故 gate 在 agent 写路径上**不生效**——下列 drop 用例经 MCP/CLI `remember` 全部**持久化**。gate 的 drop 行为仅在 SDK `eng.remember()` 上成立。详见 `_conventions §8.1`。

## Given
- workspace `scn-s09`;取 `<db>`;reset。
- gate 判定顺序(**仅 SDK 路径生效**):too short(`< min_chars=8`)→ noise(全 token 命中噪声词 `lol/ok/test/asdf/foo/bar/todo`)→ recent duplicate(与 `dedup_window_s`=3600s 内某 L1 同 content hash)。见 `src/ladym/operations/attention.py:75-102`。

## When
1. [MCP] `mcp__ladym__remember(content="hi", workspace="scn-s09")` → 返回 id;**预期** drop,**实测持久化**
2. [MCP] `mcp__ladym__remember(content="lol ok test asdf foo", workspace="scn-s09")` → 返回 id;**预期** drop,**实测持久化**
3. 先建 L1 事件:`! ladym record --agent x --action "scn-s09 dup" --observation obs1 --outcome ok -w scn-s09 --db <db>`;其 content 串为 `agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok`(见 _conventions §8.3);再 `[MCP] mcp__ladym__remember(content="agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok", workspace="scn-s09")` → **预期** recent-duplicate drop,**实测持久化**
4. [MCP] `mcp__ladym__remember(content="scn-s09 这是一条正常的足够长的语义记忆用于 pass 对照", workspace="scn-s09")` → pass(持久化)
5. [CLI] `! ladym stats -w scn-s09 --db <db>` → 记 L2 计数
6. SDK 对照:`! uv run pytest tests/unit/test_attention_gate.py -q` → 确认 `engine.remember("hi")` 等返回 `metadata.gated="dropped"`

## Then
- [硬] 步骤5 该 ws `L2_semantic` fact 计数 = 4(步骤1-4 **全部持久化**)——证明 agent 路径 `remember` **未** 应用 gate(与 SPEC C5 不符,记录为代码缺口)
- [硬] 步骤6 SDK 单测通过:drop 矩阵(too short / noise / recent duplicate)仅在 `eng.remember()` 上生效
- [软] 给一句判断:agent 路径目前缺乏噪声防护;若需在 agent 写入触发 gate,应让 MCP/CLI `remember` 路由经 `eng.remember`(待修)

## Teardown
reset scn-s09。
