export type DashboardStatus = {
  running?: boolean;
  addr?: string;
  timestamp?: string;
  version?: string;
  provider?: string;
  model?: string;
  sessions_total?: number;
  memory_total?: number;
  tools_builtin_total?: number;
  tools_model_visible_total?: number;
  total_requests?: number;
};

export type DashboardData = {
  api_addr?: string;
  provider?: string;
  model?: string;
  sessions_total?: number;
  memory_total?: number;
  tools_enabled?: number;
  tools_total?: number;
  skills_loaded?: number;
  total_requests?: number;
  telegram_platform?: string;
  telegram_registered?: boolean;
  telegram_connected?: boolean;
  telegram_proxy?: string;
  telegram_timeout_seconds?: number;
  timeout_events_24h?: number;
  timeout_events_by_layer?: Record<string, number>;
  timeout_last_error?: { layer?: string; config_path?: string; configured_seconds?: number; updated_at?: string };
  telegram_messages_received?: number;
  telegram_messages_sent?: number;
  telegram_errors?: number;
  telegram_state_source?: string;
  tools_builtin_total?: number;
  tools_model_visible_total?: number;
  sessions_recent?: Array<{ id?: string; title?: string; message_count?: number }>;
  cron_running?: boolean;
  cron_jobs_total?: number;
  cron_jobs?: Array<{ id?: string; status?: string }>;
};

export type RuntimeSession = {
  id: string;
  title?: string;
  message_count?: number;
  created_at?: string;
  updated_at?: string;
};

export type ProviderMessage = {
  role?: string;
  content?: string;
  name?: string;
  tool_call_id?: string;
};

export type SessionHistory = RuntimeSession & {
  messages?: ProviderMessage[];
  /** Paging fields, present only when the request carried a `limit`. */
  limit?: number;
  offset?: number;
  returned?: number;
  has_more?: boolean;
};

/**
 * Mirrors `gateway.Attachment` on the runtime. `file_path` is what the
 * multimodal pipeline reads: the upload endpoint writes the bytes to disk and
 * hands this descriptor back, because the chat WebSocket caps frames at 64 KiB.
 */
export type RuntimeAttachment = {
  type: 'image' | 'audio' | 'video' | 'document';
  file_name?: string;
  file_path?: string;
  file_url?: string;
  mime_type?: string;
  file_size?: number;
};

export type UploadResponse = {
  attachments?: RuntimeAttachment[];
  count?: number;
};

/** One note in the memory vault, or a wikilink target with no note behind it. */
export type MemoryTopologyNode = {
  id: string;
  title: string;
  category?: string;
  tier?: string;
  path?: string;
  tags?: string[];
  importance: number;
  degree: number;
  resolved: boolean;
};

export type MemoryTopologyEdge = {
  source: string;
  target: string;
  weight: number;
};

export type MemoryTopology = {
  nodes: MemoryTopologyNode[];
  edges: MemoryTopologyEdge[];
  total_notes: number;
  total_edges: number;
  isolated_count: number;
  unresolved: number;
  truncated: boolean;
  categories?: string[];
};

/** A node touched by a memory search: a recall seed, hop endpoint, or result. */
export type SearchTraceNode = {
  id: string;
  ref?: string;
  category?: string;
  tier?: string;
  score?: number;
  direct_score?: number;
  graph_score?: number;
  content_preview?: string;
};

/** One graph-spread step the recall took from an already-activated note. */
export type SearchTraceHop = {
  depth: number;
  from_id: string;
  from_ref?: string;
  to_id: string;
  to_ref?: string;
  via?: string;
  kind?: string;
  weight?: number;
  boost?: number;
  source_score?: number;
  target_score?: number;
};

export type SearchTraceResult = SearchTraceNode & {
  rank: number;
};

export type SearchTraceFilters = {
  category?: string;
  tier?: string;
  include_inactive?: boolean;
  include_expired?: boolean;
  as_of?: string;
};

/** Full evidence trail for one memory search: seeds, hop-by-hop spread, final results. */
export type SearchTrace = {
  query: string;
  mode?: string;
  source?: string;
  limit?: number;
  graph_depth: number;
  filters?: SearchTraceFilters;
  seeds?: SearchTraceNode[];
  hops?: SearchTraceHop[];
  results?: SearchTraceResult[];
  temporal_notes?: string[];
  warnings?: string[];
  duration_ms?: number;
};

/** A slash command the runtime can execute for a UI. */
export type CommandSpec = {
  name: string;
  usage: string;
  description: string;
  group: string;
};

export type CommandListResponse = {
  commands?: CommandSpec[];
  count?: number;
};

export type CommandResult = {
  command: string;
  ok: boolean;
  output: string;
};

export type SessionsResponse = {
  sessions?: RuntimeSession[];
  count?: number;
};

export type ToolTraceRecord = {
  name: string;
  arguments?: string;
  result?: string;
  success: boolean;
  error?: string;
  duration_ms?: number;
  annotation: string;
};

export type SessionToolTrace = {
  session_id: string;
  tools?: ToolTraceRecord[];
  total_calls?: number;
  successes?: number;
  failures?: number;
  success_rate?: number;
};

export type WsPayload = {
  type: string;
  id?: string;
  parent_id?: string;
  session_id?: string;
  timestamp?: string;
  data?: Record<string, unknown>;
};

export type ChatMessage = {
  id: string;
  /** `reasoning` and `tool_call` are live turn steps rendered inline in the thread. */
  role: 'user' | 'assistant' | 'tool' | 'system' | 'error' | 'reasoning' | 'tool_call';
  title: string;
  body: string;
  meta?: string;
};

/** Live state of one tool call, merged from its `tool_call` and `tool_result` events. */
export type ToolStep = {
  name: string;
  args?: string;
  output?: string;
  success?: boolean;
  round?: number;
  done: boolean;
};

export type GatewayStats = {
  MessagesSent?: number;
  MessagesReceived?: number;
  Errors?: number;
};

export type GatewayStatus = {
  name: string;
  running: boolean;
  stats?: GatewayStats;
};

export type GatewaysResponse = {
  gateways?: GatewayStatus[];
  count?: number;
};

export type ThoughtNote = {
  id: string;
  kind: 'reasoning' | 'tool' | 'status';
  text: string;
  meta?: string;
};

export type ActivityNote = {
  id: string;
  kind: 'reasoning' | 'tool' | 'status' | 'error' | 'socket';
  title: string;
  body: string;
  meta?: string;
};
