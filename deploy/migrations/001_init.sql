-- API 集合平台 · 初始化表结构
-- 适用于 PostgreSQL 13+

BEGIN;

CREATE TABLE IF NOT EXISTS service (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT NOT NULL,
  version       TEXT NOT NULL DEFAULT '',
  env           TEXT NOT NULL DEFAULT 'prod',
  owner         TEXT NOT NULL DEFAULT '',
  tags          TEXT[] NOT NULL DEFAULT '{}',
  conn_mode     TEXT NOT NULL DEFAULT 'relay',     -- direct / relay
  health_path   TEXT NOT NULL DEFAULT '/health',
  status        TEXT NOT NULL DEFAULT 'enabled',   -- enabled / disabled
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (name, version, env)
);

CREATE TABLE IF NOT EXISTS instance (
  id              BIGSERIAL PRIMARY KEY,
  service_id      BIGINT NOT NULL REFERENCES service(id) ON DELETE CASCADE,
  instance_uid    TEXT NOT NULL,
  local_port      INT NOT NULL,
  frp_remote_port INT NOT NULL,
  frp_proxy_name  TEXT NOT NULL,
  direct_url      TEXT NOT NULL DEFAULT '',
  relay_upstream  TEXT NOT NULL DEFAULT '',
  last_seen_at    TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (service_id, instance_uid)
);

CREATE TABLE IF NOT EXISTS api (
  id            BIGSERIAL PRIMARY KEY,
  service_id    BIGINT NOT NULL REFERENCES service(id) ON DELETE CASCADE,
  path          TEXT NOT NULL,
  method        TEXT NOT NULL,
  summary       TEXT NOT NULL DEFAULT '',
  grp           TEXT NOT NULL DEFAULT '',
  req_schema    JSONB,
  resp_schema   JSONB,
  auth_required BOOLEAN NOT NULL DEFAULT true,
  rate_limit    INT NOT NULL DEFAULT 0,
  conn_mode     TEXT,                              -- NULL = 继承 service.conn_mode
  status        TEXT NOT NULL DEFAULT 'enabled',   -- enabled / disabled
  source        TEXT NOT NULL DEFAULT 'openapi',   -- openapi / manual
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (service_id, method, path)
);

CREATE TABLE IF NOT EXISTS consumer (
  id          BIGSERIAL PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_key (
  id            BIGSERIAL PRIMARY KEY,
  consumer_id   BIGINT NOT NULL REFERENCES consumer(id) ON DELETE CASCADE,
  api_key       TEXT NOT NULL UNIQUE,
  quota_per_min INT NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'enabled',
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_api_service ON api(service_id);
CREATE INDEX IF NOT EXISTS idx_instance_service ON instance(service_id);

COMMIT;
