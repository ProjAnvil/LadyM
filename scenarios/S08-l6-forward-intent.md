# S08 — L6 forward-intent(条件分支)

| 覆盖层 | L6 | 路径 | CLI worker(MCP observe) | 需LLM | 条件分支 |

## Given
- workspace `scn-s08`;取 `<db>`;reset。
- 灌若干 L1 事件(作为预测输入):

## When
1. [MCP] 连续 3 次 `mcp__ladym__record_event(agent="claude", action="scn-s08 edit auth", observation="...", outcome="success", workspace="scn-s08")`
2. 前置确认:`! echo $LADYM_LLM_PROVIDER` 据此选分支
3. [CLI] `! ladym worker --once -w scn-s08 --db <db>`
4. [MCP] `mcp__ladym__stats(workspace="scn-s08")`
5. [MCP] `mcp__ladym__recall(query="scn-s08 predicted intent", workspace="scn-s08")`

## Then
- **分支 A(provider 为空/none)**:[硬] 步骤3 worker 退出码 0;步骤4 无 `L6_predictive` 记忆(skip 契约)。
- **分支 B(配了 LLM)**:[硬] 步骤4 出现 `L6_predictive`/`type=forward_intent`;其 metadata `valid_to > now`(用 `! sqlite3 <db> "SELECT json_extract(metadata,'$.valid_to') FROM memories WHERE workspace='scn-s08' AND layer='L6_predictive'"` 验证)。

## Teardown
reset scn-s08。
