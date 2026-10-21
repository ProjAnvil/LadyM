# 实施计划：`.ladyM/` 默认库 + `ladym init`

> 规格：`docs/superpowers/specs/2026-10-21-dotdir-default-db-init-design.md`（已确认）。
> 分支：`feat/dotdir-default-init`。基线：config/cli 包测试全绿。
> 纪律：每个任务先写失败测试（RED）→ 最小实现（GREEN）→ 提交。

## 任务 1：defaultDBPath 换默认布局 + 旧路径回退

- 文件：`config/config.go`（改 `defaultDBPath`）、`config/apply_and_load_test.go`（改 `TestDefaultDBPathFallback`）
- RED：改 `TestDefaultDBPathFallback` 断言为 `filepath.Join(wd, ".ladyM", "ladym.db")`；新增 `TestDefaultDBPathLegacyFallback`（t.Chdir 到含 `ladym.db` 文件的临时目录 → 断言返回旧平铺路径）与 `TestDefaultDBPathLegacyAbsent`（无旧库 → 新路径）
- GREEN：`defaultDBPath` 实现——env → `fileExists(wd/ladym.db)` 回退 → `wd/.ladyM/ladym.db`（`fileExists` 已存在于 config 包）
- 验证：`go test ./config/` 全绿

## 任务 2：`ladym init` 子命令

- 文件：新建 `cli/initcmd.go`、`cli/initcmd_test.go`
- RED（测试用例，全部走 `runCmd` + `isolateEnv` + `t.Chdir`）：
  1. `TestInitCreatesDotDirDB`——空目录 init → `.ladyM/ladym.db` 存在
  2. `TestInitMigratesLegacyDB`——预置 `ladym.db` + `ladym.db-wal` → init → 两者在 `.ladyM/` 下、原位置消失
  3. `TestInitConflictBothDBs`——旧库与新库并存 → 报错（含 "exists" 字样）
  4. `TestInitGitignoreAppend`——预置 `.gitignore`（无条目）→ init → 末尾含 `# ladyM` + `.ladyM/`
  5. `TestInitGitignoreIdempotent`——预置已含 `.ladyM/` 的 `.gitignore` → init → 内容不变
  6. `TestInitGitignoreNoTrailingNewline`——`.gitignore` 末尾无换行 → 追加后中间恰好一个空行分隔
  7. `TestInitGitignoreCreatedWhenGitPresent`——无 `.gitignore` 有 `.git/` → 创建含两行的文件
  8. `TestInitGitignoreSkippedNoGit`——两者皆无 → 不创建 `.gitignore`、输出含提示
  9. `TestInitCustomDBPath`——`--db` 指定路径 → 库建在指定处、`.gitignore` 不被创建
  10. `TestInitIdempotentRunTwice`——连跑两次 → 无错、`.gitignore` 条目只一份
- GREEN：实现 `initCmd()`（cobra，`--db` flag）+ 辅助函数 `migrateLegacyDB`、`ensureGitignoreEntry`；注册进 `cli.go` 的 `root.AddCommand`
- 验证：`go test ./cli/` 全绿

## 任务 3：注册命令 + 文档同步 + 全量验证

- 文件：`cli/cli.go`（AddCommand 列表加 `initCmd()`）、`README.md`、`ARCHITECTURE.md`（默认路径与 init 说明）
- 验证：`go test ./...` 全绿；`go vet ./...` 干净

## 收尾

- 汇报测试结果，给用户四个选项：合并 / 开 PR / 保留分支 / 丢弃
