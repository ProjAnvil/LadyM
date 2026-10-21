# 规格：hook 写入侧评测台（eval/）

> 日期：2026-10-21。状态：待用户确认。
> 前置调研：本轮代码侦察（hook 写入链路、layers/episodic.go 落库形态、tokenizer 离线可行性）。

## 1. 目标

在仓库内新建 Go 包 `eval/`，回答两个关于 hook **写入侧**的问题：

1. **保真**：harness 事件到底有没有真的写进 LadyM？写进去的字段对不对？
2. **性能**：每一次写入往库里落了多少 token？让 agent 多等了多久？随库规模怎么变？

非目标（本轮明确不做）：

- 不测 recall / 上下文回注。`ladym hook` 是纯观察者，stdout 恒为空（`cli/hook.go:16-19`），本轮**不给 hook 加注入能力**。
- 不测 LLM token 消耗。写入路径不调 LLM（`engine/scope.go:142-180` 全程无 LLM 调用，也不过 attention gate），且 `providers/` 内无任何 usage 记录。
- 不改任何产品代码。所有测量在 `eval/` 内完成。

## 2. 已确认的设计决策

| 决策点 | 结论 | 依据 |
|---|---|---|
| 评测方向 | 写入侧（往 LadyM 里写），非上下文回注 | 用户澄清（2026-10-21） |
| 技术栈 | Go | tiktoken-go 已在依赖树且词表编译进二进制；可复用真实实例测管线分段 |
| 语料规模 | 100 / 1k / 10k 已存在记忆 | 用户确认 |
| token 口径 | 落库文本的 token 数 | 用户确认；纯离线、零 API key |
| 耗时口径 | 耗时 + 内部分解 + token 换算 | 用户确认 |
| 分段计时方式 | 调用方复现 + 交叉校验（零产品代码改动） | 用户确认；`eng.Store/Provider/Config`、`Scope.Episodic`、`operations.NormalizeAction/ResolveResource` 全部导出，可用同一实例还原 |
| 是否计入 CI | 否，靠 build tag `eval` 隔离 | `make test` 跑 `go test ./...`，不得拖慢主套件 |

## 3. 被测链路（真实）

```
harness 事件 → fork/exec `ladym hook` → hook.Parse → ToOperation（截断）
  → eng.Scope(ws).RecordOperation(ev)
      ├─ operations.NormalizeAction(ev.Action, cfg)        ← ActionClass 为空时
      ├─ operations.ResolveResource(...)  per resource     ← 含 embedding
      ├─ Episodic.Record(...)                              ← embedding + SQLite insert
      └─ Store.PutEdge(operates_on)       per resource
  → exit
```

**每个工具调用都会 fork 一次进程**，不是常驻服务。agent 感知的耗时 = 进程冷启动 + sqlite 打开 + 上述写入管线。冷启动很可能是大头，这是本评测要验证的核心假设。

截断硬上限（`hook/hook.go:78-80`）：`PromptMaxRunes=2000`、`ObservationMaxRunes=500`。

## 4. 测量模型（三层）

### L1 保真

对每个 fixture：把真实 harness payload 喂给**真实二进制**（fork/exec `ladym hook`，而非进程内调 `hookCmd()`），然后开库断言。断言维度：

- 落库行数（应为 1；未知事件为 0）
- `action_class` / `action_raw` 元数据
- `resources` 提取结果 + `operates_on` 边数量
- `observation` 内容是否符合 2000/500 截断（含 CJK 场景，验证按 rune 而非 byte 截断）
- `metadata`：session_id、transcript_path、cwd、hook_event_name
- exit code（必须 0，永不 2）

### L2 token（落库文本）

对每条落库行，用 `tokenizer.Cl100kBase` 分别计数：

| 指标 | 定义 |
|---|---|
| `content_tokens` | `Count(m.Content)`，形如 `agent=X \| action=Y \| observation=Z \| outcome=W` |
| `summary_tokens` | `Count(m.Summary)`，形如 `agent: action` |
| `metadata_tokens` | `Count(m.Metadata 的 JSON)` |
| `persisted_tokens` | 三者之和 —— **主指标** |
| `input_tokens` | `Count(原始 stdin JSON)` |
| `amplification` | `persisted_tokens / input_tokens` —— 写入放大倍率 |

**已知待验证现象：`observation` 与 `outcome` 被存了两份**（`layers/episodic.go:29-52` 同时写进 `Content` 和 `Metadata`）。预计 prompt 类事件的 `amplification` 会明显高于无 observation 的事件。评测需量化这个放大倍率。

### L3 耗时

所有耗时都用**单调时钟**（Go 的 `time.Since()`）读取 `time.Now()`，不用系统时间两次相减 —— 后者会被 NTP 校正打断，间隔可能算成负数。字段名统一带单位后缀 `_ms`，因此不再叠加 `duration_` 前缀。

| 指标 | 定义 |
|---|---|
| `spawn_ms` | fork/exec 真实二进制到 exit 的耗时 —— **agent 真实感知** |
| `engine_open_ms` | sqlite 打开 + 索引加载（进程冷启动主体） |
| `normalize_ms` | `NormalizeAction` |
| `resolve_ms` | Σ `ResolveResource`（无 resource 时为 0） |
| `record_ms` | `Episodic.Record`（embedding + insert） |
| `edges_ms` | Σ `PutEdge` |
| `replicated_ms` | 上述分段之和 |
| `real_ms` | 同实例上真实 `RecordOperation` 的端到端耗时 |
| `drift` | `|replicated_ms - real_ms| / real_ms` —— 分解可信度校验 |

上述各项都是耗时，区别只在**覆盖哪一段**，不在时钟种类。注意本评测**不采集 CPU 时间**：CPU 时间会把进程启动、读 sqlite 文件、等跨进程索引锁这些等待全部漏掉，而它们恰恰是冷启动的主体。

`spawn_ms - real_ms` 即近似的进程冷启动开销。

**交叉校验纪律**：若 `drift > 0.20`，该组数据的分段结论标记为不可信，报告中显式降级为"仅整体可用"。

## 5. 事件 fixture 矩阵

覆盖两家 harness（`claude-code` / `codex`）与全部已注册事件：

| # | 场景 | 验证点 |
|---|---|---|
| 1-2 | SessionStart（startup / resume） | ActionClass=create，observation=source |
| 3 | SessionEnd | 落库但 ActionClass 为空 |
| 4 | UserPromptSubmit 短 prompt | 基本映射 |
| 5 | UserPromptSubmit prompt > 2000 runes | 截断按 rune |
| 6 | UserPromptSubmit CJK prompt | rune 而非 byte 截断 |
| 7 | Stop | outcome=success |
| 8 | PostToolUse Read + file_path | resource + 边，action_class=read |
| 9 | PostToolUse Write + file_path | action_class=write |
| 10 | PostToolUse Edit + notebook_path | 走 notebook_path 分支 |
| 11 | PostToolUse Bash（无路径） | 无 resource、无边，action_class=execute |
| 12 | PostToolUse `mcp__*` | action_class 留空 |
| 13 | PostToolUse tool_response > 500 runes | observation 截断 |
| 14 | PostToolUse tool_response 为对象 | 走 JSON marshal 分支 |
| 15 | PostToolUseFailure | outcome=fail |
| 16 | 未知事件 PreCompact | 零行写入，exit 0 |
| 17-18 | Codex 变体（shell 代替 Bash 等） | 两家 harness 入参键名差异 |

`resourcesFromInput` 只认 `file_path` / `notebook_path` 两个键（`hook/hook.go:131-139`）。预计场景 11、17 会暴露"resource 提取为空 → 无边"的行为，这是保真测试的高价值目标。

## 6. 语料与隔离

- 每个（规模档 × 事件场景）组合**重新播种一套库**，避免写入过程自身把语料撑大污染统计。
- 播种走 Go 直连 engine，确定性：固定 seed + 默认 hashing embedding（dim 256）+ `LLMProvider=none`。全离线。
- 每套库独立文件路径，**不并发共享一个 sqlite**（仓库有 `index_lock.go` 跨进程锁，且历史上有 sqlite 并发分支）。
- 合成记忆的内容长度走对数正态分布，贴近真实工程中长短混杂的形态。

## 7. 目录结构

```
eval/
├── README.md            # 怎么跑、指标定义、阈值含义
├── doc.go               # 包注释（scripts/check-comments.sh 要求）
├── fixtures.go          # §5 的事件 payload 矩阵 + 期望断言
├── corpus.go            # 确定性合成语料播种
├── tokenize.go          # tokenizer.Get(Cl100kBase) 封装 + 计数辅助
├── spawn.go             # 子进程探测：真实二进制耗时
├── pipeline.go          # 调用方复现的分段计时
├── stats.go             # p50/p95/p99/mean/stddev
├── report.go            # JSON + Markdown 输出
├── runner_test.go       # //go:build eval —— 编排与各档运行入口
├── fidelity_test.go     # //go:build eval —— L1 断言
└── perf_test.go         # //go:build eval —— L2/L3 测量
```

产物落 `eval/.cache/reports/<timestamp>/{metrics.json,report.md}`，并加进 `.gitignore`。

Makefile 新增：

```make
eval-hook: build-personal
	go test -tags eval -run 'TestEval' ./eval/... -v
```

默认 `make test` 不受影响（`eval` tag 未开时不编译重型文件）。

## 8. 依赖变更

把 `github.com/tiktoken-go/tokenizer v0.8.1` 从 `// indirect` 提升为 direct。该版本已在 `go.mod:38`、`go.sum:75-76`，词表以 Go 源码形式编译进二进制（`codec/cl100k_base_vocab.go`，约 2MB），**零新增下载、零网络**。

## 9. 测试策略（TDD）

沿用项目纪律，红→绿→重构：

1. `tokenize.go`：先写断言——已知文本在 Cl100kBase 下的 token 数；空串为 0；CJK 文本 > 字符数/4。
2. `stats.go`：给定分布断言 p50/p95/p99 与漂移比。
3. `fixtures.go`：每个 fixture 先跑红——`spawn hook` 后断言不成立，逐个修到绿。
4. `pipeline.go`：断言 `drift <= 0.20`，这是分段数据可用的门槛。
5. `perf_test.go`：阈值断言（先取基线值，再回头定阈值，禁止先写代码后补断言）。

为保证 `scripts/check-comments.sh` 通过，所有导出符号都要写注释。

## 10. 里程碑

| 里程碑 | 内容 | 验收 |
|---|---|---|
| M0 | `tokenize.go` + `stats.go` + go.mod 依赖提升 | 单测全绿；`make lint-comments` 通过 |
| M1 | `fixtures.go` + `spawn.go` + 保真测试 | 18 个 fixture 全绿；暴露并修掉真实缺陷（若有） |
| M2 | `corpus.go` 三档播种 | 100/1k/10k 播种确定性可复现 |
| M3 | `pipeline.go` 分段计时 + `drift` 交叉校验 | drift ≤ 0.20 |
| M4 | `report.go` + Makefile target + README | 三档报告产出，`make eval-hook` 一键跑通 |

## 11. 已接受的风险

1. **合成语料不等于真实库**：真库里 L1/L2/L4 边、consolidate 产物、别名混在一起，合成库复现不全。缓解：内容长度走对数正态分布贴近真实形态，并在报告里明示数据来自合成语料。
2. **`spawn_ms` 受机器噪声影响**：CI 上绝对值不可比。缓解：报告同一台机器上的相对量，并给出 warmup + 多次采样的统计而非单次值。
3. **复现管线会与产品代码分叉**：将来 `RecordOperation` 改了而 `pipeline.go` 没跟上，分段就失真。缓解：`drift` 校验在分叉时报警。
4. **Cl100kBase 不对应特定模型**：token 数只能作为跨场景相对比较，不是某家 API 的真实计费值。报告中明示。

## 12. 待实施时核对

- `EpisodicMemory.Record` 是否还有其他字段同样在 Content 与 Metadata 各存一份（初查为 observation/outcome 两份）。
- `ResolveResource` 在资源实体已存在时的分支是否调用 embedding（直接影响 `resolve_ms` 的量级）。
- Codex 的 `PostToolUse` payload 是否真的用 `file_path` 键（初查两家具差异）。
