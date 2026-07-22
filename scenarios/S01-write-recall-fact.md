# S01 — 写入→召回闭环(fact)

| 覆盖层 | L2 | 路径 | MCP+CLI | 需LLM | 否 |

## Given
- workspace `scn-s01`;第 0 步 `mcp__ladym__stats()` 取 `<db>`;reset(_conventions §3)。

## When
1. [MCP] `mcp__ladym__remember(content="scn-s01 认证模块使用 JWT,有效期 24 小时", tags=["auth"], source="s01", workspace="scn-s01")` → 记返回 `id_a`(返回仅含 `{id,hash}`,见 _conventions §8.1)
2. [CLI] `! ladym remember "scn-s01 密码用 bcrypt 加盐哈希存储" -w scn-s01 --db <db> --tags auth` → 从输出 `remembered id=...` 取 `id_b`
3. [MCP] `mcp__ladym__recall(query="scn-s01 认证 JWT 过期", workspace="scn-s01")` → 看结果
4. [CLI] `! ladym recall "scn-s01 密码哈希" -w scn-s01 --db <db>` → 看结果
5. [CLI] `! ladym stats -w scn-s01 --db <db>` → 记该 ws `L2_semantic` 计数(用 CLI;MCP `stats(workspace=)` 返回全局,见 _conventions §8.2)

## Then
- [硬] 步骤3/4 的 recall 结果里两条记忆 `layer=L2_semantic`、`type=fact`(写入返回不含 layer/type,须从 recall 确认)
- [硬] 步骤3 结果含 `id_a`;步骤4 结果含 `id_b`
- [硬] 步骤5 该 ws `L2_semantic` 计数 ≥ 2
- [软] 用异措辞 `mcp__ladym__recall(query="scn-s01 登录令牌时效", workspace="scn-s01")` 召回时,步骤1 记忆仍在前 3(给理由:JWT/令牌/时效 与认证令牌语义相近)

## Teardown
reset scn-s01。
