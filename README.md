# API 集合平台（API Hub）

各业务服务启动时自动向平台注册，并同步可提供的接口（OpenAPI）；平台统一管理这些接口。服务可能无外网 IP，通过 **frp** 穿透，支持「直连」与「平台中继」两种调用方式。

详细设计见 [`docs/design.md`](docs/design.md)；**服务端口、访问方式与本地启动/排错见 [`docs/operations.md`](docs/operations.md)**。

## 架构一览

- **控制面（Go）** `server/`：注册中心 + 控制台 API（注册/心跳/反注册、接口管理、frp 端口分配、APISIX 路由同步）。
- **Agent（Go）** `agent/`：随业务服务运行，自动注册、拉起 frpc、心跳、退出反注册。
- **示例服务（Go）** `examples/demo-service/`：暴露 `/openapi.json` 与业务接口，用于演示接入。
- **管理后台（React + Vite + TS + Antd）** `web/`：服务/接口管理、接口上下线、消费方与 API Key。
- **数据面** `deploy/`：APISIX 网关 + frps + PostgreSQL + Redis（docker-compose）。

```
api-hub/
├── docs/design.md
├── deploy/        # docker-compose: PG / Redis / etcd / APISIX / frps + 迁移SQL
├── server/        # Go 控制面
├── agent/         # Go Agent
├── examples/demo-service/
└── web/           # React 管理后台
```

## 一键端到端（Docker Compose，推荐）

整套（PostgreSQL / Redis / etcd / APISIX / 控制面 server / 示例服务 / Agent / 管理后台）一条命令拉起。compose 内各服务可直达，Agent 通过 `SERVICE_HOST` 通告自身地址，**无需 frp** 即可打通中继。

```bash
cd deploy
docker compose up -d --build        # 首次会编译 Go 与构建前端，耐心等
python3 e2e.py                      # 端到端冒烟校验（核心链路 PASS 即通过）
```

启动后：

- 管理后台 http://localhost:8088
- 控制面 API http://localhost:8080
- 中继网关（APISIX）http://localhost:9080 ，示例中继调用：`curl http://localhost:9080/r/1/api/time`
- 示例服务直连 http://localhost:9000

`e2e.py` 会校验：demo-service 自动注册、接口同步、实例在线、OpenAPI 可取、接口上下线、经网关中继调用、访问日志统计、审计日志。真实「无外网IP」场景再用 frp：`docker compose --profile frp up -d`。

> 说明：`docker compose up --build` 会在容器内执行 `go mod tidy && go build`（Go 编译验证）与 `vite build`，因此首次构建即等于一次完整编译校验。

## 手动启动（本地开发）

### 1. 起依赖（PG / Redis / APISIX / frps）

```bash
cd deploy
# 先把 frps.toml / apisix/config.yaml 里的 CHANGE_ME_* 改成你的值
docker compose up -d
```

迁移 SQL 会在 PostgreSQL 首次启动时自动执行（`deploy/migrations/`）。

### 2. 启动控制面

```bash
cd server
go mod tidy
PG_DSN="postgres://apihub:apihub@localhost:5432/apihub?sslmode=disable" \
REDIS_ADDR="localhost:6379" \
PUBLIC_HOST="<平台公网IP>" \
FRP_SERVER_ADDR="<平台公网IP>" FRP_SERVER_PORT=7000 FRP_TOKEN="CHANGE_ME_FRP_TOKEN" \
APISIX_ADMIN="http://localhost:9180" APISIX_ADMIN_KEY="CHANGE_ME_APISIX_ADMIN_KEY" \
go run ./cmd/server
# 监听 :8080
```

> 一期联调可不配 APISIX（留空 `APISIX_ADMIN`），路由同步会自动跳过，仅走直连。

### 3. 启动示例服务 + Agent

```bash
# 终端 A：示例业务服务
cd examples/demo-service && PORT=9000 go run .

# 终端 B：Agent（本机联调可不设 FRPC_BIN）
cd agent
PLATFORM_URL="http://localhost:8080" \
SERVICE_NAME="demo-service" SERVICE_ENV="dev" \
LOCAL_PORT=9000 OPENAPI_URL="http://localhost:9000/openapi.json" \
CONN_MODE="relay" \
go run ./cmd/agent
# 接入真实穿透时设置 FRPC_BIN=/path/to/frpc
```

### 4. 启动管理后台

```bash
cd web
npm install
npm run dev   # http://localhost:5173 （已代理 /api -> :8080）
```

打开后台先**登录**（默认账号 `admin` / 密码 `wangjie`，可用控制面环境变量 `ADMIN_USER` / `ADMIN_PASS` 覆盖）。登录后即可看到自动注册上来的 `demo-service`：服务详情页展示其**运行实例**（在线状态、直连地址、frp 端口）与**接口列表**（可逐接口上下线、切换直连/中继模式），点「查看接口文档」可打开内嵌 **Swagger UI**（直连模式下的 try-it-out 会直接打到服务的直连地址）。

> 登录态：控制面 `POST /api/v1/login` 校验后签发会话 token（存 Redis，24h）；管理类接口需带 `Authorization: Bearer <token>`，Agent 注册/心跳与网关日志上报不受影响。

> 一期已具备：自动注册 / 心跳 / 反注册、OpenAPI 同步与文档查看、接口上下线、实例在线状态、直连模式可用。

## 环境变量

控制面 `server/`：

| 变量 | 默认 | 说明 |
|------|------|------|
| `LISTEN` | `:8080` | 监听地址 |
| `PG_DSN` | `postgres://apihub:apihub@localhost:5432/apihub?sslmode=disable` | PostgreSQL |
| `REDIS_ADDR` | `localhost:6379` | Redis |
| `PUBLIC_HOST` | `127.0.0.1` | 平台可达主机，用于拼直连URL/中继上游 |
| `FRP_SERVER_ADDR` / `FRP_SERVER_PORT` / `FRP_TOKEN` | - | 下发给 Agent 的 frps 接入信息 |
| `FRP_PORT_MIN` / `FRP_PORT_MAX` | `40000` / `49999` | frp 远程端口分配区间（须与 frps.toml 一致） |
| `APISIX_ADMIN` / `APISIX_ADMIN_KEY` | 空 | 配置后启用中继路由同步（指向 Admin API，如 http://localhost:9180） |
| `APISIX_GATEWAY` | `http://127.0.0.1:9080` | APISIX 数据面对外地址，用于拼中继入口 URL |
| `LOG_INGEST_URI` | 空 | APISIX 可达的访问日志接收地址（如 http://control-plane:8080/api/v1/ingest/access）；配置后中继路由自动挂 http-logger，监控页才有数据 |
| `HEARTBEAT_SEC` | `10` | 心跳间隔（TTL=3×） |

Agent `agent/`：`PLATFORM_URL`、`SERVICE_NAME`、`SERVICE_VERSION`、`SERVICE_ENV`、`SERVICE_OWNER`、`CONN_MODE`、`LOCAL_PORT`、`HEALTH_PATH`、`OPENAPI_URL`、`FRPC_BIN`、`AGENT_WORKDIR`。

## 落地分期

- **一期（已完成）**：注册/心跳/反注册、OpenAPI 同步与文档查看、接口上下线、实例在线状态，直连可用。
- **二期（已完成）**：APISIX 中继自动发布（按 `/r/{服务ID}` 命名空间隔离 + proxy-rewrite 还原路径、多在线实例负载均衡）、消费方 API Key 鉴权（key-auth）与配额/限流（limit-count）、Swagger 经网关调试（servers 含中继入口 + apikey 安全方案）。
- **三期（已完成）**：调用监控（APISIX http-logger → 控制面 `/ingest/access` 落库 → `/stats/calls` 聚合 → 前端时序/状态分布/Top接口图表）、控制面审计日志（注册、上下线、改模式/限流、签发Key 等操作可追溯）、接口级熔断（api-breaker，连续失败自动熔断）。
  - 监控页要有数据需配置 `LOG_INGEST_URI`，让网关把访问日志推回控制面。

### 中继调用（二期）

配置 `APISIX_ADMIN` 后，接口设为「中继」并上线即自动发布到网关。消费方调用：

```bash
# 中继入口 = APISIX网关 + /r/{服务ID} + 接口路径；鉴权走请求头 apikey
curl http://<网关>:9080/r/1/api/time -H "apikey: <在后台为消费方签发的Key>"
```

需鉴权的接口未带正确 `apikey` 会被网关拒绝；为消费方设置配额后超额返回 429。后台「服务详情」页展示该服务的中继入口，「查看接口文档」的 Swagger UI 可切换 direct/中继 servers 并通过 Authorize 填入 apikey 在线调试。
