# S09 — attention gate:drop 矩阵 + pass 对照

| 覆盖层 | gate | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s09`;取 `<db>`;reset。
- gate 判定顺序:too short(`< min_chars=8`)→ noise(全 token 命中噪声词)→ recent duplicate(与 `dedup_window_s`=3600s 内某 L1 同 content hash)。见 `src/ladym/operations/attention.py:75-102`。`remember` 走 gate;`record_event` 绕过(C5)。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s09 hi", workspace="scn-s09")` → 长度 9 但有效内容短;改用 `content="hi"`(< 8 字符)→ too short(应 drop)
2. [MCP] `mcp__ladym__remember(content="lol ok test asdf foo", workspace="scn-s09")` → 全噪声且 ≥8 字符 → noise(应 drop)
3. [MCP] `mcp__ladym__record_event(agent="x", action="scn-s09 dup", observation="obs1", outcome="ok", workspace="scn-s09")`;该事件 content 字符串为 `agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok`;再 `mcp__ladym__remember(content="agent=x | action=scn-s09 dup | observation=obs1 | outcome=ok", workspace="scn-s09")` → 与近 1h 内 L1 同 content hash → recent duplicate(应 drop)
4. [MCP] `mcp__ladym__remember(content="scn-s09 这是一条正常的足够长的语义记忆用于 pass 对照", workspace="scn-s09")` → pass
5. [MCP] `mcp__ladym__stats(workspace="scn-s09")`
6. [MCP] `mcp__ladym__recall(query="scn-s09", workspace="scn-s09")`

## Then
- [硬] 步骤5 该 ws `L2_semantic` fact 计数 = 1(仅步骤4 的 pass 写入;步骤1-3 均 drop)
- [硬] 步骤6 召回结果只含步骤4 那条("正常...语义记忆"),不含步骤1-3 的内容
- [软] 若 MCP `remember` 返回值含 `gated` 标记则直接对 1-3 判 ✅;若不暴露,以 stats 计数 = 1 间接证明(给理由)

## Teardown
reset scn-s09。
