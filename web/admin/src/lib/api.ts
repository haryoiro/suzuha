const BASE_URL = import.meta.env.VITE_API_URL ?? "";

async function fetchJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    throw new Error(`API error: ${res.status}`);
  }
  return res.json();
}

// Types
export interface Memory {
  id: string;
  type: "user" | "world" | "tool";
  content: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ListParams {
  offset?: number;
  limit?: number;
  type?: string;
  q?: string;
  order?: string;
  dir?: string;
}

export interface ListResponse {
  data: Memory[];
  total: number;
}

export interface MetricItem {
  name: string;
  help: string;
  type: string;
  value?: number;
  labels?: Record<string, string>;
  buckets?: { le: number; count: number }[];
  sum?: number;
  count?: number;
}

export interface MetricsResponse {
  metrics: MetricItem[];
}

export interface LogEntry {
  seq: number;
  time: string;
  level: string;
  msg: string;
  source?: string;
  attrs?: Record<string, unknown>;
}

function toQuery(params: object): string {
  const entries = Object.entries(params).filter(
    ([, v]) => v !== undefined && v !== ""
  );
  if (entries.length === 0) return "";
  return "?" + new URLSearchParams(entries.map(([k, v]) => [k, String(v)])).toString();
}

// Memories API
export const memoriesApi = {
  list: (params: ListParams) =>
    fetchJSON<ListResponse>(`/api/memories${toQuery(params)}`),
  get: (id: string) =>
    fetchJSON<{ data: Memory }>(`/api/memories/${id}`),
  create: (body: { type: string; content: string; metadata?: Record<string, unknown> }) =>
    fetchJSON<{ data: Memory }>("/api/memories", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  update: (id: string, body: { type?: string; content?: string; metadata?: Record<string, unknown> }) =>
    fetchJSON<{ data: Memory }>(`/api/memories/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  delete: (id: string) =>
    fetch(`${BASE_URL}/api/memories/${id}`, { method: "DELETE" }),
};

// Metrics API
export const metricsApi = {
  json: () => fetchJSON<MetricsResponse>("/api/metrics/json"),
};

// Log stream (SSE)
export function connectLogStream(params?: {
  level?: string;
  source?: string;
}): EventSource {
  const qs = toQuery(params ?? {});
  return new EventSource(`${BASE_URL}/api/logs/stream${qs}`);
}
