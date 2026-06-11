// 控制面 REST API 客户端

const BASE = "/api/v1";

// ---- 登录态：token 存 localStorage ----
const TOKEN_KEY = "apihub_token";
export const getToken = () => localStorage.getItem(TOKEN_KEY) || "";
export const setToken = (t: string) => localStorage.setItem(TOKEN_KEY, t);
export const clearToken = () => localStorage.removeItem(TOKEN_KEY);
export const isAuthed = () => !!getToken();

// 登录态变化（登录/登出/会话过期）时广播，App 据此切换登录页/后台。
export const AUTH_EVENT = "apihub-auth-changed";
const notifyAuthChanged = () => window.dispatchEvent(new Event(AUTH_EVENT));

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
  const token = getToken();
  const resp = await fetch(BASE + path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init?.headers || {}),
    },
  });
  if (resp.status === 401) {
    // 未登录/会话过期：清除 token 并通知 App 跳登录页。
    clearToken();
    notifyAuthChanged();
    throw new Error("未登录或登录已过期");
  }
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`${resp.status}: ${text}`);
  }
  if (resp.status === 204) return undefined as T;
  return (await resp.json()) as T;
}

export const api = {
  // 登录态
  login: async (username: string, password: string) => {
    const r = await req<{ token: string; username: string }>("/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    });
    setToken(r.token);
    notifyAuthChanged();
    return r;
  },
  logout: async () => {
    try {
      await req<{ ok: boolean }>("/logout", { method: "POST" });
    } catch {
      /* 忽略：本地清 token 即可 */
    }
    clearToken();
    notifyAuthChanged();
  },
  me: () => req<{ username: string }>("/me"),
  // OpenAPI 文档（受登录保护，取回文档对象交给 Swagger UI 渲染）
  openapi: (id: number) => req<Record<string, unknown>>(`/services/${id}/openapi`),
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
