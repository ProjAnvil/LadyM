# 认证体系 + 管理台 + Go 客户端设计

> 日期: 2026-08-19
> 范围: ladyM 的鉴权改造(DB 内用户名/密码)、Vue+Vite 嵌入式管理台(替代 web/ Go 模板)、client/golang SDK
> 前置: P0–P4 已完成(Store 双后端、api/ HTTP 数据面、enterprise 构建、compose)

## 1. 目标与原则

- ladyM 定位:harness 工程中的**中间件**——轻量、可嵌入、默认零配置。
- 鉴权**不做重**:不要 OAuth/JWT/RBAC;就是"数据库级"的账号密码——用户存在库(SQLite/PG 同一张表)里,密码 bcrypt 哈希;可以整体关闭(默认关,个人版零变化),也允许单个用户不设密码。
- 管理台:对数据(记忆、用户)做 CRUD;前端 Vue 3 + Vite,构建产物 go:embed 进二进制,不再用 Go html/template 单文件(web/ 包随之退役)。
- 客户端:client/golang 为第一个语言 SDK,后续可有更多语言。

## 2. 认证设计(数据库级账号密码)

### 2.1 users 表(两后端同构)

```sql
users(
  username      TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL DEFAULT '',   -- bcrypt;'' = 该用户免密码
  workspace     TEXT NOT NULL DEFAULT '',   -- 非 admin 用户的强制 workspace
  admin         INTEGER/BOOLEAN NOT NULL DEFAULT 0,
  created_at    DOUBLE PRECISION NOT NULL
)
```

### 2.2 Store 接口扩展

`PutUser(u *schema.User) / GetUser(username) / DeleteUser(username) / ListUsers() ([]*schema.User)`,SQLite/PG 双实现 + 参数化套件测试。密码哈希只在 api/cli 层做(bcrypt,golang.org/x/crypto 已在依赖),Store 只存哈希字符串。

### 2.3 HTTP 认证

- 请求头:`Authorization: Basic base64(username:password)`。
- 校验:users 表查用户;password_hash 非空 → bcrypt.CompareHashAndPassword;为空(免密用户)→ password 必须为空。
- `POST /api/login`:body {username, password};验证通过返回 {username, workspace, admin};失败 401。供管理台与客户端验证凭据(无 session,无状态,多实例安全)。
- 生效 workspace:非 admin → 强制 user.workspace(请求体 workspace 忽略,响应头 X-Ladym-Workspace 回显);admin → 请求自带 workspace。
- 配置:`[auth] enabled = true`(flat `auth_enabled`,env `LADYM_AUTH_ENABLED`)。默认 false = 全放行(现状)。
- **替换** P3a 的 bearer 体系:删除 `server.token_env` / `server.tenants` 配置与中间件(tenant 语义由 user.workspace 承接)。
- 引导:`ladym user add <username> [--workspace ws] [--admin] [--password-env VAR]`(密码不经命令行明文;交互式 prompt 或 env)。auth enabled 但 users 表空 → 所有请求 401 且 stderr 提示用 CLI 加用户。

### 2.4 CLI 远程模式对接

`--user` / `--password`(env `LADYM_USER` / `LADYM_PASSWORD`),内部走 Basic 头。替换 --token / LADYM_SERVER_TOKEN。

## 3. 管理台(console/)

### 3.1 数据 CRUD API(api/ 包新增)

- `GET /api/memories?workspace=&layer=&type=&limit=&offset=` — 列表(复用 Store.IterMemories,api 层分页;v1 不做全文检索,检索用 /api/recall)。
- `PUT /api/memories/{id}` — 更新 content/tags/summary(重算 embedding 后 PutMemory 覆盖)。
- `DELETE /api/memories/{id}` — 复用 forget 语义。
- 用户管理(仅 admin):`GET /api/users`、`POST /api/users`、`PUT /api/users/{username}`(改密码/workspace/admin)、`DELETE /api/users/{username}`。
- 鉴权:全部 /api/* 走 Basic 认证中间件;users 端点额外要求 admin。

### 3.2 前端(console/)

- Vite + Vue 3(无 UI 组件库,手写简洁 CSS;vue-router;fetch 封装带 Basic 头)。
- 页面:Login、Memories(筛选/分页列表、新建、编辑、删除)、Users(admin:列表/新建/改密/删除)、Stats(仪表数字 + workspaces)。
- 构建:`npm run build` → console/dist/,**dist 提交进仓库**(个人版单二进制哲学:纯 go build 不需要 node);api 包 go:embed console/dist,SPA fallback 到 index.html,挂在 serve --http 的 `/`。
- web/ 包与 `ladym web` 命令删除(README/文档同步)。Makefile 加 `console-build`(npm ci && npm run build)。Dockerfile 加 node 构建阶段跑 console-build。

## 4. Go 客户端(client/golang)

- `client/golang/` 包(import path `github.com/ProjAnvil/LadyM/client/golang`)。
- `client.New(baseURL string, opts ...Option)`;`WithAuth(username, password)`;`WithHTTPClient`。
- 方法覆盖数据面:Login / Remember / Recall / RecordEvent / SearchCode / Consolidate / Stats / Link / Forget + console 的 Memories/Users CRUD。
- 类型复用 schema 包;错误:非 2xx → `*client.Error{Status, Message}`。
- cli/remote.go 改为基于 client/golang 实现(dogfood),CLI 行为逐字不变。
- 测试:httptest + 真 api.NewHandler 端到端。

## 5. 非目标

- 不做 session/token 表、不做 RBAC 粒度、不做 OAuth。
- 管理台不做 edges 图编辑、不做配置编辑(配置仍走 ladym.toml / CLI config)。
- 其他语言客户端(Python/TS)后续单独立项。

## 6. 实施顺序

T1 认证(users 表 + Basic 认证 + auth.enabled + CLI user 子命令 + 替换 bearer)→ T2 CRUD API → T3 console 前端 → T4 client/golang + cli 远程重构 → 终审。
