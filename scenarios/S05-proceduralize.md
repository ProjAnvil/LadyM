# S05 — proceduralize → L3 playbook

| 覆盖层 | L3 | 路径 | CLI worker(MCP observe) | 需LLM | 否 |

## Given
- workspace `scn-s05`;取 `<db>`;reset。
- 先灌 ≥3 条 `outcome=success` 的同类 L1 事件(C10:相似度阈值 0.55,`min_cluster_size=3`):

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s05 deploy to prod", observation="ran deploy.sh", outcome="success", workspace="scn-s05")`
2. [CLI] `! ladym worker --once -w scn-s05 --db <db>`(触发 consolidate + proceduralize + L5/L6 skip + decay)
3. [MCP] `mcp__ladym__stats(workspace="scn-s05")`
4. [MCP] `mcp__ladym__recall(query="scn-s05 deploy playbook", workspace="scn-s05")`

## Then
- [硬] 步骤2 worker 退出码 0(不报错)
- [硬] 步骤3 出现 `L3_procedural` 层 / `type=playbook`
- [硬] 步骤4 召回到 playbook,其 summary/content 含动作词 `deploy` + `(3 episodes)`

## Teardown
reset scn-s05。
