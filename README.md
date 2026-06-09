# API 集合平台（API Hub）

各业务服务启动时自动向平台注册，并同步可提供的接口（OpenAPI）；平台统一管理这些接口。服务可能无外网 IP，通过 **frp** 穿透，支持「直连」与「平台中继」两种调用方式。

详细设计见 [`docs/design.md`](docs/design.md)。

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

## 快速开始

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

打开后台即可看到自动注册上来的 `demo-service` 及其接口，可对接口做上下线与模式切换。

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
| `APISIX_ADMIN` / `APISIX_ADMIN_KEY` | 空 | 配置后启用中继路由同步 |
| `HEARTBEAT_SEC` | `10` | 心跳间隔（TTL=3×） |

Agent `agent/`：`PLATFORM_URL`、`SERVICE_NAME`、`SERVICE_VERSION`、`SERVICE_ENV`、`SERVICE_OWNER`、`CONN_MODE`、`LOCAL_PORT`、`HEALTH_PATH`、`OPENAPI_URL`、`FRPC_BIN`、`AGENT_WORKDIR`。

## 落地分期

- **一期（本脚手架）**：注册/心跳/接口同步 + 后台管理 + frp 接入，直连可用。
- **二期**：APISIX 中继 + 消费方鉴权/限流 + 在线调试。
- **三期**：监控统计、审计日志、多实例负载、版本/灰度/熔断。
