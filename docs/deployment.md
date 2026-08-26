# LadyM 企业版部署指南

本文是 LadyM 企业版(Postgres 后端、`enterprise` build tag)的参考部署:容器镜像、
docker-compose 拓扑、配置参考与运维基线。个人版 / 企业版的构建差异见仓库根
`Makefile`(`build-personal` / `build-enterprise` / `verify-enterprise`)。

## 1. 角色与拓扑

企业版参考部署(`docker-compose.enterprise.yml`)是三层结构,gateway 是唯一对外节点:

```
                ┌──────────────────────────────────┐
   clients ───▶ │ gateway  nginx :80               │  反向代理,唯一暴露节点
   (宿主 18080)  │  /        → console:8080         │  (宿主只映射 18080→80;
                │  /api/*   → ladym_api upstream   │   路由详见 §3 路由表)
                │  /healthz → ladym_api upstream   │
                └──┬──────────────┬────────────────┘
                   │              │
        ┌──────────▼─────┐  ┌─────▼────────────────┐
        │ api-1 / api-2  │  │ console              │  管理台角色(企业版独立二进制):
        │ ladym serve    │  │ ladymconsole --http  │  完整 /api 数据面 + Vue SPA 挂 /
        │   --http :8080 │  │   :8080              │  (同一镜像,entrypoint 覆盖)
        │ (无状态 HTTP    │  └─────┬────────────────┘
        │  数据面;不内嵌  │        │
        │  管理台,/ 404) │  ┌─────▼────────────────┐
        └────────┬───────┘  │ worker  ladym worker │  System 2 后台循环(consolidate /
                 │          │ (同一镜像,command 覆盖)│  proceduralize / L5 / L6 / decay)
                 │          └─────┬────────────────┘
                 │   postgres DSN(全部同一套库)│
                 ┌────────▼───────▼────────┐
                 │ pg                      │  Postgres 17 + pgvector
                 │ (不映射宿主端口,仅内部) │  (三层结构里 PG 不对外)
                 └─────────────────────────┘
```

开发调试另有单节点开发组 `docker-compose.dev.yml`(pg + 单 api + worker,api 直连
宿主 8080、pg 直连 55433),对照见 §3。

- **gateway**:nginx(`nginx:1.27-alpine`,配置 `deploy/nginx.conf`),唯一映射宿主
  端口(18080→80)的节点;`/api/*` 与 `/healthz` 反代到 `ladym_api` upstream
  (`api-1:8080` + `api-2:8080`,默认轮询),`/` 反代到 `console:8080`。其余服务
  都在内部 network,不映射任何宿主端口。
- **api**:无状态,可水平扩副本——参考部署直接起 **api-1 / api-2 两个节点**,由
  gateway 轮询负载均衡。注意单副本内所有 `/api/*` 请求经一把进程级互斥锁
  串行(`api.Handler.mu`),因此**单副本吞吐靠串行,横向扩展靠多副本**(多副本共享
  同一个 PG,工作区隔离见下文)。
  企业版的 api 节点**完全不内嵌管理台**(`ladym` 二进制不含 Vue 资产,由
  `go list -deps` 门禁保证):`/` 与其他非 `/api` 路径返回 404 JSON
  (`console not embedded in enterprise build; run \`ladymconsole\``);`/api/*`
  行为与个人版完全一致(未知 `/api` 路径仍是同样的 JSON 404)。
- **console**:管理台(Vue 3 SPA,源码 `console/`,构建产物 `console/dist` go:embed
  进 `ladymconsole` 二进制)的独立角色。**同一镜像、独立二进制**:镜像同时携带
  `/ladym` 与 `/ladymconsole`,console 服务用 `entrypoint: ["/ladymconsole"]` 覆盖
  启动(compose 的 `command:` 只能替换传给 `/ladym` 的参数,换二进制必须覆盖
  entrypoint),**连同一套 PG**(配置与 api 完全相同:`store.backend/dsn`、
  `auth.enabled` 等)。它提供完整 `/api` 数据面 + 挂在 `/` 的 SPA(静态资源与 SPA
  fallback 不鉴权,`/api/*` 走 Basic 认证,见下),浏览器打开 `http://<addr>/` 即
  登录页。个人版无此二进制——个人版的 console 仍内嵌于 `ladym serve --http`。
- **worker**:与 api 同一镜像,`command: ["worker"]` 覆盖默认 CMD。System 2 的启用
  方式就是这个命令本身——`config applyEnv` 中**不存在** `LADYM_SYSTEM2_ENABLED`
  之类的 env;周期参数用 `--interval`(秒,默认 300)/ `--once`,更深的旋钮走
  `ladym.toml` 的 `[system2]` 表。离线部署(LLM provider = none)时 worker 自动跳过
  L5/L6 两个需要 LLM 的步骤,其余步骤照常。
- **pg**:唯一的持久层。三层拓扑里**不映射宿主端口**,只经内部 network 被
  ladym 服务访问。schema(memories / edges / code_symbols / …)与
  `CREATE EXTENSION IF NOT EXISTS vector` 由服务启动时自动建立,无需手工迁移。

## 2. 配置参考

配置优先级:CLI flag → `LADYM_*` env → `./ladym.toml` → `~/.ladym/config.toml` →
默认值。容器内推荐全部用 env 注入。

| 配置 | env | 说明 |
|---|---|---|
| `store.backend` | `LADYM_STORE_BACKEND` | 企业版只能 `postgres`(sqlite 已编译期剔除) |
| `store.dsn` | `LADYM_STORE_DSN` | PG DSN,如 `postgres://postgres:ladym@pg:5432/ladym?sslmode=disable`;也可用 `store.dsn_env` 间接指向另一个 env |
| `auth.enabled` | `LADYM_AUTH_ENABLED` | HTTP 数据面的 Basic 认证总开关(默认 `false` = 全放行,个人模式) |
| `dict_dir` | `LADYM_DICT_DIR` | CJK 分词词典目录(默认 `~/.ladyM/dict`;微服务部署指向共享卷,见下文) |
| `embedding.provider` | `LADYM_EMBEDDING` | 默认 `hashing`(离线可用);`http`/`openai`/`ollama` 可外置 |
| `llm.provider` | `LADYM_LLM_PROVIDER` | 默认 `none`(离线);配置后 worker 才执行 L5/L6 |

### 认证与 workspace 隔离模型

鉴权是"数据库级"账号密码:用户存在库里(users 表,SQLite/PG 同构),密码只存
bcrypt 哈希。`[auth] enabled`(默认 `false`)是总开关。

- **`auth.enabled = false`(默认)**:所有 `/api/*` 请求隐式信任(个人模式,零行为
  变化),不需要任何请求头。
- **`auth.enabled = true`**:所有 `/api/*` 要求 `Authorization: Basic
  base64(username:password)`,服务端查 users 表校验。用户不存在 / 密码错误统一
  401。`password_hash` 为空的用户是**免密用户**,只有空密码能通过。
- **非 admin 用户**:workspace 被**强制**为该用户的 `workspace` 字段(请求 body 里的
  `workspace` 被忽略,响应头 `X-Ladym-Workspace` 回显);`stats` 的 workspaces 名单
  收窄为自己的 workspace,`forget`/`link` 触碰其他 workspace 的 memory 返回 403。
- **admin 用户**:不受 workspace 强制,可在请求 body 里指定任意 workspace。
- **auth enabled 但 users 表为空**:所有请求 401;启动 banner 打印 WARNING 提示用
  `ladym user add` 建用户。
- `POST /api/login`:body `{"username","password"}`,验证通过返回
  `{"username","workspace","admin"}`,失败 401 `{"error":"invalid credentials"}`
  (不区分用户不存在/密码错)。供管理台与客户端验证凭据;与其他 `/api/*` 一样要求
  有效的 Basic 头。
- `/healthz` 在 `/api/` 前缀之外,**不鉴权**,供 LB/编排探活。

用户管理走本地 CLI(直开 store,不过 HTTP),密码**永不进命令行参数**:交互式
prompt(两次确认,需 TTY)或 `--password-env VAR` 从 env 读:

```bash
ladym user add root --admin                       # 交互式输入密码
LADYM_PW=... ladym user add alice --workspace acme --password-env LADYM_PW
LADYM_PW=  ladym user add bot   --password-env LADYM_PW   # 显式空值 = 免密用户
ladym user list                                   # username/workspace/admin/created(无哈希)
ladym user passwd alice --password-env LADYM_PW
ladym user delete alice
```

### CLI 远程模式

数据子命令(`remember` / `record` / `recall` / `consolidate` / `stats` /
`forget` / `link`)都可以不开本地 db、直接打远程 `ladym serve --http` 数据面
(`index` 只支持本地,不走远程):

```bash
# 显式账号密码(优先级高于 env)
ladym recall --server http://127.0.0.1:8080 --user root --password "$ROOT_PW" "how do we deploy"

# 不传时回落到 LADYM_USER / LADYM_PASSWORD;两者都空 = 无鉴权部署
export LADYM_USER=alice LADYM_PASSWORD="$ALICE_PW"
ladym remember --server http://127.0.0.1:8080 "deploys go through Argo CD"

# 免密用户:只传 --user 即可
ladym stats --server http://127.0.0.1:8080 --user bot
```

- `--server` 与 `--db` **互斥**:远程模式下存储归服务端,本地 `--db` 会被静默忽略,
  因此同传会直接报错(`--db and --server are mutually exclusive`)。
- 远程模式的 stdout 与本地模式**逐字节一致**,只有取数路径不同。
- `-w/--workspace` 会随请求体发给服务端。但**非 admin 用户**的 workspace 被服务端
  强制为其 users 表里的映射值,`-w` 不生效;`stats` 只返回该 workspace 的计数与
  名单,`forget`/`link` 触碰其他 workspace 的 memory 会收到 403。admin 用户与无
  鉴权模式不受此限。

## 3. Compose 快速开始

仓库根有两个 compose 文件,用途对照:

| 文件 | 定位 | 拓扑 | 宿主端口 |
|---|---|---|---|
| `docker-compose.enterprise.yml` | 企业版三层参考部署(对外展示微服务 / 分布式能力) | gateway(nginx)→ api-1/api-2 + console + worker → pg,全部服务在内部 network,**只有 gateway 对外** | `18080`(gateway) |
| `docker-compose.dev.yml` | 开发用容器组(本地调试) | pg + 单 api + worker,api / pg 直连宿主 | `8080`(api)、`55433`(pg) |

### 企业版三层拓扑

```bash
# 起整套(gateway + api-1/api-2 + console + worker + pg;项目名 ladym-ent;
# 首次 --build 即构建镜像,含企业版门卫:二进制不得含 modernc.org/sqlite,必须含 pgx)
docker compose -f docker-compose.enterprise.yml -p ladym-ent up -d --build

# 探活(免鉴权,经 gateway 轮询打到 api-1 / api-2)
curl localhost:18080/healthz
# {"status":"ok"}

# 管理台 SPA(经 gateway 打到 console)
curl localhost:18080/
# <!doctype html> ... <script ... src="/assets/index-*.js"> ...(vite 产物引用)

# 写入(未配鉴权时直接可用)
curl -X POST localhost:18080/api/remember \
  -H 'Content-Type: application/json' \
  -d '{"content":"deploys go through Argo CD","tags":["ops"]}'
# {"id":"...","hash":"..."}

# 检索命中
curl -X POST localhost:18080/api/recall \
  -H 'Content-Type: application/json' \
  -d '{"query":"how do we deploy","top_k":5}'

# 配了鉴权之后带 Basic 凭据(admin 用户示例;非 admin 用户无需也不允许指定 workspace)
curl -X POST localhost:18080/api/recall \
  -u "root:$ROOT_PW" \
  -H 'Content-Type: application/json' \
  -d '{"query":"how do we deploy","workspace":"acme"}'

# 负载均衡证据:连续多次请求后看两个 api 节点的请求日志
docker compose -p ladym-ent logs api-1 api-2

# 清理(连同 named volume 一起删)
docker compose -f docker-compose.enterprise.yml -p ladym-ent down -v
```

gateway 路由表(`deploy/nginx.conf`,挂载为 `/etc/nginx/conf.d/default.conf`):

| 路径 | 上游 | 说明 |
|---|---|---|
| `/api/*` | `ladym_api`(`api-1:8080` + `api-2:8080`,默认轮询;可改 `least_conn`) | 含 `/api/metrics`;全部走 Basic 认证(若开启) |
| `/healthz` | `ladym_api`(同上) | 免鉴权探活 |
| `/` | `console:8080`(ladymconsole) | Vue SPA + 静态资源 + SPA fallback |

`docker-compose.enterprise.yml` 开箱即用:embedding 用默认 hashing,LLM 为 none,不依赖
任何外部 key。api-1 / api-2 / worker / console 四个角色共享同一份环境变量(YAML 锚点
`x-ladym-env: &ladym-env`,`LADYM_STORE_BACKEND` + `LADYM_STORE_DSN` 指向 pg 服务)。
api 服务**没有**容器级 healthcheck——distroless static 运行时里没有 shell / curl /
wget,`ladym` 也没有健康检查子命令;探活用 `/healthz`,由 gateway / LB / 编排器经
HTTP 发起。console 角色用 `entrypoint: ["/ladymconsole"]` 覆盖启动(compose 的
`command:` 只能替换传给 `/ladym` 的参数,换二进制必须覆盖 entrypoint)。

### 开发组

```bash
# 单 api 直连宿主 8080、pg 直连 55433,供本地调试
docker compose -f docker-compose.dev.yml -p ladym-dev up -d --build
curl localhost:8080/healthz
docker compose -f docker-compose.dev.yml -p ladym-dev down -v
```

## 4. 运维基线

- **`GET /healthz`**(免鉴权):探测存储连通性。200 `{"status":"ok"}` /
  503 `{"status":"error","detail":"..."}`。
- **请求日志**:每个 `/api/*` 请求(含被 401 拒绝的)向 stderr 打一行:
  `METHOD path status duration_ms workspace`,例:
  `POST /api/recall 200 12.3ms acme`。`docker compose -p ladym-ent logs api-1 api-2`
  即可查看(开发组为 `docker compose -p ladym-dev logs api`)。
- **`GET /api/metrics`**(与 `/api/*` 一样受鉴权):进程内计数器,字段:
  - `endpoints`:每个端点 `{requests, errors}`(errors = 非 2xx);
  - `recall_avg_ms`:recall 请求的运行平均耗时。
- **启动 banner**(stderr):监听地址、db、workspace、鉴权模式(`auth=off/on`;
  `auth=on` 且 users 表为空时带 WARNING,提示 `ladym user add`)。

### 中文/CJK 分词词典

默认构建**不内嵌**分词词典(省 ~31MB 二进制):中文/日文/韩文开箱即用逐字回退
分词,词级分词需要词典,三种来源按优先级:

1. **文件词典**(最高):管理台 **Settings → Memory** 选择变体后点「下载词典」,
   或管理员调 `POST /api/cjk_dict/download`(body `{"dict": "zh"}`,可选
   `{"mirror_base": "https://内网镜像/"}`)。可下载变体(`GET /api/cjk_dict`
   返回 `variants` 枚举):

   | 变体 | 内容 | 下载量 | 说明 |
   |---|---|---|---|
   | `zh`(默认) | 简体+繁体 | 8.2MB | 覆盖汉字 |
   | `zh_s` | 仅简体 | 4.9MB | 覆盖汉字 |
   | `zh_t` | 仅繁体 | 3.4MB | 覆盖汉字 |
   | `jp` | 日文(汉字+假名) | 22.6MB | 假名也走词级分词;jsDelivr 拒绝 >20MB 文件,自动回退 GitHub raw |

   所有文件 sha256 固定校验 gse v1.0.2,下载后分段器热加载、变体切换自动清理
   旧文件,无需重启。目录可用 `dict_dir` / `LADYM_DICT_DIR` 配置(默认
   `~/.ladyM/dict`)。
2. **内嵌词典**:`go build -tags fulldict`(或下游 `import _
   ".../storage/fulldict"`)自带 zh 词典,适合无法出网的环境(可与 `enterprise`
   组合:`-tags enterprise,fulldict`)。
3. **无词典**:汉字/假名/谚文逐字切分 + 相邻二元组特征,检索仍然可用,只是无
   词级语义。

相关端点(下载/删除需管理员):`GET /api/cjk_dict`(状态+变体枚举)、
`POST /api/cjk_dict/download`、`DELETE /api/cjk_dict`(删除并回退)。

### 微服务部署:每台机器都有词典

多实例(api×N / worker / console)下保证词典一致的三种方案,按运维偏好选一:

**方案 A — 共享卷(参考 compose 已内置)**:所有角色设 `LADYM_DICT_DIR` 指向同
一个挂载卷(`docker-compose.enterprise.yml` 里 `cjk-dict` 卷挂到
`/data/cjk-dict`)。在**任一**实例上下载一次(console 勾选或对网关调
`POST /api/cjk_dict/download`),全部实例共享;每个实例最迟约 30 秒(分词时的
目录探测)自动加载新词典或跟随变体切换,无需重启,词典升级也不用重新部署。
K8s 用 PVC + `env: LADYM_DICT_DIR` + 同一 volumeMount 即等价(NFS/ReadWriteMany,
或每节点一份的 hostPath 由 DaemonSet 预置)。

指定词典目录的三种等价方式(优先级从高到低,`serve` / `worker` /
`ladymconsole` 三个常驻命令均支持;不指定时默认扫描本机 `~/.ladyM/dict`):

```bash
ladym serve --http :8080 --dict-dir /data/cjk-dict   # CLI flag(最高)
LADYM_DICT_DIR=/data/cjk-dict ladym serve --http :8080  # env
# ladym.toml: dict_dir = "/data/cjk-dict"             # toml
```

**方案 B — 词典镜像(推荐的无共享卷形态)**:主 `Dockerfile` 内置 `dict`
target(BuildKit 按需构建词典数据层;普通 `docker build .` 默认仍是无词典镜像,
不触网)。dev / enterprise 两组 compose 各配一个 dict 覆盖文件,一条命令构建并
运行:

```bash
# 企业版(三层拓扑全员词典镜像)
docker compose -f docker-compose.enterprise.yml \
  -f docker-compose.enterprise.dict.yml -p ladym-ent up -d --build

# 开发组(api + worker 词典镜像)
docker compose -f docker-compose.dev.yml \
  -f docker-compose.dev.dict.yml -p ladym-dev up -d --build
```

词典落在镜像 `/opt/ladym/dict`(镜像内置 `LADYM_DICT_DIR`),所有角色零下载、
零共享卷、零运行时联网。特点:词典与二进制版本解耦(升级 LadyM 重建,基底层
缓存命中,词典层秒级);数据层与 CPU 架构无关;`DICT_VARIANT=zh|zh_s|zh_t|jp`
在覆盖文件的 `x-dict-build` 锚点处改;气隙构建把 pin 好的词典文件放进仓库
`dict/` 目录即可(构建时优先 COPY,不走网络)。

给**已发布的** LadyM 镜像叠词典层(不重编):`Dockerfile.dict` 薄层方案
`docker build -f Dockerfile.dict --build-arg BASE=ladym-enterprise:v0.5.1
-t ladym-enterprise:v0.5.1-dict .`。

**变体 B' — fulldict 编译标签**: `--build-arg BUILD_TAGS=enterprise,fulldict`
把词典**编进**二进制(每个二进制 +31MB)。单二进制自包含、适合非容器的离线
分发;容器场景方案 B 更省(8.2MB 数据 vs 两个二进制各 +31MB)。

**方案 C — init 容器/入口脚本下载**:每个 Pod 启动时从**内网镜像源**下载(K8s
initContainer + emptyDir,或 entrypoint 包一层),配合
`POST /api/cjk_dict/download` 的 `mirror_base` 指向内部 mirror(按
`<base>/<rel_path>` 布局,如 `<base>/data/dict/zh/s_1.txt`)。适合有镜像源管理
规范、但不想改构建流水线的团队。

不推荐对每个实例逐一调下载 API(方案 C 之外):实例间会存在词典缺失窗口,且
滚动扩容的新实例仍需再下载。

## 5. 镜像说明

镜像构建支持 `BUILD_TAGS` 参数(默认 `enterprise`,行为不变):需要内嵌中文分词
词典的气隙环境用
`docker build --build-arg BUILD_TAGS=enterprise,fulldict -t ladym-enterprise:full .`
(二进制约 +31MB,无需任何下载即有词级中文分词)。

`Dockerfile` 三阶段:`node:24-alpine` 先在镜像内重建管理台(`cd console && npm ci &&
npm run build`,比随仓的 console/dist 更可复现);随后 `golang:1.26` 以 `CGO_ENABLED=0
go build -tags enterprise -trimpath -ldflags "-s -w"` 构建**两个二进制**:
`/ladym`(api/worker 角色,`./cmd/ladym`)与 `/ladymconsole`(console 角色,
`./cmd/ladymconsole`;console/dist 只 embed 进它),并在同一阶段跑企业版门卫
(`go version -m` 断言 ladym 无 `modernc.org/sqlite`、有 `pgx`;`go list -deps` 断言
ladym 不依赖 console 包、ladymconsole 依赖之,与 `make verify-enterprise` 一致,违反
则镜像构建失败);运行阶段 `gcr.io/distroless/static-debian12:nonroot`,显式
`USER nonroot:nonroot`,`EXPOSE 8080`,两个二进制都 COPY 进镜像,
`ENTRYPOINT ["/ladym"]`,默认 `CMD ["serve", "--http", ":8080"]`。worker 角色 command
覆盖为 `["worker"]`;console 角色 entrypoint 覆盖为 `["/ladymconsole"]`——企业版容器里
没有 sqlite,所有状态都在 PG;`ladym` 二进制不携带任何 console 资产。
