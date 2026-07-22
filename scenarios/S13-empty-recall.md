# S13 — 空召回 / 不崩溃

| 覆盖层 | 健壮性 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s13`;取 `<db>`;reset(空)。

## When
1. [MCP] `mcp__ladym__recall(query="zzqqxx123 不存在的奇异查询 scn-s13", workspace="scn-s13")` → 看 `results`、`tier_reached`
2. [CLI] `! ladym recall "zzqqxx123 不存在 scn-s13" -w scn-s13 --db <db>` → 看输出

## Then
- [硬] 步骤1 `results` 为空、`tier_reached=1`、不抛异常
- [硬] 步骤2 CLI 输出 `no memories matched`、退出码 0

## Teardown
reset scn-s13(本就空)。
