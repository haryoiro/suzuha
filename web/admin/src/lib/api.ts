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
  type: "user" | "world" | "tool" | "rss" | "episode" | "self";
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
  vecStats: () =>
    fetchJSON<VecStatsResponse>("/api/memories/vec-stats"),
  listWithVec: (params: ListParams) =>
    fetchJSON<ListWithVecResponse>(`/api/memories/with-vec${toQuery(params)}`),
  duplicates: (threshold?: number) =>
    fetchJSON<DuplicatesResponse>(
      `/api/memories/duplicates${threshold != null ? `?threshold=${threshold}` : ""}`
    ),
};

export interface VecStatsResponse {
  total_memories: number;
  embedded_count: number;
  missing_count: number;
  coverage_pct: number;
}

export interface MemoryWithVec extends Memory {
  has_embedding: boolean;
}

export interface ListWithVecResponse {
  data: MemoryWithVec[];
  total: number;
}

export interface DuplicateGroup {
  memories: Memory[];
}

export interface DuplicatesResponse {
  data: DuplicateGroup[];
  total: number;
}

// Users API
export interface User {
  id: string;
  display_name: string;
  role: string;
  is_bot: boolean;
  affinity: number;
  closeness: number;
  trust: number;
  interest: number;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  platforms?: PlatformLink[];
}

export interface PlatformLink {
  platform: string;
  platform_user_id: string;
  platform_name: string;
}

export interface AffinityEvent {
  id: string;
  user_id: string;
  delta: number;
  axis: string;
  reason: string;
  created_at: string;
}

export interface UserGuildChannel {
  guild_id: string;
  guild_name: string;
  channel_id: string;
  channel_name: string;
  last_seen_at: string;
}

export interface UserMemory {
  id: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export const usersApi = {
  list: (params: ListParams) =>
    fetchJSON<{ data: User[]; total: number }>(`/api/users${toQuery(params)}`),
  get: (id: string) =>
    fetchJSON<{ data: User }>(`/api/users/${id}`),
  update: (id: string, body: { display_name?: string; role?: string; is_bot?: boolean }) =>
    fetchJSON<{ ok: boolean }>(`/api/users/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  affinityEvents: (id: string, limit?: number) =>
    fetchJSON<{ data: AffinityEvent[] }>(
      `/api/users/${id}/affinity${limit ? `?limit=${limit}` : ""}`
    ),
  guilds: (id: string) =>
    fetchJSON<{ data: UserGuildChannel[] }>(`/api/users/${id}/guilds`),
  memories: (id: string, limit?: number) =>
    fetchJSON<{ data: UserMemory[] }>(
      `/api/users/${id}/memories${limit ? `?limit=${limit}` : ""}`
    ),
};

// Guilds API
export interface Guild {
  id: string;
  name: string;
  updated_at: string;
  member_count: number;
  channel_count: number;
}

export interface GuildChannel {
  channel_id: string;
  channel_name: string;
  user_count: number;
  last_seen_at: string;
  last_user_message_at?: string;
}

export interface ChannelEntry {
  channel_id: string;
  channel_name: string;
  guild_id: string;
  guild_name: string;
}

export const guildsApi = {
  list: () =>
    fetchJSON<{ data: Guild[] }>("/api/guilds"),
  channels: (id: string) =>
    fetchJSON<{ data: GuildChannel[] }>(`/api/guilds/${id}/channels`),
  allChannels: () =>
    fetchJSON<{ data: ChannelEntry[] }>("/api/channels"),
};

// Channel Settings API
export interface ChannelSetting {
  channel_id: string;
  channel_name: string;
  guild_id: string;
  guild_name: string;
  user_count: number;
  mode: "active" | "listen" | "disabled";
  home: boolean;
  last_user_message_at?: string;
  settings_updated_at?: string;
}

export const channelSettingsApi = {
  list: (guildId?: string) =>
    fetchJSON<{ data: ChannelSetting[] }>(
      `/api/channel-settings${guildId ? `?guild_id=${guildId}` : ""}`
    ),
  upsert: (channelId: string, body: { mode: string; home: boolean; guild_id?: string }) =>
    fetchJSON<{ ok: boolean }>(`/api/channel-settings/${channelId}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  delete: (channelId: string) =>
    fetch(`${BASE_URL}/api/channel-settings/${channelId}`, { method: "DELETE" }),
};

// Scheduled Actions API
export interface ScheduledAction {
  id: string;
  channel_id: string;
  content: string;
  scheduled_at: string;
  cron_expr?: string;
  created_by?: string;
  status: string;
  executed_at?: string;
  created_at: string;
}

export const actionsApi = {
  list: (status?: string) =>
    fetchJSON<{ data: ScheduledAction[] }>(
      `/api/scheduled-actions${status ? `?status=${status}` : ""}`
    ),
  create: (body: { channel_id: string; content: string; scheduled_at: string; cron_expr?: string }) =>
    fetchJSON<{ data: { id: string } }>("/api/scheduled-actions", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  update: (id: string, body: { channel_id?: string; content?: string; scheduled_at?: string; cron_expr?: string; status?: string }) =>
    fetchJSON<{ ok: boolean }>(`/api/scheduled-actions/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  delete: (id: string) =>
    fetch(`${BASE_URL}/api/scheduled-actions/${id}`, { method: "DELETE" }),
};

// Feeds API
export interface Feed {
  id: string;
  name: string;
  url: string;
  channel_id: string;
  created_by: string;
  enabled: boolean;
  last_polled?: string;
  created_at: string;
  updated_at: string;
}

export interface FeedItem {
  id: string;
  feed_id: string;
  guid: string;
  title: string;
  link: string;
  description: string;
  published_at?: string;
  memory_id: string;
  notified: boolean;
  created_at: string;
}

export interface FeedStats {
  total: number;
  enabled: number;
}

export const feedsApi = {
  list: () =>
    fetchJSON<{ data: Feed[]; total: number }>("/api/feeds"),
  get: (id: string) =>
    fetchJSON<{ data: Feed }>(`/api/feeds/${id}`),
  create: (body: { name: string; url: string; channel_id: string }) =>
    fetchJSON<{ data: Feed }>("/api/feeds", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  update: (id: string, body: { name?: string; url?: string; channel_id?: string; enabled?: boolean }) =>
    fetchJSON<{ data: Feed }>(`/api/feeds/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  delete: (id: string) =>
    fetch(`${BASE_URL}/api/feeds/${id}`, { method: "DELETE" }),
  items: (id: string, params: { offset?: number; limit?: number }) =>
    fetchJSON<{ data: FeedItem[]; total: number }>(`/api/feeds/${id}/items${toQuery(params)}`),
  stats: () =>
    fetchJSON<FeedStats>("/api/feeds/stats"),
};

// Metrics API
export const metricsApi = {
  json: () => fetchJSON<MetricsResponse>("/api/metrics/json"),
};

// Agent API
export const agentApi = {
  compact: () =>
    fetchJSON<{ ok: boolean; message_count: number }>("/api/agent/compact", {
      method: "POST",
    }),
};

// Boredom API
export interface BoredomStatus {
  boredom: number;
  last_interaction: string | null;
  last_channel?: string;
  last_posted_at: string | null;
  post_threshold: number;
}

export const boredomApi = {
  get: () => fetchJSON<BoredomStatus>("/api/boredom"),
};

// Context API
export interface ContextMessage {
  role: string;
  content: string;
  user_id?: string;
  user_name?: string;
  source?: string;
  channel?: string;
  message_id?: string;
  timestamp: string;
  tool_call_id?: string;
  tool_calls?: unknown[];
}

export interface ContextResponse {
  messages: ContextMessage[];
  count: number;
  estimated_tokens: number;
  usage_ratio: number;
  max_tokens: number;
}

export const contextApi = {
  get: () => fetchJSON<ContextResponse>("/api/context"),
};

// Prompts API
export interface PromptFile {
  name: string;
  content: string;
  updated_at?: string;
}

export const promptsApi = {
  list: () => fetchJSON<PromptFile[]>("/api/prompts"),
  get: (name: string) => fetchJSON<PromptFile>(`/api/prompts/${name}`),
  update: (name: string, content: string) =>
    fetchJSON<{ ok: boolean; reloaded: boolean }>(`/api/prompts/${name}`, {
      method: "PUT",
      body: JSON.stringify({ content }),
    }),
};

// Log stream (SSE)
export function connectLogStream(params?: {
  level?: string;
  source?: string;
}): EventSource {
  const qs = toQuery(params ?? {});
  return new EventSource(`${BASE_URL}/api/logs/stream${qs}`);
}
