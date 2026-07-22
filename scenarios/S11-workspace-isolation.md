# S11 — workspace 隔离

| 覆盖层 | 隔离 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s11a`、`scn-s11b`;取 `<db>`;两者 reset。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s11a 团队 A 的部署密钥 abc", workspace="scn-s11a")` → `id_a`
2. [MCP] `mcp__ladym__recall(query="scn-s11 部署密钥", workspace="scn-s11a")` → 应命中
3. [MCP] `mcp__ladym__recall(query="scn-s11 部署密钥", workspace="scn-s11b")` → 应空
4. [CLI] `! ladym recall "scn-s11 部署密钥" -w scn-s11a --db <db>` → 命中
5. [CLI] `! ladym recall "scn-s11 部署密钥" -w scn-s11b --db <db>` → 空

## Then
- [硬] 步骤2/4 结果含 `id_a`(scn-s11a 可见)
- [硬] 步骤3/5 结果不含;步骤5 CLI 输出 `no memories matched`

## Teardown
reset scn-s11a、scn-s11b。
