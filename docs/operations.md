# API 集合平台 · 操作文档（本地运行与访问）

> 配套设计文档见 [`design.md`](design.md)，一键启动见根目录 [`README.md`](../README.md)。
> 本文聚焦：**有哪些服务、各自端口与用途、怎么访问、怎么启动/停止/排错**。

---

## 1. 服务总览

| 服务 | 端口 | 类型 | 用途 | 怎么访问 |
|------|------|------|------|----------|
| **Web 管理后台** | `5173`（本地 dev）/ `8088`（Docker） | 网页 | 服务/接口管理、上下线、Swagger 文档、监控、审计、消费方与 API Key | 浏览器打开 |
| **控制面 API** | `8080` | HTTP API（非网页） | 注册中心 + 控制台后端：注册/心跳/反注册、接口管理、APISIX 路由同步、访问日志采集与统计、审计 | curl / 前端 / GET 端点可浏览器打开 |
| **APISIX 中继网关** | `9080`（数据面）/ `9180`（Admin API） | 网关 | 消费方经 `/r/{服务ID}{路径}` 中继调用后端服务，统一鉴权/限流/熔断/日志 | curl / 浏览器（GET 接口） |
| **示例服务 demo-service** | `9000` | 业务服务 | 演示接入：暴露 `/openapi.json` 与 `/api/time`、`/api/echo` | curl / 浏览器（GET 接口） |
| **Agent** | 无监听端口 | 边车进程 | 随业务服务运行：自动注册、心跳、退出反注册、（可选）拉起 frpc | 看日志 |
| PostgreSQL | `5432` | 依赖 | 元数据 / 访问日志 / 审计落库 | 内部 |
| Redis | `6379` | 依赖 | 实例在线状态（TTL）、frp 端口分配 | 内部 |
| etcd | `2379` | 依赖 | APISIX 配置存储 | 内部 |

> ⚠️ **最常见误解**：直接用浏览器打开 `http://localhost:8080` 会显示 `404 page not found`。
> 这是**正常**的 —— `8080` 是后端 **API 服务**，没有注册根路径 `/`，不是给浏览器看的页面。
> **要看界面请打开 Web 管理后台（`http://localhost:5173`）。**
>
> 一句话记忆：**8080 = 后端接口，5173/8088 = 管理界面。**

---

## 2. 控制面 API 端点清单（`:8080`）

### 2.1 可在浏览器直接打开的 GET 端点

| 端点 | 说明 |
|------|------|
| `GET /healthz` | 健康检查，返回纯文本 `ok` |
| `GET /api/v1/services` | 服务列表 |
| `GET /api/v1/services/{id}` | 服务详情 |
| `GET /api/v1/services/{id}/apis` | 某服务的接口清单 |
| `GET /api/v1/services/{id}/instances` | 某服务的运行实例（在线状态、直连地址、frp 端口） |
| `GET /api/v1/services/{id}/openapi` | 该服务的 OpenAPI 文档 |
| `GET /api/v1/config` | 平台下发配置 |
| `GET /api/v1/consumers` | 消费方列表 |
| `GET /api/v1/stats/overview` | 概览统计 |
| `GET /api/v1/stats/calls?hours=1` | 调用时序/状态分布/Top 接口统计 |
| `GET /api/v1/audit` | 审计日志 |

示例（服务 ID = 1）：

```
http://localhost:8080/healthz
http://localhost:8080/api/v1/services
http://localhost:8080/api/v1/services/1
http://localhost:8080/api/v1/services/1/apis
http://localhost:8080/api/v1/services/1/instances
http://localhost:8080/api/v1/services/1/openapi
http://localhost:8080/api/v1/stats/calls?hours=1
http://localhost:8080/api/v1/audit
```

### 2.2 写操作端点（POST / PATCH，需用 curl 或后台触发，浏览器直接打开会 404/405）

| 端点 | 说明 |
|------|------|
| `POST /api/v1/register` | 服务注册（Agent 调用） |
| `POST /api/v1/heartbeat` | 心跳（Agent 调用） |
| `POST /api/v1/deregister` | 反注册（Agent 退出时调用） |
| `PATCH /api/v1/apis/{id}` | 接口上下线 / 切直连·中继 / 限流 / 熔断 |
| `POST /api/v1/services/{id}/sync` | 把该服务的中继路由重新发布到 APISIX |
| `POST /api/v1/consumers` | 新建消费方 |
| `POST /api/v1/consumers/{id}/keys` | 为消费方签发 API Key |
| `POST /api/v1/ingest/access` | 接收 APISIX http-logger 推送的访问日志（网关内部调用） |

---

## 3. 中继调用（经网关 `:9080`）

中继入口 = `网关 + /r/{服务ID} + 接口原始路径`，鉴权走请求头 `apikey`。

```bash
# 无需鉴权的接口
curl http://localhost:9080/r/1/api/time

# 需鉴权的接口：未带正确 apikey 会被网关拒绝（401）
curl -X POST http://localhost:9080/r/1/api/echo -H 'Content-Type: application/json' -d '{"hello":"world"}'
# -> 401

# 带在后台为消费方签发的 Key
curl -X POST http://localhost:9080/r/1/api/echo -H 'apikey: <你的Key>' -H 'Content-Type: application/json' -d '{"hello":"world"}'
```

> 接口设为「中继」并上线后，控制面会自动把路由发布到 APISIX；下线则删除路由。
> 为消费方设置配额后，超额返回 `429`。

---

## 4. 启动方式

### 方式 A：Docker Compose 全量（推荐，最接近真实部署）

整套（PG / Redis / etcd / APISIX / 控制面 / 示例服务 / Agent / Web 后台）一条命令拉起：

```bash
cd deploy
docker compose up -d --build     # 首次会在容器内编译 Go 与构建前端，耐心等
python3 e2e.py                   # 端到端冒烟校验（核心链路 PASS 即通过）
```

启动后访问：

- 管理后台 → http://localhost:8088
- 控制面 API → http://localhost:8080
- 中继网关 → http://localhost:9080 （示例：`curl http://localhost:9080/r/1/api/time`）
- 示例服务直连 → http://localhost:9000

> 容器内部各服务用服务名互通（如 `server:8080`、`demo-service:9000`），无需 frp。
> 真实「无外网 IP」场景再启用 frp：`docker compose --profile frp up -d`。

### 方式 B：混合本地联调（基础设施 Docker + Go 服务原生跑）

适合**网络受限**（Docker Hub 拉 `golang`/`node` 基础镜像很慢）或想本地改 Go 代码热调的场景：基础设施用 Docker（镜像小、易缓存），三个 Go 服务用本机 `go` 直接跑（配 `GOPROXY` 后依赖拉取很快）。

```bash
# 1) 只起基础设施
cd deploy
docker compose up -d postgres redis etcd apisix

# 2) 起示例业务服务（终端 A）
cd ../examples/demo-service && PORT=9000 go run .

# 3) 起控制面（终端 B）
cd ../server && go mod tidy        # 首次需要，生成 go.sum
PG_DSN="postgres://apihub:apihub@localhost:5432/apihub?sslmode=disable" \
REDIS_ADDR="localhost:6379" LISTEN=":8080" PUBLIC_HOST="localhost" \
APISIX_ADMIN="http://localhost:9180" APISIX_ADMIN_KEY="apihub-admin-key" \
APISIX_GATEWAY="http://localhost:9080" \
LOG_INGEST_URI="http://host.docker.internal:8080/api/v1/ingest/access" \
HEARTBEAT_SEC="10" \
go run ./cmd/server

# 4) 起 Agent（终端 C）—— SERVICE_HOST 让网关可直达本机服务，免 frp
cd ../agent
PLATFORM_URL="http://localhost:8080" \
SERVICE_NAME="demo-service" SERVICE_VERSION="1.0.0" SERVICE_ENV="dev" \
SERVICE_HOST="host.docker.internal" LOCAL_PORT="9000" \
OPENAPI_URL="http://localhost:9000/openapi.json" CONN_MODE="relay" \
go run ./cmd/agent

# 5) 起 Web 后台（终端 D）
cd ../web && npm install && npm run dev   # http://localhost:5173 （已代理 /api -> :8080）
```

> **关键点**：方式 B 下控制面跑在宿主机，而 APISIX 在容器里。为让「网关 → 后端服务」和「网关 http-logger → 控制面」都能从容器回到宿主机，需要用 `host.docker.internal`（Docker Desktop 提供）。
> 若个别场景 `host.docker.internal` 解析异常，可改用宿主机在容器网络里的 IP（Docker Desktop 一般是 `192.168.65.254`）。
> 在**方式 A（全 Docker）**里则用容器服务名 `server:8080`，无此问题。

---

## 5. 快速验证

```bash
# 控制面活着
curl -s http://localhost:8080/healthz                       # -> ok

# demo-service 是否自动注册
curl -s http://localhost:8080/api/v1/services

# 接口是否已同步
curl -s http://localhost:8080/api/v1/services/1/apis

# 中继调用是否打通
curl -s http://localhost:9080/r/1/api/time                  # -> {"now":...}

# 监控统计是否有数据（需配置 LOG_INGEST_URI 并产生过中继调用）
curl -s "http://localhost:8080/api/v1/stats/calls?hours=1"

# 一键端到端冒烟
cd deploy && python3 e2e.py
```

---

## 6. 停止与清理

```bash
# 方式 B 的原生进程（按端口/进程名结束）
kill $(lsof -nP -iTCP:5173 -iTCP:8080 -iTCP:9000 -sTCP:LISTEN -t) 2>/dev/null
pkill -f 'cmd/agent' 2>/dev/null

# 容器（-v 连数据卷一起删，下次为全新库）
cd deploy && docker compose down -v
```

---

## 7. 运行测试

Go 测试分两类：

- **离线单元测试**：无需任何外部依赖，直接跑。覆盖 APISIX 路由/消费方下发（`apisix`）、frp 端口分配（`frpalloc`，用内存版 redis）、工具函数与访问日志解析（`api`）、Agent 的 OpenAPI 解析。

  ```bash
  cd server && go test ./...
  cd ../agent && go test ./...
  ```

- **store 集成测试**：需要真实 PG + Redis，默认**跳过**；配置环境变量后才运行。

  ```bash
  cd deploy && docker compose up -d postgres redis     # 起依赖（镜像小、快）
  cd ../server
  APIHUB_TEST_PG_DSN="postgres://apihub:apihub@localhost:5432/apihub?sslmode=disable" \
  APIHUB_TEST_REDIS="localhost:6379" \
  go test ./internal/store/ -v
  ```

  > 集成测试会在每个用例开始时 `TRUNCATE` 相关表并 `FLUSHDB`，请只对**专用测试库**运行。

看覆盖率加 `-cover`。

## 8. 国内镜像加速（Docker 全量构建慢/拉取超时时）

国内直连 Docker Hub 拉 `golang`/`node`/`alpine`/`nginx` 基础镜像可能被限速到不可用。三处分别走国内源即可让 `docker compose up --build` 顺畅跑通：

1. **基础镜像 → 配置 Docker registry 镜像源**（机器级，不在仓库里）。编辑 `~/.docker/daemon.json` 加入 `registry-mirrors`，然后**重启 Docker**：

   ```json
   {
     "registry-mirrors": [
       "https://docker.m.daocloud.io",
       "https://docker.1panel.live",
       "https://hub.rat.dev",
       "https://docker.1ms.run"
     ]
   }
   ```

   > 公共镜像源时有失效，可换其它可用源；`docker info | grep -A5 'Registry Mirrors'` 确认生效。
   > macOS 改完需在 Docker Desktop 里**完全退出再启动**（仅点 Restart 有时不重载 daemon.json）。

2. **Go 模块 → `GOPROXY`**：各 Go 服务的 `Dockerfile` 已内置 `ARG GOPROXY=https://goproxy.cn,direct`（`direct` 兜底保证海外仍可用）。需要改回官方：`docker compose build --build-arg GOPROXY=https://proxy.golang.org,direct`。

3. **npm 依赖 → `NPM_REGISTRY`**：`web/Dockerfile` 已内置 `ARG NPM_REGISTRY=https://registry.npmmirror.com`。改回官方：`--build-arg NPM_REGISTRY=https://registry.npmjs.org`。

> 配好镜像源后，全量构建（含编译 Go 三个服务 + 构建前端 + 拉 PG/Redis/etcd/APISIX）几分钟内即可完成，`python3 e2e.py` 应全绿。

## 9. 已知注意事项

- **监控页要有数据**：必须给控制面配置 `LOG_INGEST_URI`（APISIX 可达的 `/api/v1/ingest/access` 地址），网关才会把访问日志推回控制面落库；否则 `/stats/calls` 为空。
- **etcd 镜像**：`bitnami/etcd:3.5` 已在 Docker Hub 下架，compose 使用 `quay.io/coreos/etcd:v3.5.21`。
- **`/healthz` 返回纯文本**：是 `ok` 而非 JSON，自写探活脚本时不要对它做 JSON 解析。
- **首次原生构建 server**：需先 `go mod tidy` 生成 `go.sum`；建议设置 `GOPROXY`（国内可用 `https://goproxy.cn,direct`）。
- **APISIX 关键端口**：`9080` 数据面（对外中继入口）、`9180` Admin API（控制面下发路由用，带 `X-API-KEY`）。
