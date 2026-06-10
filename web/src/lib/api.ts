// 控制面 REST API 客户端

const BASE = "/api/v1";

export interface Service {
  id: number;
  name: string;
  version: string;
  env: string;
  owner: string;
  tags: string[];
  conn_mode: string;
  health_path: string;
  status: string;
  online_count: number;
  created_at: string;
  updated_at: string;
}

export interface Api {
  id: number;
  service_id: number;
  path: string;
  method: string;
  summary: string;
  group: string;
  auth_required: boolean;
  rate_limit: number;
  conn_mode: string | null;
  breaker_enabled: boolean;
  status: string;
  source: string;
}

export interface Instance {
  id: number;
  service_id: number;
  instance_uid: string;
  local_port: number;
  frp_remote_port: number;
  frp_proxy_name: string;
  direct_url: string;
  relay_upstream: string;
  last_seen_at: string | null;
  online: boolean;
}

export interface Consumer {
  id: number;
  name: string;
  description: string;
  created_at: string;
}

export interface ApiKey {
  id: number;
  consumer_id: number;
  api_key: string;
  quota_per_min: number;
  status: string;
  created_at: string;
}

export interface Overview {
  services: number;
  apis: number;
  online_services: number;
  consumers: number;
}

export interface AppConfig {
  apisix_enabled: boolean;
  gateway_url: string;
}

export interface SeriesPoint {
  t: string;
  count: number;
}
export interface StatusCount {
  status: number;
  count: number;
}
export interface PathCount {
  path: string;
  count: number;
}
export interface CallStats {
  total: number;
  success: number;
  error: number;
  series: SeriesPoint[];
  by_status: StatusCount[];
  top_apis: PathCount[];
}
export interface AuditEntry {
  id: number;
  action: string;
  target: string;
  detail: string;
  ts: string;
}

// 浏览器直接访问的 OpenAPI 文档地址（供 Swagger UI 加载）
export const openapiUrl = (serviceId: number) => `${BASE}/services/${serviceId}/openapi`;

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(BASE + path, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`${resp.status}: ${text}`);
  }
  if (resp.status === 204) return undefined as T;
  return (await resp.json()) as T;
}

export const api = {
  config: () => req<AppConfig>("/config"),
  overview: () => req<Overview>("/stats/overview"),
  callStats: (hours = 24, serviceId?: number) =>
    req<CallStats>(`/stats/calls?hours=${hours}${serviceId ? `&service_id=${serviceId}` : ""}`),
  audit: (limit = 200) => req<AuditEntry[]>(`/audit?limit=${limit}`),
  services: () => req<Service[]>("/services"),
  service: (id: number) => req<Service>(`/services/${id}`),
  serviceApis: (id: number) => req<Api[]>(`/services/${id}/apis`),
  serviceInstances: (id: number) => req<Instance[]>(`/services/${id}/instances`),
  patchApi: (
    id: number,
    patch: Partial<Pick<Api, "status" | "conn_mode" | "rate_limit" | "breaker_enabled">>
  ) => req<{ ok: boolean }>(`/apis/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),
  syncService: (id: number) =>
    req<{ ok: boolean }>(`/services/${id}/sync`, { method: "POST" }),
  consumers: () => req<Consumer[]>("/consumers"),
  createConsumer: (name: string, description: string) =>
    req<Consumer>("/consumers", { method: "POST", body: JSON.stringify({ name, description }) }),
  createKey: (consumerId: number, quota_per_min: number) =>
    req<ApiKey>(`/consumers/${consumerId}/keys`, {
      method: "POST",
      body: JSON.stringify({ quota_per_min }),
    }),
};
