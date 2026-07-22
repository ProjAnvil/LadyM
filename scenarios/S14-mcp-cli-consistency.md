# S14 — MCP↔CLI 一致性专项

| 覆盖层 | 一致性 | 路径 | 跨路径 | 需LLM | 否 |

## Given
- workspace `scn-s14`;取 `<db>`。本剧本核心:证明 MCP 与 CLI 同库同 ws 互见("same engine" 契约)。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s14 经 MCP 写入的共享事实", workspace="scn-s14")` → `id_a`(返回仅 `{id,hash}`)
2. [CLI] `! ladym recall "scn-s14 经 MCP 写入" -w scn-s14 --db <db>` → CLI 能否召回到 `id_a`?
3. [CLI] `! ladym remember "scn-s14 经 CLI 写入的共享事实" -w scn-s14 --db <db>` → `id_b`
4. [MCP] `mcp__ladym__recall(query="scn-s14 经 CLI 写入", workspace="scn-s14")` → MCP 能否召回到 `id_b`?

## Then
- [硬] 步骤2 CLI 结果含 `id_a`(CLI 见到 MCP 写入;召回项 `source=mcp` 可作旁证)
- [硬] 步骤4 MCP 结果含 `id_b`(MCP 见到 CLI 写入;召回项 `source=cli` 可作旁证)
- → 两条路径同库同 ws 互见,一致性契约成立(2026-07-22 试跑双向通过)

## Teardown
reset scn-s14。
