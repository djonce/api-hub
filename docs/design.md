# API 集合平台 · 设计文档

> 版本：v0.1（MVP 方案）
> 目标：各业务服务启动时自动向平台注册，并同步其可提供的接口信息；平台统一管理这些接口。服务可能没有外网 IP，通过 frp 穿透，支持「直连」与「平台中继」两种调用方式。

---

## 1. 概述

### 1.1 一句话定义

一个「服务 + 接口」的注册、管理与统一接入平台：

- **服务侧**：业务服务内置 Agent/SDK，启动即自动注册自己 + 上报接口清单（OpenAPI），并通过 frp 把自己暴露给平台。
- **平台侧**：维护服务/接口目录，提供管理后台；既能让消费方直连服务，也能作为网关中继转发（统一鉴权、限流、审计）。

### 1.2 设计主线：控制面 / 数据面分离

整套系统拆成两个相互独立的「面」，这是方案能稳、能落地的核心判断：

| 面 | 职责 | 关键词 | 技术载体 |
|----|------|--------|----------|
| **控制面 Control Plane** | 注册、心跳、元数据、接口管理、路由下发、端口分配 | 轻量、强一致、低频 | Go 注册中心 + PostgreSQL + Redis + React 后台 |
| **数据面 Data Plane** | 承载真实接口调用流量、穿透、鉴权、限流 | 高吞吐、高可用 | APISIX 网关 + frp 隧道 |

「注册」与「穿透/转发」是两件事：注册走控制面（HTTP，低频），流量走数据面（frp + 网关）。两者通过「控制面把路由/上游下发给数据面」来联动。

---

## 2. 技术选型（定稿）

| 模块 | 选型 | 说明 |
|------|------|------|
| 控制面后端（注册中心 + 控制台 API） | **Go** | 与 frp 同栈；并发与代理友好；Agent 可编译为单二进制易分发 |
| 数据面网关 | **APISIX**（etcd 存储） | 动态路由、插件齐全（key-auth / jwt-auth / limit-count / limit-req）；控制面通过其 Admin API 下发路由 |
| 穿透 | **frp**（fatedier/frp） | 无公网 IP 服务的隧道；frps 部署在平台侧 |
| 服务侧 Agent / SDK | **Go** | 自动注册 + 拉起 frpc + 心跳 + 反注册 |
| 元数据存储 | **PostgreSQL** | 服务、接口、消费方、Key 等持久化 |
| 在线状态 / 心跳 / 限流计数 | **Redis** | 实例在线集合、心跳 TTL、配额计数 |
| 管理后台前端 | **React + Vite + TypeScript + Ant Design** | SPA，对接控制面 REST API |
| 接口文档 / 调试 | Swagger UI / Redoc | 嵌入后台，try-it-out 经网关代理 |

> 备选：网关可换 **Higress**（Go 控制面 + Envoy 数据面，与 Go 同栈）；当前先用 APISIX，插件生态更成熟。

---

## 3. 总体架构

```
                         ┌──────────────────── 平台（有公网 IP） ─────────────────────┐
                         │                                                            │
   消费方 / 前端  ─直连──▶ │  frps           APISIX 网关          控制面 (Go)            │
        │                │ (穿透入口)   ┌──(中继转发)──┐   ┌── 注册中心 ──┐  ┌─ 控制台API ─┐ │
        │                │    ▲        │  鉴权/限流   │   │ register     │  │  管理后台    │ │
        └────中继─────────┼────┼────────┘  审计/路由   │   │ heartbeat    │  │  React SPA  │ │
                         │    │                ▲     │   │ openapi sync │  └─────────────┘ │
                         │    │ frp 隧道        │ 路由下发 │ port alloc   │                  │
                         │    │           (Admin API)    └──────┬───────┘                  │
                         │    │                                  │ 元数据/在线状态           │
                         │    │                          ┌───────┴────────┐                 │
                         │    │                          │ PostgreSQL+Redis│                 │
                         │    │                          └────────────────┘                 │
                         └────┼───────────────────────────────────────────────────────────┘
                              │ (frpc 反向连接)
            ┌─────────────────┼──────────────────┐
            │                 │                  │
   ┌────────┴────────┐  ┌─────┴──────────┐
   │ 服务 A（无公网IP） │  │ 服务 B（无公网IP） │
   │ ┌─ Agent ─┐      │  │ ┌─ Agent ─┐     │
   │ │ 注册/心跳 │ frpc │  │ │ 注册/心跳 │ frpc│
   │ └─────────┘      │  │ └─────────┘     │
   │   业务接口         │  │   业务接口        │
   └─────────────────┘  └────────────────┘
```

### 3.1 组件职责

- **注册中心（Go）**：接收注册 / 心跳 / 反注册；持久化服务与接口元数据；分配 frp 远程端口；维护实例在线状态。
- **控制台 API（Go）**：后台用的管理接口（服务/接口列表、上下线、消费方与 Key 管理、统计）。
- **APISIX 路由同步器（Go，控制面内）**：当接口「上线 + 中继模式」时，把它翻译成 APISIX 的 route / upstream / consumer 配置写入；下线时删除。
- **APISIX**：数据面网关，承载中继流量，执行鉴权与限流。
- **frps**：穿透入口，部署在平台侧。
- **Agent（Go）**：随业务服务一起运行（库 / sidecar / 进程），负责自动注册、拉起 frpc、心跳、退出反注册。

---

## 4. 直连 vs 中继（无公网 IP 前提下的真实语义）

无公网 IP 时，两种模式都要经过平台侧的 frps，区别在于「HTTP 链路上有没有平台网关」：

### 4.1 直连模式 Direct

```
消费方 ──HTTP──▶ frps 公网端口(:外部port) ──frp隧道──▶ 服务本地端口
```

- frp 只做 TCP/HTTP 转发，**平台网关不在链路上**。
- 平台目录记录该服务对外的 `直连 URL`（frps 公网地址 + 分配端口）。
- 优点：链路最短、延迟最低、平台无流量压力。
- 缺点：无统一鉴权 / 限流 / 审计（仅靠服务自身）。
- 适用：内部可信调用、对延迟敏感、平台不需要管控的接口。

### 4.2 中继模式 Relay

```
消费方 ──HTTP──▶ APISIX(平台) ──▶ frps 内网端口 ──frp隧道──▶ 服务本地端口
```

- frp 将服务暴露为**仅平台内网可达**的地址；APISIX 的 upstream 指向它。
- 平台在链路上，可统一做 key-auth / jwt-auth、limit-count 限流、访问日志与审计。
- 服务**不暴露任何公网端口**，最安全。
- 缺点：流量都过平台，平台是吞吐瓶颈（可水平扩展 APISIX）。
- 适用：对外 / 跨团队的程序化调用，需要管控配额与审计。

### 4.3 模式选择

按「服务」设默认，按「接口」可覆盖。后台可逐接口切换 Direct / Relay。

---

## 5. frp 集成设计

### 5.1 端口由控制面统一分配（关键）

平台是 frp 端口的**唯一真相源**，避免端口冲突与配置漂移：

1. Agent 启动，向注册中心 `POST /api/v1/register`，请求体含服务信息 + 接口清单 + 期望模式。
2. 注册中心为该实例分配 `frp_remote_port`（从可配置区间，如 `40000–49999`），返回：`frps 地址`、`frp token`、`remote_port`、`proxy_name`。
3. Agent 据此**动态生成 frpc 配置**并拉起 frpc 进程（或调用 frpc 的库 API）。
4. frpc 连上 frps，建立隧道；平台据 `remote_port` 即可访问该服务。

### 5.2 frps 配置（平台侧，节选）

```toml
# deploy/frps/frps.toml
bindPort = 7000
auth.method = "token"
auth.token = "CHANGE_ME_FRP_TOKEN"

# 允许的端口区间（与控制面端口分配器一致）
allowPorts = [{ start = 40000, end = 49999 }]

webServer.addr = "0.0.0.0"
webServer.port = 7500
webServer.user = "admin"
webServer.password = "CHANGE_ME"
```

### 5.3 frpc 配置（Agent 动态生成，节选）

```toml
serverAddr = "<平台公网IP>"
serverPort = 7000
auth.method = "token"
auth.token = "<注册返回的 token>"

[[proxies]]
name = "<proxy_name 由平台返回>"
type = "tcp"
localIP = "127.0.0.1"
localPort = <服务本地端口>
remotePort = <注册返回的 remote_port>
```

---

## 6. 注册与生命周期

### 6.1 注册流程

```
服务启动
  └─ Agent 读取本地 OpenAPI（/openapi.json 或框架路由表）
       └─ POST /api/v1/register  { service, instance, apis[], mode }
            └─ 注册中心：落库服务+接口，分配 frp 端口，写 Redis 在线集合
                 └─ 返回 { frps_addr, frp_token, remote_port, proxy_name, heartbeat_interval }
                      └─ Agent 生成 frpc 配置并拉起 frpc
                           └─ 进入心跳循环
```

### 6.2 心跳与摘除

- Agent 每 `heartbeat_interval`（默认 10s）`POST /api/v1/heartbeat`，刷新 Redis key TTL（默认 30s）。
- TTL 过期即视为离线，实例从在线集合摘除；中继路由对应 upstream 节点剔除。
- 元数据（服务/接口定义）保留在 PG，仅在线状态变化。

### 6.3 反注册

- 服务正常退出，Agent 捕获信号 `POST /api/v1/deregister`，立即摘除并停 frpc。

---

## 7. 数据模型（PostgreSQL DDL 摘要）

```sql
-- 服务
CREATE TABLE service (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT NOT NULL,
  version       TEXT NOT NULL DEFAULT '',
  env           TEXT NOT NULL DEFAULT 'prod',     -- prod/staging/dev
  owner         TEXT NOT NULL DEFAULT '',
  tags          TEXT[] NOT NULL DEFAULT '{}',
  conn_mode     TEXT NOT NULL DEFAULT 'relay',    -- 默认 direct/relay
  health_path   TEXT NOT NULL DEFAULT '/health',
  status        TEXT NOT NULL DEFAULT 'enabled',  -- enabled/disabled
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (name, version, env)
);

-- 实例（运行态，频繁变化的在线信息放 Redis；这里存稳定的连接信息）
CREATE TABLE instance (
  id              BIGSERIAL PRIMARY KEY,
  service_id      BIGINT NOT NULL REFERENCES service(id) ON DELETE CASCADE,
  instance_uid    TEXT NOT NULL,                  -- Agent 生成的唯一标识
  local_port      INT NOT NULL,
  frp_remote_port INT NOT NULL,
  frp_proxy_name  TEXT NOT NULL,
  direct_url      TEXT NOT NULL DEFAULT '',       -- 直连模式对外URL
  relay_upstream  TEXT NOT NULL DEFAULT '',       -- 中继模式平台内网地址
  last_seen_at    TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (service_id, instance_uid)
);

-- 接口
CREATE TABLE api (
  id           BIGSERIAL PRIMARY KEY,
  service_id   BIGINT NOT NULL REFERENCES service(id) ON DELETE CASCADE,
  path         TEXT NOT NULL,
  method       TEXT NOT NULL,
  summary      TEXT NOT NULL DEFAULT '',
  grp          TEXT NOT NULL DEFAULT '',          -- 分组/标签
  req_schema   JSONB,
  resp_schema  JSONB,
  auth_required BOOLEAN NOT NULL DEFAULT true,
  rate_limit   INT NOT NULL DEFAULT 0,            -- 0=不限
  conn_mode    TEXT,                              -- 覆盖 service.conn_mode；NULL=继承
  status       TEXT NOT NULL DEFAULT 'enabled',   -- enabled/disabled（上下线）
  source       TEXT NOT NULL DEFAULT 'openapi',   -- openapi/manual
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (service_id, method, path)
);

-- 消费方
CREATE TABLE consumer (
  id          BIGSERIAL PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 消费方 API Key（中继鉴权）
CREATE TABLE api_key (
  id          BIGSERIAL PRIMARY KEY,
  consumer_id BIGINT NOT NULL REFERENCES consumer(id) ON DELETE CASCADE,
  api_key     TEXT NOT NULL UNIQUE,
  quota_per_min INT NOT NULL DEFAULT 0,           -- 0=不限
  status      TEXT NOT NULL DEFAULT 'enabled',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Redis 键约定：

- `instance:online:{service_id}` → Set，成员为在线 `instance_uid`，每成员独立 TTL（用 sorted set + score=过期时间，或单 key+TTL）。
- `heartbeat:{instance_uid}` → string，TTL=30s。
- `ratelimit:{api_key}:{minute}` → counter（也可全交给 APISIX 插件）。

---

## 8. 接口契约（控制面 REST API 摘要）

Agent ↔ 注册中心：

```
POST /api/v1/register
  req:  { service:{name,version,env,owner,tags,conn_mode,health_path},
          instance:{instance_uid,local_port},
          apis:[{path,method,summary,group,req_schema,resp_schema,auth_required}] }
  resp: { service_id, instance_id,
          frp:{server_addr,server_port,token,remote_port,proxy_name},
          heartbeat_interval }

POST /api/v1/heartbeat     req:{ instance_uid }                resp:{ ok }
POST /api/v1/deregister    req:{ instance_uid }                resp:{ ok }
```

控制台 ↔ 后端（React 调用）：

```
GET    /api/v1/services                  服务列表（含在线实例数）
GET    /api/v1/services/{id}             服务详情
GET    /api/v1/services/{id}/apis        接口列表
PATCH  /api/v1/apis/{id}                 改 status(上下线)/conn_mode/rate_limit
GET    /api/v1/apis/{id}/openapi         单接口 OpenAPI 片段（供 Swagger 调试）
GET    /api/v1/consumers                 消费方列表
POST   /api/v1/consumers                 新建消费方
POST   /api/v1/consumers/{id}/keys       签发 API Key
GET    /api/v1/stats/overview            概览统计
```

---

## 9. 鉴权设计（覆盖两类消费方）

- **服务 → 平台（注册鉴权）**：注册请求携带平台签发的 `register token`（部署时分发，或首启引导）。
- **消费方 → 接口（调用鉴权）**：
  - 内部开发者：登录后台（SSO / 账号），在线查文档、调试，调试请求由后台代签。
  - 程序化客户端：持 **API Key**（或 JWT），中继模式下由 APISIX 的 `key-auth` / `jwt-auth` 插件校验，`limit-count` 按 Key 限流配额。
- **直连模式**的鉴权由业务服务自理（平台不在链路上）；后台会对「直连 + 需鉴权」的接口给出提示。

---

## 10. 落地分期

### 一期 · 控制面闭环（MVP，本次脚手架覆盖）

注册中心（注册/心跳/反注册/列表）、Agent、示例服务、React 管理后台（看 / 上下线 / Swagger 文档）、frp 自动接入；**直连模式可用**。

### 二期 · 数据面中继 + 鉴权

APISIX 接入与路由自动同步、中继模式打通、消费方 / API Key 鉴权与限流、在线调试经网关代理。

### 三期 · 运营能力

Prometheus + Grafana 调用统计与监控、访问日志与审计、多实例负载均衡与健康联动摘除、接口版本管理 / 灰度 / 熔断。

---

## 11. 仓库结构

```
api-hub/
├── docs/
│   └── design.md            # 本文档
├── deploy/
│   ├── docker-compose.yml   # PG / Redis / etcd / APISIX / frps
│   ├── frps/frps.toml
│   ├── apisix/config.yaml
│   └── migrations/001_init.sql
├── server/                  # Go 控制面（注册中心 + 控制台 API）
│   ├── go.mod
│   ├── cmd/server/main.go
│   └── internal/{api,store,frpalloc,apisix,model}
├── agent/                   # Go Agent / SDK
│   ├── go.mod
│   └── cmd/agent/main.go
├── examples/
│   └── demo-service/        # Go 示例业务服务（含 /openapi.json）
├── web/                     # React + Vite + TS + Antd 管理后台
└── README.md
```

---

## 12. 待二期细化的开放项

- APISIX 路由同步的幂等与一致性（控制面崩溃恢复后的对账）。
- frp 端口耗尽 / 回收策略，frps 多副本时的端口协调。
- 多实例时直连模式的负载均衡（DNS / 客户端 LB / 平台下发实例列表）。
- OpenAPI 版本变更的 diff 与接口变更审批流。
- 后台权限模型（RBAC：服务 owner / 平台管理员 / 只读）。
```
