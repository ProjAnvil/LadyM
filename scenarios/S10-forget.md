# S10 — forget 删除

| 覆盖层 | 维护 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s10`;取 `<db>`;reset。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s10 待删除的记忆 XYZ", workspace="scn-s10")` → `id_a`
2. [CLI] `! ladym stats -w scn-s10 --db <db>` → 记该 ws L2 计数 N(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)
3. [MCP] `mcp__ladym__forget(memory_id=id_a)` → 看返回
4. [MCP] `mcp__ladym__recall(query="scn-s10 待删除", workspace="scn-s10")`
5. [CLI] `! ladym stats -w scn-s10 --db <db>` → 记该 ws 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)

## Then
- [硬] 步骤3 forget 返回 `{"forgotten": id_a}`
- [硬] 步骤4 召回结果不含 `id_a`
- [硬] 步骤5 L2 计数 = N-1

## Teardown
reset scn-s10(已 forget,基本干净)。
