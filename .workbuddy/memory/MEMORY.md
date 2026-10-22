# LadyM 项目长期记忆

## Hook 写入侧事实（2026-10-21 核实，勿再重复调研）

- **`ladym hook` 是纯观察者，不做上下文回注**：stdin 读事件、落库、exit 0，stdout 恒为空（cli/hook.go:16-19，注释明写 pure observer）。ARCHITECTURE.md 措辞为 "hook carrier"，不是 injector。
- **全仓库唯一注入实现**是 `langgraph/nodes.go:54 CreateRecallNode`（topK=6，前缀 "Relevant long-term memory:"，格式 `prefix\n- {Summary||Content}`）。
- **写入路径零 LLM token**：engine/scope.go:142-180 不调 LLM，也不过 attention gate（只有 `Remember` 过 gate，`RecordOperation` 不过）。`providers/` 内无任何 token usage 记录，接真 LLM 也统计不到消耗。真实 LLM 消耗只在下游 worker 的 MaintainL4 / ExtractL5。
- **每个工具调用 fork 一次进程**（非常驻服务）：agent 感知延时 = 进程冷启动 + sqlite 打开 + 写入管线，冷启动大概率是大头（待评测验证）。
- **`EpisodicMemory.Record` 重复存储 observation/outcome**：同时写进 Content（`agent=..|action=..|observation=..|outcome=..`）和 Metadata（`layers/episodic.go:29-52`），落库 token 约翻倍。

## 评测基础设施（2026-10-21）

- **eval/ 用 Go，不用 uv+Python**。硬依据：`tiktoken-go/tokenizer v0.8.1` 已在依赖树（go.mod:38 经 langchain-golang 间接引入），BPE 词表是编译进二进制的 Go 源码（codec/*_vocab.go），零网络零 API key；Python 侧 benchmarks/.venv 实测无 tiktoken。
- 零侵入测量可行性：`eng.Store/Provider/Config`、`Scope.Episodic`、`operations.NormalizeAction/ResolveResource` 全部导出，eval 可用同一实例复现写入管线各段并用 drift 交叉校验。
- 端到端保真测试已存在于 `cli/hook_test.go:35`（仅 PostToolUse、进程内调 hookCmd 测不到 fork、不断言截断、不含 Codex）。别再误判为完全没有。

## eval/ 评测台（2026-10-21 已交付，分支 feat/hook-write-path-eval）
- **入口 `make eval-hook`**，产物 `eval/.cache/reports/<时间戳>/{metrics.json,report.md}`，约 45s。全部 Go 文件带 `eval` build tag（`doc.go` 除外，避免 "build constraints exclude all Go files"）。
- 实测基线（darwin/arm64）：spawn p50 **10.6 / 13.2 / 30.9ms**（100/1k/10k 行），进程内 real 仅 0.26/0.33/1.59ms。**95% 体感耗时与写记忆无关**。
- **引擎打开随规模线性增长 0.6→2.1→15.2ms**，是进程税主体，疑似每次调用重新加载向量索引。这是优化 ladyM 体感延时的第一靶点。
- **中文切词是唯一真瓶颈**：2000 rune 中文 348ms，英文几乎无额外开销，约 0.1ms/字（非线性字典加载）。
- 写入放大 1.5-2.3x（observation/outcome 在 Content 与 Metadata 双写），已用测试钉住。
- 方法论：复制文件后立刻 spawn 测不到「冷」（文件在页缓存里）；不预热就平均会让首调虚高 20 倍；同一 payload 重复采样会让 p50==p95。、不含 Codex）。别再误判为完全没有。

## macOS grep 铁律（踩过 3 次）

- **BSD grep 不支持 `\|` 转义**（BRE 下当字面量）。用 bash `grep "a\|b"` 会静默返回空 → 误判"依赖/函数不存在"。已导致误判 go.mod 无 tiktoken、tokenizer.go 无导出符号。
- **检索一律用 Grep 工具**；bash grep 若必须用则改用 `grep -E "a|b"`。Grep 输出也别设过小 head_limit（会截断误导结论）。

## 沟通偏好（用户明确要求）

- 技术论述用直白术语：说"问题/潜在问题"，不用"病灶"等比喻词；用标准术语（如"悬挂边 dangling edge"）并解释机制，不用"幽灵"之类的比喻性说法。
- 时间度量一律叫**「耗时」**，不用「墙钟」（wall-clock 行话，用户不喜欢）。也不要用「系统时间」指代时间间隔 —— 系统时间是当前的日期时刻，两次相减会被 NTP 校正打断而算出负数。
- 代码里的时间段字段用单调时钟（`time.Since()` 读 `time.Now()`），字段名带单位后缀 `_ms`，不叠 `duration_` 前缀。
- 正文优先用自然说法：回答"要让 agent 多等多久"时用「让 agent 多等了多久」，比「感知延时」顺。

## 架构探索结论（2026-10-19，均停留在研究阶段，未进规格）

### L1 历史快速检索（资源+动作复合键）
- 核心判断：向量从"整个键"降级为"资源解析这一步的工具"；动作槽走受控词表近精确匹配（可 hash 分桶），时间是精确轴。
- 三段式管线：资源解析（别名表→向量最近邻）→ L4 图连接（operates_on 边+时间窗）→ ACT-R 激活排序。读路径保持无 LLM。
- 理论对齐：ACT-R partial match、海马体索引理论（Teyler & DiScenna 1986）、CBR、Zep entity–episode 二部图。

### 职责分工
- System 2 worker（`RunSystem2Cycle`）负责 L2/L3/L5/L6 整理；worker 双宿主（CLI `ladym worker` + 引擎内嵌 `Engine.StartSystem2`），跨进程 flock 互斥。
- L4 例外：无自动维护者。ARCHITECTURE.md 的 "Auto-link on write" 未实现（文档超前于代码）。

### L4 自动维护研究结论：需要
- 三问题：建边不足（code_refs 在独立表不进 L4，δ 中心性空转）；悬挂边（DeleteMemory 不级联删边，NeighborCounts 不查端点存在性，Decay 后偏差累积）；溯源不进图（consolidate 的 source_episode/updated_from 只在 metadata，无 produced_from 边；注意 UPDATE 本身已是"新建+Retire 建 supersedes"，早先"原地改写"结论有误——grep 输出被 head_limit 截断误导）。
- 修正（10:09 核实）：code indexer 并非无清理——code_file 按文件删旧、重发符号按限定名删旧；真实残留是：消失符号不清理、code_symbols/code_refs 投影行残留、批量删除不同步 SQLite 进程内向量索引。教训：勿用 bash grep 在 macOS（BSD grep 不支持 `\|`），用 Grep 工具；Grep 输出勿设过小 head_limit。
- 四机制两档时机：写时确定性建边（operates_on、code_refs→edges 投影）/ 写时失效标注（consolidate 复用 Retire）/ worker 语义建链（A-MEM 式，门控，桶内连边防 GAM 语义漂移）/ worker 垃圾回收（悬挂边清扫+NeighborCounts join）。
- 优先级：垃圾回收 > 确定性建边 > 失效标注 > 语义建链。L4 维护与 L1 资源+动作方案互为前置。

### 已知文档-代码偏差（待修）
- ARCHITECTURE.md 声称的 "Auto-link on write"、"callers/callees via L4 graph expansion" 均未实现。

## 规格状态
- 2026-10-19 spec `docs/superpowers/specs/2026-10-19-l1-ops-retrieval-l4-maintenance-design.md` + plan `docs/superpowers/plans/2026-10-19-l1-ops-retrieval-l4-maintenance.md`（注意文件名含 "retrieval"）。
- **已全部实施完成**：分支 feat/l1-ops-l4-maintenance，31 commits，M0-M4 全交付，21 包测试全绿，覆盖率 operations 94.6%/engine 96.5%/code 95.4%。待用户决定合并/PR/保留。
- 2026-10-21 `.ladyM/` 默认库 + `ladym init`：spec/plans `docs/superpowers/{specs,plans}/2026-10-21-dotdir-default-db-init*.md`。已合并 main（ea38aa8）并发布 v0.8.1（Latest）。遗留：`~/.ladym` 与 `~/.ladyM` 大小写分裂待修。

## 默认存储布局（2026-10-21 起）
- 个人版默认库：`$(cwd)/.ladyM/ladym.db`；旧平铺 `./ladym.db` 存在则回退读旧路径；`ladym init` 迁移（db/-wal/-shm，index.lock 留原地）+ 追加 gitignore（幂等）。大小写定为 `.ladyM`（大写 M，对齐 home 目录）。
- 已交付能力：record_event 结构化槽位（resources/action_class）、recall_ops 三段式 pivot 检索（MCP 第 10 工具 + CLI recall-ops + SDK）、L4 写时确定性建边（operates_on/produced_from/calls-imports 投影，ProjectedEdgeID 幂等）、重索引残留清理、MaintainL4 worker 步骤（GC 恒跑 + 门控回填/语义建链/别名合并三 op）。
