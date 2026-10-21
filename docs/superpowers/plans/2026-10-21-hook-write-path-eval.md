# 计划：hook 写入侧评测台（eval/）

> 日期：2026-10-21。分支：`feat/hook-write-path-eval`。
> 规格（已签字）：`docs/superpowers/specs/2026-10-21-hook-write-path-eval-design.md`。

## 目标

在 `eval/` 建一个 Go 评测包，回答两件事：hook 写入是否真的落库且字段正确（保真），以及每次写入落多少 token、耗时多少（性能）。

硬约束：**不修改任何产品代码**。所有测量在 `eval/` 内完成。

## 已核实的产品 API（写代码前已逐个确认签名）

| 用途 | 调用 |
|---|---|
| 建配置（离线可复现） | `config.ForTesting(dir)` → 覆盖 `DBPath` / `Workspace` |
| 建引擎 | `engine.New(cfg) (*Engine, error)` |
| 引擎内部实例（复现管线用） | `eng.Store`、`eng.Provider`、`eng.Config`（均导出） |
| 动作归一化 | `operations.NormalizeAction(raw string, cfg *config.Config) string` |
| 资源解析 | `operations.ResolveResource(store, embedder, cfg, ws, name) (*schema.Memory, bool, error)` |
| 情景层写入 | `layers.NewEpisodicMemory(store, embedder, ws)` → `.Record(agent, action, observation, outcome, tags, metadata)` |
| 建边 | `schema.NewEdge(src, rel, dst)` + `schema.ProjectedEdgeID(src, rel, dst)` → `Store.PutEdge(e)` |
| 直接读库断言 | `sql.Open("sqlite", dbPath)`（驱动已由 `_ "modernc.org/sqlite"` 注册为 `"sqlite"`） |
| token 计数 | `tokenizer.Get(tokenizer.Cl100kBase)` → `enc.Count(s) (int, error)` |

`memories` 表关键列：`id, layer, type, content, summary, tags, metadata, workspace, source`；另有 `edges` 表。

## 任务序列（TDD：每个任务先写红测试）

### M0 · 地基

**文件**：`eval/doc.go`、`eval/tokenize.go`、`eval/stats.go`、`eval/tokenize_test.go`、`eval/stats_test.go`；改 `go.mod`。

- `tokenize.go`：`NewCounter(enc Encoding) (*Counter, error)` + `(*Counter).Count(s string) int`（内部忽略 error，评测计数失败不应中断；测试另行验证 error 分支）。
- `stats.go`：`Summary` 结构体含 `n, mean, stddev, p50, p95, p99, min, max`；`Summarize(vals []float64) Summary`；百分位用**线性插值**（别用最近邻，样本小时偏差大）。
- `go.mod`：把 `github.com/tiktoken-go/tokenizer v0.8.1` 从 indirect 提升为 direct（`go get` 后 `go mod tidy`）。
- **红测先行**：已知英文串的 token 数、空串 = 0、CJK 串 token 数 > 字符数 / 4；`Summarize` 对 `[]float64{1,2,3,4,5}` 断言 p50=3、p95=4.8。
- **验证**：`go test ./eval/... && make lint-comments`。

### M1 · 保真

**文件**：`eval/fixtures.go`、`eval/spawn.go`、`eval/dbread.go`、`eval/fidelity_test.go`。

- `fixtures.go`：`Fixture{Payload string, Want Want}`；18 个用例见规格 §5。`Want` 含：行数、`action_class`、`resources`、边数、`observation` 是否被截断、期望退出码。
- `spawn.go`：`SpawnHook(bin, dbPath, harness, payload string) (SpawnResult, error)` —— `exec.Command(bin, "hook", "--db", dbPath, "--harness", harness)`，stdin 喂 payload，用单调递增时钟计时，返回 `duration_ms`、stdout、stderr、退出码。
- `dbread.go`：`ReadRows(dbPath, workspace) ([]Row, error)` 直连 sqlite；`ReadEdges(dbPath)` 查 `edges`。
- **红测先行**：先跑全部 fixture，看哪些断言不成立 —— 不成立的就是真实缺陷，逐个记录再决定是修 fixture 还是记作发现。
- **验证**：`TestFidelityMatrix` 全绿。

### M2 · 语料播种

**文件**：`eval/corpus.go`、`eval/corpus_test.go`。

- `Seed(size int, dir string, seed uint64) (dbPath string, err error)`：用 `engine.New` 直写入 L1 episode + L2 resource，内容长度按对数正态分布采样。
- 每档一个独立 db 文件，禁止并发共享同一个 sqlite。
- **红测先行**：断言播种行数 == size；断言同 seed 两次播种的 `content` 集合完全一致（确定性）。
- **验证**：100 / 1k / 10k 三档都能在可接受时间内播完。

### M3 · 分段计时

**文件**：`eval/pipeline.go`、`eval/pipeline_test.go`。

- `MeasureSegments(eng *engine.Engine, ws string, ev engine.OperationEvent) (Segments, error)`：用同一个 `eng` 实例复现 `RecordOperation` 的四段并分别计时。
- 同时 `MeasureReal` 跑真实 `eng.Scope(ws).RecordOperation(ev)` 计时。
- `drift = |replicated_ms - real_ms| / real_ms`。
- **红测先行**：断言 `drift <= 0.20`；断言无 resource 的场景 `resolve_ms` 与 `edges_ms` 为 0。
- **验证**：`TestSegmentDriftWithinTolerance` 绿。

### M4 · 报告与入口

**文件**：`eval/report.go`、`eval/runner_test.go`、`eval/README.md`；改 `Makefile`、`.gitignore`。

- `report.go`：`WriteReport(dir, Results) error` —— 落 `eval/.cache/reports/<timestamp>/{metrics.json,report.md}`。Markdown 表按三档 × 事件场景排列，含 token 统计与耗时统计。
- `runner_test.go`：编排入口，`//go:build eval`。
- Makefile：`eval-hook: build-personal` + `go test -tags eval -run TestEval ./eval/...`。
- `.gitignore` 追加 `eval/.cache/`。
- **验证**：`make eval-hook` 一键跑通并产出三档报告。

## 完成定义

1. `go build ./...` 通过；`go vet ./eval/...` 干净。
2. `make lint-comments` 通过（仓库对注释有 lint 约束，所有导出符号都要写注释）。
3. `make test`（不带 eval tag）不受影响、不拖慢。
4. `make eval-hook` 一键跑通，产出三档报告。
5. 18 个保真 fixture 全绿，或明确记下哪几个暴露了真实缺陷。

## 显式不做

- 不给 `ladym hook` 加上下文回注能力。
- 不改 `RecordOperation`。
- 不引入 CPU 时间作为替代指标（会漏掉 IO 等待与跨进程锁等待）。
