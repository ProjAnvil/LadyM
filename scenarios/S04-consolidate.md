# S04 — consolidate L1→L2

| 覆盖层 | L1→L2 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s04`;取 `<db>`;reset。
- 先灌 ≥ `min_episodes_to_trigger`(默认 3)条同主题 L1 事件:

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s04 deploy to prod", observation="ran deploy.sh release N", outcome="success", workspace="scn-s04")`(N=1,2,3)
2. [MCP] `mcp__ladym__consolidate(workspace="scn-s04")` → 记报告 JSON(`promoted_to_semantic`、`actions`)
3. [MCP] `mcp__ladym__stats(workspace="scn-s04")`
4. [CLI] `! ladym consolidate -w scn-s04 --db <db>`(二次,应多为 NOOP)

## Then
- [硬] 步骤2 `promoted_to_semantic >= 1`;`actions` 含 `ADD/UPDATE/DELETE/NOOP` 键
- [硬] 步骤3 `L2_semantic` 出现 fact(consolidate 产物)
- [硬] 步骤4 第二次后 ADD 不再增长(幂等,NOOP 占主导)

## Teardown
reset scn-s04。
