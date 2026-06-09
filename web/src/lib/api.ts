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
  status: string;
  source: string;
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
  overview: () => req<Overview>("/stats/overview"),
  services: () => req<Service[]>("/services"),
  service: (id: number) => req<Service>(`/services/${id}`),
  serviceApis: (id: number) => req<Api[]>(`/services/${id}/apis`),
  patchApi: (id: number, patch: Partial<Pick<Api, "status" | "conn_mode" | "rate_limit">>) =>
    req<{ ok: boolean }>(`/apis/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),
  consumers: () => req<Consumer[]>("/consumers"),
  createConsumer: (name: string, description: string) =>
    req<Consumer>("/consumers", { method: "POST", body: JSON.stringify({ name, description }) }),
  createKey: (consumerId: number, quota_per_min: number) =>
    req<ApiKey>(`/consumers/${consumerId}/keys`, {
      method: "POST",
      body: JSON.stringify({ quota_per_min }),
    }),
};
