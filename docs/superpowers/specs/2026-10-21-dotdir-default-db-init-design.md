# 规格：默认库迁入 `.ladyM/` + `ladym init` 子命令（个人版）

> 日期：2026-10-21。状态：已确认（对话中敲定，本文落档）。
> 前置调研：业界惯例对照（wrangler `.wrangler/state/v3/`、OpenWorkflow `./openworkflow/backend.db` + init 自动写 gitignore、Prisma `prisma/dev.db`、Claude Code `~/.claude` + 项目级 `.claude/`）。结论：每项目一份运行时数据的工具，主流是项目级专用目录，不是平铺。

## 1. 目标

解决默认 SQLite 库平铺在仓库根目录带来的问题：

- `ladym.db` + `-wal` + `-shm` + `.index.lock` 四个文件污染项目根目录；
- gitignore 要写多条规则，漏掉 sidecar 文件是常见事故；
- 没有 `init` 命令，库靠首次使用懒创建，用户无感知、无迁移路径。

## 2. 已确认的设计决策

| 决策点 | 结论 |
|---|---|
| 默认路径 | `$(cwd)/.ladyM/ladym.db`（大小写对齐 home 目录 `~/.ladyM`） |
| 优先级 | `LADYM_DB` > 旧版平铺 `./ladym.db`（存在则继续读，兼容存量用户）> `.ladyM/ladym.db` |
| 新命令 | `ladym init`：迁移旧库 + 建库 + 处理 gitignore |
| gitignore 内容 | 末尾追加两行：`# ladyM` / `.ladyM/` |
| gitignore 规则 | 已有 `.gitignore` 且未忽略 → 追加；无 `.gitignore` 但有 `.git` → 创建；两者都无 → 打提示不创建 |
| 幂等 | 重复执行 init 不重复追加、不报错 |
| 显式路径 | `--db` / `LADYM_DB` / toml `db_path` 指定了路径 → 只建库，不迁移、不动 gitignore |
| 配置文件位置 | `ladym.toml` 仍留在项目根目录（配置进版本库，数据忽略） |

## 3. 改动面

### 3.1 `config.defaultDBPath()`（config/config.go）

```go
func defaultDBPath() string {
    if env := os.Getenv("LADYM_DB"); env != "" {
        return env
    }
    wd, err := os.Getwd()
    if err != nil {
        return "ladym.db"
    }
    // 旧版布局（<= v0.8.0）平铺在根目录：继续读，存量记忆不丢。
    if fileExists(filepath.Join(wd, "ladym.db")) {
        return filepath.Join(wd, "ladym.db")
    }
    return filepath.Join(wd, ".ladyM", "ladym.db")
}
```

说明：

- `NewStore` 已有 `MkdirAll(filepath.Dir(dbPath))`，父目录自动创建，无需额外改动。
- `ladym.db.index.lock` 路径由 db 路径派生，自动跟随迁移。
- 每次默认加载多一次 `os.Stat`，成本可忽略。

### 3.2 `ladym init`（新文件 cli/initcmd.go）

行为（按序执行）：

1. 加载配置（与其他命令同一优先级链，支持 `--db` 覆盖）。
2. **判定布局**：`cfg.DBPath` 等于 `$(cwd)/.ladyM/ladym.db` 或旧版 `$(cwd)/ladym.db` → 默认布局模式；否则自定义路径模式。
3. **迁移**（仅默认布局模式）：
   - 旧版 `./ladym.db` 存在且 `.ladyM/ladym.db` 不存在 → `MkdirAll(.ladyM)` 后逐个 `os.Rename`：`ladym.db`、`ladym.db-wal`、`ladym.db-shm`（存在才搬）。`.index.lock` 是陈旧锁文件，不搬，留在原地由用户清理（或已被覆盖）。
   - 两者都存在 → 报 `ConfigError`，给出一行可操作提示（手动二选一），不静默覆盖。
4. **建库**：`engine.New(cfg)` + `Close()`（schema 全部 `IF NOT EXISTS`，幂等）。
5. **gitignore**（仅默认布局模式）：按 §2 规则处理。追加时保证前置换行（原文件末尾无换行时补一个）。
6. 输出一行摘要：db 路径 + 迁移状态 + gitignore 状态。

自定义路径模式：执行第 4 步，打印 db 路径；若路径在 cwd 内，提示用户自行加入 gitignore。

### 3.3 显式不做的（YAGNI）

- 不做 `~/.ladym/config.toml` 与 `~/.ladyM` 大小写统一（独立问题，另行处理）。
  → 已于 2026-10-23 处理，见 `2026-10-23-home-dir-case-unification-design.md`：统一到
  `.ladyM`，旧小写全局配置只做兼容读。
- 不自动迁移：只有跑 `init` 才搬旧库；不跑 init 的存量用户靠 defaultDBPath 的旧路径回退继续用。
- 不检测 db 是否被其他进程占用（文档提示：先停 serve/worker 再 init）。

## 4. 测试要点

**config 包**：

- 默认路径 = `$(cwd)/.ladyM/ladym.db`（改造现有 `TestDefaultDBPathFallback`）；
- 旧版 `./ladym.db` 存在 → 回退旧路径；不存在 → 新路径；
- `LADYM_DB` 依旧最高优先级。

**cli 包**（新 initcmd_test.go，用 `isolateEnv` + `t.Chdir`）：

- init 建库：`.ladyM/ladym.db` 存在且可打开；
- 迁移：旧 `ladym.db` + `-wal` 被搬入 `.ladyM/`，原位置消失；
- 冲突：旧库与新库同时存在 → 报错；
- gitignore 三分支：追加 / 已含条目不重复 / 无文件时创建（有 `.git`）；
- 末尾无换行的 `.gitignore` → 追加后格式正确；
- 自定义 `--db` → 不动 `.gitignore`，库建在指定路径；
- 幂等：连续两次 init 输出一致、无报错。

## 5. 风险

- 存量用户不跑 init：默认读取回退保证数据可见；但新旧两套路径并存期间，若用户先在新路径建了库又残留旧库，init 的冲突报错兜底。
- `os.Rename` 跨进程并发：init 时 serve/worker 正在写库可能搬出半途状态。文档提示先停进程；不代码级检测。
