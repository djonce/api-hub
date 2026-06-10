-- 三期：访问日志（监控统计）、审计日志、接口熔断开关

BEGIN;

-- 接口熔断开关
ALTER TABLE api ADD COLUMN IF NOT EXISTS breaker_enabled BOOLEAN NOT NULL DEFAULT false;

-- 访问日志（由 APISIX http-logger 经控制面 /ingest/access 落库）
CREATE TABLE IF NOT EXISTS access_log (
  id          BIGSERIAL PRIMARY KEY,
  service_id  BIGINT,
  api_id      BIGINT,
  api_path    TEXT NOT NULL DEFAULT '',
  method      TEXT NOT NULL DEFAULT '',
  status      INT  NOT NULL DEFAULT 0,
  latency_ms  DOUBLE PRECISION NOT NULL DEFAULT 0,
  consumer    TEXT NOT NULL DEFAULT '',
  ts          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_access_ts ON access_log(ts);
CREATE INDEX IF NOT EXISTS idx_access_service ON access_log(service_id, ts);

-- 审计日志（控制面变更操作）
CREATE TABLE IF NOT EXISTS audit_log (
  id       BIGSERIAL PRIMARY KEY,
  action   TEXT NOT NULL,
  target   TEXT NOT NULL DEFAULT '',
  detail   TEXT NOT NULL DEFAULT '',
  ts       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);

COMMIT;
