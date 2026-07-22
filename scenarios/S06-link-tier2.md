# S06 — link + tier2 扩展

| 覆盖层 | L4 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s06`;取 `<db>`;reset。
- 先建两条相关 fact:

## When
1. [MCP] `mcp__ladym__remember(content="scn-s06 锚点事实 xyzzy 含义模糊", workspace="scn-s06")` → `id_a`
2. [MCP] `mcp__ladym__remember(content="scn-s06 xyzzy 的邻居阐述其真实含义", workspace="scn-s06")` → `id_b`
3. [MCP] `mcp__ladym__link(src=id_a, dst=id_b, relation="elaborates")` → 记 edge id
4. [CLI] `! ladym stats -w scn-s06 --db <db>` → 记该 ws 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)
5. [MCP] `mcp__ladym__recall(query="scn-s06 xyzzy", workspace="scn-s06")` → 看 `tier_reached` 与结果

## Then
- [硬] 步骤3 返回 edge 含 `src=id_a`、`dst=id_b`、`relation=elaborates`
- [硬] 步骤4 `edges >= 1`
- [硬] 步骤5 结果含 `id_a`(锚点被召回);若启用 tier2,`tier_reached` 可为 2

## Teardown
reset scn-s06。
