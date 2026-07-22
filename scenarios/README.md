# LadyM Agent E2E Scenarios

面向 agent 执行的端到端剧本,验证 ladyM 在 **MCP + CLI** 两条路径下的 **L1–L6** 行为契约。

## 这是什么
15 个 Markdown 剧本(S01–S15),每个自包含(Given/When/Then + 分级断言)。由 agent(如 Claude Code)逐本执行,据断言判定 pass/fail,最后汇总。与 `tests/`(pytest SDK 测试)互补:这里测的是 agent 真实使用的 CLI/MCP 路径。

## 如何执行一个剧本
1. 读它的 Given/When/Then。
2. 第 0 步 `mcp__ladym__stats()` 取 `<db>`;之后所有 CLI 调用带 `--db <db>`(见 _conventions §1)。
3. 逐步执行 When,每步记录返回值(id 等)。
4. 对每个 Then 断言判定 ✅/❌(硬断言用工具结果核对;软断言给理由)。
5. Teardown:reset workspace(见 _conventions §3)。
6. 全部跑完输出汇总表(见 _conventions §7)。

## 剧本索引
| # | 标题 | 覆盖 | 路径 | 需LLM |
|---|------|------|------|------|
| S01 | 写入→召回闭环(fact) | L2 | MCP+CLI | 否 |
| S02 | episodic 记录与召回 | L1 | MCP+CLI | 否 |
| S03 | 代码索引与符号召回 | L2(code) | MCP+CLI | 否 |
| S04 | consolidate L1→L2 | L1→L2 | MCP+CLI | 否 |
| S05 | proceduralize→playbook | L3 | CLI worker | 否 |
| S06 | link + tier2 扩展 | L4 | MCP+CLI | 否 |
| S07 | L5 mental model(条件) | L5 | CLI worker | 条件 |
| S08 | L6 forward-intent(条件) | L6 | CLI worker | 条件 |
| S09 | attention gate:drop 矩阵 | gate | MCP+CLI+SDK | 否 |
| S10 | forget 删除 | 维护 | MCP+CLI | 否 |
| S11 | workspace 隔离 | 隔离 | MCP+CLI | 否 |
| S12 | 增量索引 | L2(code) | MCP+CLI | 否 |
| S13 | 空召回/不崩溃 | 健壮性 | MCP+CLI | 否 |
| S14 | MCP↔CLI 一致性 | 一致性 | 跨路径 | 否 |
| S15 | decay 不伤 L2/L3 | 衰减 | CLI worker | 否 |

## 约定与骨架
- 共享约定(db 对齐 / worker 不对称 / reset / 命名 / 判定 / 报告):见 `_conventions.md`。
- 剧本骨架:见 `_template.md`。

## 相关
- 设计:`docs/superpowers/specs/2026-07-22-agent-e2e-scenarios-design.md`
- SDK 层 e2e 测试:`tests/integration/test_end_to_end.py`