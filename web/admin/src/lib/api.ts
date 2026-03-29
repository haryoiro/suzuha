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
export interface MemoryAttachment {
  key: string;
  modality: "image" | "audio";
  mime_type: string;
}

export interface Memory {
  id: string;
  type: "user" | "world" | "tool" | "episode" | "self" | "memo";
  content: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

/** Extract attachments from metadata (stored as metadata.attachments in DB). */
export function getAttachments(mem: Memory): MemoryAttachment[] {
  const raw = mem.metadata?.attachments;
  if (!Array.isArray(raw)) return [];
  return raw.filter(
    (a): a is MemoryAttachment => typeof a === "object" && a !== null && "key" in a
  );
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
  uploadMedia: async (memoryId: string, file: File, modality?: string) => {
    const form = new FormData();
    form.append("file", file);
    if (modality) form.append("modality", modality);
    const res = await fetch(`${BASE_URL}/api/memories/${memoryId}/media`, {
      method: "POST",
      body: form,
    });
    if (!res.ok) throw new Error(`Upload failed: ${res.status}`);
    return res.json() as Promise<MemoryAttachment>;
  },
};

export function getMediaURL(key: string): string {
  return `${BASE_URL}/api/media/${key}`;
}

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
  mode: string;
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
  create: (body: { channel_id: string; content: string; mode?: string; scheduled_at?: string; cron_expr?: string }) =>
    fetchJSON<{ data: { id: string } }>("/api/scheduled-actions", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  update: (id: string, body: { channel_id?: string; content?: string; mode?: string; scheduled_at?: string; cron_expr?: string; status?: string }) =>
    fetchJSON<{ ok: boolean }>(`/api/scheduled-actions/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  delete: (id: string) =>
    fetch(`${BASE_URL}/api/scheduled-actions/${id}`, { method: "DELETE" }),
};

// VOICEVOX API
export interface VoicevoxStyle {
  name: string;
  id: number;
}

export interface VoicevoxSpeaker {
  name: string;
  speaker_uuid: string;
  styles: VoicevoxStyle[];
}

export const voicevoxApi = {
  speakers: () => fetchJSON<VoicevoxSpeaker[]>("/api/voicevox/speakers"),
  currentSpeaker: () => fetchJSON<{ speaker_id: number }>("/api/voicevox/speaker"),
  setSpeaker: (speakerId: number) =>
    fetchJSON<{ ok: boolean }>("/api/voicevox/speaker", {
      method: "PUT",
      body: JSON.stringify({ speaker_id: speakerId }),
    }),
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

export type ContextSource = "discord" | "device" | "web";

export interface ContextResponse {
  messages: ContextMessage[];
  count: number;
  estimated_tokens: number;
  usage_ratio: number;
  max_tokens: number;
  ephemeral?: ContextMessage[];
  source?: ContextSource;
}

export const contextApi = {
  get: (source?: ContextSource) => {
    const q = source ? `?source=${source}` : "";
    return fetchJSON<ContextResponse>(`/api/context${q}`);
  },
  deleteChannel: (channelId: string) => {
    return fetch(`/api/channels/${channelId}`, { method: "DELETE" }).then(
      (r) => r.json()
    );
  },
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

// Forget (memory deduplication) API
export interface ForgetMemory {
  id: string;
  type: string;
  content: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface ForgetGroup {
  type: string;
  members: ForgetMemory[];
  avg_distance: number;
}

export interface ForgetStatus {
  has_run: boolean;
  last_run_at?: string;
  total_deleted?: number;
  total_merged?: number;
}

export const forgetApi = {
  groups: (threshold?: number) =>
    fetchJSON<{ data: ForgetGroup[]; total: number }>(
      `/api/forget/groups${threshold != null ? `?threshold=${threshold}` : ""}`
    ),
  status: () => fetchJSON<ForgetStatus>("/api/forget/status"),
  delete: (deleteIds: string[]) =>
    fetchJSON<{ deleted: number }>("/api/forget/delete", {
      method: "POST",
      body: JSON.stringify({ delete_ids: deleteIds }),
    }),
  merge: (deleteIds: string[], mergedContent: string, type: string) =>
    fetchJSON<{ deleted: number; merged: boolean }>("/api/forget/merge", {
      method: "POST",
      body: JSON.stringify({ delete_ids: deleteIds, merged_content: mergedContent, type }),
    }),
  run: (similarityThreshold?: number) =>
    fetchJSON<{ ok: boolean; error?: string }>("/api/forget/run", {
      method: "POST",
      body: similarityThreshold != null
        ? JSON.stringify({ similarity_threshold: similarityThreshold })
        : undefined,
    }),
};

// Location API
export interface LocationDevice {
  device_id: string;
  owner_name: string;
  user_id?: string;
  user_display_name?: string;
  created_at?: string;
}

export interface LocationPlace {
  id: string;
  name: string;
  latitude: number;
  longitude: number;
  radius_m: number;
  created_at?: string;
}

export const locationApi = {
  listDevices: () =>
    fetchJSON<{ data: LocationDevice[] }>("/api/location/devices"),
  upsertDevice: (deviceId: string, body: { owner_name: string; user_id?: string }) =>
    fetchJSON<{ ok: boolean }>(`/api/location/devices/${encodeURIComponent(deviceId)}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  deleteDevice: (deviceId: string) =>
    fetch(`${BASE_URL}/api/location/devices/${encodeURIComponent(deviceId)}`, { method: "DELETE" }),
  listPlaces: () =>
    fetchJSON<{ data: LocationPlace[] }>("/api/location/places"),
  createPlace: (body: { name: string; latitude: number; longitude: number; radius_m: number }) =>
    fetchJSON<{ ok: boolean }>("/api/location/places", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updatePlace: (id: string, body: { name: string; latitude: number; longitude: number; radius_m: number }) =>
    fetchJSON<{ ok: boolean }>(`/api/location/places/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  deletePlace: (id: string) =>
    fetch(`${BASE_URL}/api/location/places/${id}`, { method: "DELETE" }),
};

// Tools API
export interface ToolInfo {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
  enabled: boolean;
}

export const toolsApi = {
  list: () => fetchJSON<{ data: ToolInfo[] }>("/api/tools"),
  toggle: (name: string, enabled: boolean) =>
    fetchJSON<{ ok: boolean }>(`/api/tools/${encodeURIComponent(name)}/enabled`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),
};

// LLM Provider API
export interface LLMPreset {
  name: string;
  provider: string;
  model: string;
  api_base: string;
  max_tokens: number;
  vision: boolean;
}

export interface LLMProviderInfo {
  provider: string;
  model: string;
  api_base: string;
  max_ctx: number;
  vision: boolean;
  presets: LLMPreset[];
}

export const llmApi = {
  get: () => fetchJSON<LLMProviderInfo>("/api/llm"),
  update: (body: { preset?: string; provider?: string; model?: string; api_key?: string; api_base?: string; max_ctx?: number; vision?: boolean }) =>
    fetchJSON<{ ok: boolean }>("/api/llm", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
};

// Identity API
export interface BotIdentity {
  bot_platform_id: string;
  bot_user_id?: string;
  bot_name?: string;
}

export const identityApi = {
  get: () => fetchJSON<BotIdentity>("/api/identity"),
};

// Scheduler API
export interface SchedulerJob {
  name: string;
  task: string;
  cron: string;
  config?: Record<string, unknown>;
  prev: string;
  next: string;
}

export const schedulerApi = {
  jobs: () => fetchJSON<{ data: SchedulerJob[] }>("/api/scheduler/jobs"),
  trigger: (task: string) =>
    fetchJSON<{ ok: boolean }>(`/api/scheduler/trigger/${encodeURIComponent(task)}`, {
      method: "POST",
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

// Device API
export interface DeviceDetection {
  label: string;
  confidence: number;
  bbox: [number, number, number, number]; // x1, y1, x2, y2
}

export interface DeviceDetectionEvent {
  detections: DeviceDetection[];
  inference_ms: number;
  timestamp: number;
  frame_width: number;
  frame_height: number;
}

export function getDeviceFrameURL(): string {
  return `${BASE_URL}/api/device/frame`;
}

export function connectDetectionStream(): EventSource {
  return new EventSource(`${BASE_URL}/api/device/detections`);
}

export const deviceVisionApi = {
  get: () => fetchJSON<{ enabled: boolean }>("/api/device/vision"),
  set: (enabled: boolean) =>
    fetchJSON<{ ok: boolean }>("/api/device/vision", {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),
};

export const deviceServoApi = {
  set: (pan: number, tilt: number) =>
    fetchJSON<{ ok: boolean }>("/api/device/servo", {
      method: "POST",
      body: JSON.stringify({ pan, tilt }),
    }),
};

export const deviceVolumeApi = {
  set: (level: number) =>
    fetchJSON<{ ok: boolean; level: number }>("/api/device/volume", {
      method: "PUT",
      body: JSON.stringify({ level }),
    }),
};

export interface TrackerConfig {
  target_label: string;
  confirm_frames: number;
  lost_frames: number;
  iou_threshold: number;
  min_confidence: number;
  smoothing_alpha: number;
  dead_zone: number;
  proportional_gain: number;
  max_deg_per_frame: number;
  frame_width: number;
  frame_height: number;
  invert_pan: boolean;
  invert_tilt: boolean;
}

export interface TrackerStatus {
  enabled: boolean;
  config: TrackerConfig;
}

export const deviceTrackerApi = {
  get: () => fetchJSON<TrackerStatus>("/api/device/tracker"),
  set: (body: { enabled?: boolean; target_label?: string } & Partial<TrackerConfig>) =>
    fetchJSON<{ ok: boolean }>("/api/device/tracker", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
};
