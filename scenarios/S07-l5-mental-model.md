# S07 — L5 mental model(条件分支)

| 覆盖层 | L5 | 路径 | CLI worker(MCP observe) | 需LLM | 条件分支 |

## Given
- workspace `scn-s07`;取 `<db>`;reset。
- 灌 ≥ `min_episodes_to_run`(默认 3)条 L1 + ≥3 条相似 L2 fact(L5 抽取对象为 L2/L3,`l5_min_cluster_size=3`、`l5_cluster_similarity=0.65`):

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s07 build feature", observation="...", outcome="success", workspace="scn-s07")`
2. [MCP] `mcp__ladym__remember(content="scn-s07 认证模块使用 JWT 24h", workspace="scn-s07")`;同样再写两条高度相似的认证 fact(共 3 条 L2,易聚类)
3. 前置确认:`! echo $LADYM_LLM_PROVIDER` 据此选分支
4. [CLI] `! ladym worker --once -w scn-s07 --db <db>`
5. [MCP] `mcp__ladym__stats(workspace="scn-s07")`
6. [MCP] `mcp__ladym__recall(query="scn-s07 mental model", workspace="scn-s07")`

## Then
- **分支 A(provider 为空/none)**:[硬] 步骤4 worker 退出码 0;步骤5 无 `L5_mental` 记忆(skip 契约)。
- **分支 B(配了 LLM)**:[硬] 步骤5 出现 `L5_mental`/`type=mental_model`;步骤6 召回 mental model;该 model 经 `abstracts` 边连到成员(steps 中可用 stats `edges` 增加佐证)。

## Teardown
reset scn-s07。
