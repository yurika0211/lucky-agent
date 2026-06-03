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
};

export type SessionsResponse = {
  sessions?: RuntimeSession[];
  count?: number;
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
  role: 'user' | 'assistant' | 'tool' | 'system' | 'error';
  title: string;
  body: string;
  meta?: string;
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
