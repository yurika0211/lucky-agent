import { useEffect, useMemo, useRef, useState } from 'react';
import type {
  ActivityNote,
  ChatMessage,
  CommandListResponse,
  CommandResult,
  CommandSpec,
  DashboardData,
  DashboardStatus,
  ProviderMessage,
  RuntimeAttachment,
  RuntimeSession,
  SearchTrace,
  SessionHistory,
  SessionsResponse,
  ToolStep,
  UploadResponse,
  WsPayload,
} from './types';
import { Markdown } from './components/Markdown';
import { Gateways } from './components/Gateways';
import { Settings } from './components/Settings';
import { Trajectory } from './components/Trajectory';
import { MemoryGraph } from './components/MemoryGraph';
import { Skills } from './components/Skills';

type ThemeMode = 'light' | 'dark';
type WorkspaceView = 'chat' | 'trajectory' | 'gateways' | 'skills' | 'settings' | 'memory';

type Bubble = ChatMessage & {
  attachments?: Array<{ type: 'image' | 'file'; name: string; url: string }>;
  /** Correlates a `tool_call` with the `tool_result` that completes it. */
  stepId?: string;
  tool?: ToolStep;
  /** Round this step belongs to (role === 'reasoning'), used to merge in the real content once it arrives. */
  round?: number;
};

/**
 * A file the composer is holding. `url` is a local blob URL used only for the
 * thumbnail; `uploaded` is the descriptor the runtime actually consumes, and it
 * only exists once the bytes have reached the server.
 */
type Pending = {
  id: string;
  type: 'image' | 'file';
  name: string;
  url: string;
  status: 'uploading' | 'ready' | 'failed';
  error?: string;
  uploaded?: RuntimeAttachment;
};

const DEFAULT_API_BASE = 'http://127.0.0.1:9090';
const DEFAULT_SESSION = 'dashboard-main';
const MAX_MESSAGES = 500;
const MAX_ACTIVITY = 48;
const HISTORY_PAGE = 60;
const COMPOSER_MAX_HEIGHT = 260;

const SUGGESTIONS = [
  'Summarize what this session has done so far',
  'Which tools are available right now?',
  'Check the gateway status and report failures',
  'Explain the current runtime configuration',
];

/* ---------------------------------------------------------------- icons */

const stroke = {
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.7,
  strokeLinecap: 'round' as const,
  strokeLinejoin: 'round' as const,
};

function IconPanel() {
  return (
    <svg {...stroke}>
      <rect x="3" y="4" width="18" height="16" rx="2.5" />
      <path d="M9.5 4v16" />
    </svg>
  );
}

function IconPlus() {
  return (
    <svg {...stroke}>
      <path d="M12 5v14M5 12h14" />
    </svg>
  );
}

function IconChat() {
  return (
    <svg {...stroke}>
      <path d="M20 15a3 3 0 0 1-3 3H8l-4 3V6a3 3 0 0 1 3-3h10a3 3 0 0 1 3 3z" />
    </svg>
  );
}

function IconRoute() {
  return (
    <svg {...stroke}>
      <circle cx="6" cy="18" r="2.5" />
      <circle cx="18" cy="6" r="2.5" />
      <path d="M15.5 6H10a4 4 0 0 0 0 8h4a4 4 0 0 1 0 8H8.5" />
    </svg>
  );
}

function IconPlug() {
  return (
    <svg {...stroke}>
      <path d="M9 3v6M15 3v6M6 9h12v3a6 6 0 0 1-6 6 6 6 0 0 1-6-6zM12 18v3" />
    </svg>
  );
}

function IconGear() {
  return (
    <svg {...stroke}>
      <circle cx="12" cy="12" r="3.2" />
      <path d="M19.5 12a7.5 7.5 0 0 0-.1-1.2l2-1.5-2-3.4-2.3.9a7.6 7.6 0 0 0-2.1-1.2L14.6 3h-4l-.4 2.6c-.8.3-1.5.7-2.1 1.2l-2.3-.9-2 3.4 2 1.5a7.5 7.5 0 0 0 0 2.4l-2 1.5 2 3.4 2.3-.9c.6.5 1.3.9 2.1 1.2l.4 2.6h4l.4-2.6c.8-.3 1.5-.7 2.1-1.2l2.3.9 2-3.4-2-1.5c.1-.4.1-.8.1-1.2z" />
    </svg>
  );
}

function IconSun() {
  return (
    <svg {...stroke}>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
    </svg>
  );
}

function IconMoon() {
  return (
    <svg {...stroke}>
      <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z" />
    </svg>
  );
}

function IconRefresh() {
  return (
    <svg {...stroke}>
      <path d="M20 11a8 8 0 1 0-.6 4M20 5v6h-6" />
    </svg>
  );
}

function IconClip() {
  return (
    <svg {...stroke}>
      <path d="M20 11.5 12 19.5a4.6 4.6 0 0 1-6.5-6.5l8.3-8.3a3 3 0 0 1 4.3 4.3l-8.3 8.3a1.5 1.5 0 0 1-2.2-2.2l7.6-7.6" />
    </svg>
  );
}

function IconArrowUp() {
  return (
    <svg {...stroke} strokeWidth={2}>
      <path d="M12 19V5M6 11l6-6 6 6" />
    </svg>
  );
}

function IconStop() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor">
      <rect x="7" y="7" width="10" height="10" rx="2" />
    </svg>
  );
}

function IconSearch() {
  return (
    <svg {...stroke}>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m16 16 4.5 4.5" />
    </svg>
  );
}

function IconPencil() {
  return (
    <svg {...stroke}>
      <path d="M4 20h4l10-10a2.8 2.8 0 0 0-4-4L4 16z" />
    </svg>
  );
}

function IconCheck() {
  return (
    <svg {...stroke} strokeWidth={2}>
      <path d="m5 12.5 4.5 4.5L19 7" />
    </svg>
  );
}

function IconClose() {
  return (
    <svg {...stroke}>
      <path d="m6 6 12 12M18 6 6 18" />
    </svg>
  );
}

function IconCopy() {
  return (
    <svg {...stroke}>
      <rect x="9" y="9" width="11" height="11" rx="2.5" />
      <path d="M15 5.5A2.5 2.5 0 0 0 12.5 3h-7A2.5 2.5 0 0 0 3 5.5v7A2.5 2.5 0 0 0 5.5 15" />
    </svg>
  );
}

function IconThought() {
  return (
    <svg {...stroke}>
      <path d="M9 18h6M10 21h4" />
      <path d="M12 3a6 6 0 0 0-3.5 10.9c.5.4.8 1 .8 1.6v.5h5.4v-.5c0-.6.3-1.2.8-1.6A6 6 0 0 0 12 3z" />
    </svg>
  );
}

function IconTool() {
  return (
    <svg {...stroke}>
      <path d="M14.5 6.5a3.8 3.8 0 0 0 5 5l-8 8a2.6 2.6 0 0 1-3.7-3.7z" />
      <path d="M14.5 6.5 12 4l2-2 4 4-2 2z" />
    </svg>
  );
}

function IconSkill() {
  return (
    <svg {...stroke}>
      <path d="M12 3.2 4.8 7v10L12 20.8 19.2 17V7Z" />
      <path d="M12 3.2V20.8M4.8 7l7.2 4 7.2-4" />
    </svg>
  );
}

function IconGraph() {
  return (
    <svg {...stroke}>
      <circle cx="12" cy="5" r="2.2" />
      <circle cx="5" cy="17" r="2.2" />
      <circle cx="19" cy="17" r="2.2" />
      <path d="M10.6 6.8 6.4 15.2M13.4 6.8l4.2 8.4M7.2 17h9.6" />
    </svg>
  );
}

function IconActivity() {
  return (
    <svg {...stroke}>
      <path d="M3 12h3.5l2.5 7 4-14 2.5 7H21" />
    </svg>
  );
}

function IconFile() {
  return (
    <svg {...stroke}>
      <path d="M14 3v5h5" />
      <path d="M19 8v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7z" />
    </svg>
  );
}

// One heart-shaped leaf, tip at the origin, body pointing up; four rotated
// copies plus a stem make the four-leaf clover from the app icon.
const CLOVER_LEAF =
  'M0 0C0 0 -3.78 -2.73 -3.78 -5.12C-3.78 -6.47 -2.77 -7.39 -1.64 -7.39C-0.84 -7.39 -0.25 -6.93 0 -6.47C0.25 -6.93 0.84 -7.39 1.64 -7.39C2.77 -7.39 3.78 -6.47 3.78 -5.12C3.78 -2.73 0 0 0 0Z';

function LogoMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <g transform="translate(11.4 11)" fill="currentColor">
        <path d={CLOVER_LEAF} transform="rotate(40)" />
        <path d={CLOVER_LEAF} transform="rotate(130)" />
        <path d={CLOVER_LEAF} transform="rotate(220)" />
        <path d={CLOVER_LEAF} transform="rotate(310)" />
      </g>
      <path
        d="M11.6 12.6c.2 3.4 1.1 5.6 2.8 7.2"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

/* ------------------------------------------------------------- helpers */

function normalizeApiBase(value: string): string {
  const raw = value.trim();
  if (!raw) return '';
  const defaultHost = window.location.hostname || '127.0.0.1';
  const defaultScheme = window.location.protocol === 'https:' ? 'https://' : 'http://';
  let target = raw;
  if (/^\d+$/.test(target)) target = `${defaultHost}:${target}`;
  else if (/^:\d+$/.test(target)) target = `${defaultHost}${target}`;
  else if (/^\/\//.test(target)) target = `${window.location.protocol}${target}`;
  else if (/^wss?:\/\//i.test(target)) target = target.replace(/^ws/i, 'http');
  else if (!/^https?:\/\//i.test(target)) target = `${defaultScheme}${target}`;

  try {
    const url = new URL(target);
    if (!url.hostname || url.hostname === '0.0.0.0' || url.hostname === '::') {
      url.hostname = defaultHost;
    }
    return `${url.protocol}//${url.host}`.replace(/\/+$/, '');
  } catch {
    return target.replace(/\/+$/, '');
  }
}

function nowLabel(): string {
  return new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function makeId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === '') return 'n/a';
  if (Array.isArray(value)) return value.length ? value.join(', ') : '[]';
  return String(value);
}

function preview(value: unknown, max = 260): string {
  const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  const clean = (text || '').trim();
  if (!clean) return 'No output';
  return clean.length > max ? `${clean.slice(0, max - 3)}...` : clean;
}

function sessionAge(value?: string): string {
  if (!value) return 'unknown';
  const time = new Date(value).getTime();
  if (!Number.isFinite(time)) return 'unknown';
  const hours = Math.max(0, (Date.now() - time) / 36e5);
  if (hours < 1) return 'now';
  if (hours < 24) return `${Math.floor(hours)}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function greeting(): string {
  const hour = new Date().getHours();
  if (hour < 5) return 'Still up';
  if (hour < 12) return 'Good morning';
  if (hour < 18) return 'Good afternoon';
  return 'Good evening';
}

function roleTitle(role: Bubble['role'], name?: string): string {
  if (role === 'user') return 'You';
  if (role === 'assistant') return 'LuckyAgent';
  if (role === 'tool') return name ? `Tool: ${name}` : 'Tool';
  if (role === 'error') return 'Runtime Error';
  return 'System';
}

function normalizeBubbleRole(role?: string): Bubble['role'] {
  if (role === 'user' || role === 'assistant' || role === 'tool' || role === 'system') return role;
  return 'system';
}

function historyToBubbles(history?: ProviderMessage[]): Bubble[] {
  const bubbles: Bubble[] = [];
  for (const msg of history || []) {
    const role = normalizeBubbleRole(msg.role);
    const body = String(msg.content || '').trim();
    if (!body) continue;
    bubbles.push({
      id: makeId(`history-${role}`),
      role,
      title: roleTitle(role, msg.name),
      body,
      meta: 'history',
    });
  }
  return bubbles;
}

const VIEW_TITLES: Record<WorkspaceView, string> = {
  chat: 'Chat',
  trajectory: 'Tool trajectory',
  gateways: 'Messaging gateways',
  skills: 'Skills',
  settings: 'Settings',
  memory: 'Memory graph',
};

/* ----------------------------------------------------------------- app */

export function App() {
  const [theme, setTheme] = useState<ThemeMode>(() => {
    const current = typeof document !== 'undefined' ? document.documentElement.dataset.theme : '';
    return current === 'dark' ? 'dark' : 'light';
  });
  const [view, setView] = useState<WorkspaceView>('chat');
  const [apiBase, setApiBase] = useState(DEFAULT_API_BASE);
  const [session, setSession] = useState(DEFAULT_SESSION);
  const [status, setStatus] = useState<DashboardStatus>({});
  const [data, setData] = useState<DashboardData>({});
  const [connected, setConnected] = useState(false);
  const [socketState, setSocketState] = useState<'idle' | 'connecting' | 'connected' | 'running' | 'error'>('idle');
  const [messages, setMessages] = useState<Bubble[]>([]);
  const [activity, setActivity] = useState<ActivityNote[]>([]);
  // Lifted here (not local to MemoryGraph) because the Memory tab fully
  // unmounts on view switch — live traces must survive that.
  const [memoryTraces, setMemoryTraces] = useState<Array<SearchTrace & { _receivedAt: number }>>([]);
  const [feed, setFeed] = useState<string[]>([]);
  const [sessions, setSessions] = useState<RuntimeSession[]>([]);
  const [sessionQuery, setSessionQuery] = useState('');
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [sessionsError, setSessionsError] = useState('');
  const [composer, setComposer] = useState('');
  const [rawLog, setRawLog] = useState('Waiting for runtime data...');
  const [loadingDashboard, setLoadingDashboard] = useState(false);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [connectionOpen, setConnectionOpen] = useState(false);
  const [autoScroll, setAutoScroll] = useState(true);
  const [copiedId, setCopiedId] = useState('');
  const [lightbox, setLightbox] = useState<{ src: string; alt: string } | null>(null);
  const [commands, setCommands] = useState<CommandSpec[]>([]);
  const [commandIndex, setCommandIndex] = useState(0);
  const [historyLoaded, setHistoryLoaded] = useState(0);
  const [historyHasMore, setHistoryHasMore] = useState(false);
  const [loadingEarlier, setLoadingEarlier] = useState(false);
  const [attachments, setAttachments] = useState<Pending[]>([]);
  const [renamingSession, setRenamingSession] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const wsRef = useRef<WebSocket | null>(null);
  const assistantDraftRef = useRef('');
  const assistantBubbleRef = useRef<string | null>(null);
  const streamRef = useRef<HTMLDivElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const composerRef = useRef<HTMLTextAreaElement | null>(null);
  const streamFrameRef = useRef<number | null>(null);
  const keepScrollRef = useRef<number | null>(null);
  const messageCapRef = useRef(MAX_MESSAGES);
  const commandDismissedRef = useRef(false);

  const effectiveBase = useMemo(() => normalizeApiBase(apiBase) || DEFAULT_API_BASE, [apiBase]);
  const busy = socketState === 'running';
  const uploading = attachments.some((item) => item.status === 'uploading');

  // The palette shows while the composer holds a bare `/name` prefix.
  const commandQuery = /^\/[a-z_-]*$/i.test(composer) ? composer.slice(1).toLowerCase() : null;
  const commandMatches = useMemo(() => {
    if (commandQuery === null || commandDismissedRef.current) return [];
    return commands.filter((spec) => spec.name.startsWith(commandQuery)).slice(0, 8);
  }, [commandQuery, commands]);
  const activeSession = sessions.find((item) => item.id === session);
  const chatTitle = activeSession?.title || session || DEFAULT_SESSION;

  function pushBubble(role: Bubble['role'], title: string, body: string, meta?: string, attachments?: Bubble['attachments']): string {
    const next: Bubble = { id: makeId(role), role, title, body, meta: meta || nowLabel(), attachments };
    setMessages((prev) => [...prev, next].slice(-messageCapRef.current));
    return next.id;
  }

  function updateBubble(id: string, body: string, meta?: string) {
    setMessages((prev) => prev.map((item) => (item.id === id ? { ...item, body, meta: meta ?? item.meta } : item)));
  }

  /**
   * Places a turn step (reasoning or tool call) directly before the assistant
   * bubble it belongs to. The bubble is created the moment a message is sent,
   * so appending would show the agent's steps *after* its answer.
   */
  function insertStep(step: Bubble) {
    setMessages((prev) => {
      const anchor = assistantBubbleRef.current;
      const at = anchor ? prev.findIndex((item) => item.id === anchor) : -1;
      const next = at >= 0 ? [...prev.slice(0, at), step, ...prev.slice(at)] : [...prev, step];
      return next.slice(-messageCapRef.current);
    });
  }

  /** Merges a tool_call and its later tool_result into one step in the thread. */
  function upsertToolStep(stepId: string, name: string, build: (prev?: ToolStep) => ToolStep) {
    setMessages((prev) => {
      const matches = (item: Bubble) =>
        item.role === 'tool_call' &&
        (stepId ? item.stepId === stepId : item.tool?.name === name && !item.tool?.done);
      // Without a step_id, fall back to the newest unfinished call of the same
      // tool, which is the one a result can only belong to.
      let index = -1;
      for (let i = prev.length - 1; i >= 0; i -= 1) {
        if (matches(prev[i])) {
          index = i;
          break;
        }
      }
      if (index >= 0) {
        const existing = prev[index];
        const tool = build(existing.tool);
        const updated: Bubble = { ...existing, tool, title: tool.name, meta: tool.done ? 'done' : 'running' };
        return prev.map((item, i) => (i === index ? updated : item));
      }
      const tool = build(undefined);
      const step: Bubble = {
        id: makeId('tool'),
        role: 'tool_call',
        title: tool.name,
        body: '',
        meta: tool.done ? 'done' : 'running',
        stepId: stepId || undefined,
        tool,
      };
      const anchor = assistantBubbleRef.current;
      const at = anchor ? prev.findIndex((item) => item.id === anchor) : -1;
      const next = at >= 0 ? [...prev.slice(0, at), step, ...prev.slice(at)] : [...prev, step];
      return next.slice(-messageCapRef.current);
    });
  }

  /**
   * Replaces a reasoning step's placeholder body ("Analyzing the request") with
   * the model's real reasoning text once it arrives, in place, instead of
   * inserting a second card for the same round.
   */
  function upsertReasoningStep(round: number, body: string) {
    setMessages((prev) => {
      let index = -1;
      for (let i = prev.length - 1; i >= 0; i -= 1) {
        if (prev[i].role === 'reasoning' && prev[i].round === round) {
          index = i;
          break;
        }
      }
      if (index < 0) return prev;
      const updated: Bubble = { ...prev[index], body, meta: nowLabel() };
      return prev.map((item, i) => (i === index ? updated : item));
    });
  }

  function pushActivity(kind: ActivityNote['kind'], title: string, body: string, meta?: string) {
    const next: ActivityNote = { id: makeId(kind), kind, title, body, meta: meta || nowLabel() };
    setActivity((prev) => [next, ...prev].slice(0, MAX_ACTIVITY));
  }

  function pushFeed(text: string) {
    setFeed((prev) => [text, ...prev.filter((item) => item !== text)].slice(0, 5));
  }

  function runtimeProxyPath(path: string): string {
    return `/lh-api${path.startsWith('/') ? path : `/${path}`}`;
  }

  function runtimeDirectPath(path: string): string {
    return `${effectiveBase.replace(/\/+$/, '')}/api${path.startsWith('/') ? path : `/${path}`}`;
  }

  async function fetchRuntime(path: string, init?: RequestInit): Promise<Response> {
    const proxyPath = runtimeProxyPath(path);
    try {
      const response = await fetch(proxyPath, init);
      if (response.ok || response.status !== 502) return response;
    } catch {
      // Fall through to the direct runtime URL below.
    }
    return fetch(runtimeDirectPath(path), init);
  }

  async function loadDashboard() {
    setLoadingDashboard(true);
    try {
      const [statusRes, dataRes] = await Promise.all([fetch('/api/status'), fetch('/api/data')]);
      const nextStatus = (await statusRes.json()) as DashboardStatus;
      const nextData = (await dataRes.json()) as DashboardData;
      setStatus(nextStatus);
      setData(nextData);
      setRawLog(JSON.stringify({ status: nextStatus, data: nextData }, null, 2));
      const preferred = normalizeApiBase(String(nextData.api_addr || nextStatus.addr || ''));
      if (preferred) setApiBase(preferred);
      if (nextData.sessions_recent?.length) {
        setSessions((prev) => {
          const byID = new Map<string, RuntimeSession>();
          [...nextData.sessions_recent!, ...prev].forEach((item) => {
            if (item.id) byID.set(item.id, { ...item, id: item.id });
          });
          return Array.from(byID.values()).slice(0, 20);
        });
      }
    } catch (error) {
      setRawLog(String(error));
      pushActivity('error', 'Dashboard data failed', String(error));
    } finally {
      setLoadingDashboard(false);
    }
  }

  async function loadSessions(query = sessionQuery) {
    setSessionsLoading(true);
    setSessionsError('');
    try {
      const suffix = query.trim() ? `?q=${encodeURIComponent(query.trim())}` : '';
      const response = await fetchRuntime(`/v1/sessions${suffix}`);
      if (!response.ok) throw new Error(`sessions ${response.status}`);
      const payload = (await response.json()) as SessionsResponse;
      setSessions(payload.sessions || []);
    } catch (error) {
      setSessionsError(String(error));
      pushActivity('error', 'Sessions unavailable', String(error));
    } finally {
      setSessionsLoading(false);
    }
  }

  // Only the newest page is fetched: a long session runs to thousands of
  // messages and megabytes of JSON, and rendering all of it blocks the thread.
  async function loadSessionHistory(id = session) {
    const target = id.trim();
    if (!target) return;
    try {
      const response = await fetchRuntime(`/v1/sessions/${encodeURIComponent(target)}?limit=${HISTORY_PAGE}`);
      if (!response.ok) throw new Error(`session ${response.status}`);
      const payload = (await response.json()) as SessionHistory;
      const page = payload.messages || [];
      setSession(target);
      setView('chat');
      messageCapRef.current = MAX_MESSAGES;
      setMessages(historyToBubbles(page));
      setHistoryLoaded(page.length);
      setHistoryHasMore(Boolean(payload.has_more));
      assistantDraftRef.current = '';
      assistantBubbleRef.current = null;
      const total = payload.message_count ?? page.length;
      pushActivity(
        'socket',
        'Session loaded',
        `${payload.title || target} · showing ${page.length} of ${total} messages`,
      );
    } catch (error) {
      pushActivity('error', 'Load session failed', String(error));
    }
  }

  async function loadEarlierMessages() {
    const target = session.trim();
    if (!target || loadingEarlier) return;
    setLoadingEarlier(true);
    try {
      const response = await fetchRuntime(
        `/v1/sessions/${encodeURIComponent(target)}?limit=${HISTORY_PAGE}&offset=${historyLoaded}`,
      );
      if (!response.ok) throw new Error(`session ${response.status}`);
      const payload = (await response.json()) as SessionHistory;
      const older = historyToBubbles(payload.messages);
      if (!older.length) {
        setHistoryHasMore(false);
        return;
      }
      // Hold the reading position: prepending grows the scroll height, so
      // remember the distance to the bottom and restore it after paint.
      const node = streamRef.current;
      keepScrollRef.current = node ? node.scrollHeight - node.scrollTop : null;
      setMessages((prev) => {
        const next = [...older, ...prev];
        // Pages the reader asked for are never trimmed away by later replies.
        messageCapRef.current = Math.max(messageCapRef.current, next.length);
        return next;
      });
      setHistoryLoaded((prev) => prev + older.length);
      setHistoryHasMore(Boolean(payload.has_more));
    } catch (error) {
      pushActivity('error', 'Load earlier messages failed', String(error));
    } finally {
      setLoadingEarlier(false);
    }
  }

  async function createSession() {
    try {
      const response = await fetchRuntime('/v1/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'GUI session' }),
      });
      if (!response.ok) throw new Error(`create session ${response.status}`);
      const next = (await response.json()) as RuntimeSession;
      if (!next.id) throw new Error('runtime did not return a session id');
      disconnect(false);
      setSession(next.id);
      setView('chat');
      messageCapRef.current = MAX_MESSAGES;
      setMessages([]);
      setHistoryLoaded(0);
      setHistoryHasMore(false);
      setActivity([]);
      pushActivity('socket', 'New session', next.id);
      await loadSessions('');
    } catch (error) {
      pushActivity('error', 'New session failed', String(error));
    }
  }

  async function renameSession(id: string, newTitle: string) {
    try {
      const response = await fetchRuntime(`/v1/sessions/${encodeURIComponent(id)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: newTitle }),
      });
      if (!response.ok) throw new Error(`rename session ${response.status}`);
      pushActivity('socket', 'Session renamed', `${id} → ${newTitle}`);
      await loadSessions('');
    } catch (error) {
      pushActivity('error', 'Rename failed', String(error));
    } finally {
      setRenamingSession(null);
      setRenameValue('');
    }
  }

  async function loadCommands() {
    try {
      const response = await fetchRuntime('/v1/commands');
      if (!response.ok) throw new Error(`commands ${response.status}`);
      const payload = (await response.json()) as CommandListResponse;
      setCommands(payload.commands || []);
    } catch {
      // A runtime without the endpoint simply has no slash commands.
      setCommands([]);
    }
  }

  /**
   * Runs a slash command against the runtime instead of sending it to the
   * model. The transcript keeps both the typed command and its output so the
   * conversation stays a readable record of what was done.
   */
  async function runCommand(raw: string) {
    const body = raw.slice(1);
    const [name, ...rest] = body.split(/\s+/);
    const args = body.slice(name.length).trim();

    pushBubble('user', 'You', raw);
    setComposer('');
    try {
      const response = await fetchRuntime('/v1/commands', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: name, args, session_id: session.trim() }),
      });
      if (!response.ok) throw new Error(`command ${response.status}`);
      const payload = (await response.json()) as CommandResult;
      pushBubble(payload.ok ? 'system' : 'error', `/${payload.command}`, payload.output || '(no output)');
      if (payload.ok && (name === 'rename' || name === 'new')) void loadSessions('');
    } catch (error) {
      pushBubble('error', `/${name}`, String(error));
    }
    void rest;
  }

  async function copyMessage(id: string, body: string) {
    try {
      await navigator.clipboard.writeText(body);
      setCopiedId(id);
      window.setTimeout(() => setCopiedId((current) => (current === id ? '' : current)), 1500);
    } catch {
      pushActivity('error', 'Copy failed', 'Clipboard access was blocked.');
    }
  }

  // Files are uploaded as soon as they are picked. The runtime needs the bytes
  // on disk — the chat socket caps frames at 64 KiB, and document extraction
  // reads from a real path — so a blob URL alone is invisible to the agent.
  async function uploadAttachment(id: string, file: File) {
    try {
      const form = new FormData();
      form.append('file', file);
      const response = await fetchRuntime('/v1/uploads', { method: 'POST', body: form });
      if (!response.ok) {
        const detail = await response.text().catch(() => '');
        throw new Error(detail ? `${response.status}: ${detail}` : `upload ${response.status}`);
      }
      const payload = (await response.json()) as UploadResponse;
      const uploaded = payload.attachments?.[0];
      if (!uploaded) throw new Error('runtime returned no attachment descriptor');
      setAttachments((prev) =>
        prev.map((item) => (item.id === id ? { ...item, status: 'ready', uploaded } : item)),
      );
    } catch (error) {
      const message = String(error);
      setAttachments((prev) =>
        prev.map((item) => (item.id === id ? { ...item, status: 'failed', error: message } : item)),
      );
      pushActivity('error', 'Upload failed', message);
    }
  }

  function handleFileSelect(event: React.ChangeEvent<HTMLInputElement>) {
    const files = event.target.files;
    if (!files) return;

    const picked: Pending[] = Array.from(files).map((file) => ({
      id: makeId('att'),
      type: file.type.startsWith('image/') ? 'image' : 'file',
      name: file.name,
      url: URL.createObjectURL(file),
      status: 'uploading',
    }));

    setAttachments((prev) => [...prev, ...picked]);
    picked.forEach((item, index) => void uploadAttachment(item.id, files[index]));
    if (fileInputRef.current) fileInputRef.current.value = '';
  }

  function removeAttachment(index: number) {
    setAttachments((prev) => {
      const item = prev[index];
      if (item.url.startsWith('blob:')) URL.revokeObjectURL(item.url);
      return prev.filter((_, i) => i !== index);
    });
  }

  function connect() {
    let wsUrl: URL;
    try {
      wsUrl = new URL(effectiveBase);
    } catch {
      setSocketState('error');
      pushActivity('error', 'Invalid API Base', effectiveBase);
      return;
    }
    wsUrl.protocol = wsUrl.protocol === 'https:' ? 'wss:' : 'ws:';
    wsUrl.pathname = '/api/v1/ws';
    wsUrl.search = new URLSearchParams({ session: session.trim() || DEFAULT_SESSION }).toString();

    if (wsRef.current) wsRef.current.close();

    setSocketState('connecting');
    const socket = new WebSocket(wsUrl.toString());
    wsRef.current = socket;

    socket.addEventListener('open', () => {
      setConnected(true);
      setSocketState('connected');
      pushActivity('socket', 'Connected', wsUrl.toString());
      pushFeed('connected');
    });

    socket.addEventListener('close', () => {
      setConnected(false);
      setSocketState('idle');
      if (wsRef.current === socket) wsRef.current = null;
      pushFeed('disconnected');
    });

    socket.addEventListener('error', () => {
      setSocketState('error');
      pushActivity('error', 'WebSocket error', wsUrl.toString());
      pushFeed('socket error');
    });

    socket.addEventListener('message', (event) => {
      let payload: WsPayload;
      try {
        payload = JSON.parse(event.data) as WsPayload;
      } catch {
        pushActivity('error', 'Protocol parse failed', String(event.data));
        return;
      }
      handleWsMessage(payload);
    });
  }

  function disconnect(log = true) {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    cancelStreamFlush();
    setConnected(false);
    setSocketState('idle');
    assistantDraftRef.current = '';
    assistantBubbleRef.current = null;
    if (log) pushActivity('socket', 'Disconnected', session);
  }

  function stopRun() {
    disconnect(false);
    pushActivity('socket', 'Stopped locally', 'Closed the current WebSocket connection.');
    pushFeed('stopped');
  }

  function ensureAssistantBubble() {
    if (assistantBubbleRef.current) return assistantBubbleRef.current;
    const id = pushBubble('assistant', 'LuckyAgent', '', 'streaming');
    assistantBubbleRef.current = id;
    return id;
  }

  // Chunks arrive far faster than the screen refreshes. Accumulate into the
  // draft ref and commit at most once per frame, so a burst of tokens costs one
  // render instead of one per token.
  function scheduleStreamFlush() {
    if (streamFrameRef.current !== null) return;
    streamFrameRef.current = window.requestAnimationFrame(() => {
      streamFrameRef.current = null;
      const id = assistantBubbleRef.current;
      if (id) updateBubble(id, assistantDraftRef.current, 'streaming');
    });
  }

  function cancelStreamFlush() {
    if (streamFrameRef.current === null) return;
    window.cancelAnimationFrame(streamFrameRef.current);
    streamFrameRef.current = null;
  }

  function handleWsMessage(msg: WsPayload) {
    const payload = (msg.data || {}) as Record<string, unknown>;
    switch (msg.type) {
      case 'status': {
        const state = String(payload.state || 'status');
        const message = String(payload.message || '');
        setSocketState(state === 'idle' ? 'connected' : state === 'error' ? 'error' : 'running');
        pushFeed(message ? `${state}: ${message}` : state);
        if (message || state === 'error') pushActivity(state === 'error' ? 'error' : 'status', state, message || 'State changed');
        break;
      }
      case 'reasoning': {
        const roundNum = Number(payload.round) || undefined;
        if (payload.stage === 'content') {
          const content = String(payload.content || '').trim();
          if (!content || !roundNum) break;
          pushActivity('reasoning', `Reasoning round ${roundNum}`, content);
          upsertReasoningStep(roundNum, content);
          break;
        }
        const summary = String(payload.summary || '').trim();
        if (!summary) break;
        const round = payload.round ? ` round ${payload.round}` : '';
        pushActivity('reasoning', `Reasoning${round}`, summary);
        // The assistant bubble is created up-front when a message is sent, so
        // steps must be spliced in before it to stay in causal order.
        insertStep({
          id: makeId('reasoning'),
          role: 'reasoning',
          title: `Thought${round}`,
          body: summary,
          meta: nowLabel(),
          round: roundNum,
        });
        break;
      }
      case 'tool_call': {
        const name = String(payload.name || 'tool');
        const stepId = String(payload.step_id || '');
        // `display` is the compact one-line label the drawer uses and it is
        // capped server-side; the step card wants the untruncated payload.
        const args = String(
          payload.args || (payload.params ? JSON.stringify(payload.params, null, 2) : '') || payload.display || '',
        );
        pushActivity('tool', `Calling ${name}`, preview(payload.display || payload.args || payload.params), String(payload.phase || 'start'));
        upsertToolStep(stepId, name, (prev) => ({
          name,
          args: args || prev?.args,
          output: prev?.output,
          success: prev?.success,
          round: typeof payload.round === 'number' ? payload.round : prev?.round,
          done: prev?.done ?? false,
        }));
        break;
      }
      case 'tool_result': {
        const name = String(payload.name || 'tool');
        if (name === '__memory_trace') {
          try {
            const trace = JSON.parse(String(payload.output || '')) as SearchTrace;
            setMemoryTraces((prev) => [{ ...trace, _receivedAt: Date.now() }, ...prev].slice(0, 20));
            pushActivity('tool', 'Memory recall', trace.query, `${trace.results?.length ?? 0} hits`);
          } catch {
            // Malformed trace payload: drop it silently, the underlying tool
            // result was already reported through its own tool_result event.
          }
          break;
        }
        const stepId = String(payload.step_id || '');
        // Same here: `output` carries the whole result, `display` is truncated
        // to ~160 characters for compact surfaces.
        const output = String(payload.output || payload.display || '');
        pushActivity(payload.success === false ? 'error' : 'tool', `Result ${name}`, preview(payload.display || payload.output), 'done');
        upsertToolStep(stepId, name, (prev) => ({
          name,
          args: prev?.args,
          output,
          success: payload.success !== false,
          round: typeof payload.round === 'number' ? payload.round : prev?.round,
          done: true,
        }));
        break;
      }
      case 'stream_chunk': {
        const chunk = String(payload.content || '');
        if (!chunk) break;
        assistantDraftRef.current += chunk;
        ensureAssistantBubble();
        scheduleStreamFlush();
        break;
      }
      case 'stream_end': {
        cancelStreamFlush();
        const finalText = String(payload.full_response || assistantDraftRef.current || '').trim() || 'Done.';
        updateBubble(ensureAssistantBubble(), finalText, 'done');
        assistantDraftRef.current = '';
        assistantBubbleRef.current = null;
        setSocketState('connected');
        pushFeed('done');
        void loadSessions('');
        break;
      }
      case 'error': {
        cancelStreamFlush();
        const message = String(payload.message || 'unknown error');
        pushBubble('error', 'Runtime Error', message);
        pushActivity('error', String(payload.code || 'Runtime error'), message);
        assistantDraftRef.current = '';
        assistantBubbleRef.current = null;
        setSocketState('error');
        break;
      }
      case 'pong':
        pushFeed('pong');
        break;
      default:
        pushActivity('status', msg.type, preview(payload));
        break;
    }
  }

  function sendMessage() {
    const text = composer.trim();
    if (!text && attachments.length === 0) return;
    // Slash commands run against the runtime and never reach the model, so they
    // work whether or not the chat socket is connected.
    if (text.startsWith('/') && attachments.length === 0) {
      void runCommand(text);
      return;
    }
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) {
      pushActivity('error', 'Not connected', 'Connect to the runtime before sending.');
      return;
    }
    if (attachments.some((item) => item.status === 'uploading')) {
      pushActivity('status', 'Upload in progress', 'Wait for the attachments to finish uploading.');
      return;
    }

    const ready: RuntimeAttachment[] = attachments
      .map((item) => item.uploaded)
      .filter((item): item is RuntimeAttachment => Boolean(item));
    const dropped = attachments.length - ready.length;
    if (dropped > 0) {
      pushActivity('error', 'Attachments skipped', `${dropped} file(s) failed to upload and were not sent.`);
    }

    // Thumbnails keep the local blob URL; the runtime gets the descriptors.
    const preview = attachments.length > 0 ? attachments.map(({ type, name, url }) => ({ type, name, url })) : undefined;
    pushBubble('user', 'You', text || '[Attachments]', undefined, preview);

    setComposer('');
    setAttachments([]);
    assistantDraftRef.current = '';
    assistantBubbleRef.current = pushBubble('assistant', 'LuckyAgent', '', 'streaming');
    setSocketState('running');
    pushFeed('sent');
    wsRef.current.send(JSON.stringify({
      type: 'chat',
      data: {
        message: text,
        stream: true,
        max_iterations: 8,
        attachments: ready.length > 0 ? ready : undefined,
      },
    }));
  }

  // Enter sends, Shift+Enter inserts a newline — the same shortcut the Claude web chat uses.
  function handleComposerKey(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (commandMatches.length > 0) {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setCommandIndex((prev) => (prev + 1) % commandMatches.length);
        return;
      }
      if (event.key === 'ArrowUp') {
        event.preventDefault();
        setCommandIndex((prev) => (prev - 1 + commandMatches.length) % commandMatches.length);
        return;
      }
      if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing)) {
        event.preventDefault();
        applyCommand(commandMatches[Math.min(commandIndex, commandMatches.length - 1)]);
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        setCommands((prev) => prev);
        setComposer((prev) => prev);
        commandDismissedRef.current = true;
        return;
      }
    }
    if (event.key !== 'Enter') return;
    if (event.shiftKey) return;
    if (event.nativeEvent.isComposing) return;
    event.preventDefault();
    sendMessage();
  }

  function applyCommand(spec: CommandSpec) {
    // Commands that take an argument keep the caret going; the rest are ready
    // to send immediately.
    const needsArgs = /<|\[/.test(spec.usage.replace(`/${spec.name}`, ''));
    setComposer(`/${spec.name}${needsArgs ? ' ' : ''}`);
    setCommandIndex(0);
    commandDismissedRef.current = !needsArgs ? false : false;
    composerRef.current?.focus();
  }

  useEffect(() => {
    if (!lightbox) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setLightbox(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [lightbox]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem('lh-gui-theme', theme);
    } catch {
      /* localStorage unavailable */
    }
  }, [theme]);

  useEffect(() => {
    void loadDashboard();
    void loadSessions('');
    void loadCommands();
    return () => {
      if (wsRef.current) wsRef.current.close();
      if (streamFrameRef.current !== null) window.cancelAnimationFrame(streamFrameRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const node = streamRef.current;
    if (!node) return;

    // A page of older messages was just prepended: restore the previous
    // distance to the bottom instead of jumping anywhere.
    if (keepScrollRef.current !== null) {
      node.scrollTop = node.scrollHeight - keepScrollRef.current;
      keepScrollRef.current = null;
      return;
    }

    if (!autoScroll) return;
    // Smooth scrolling can't keep up with streamed chunks, and queuing an
    // animation per frame is what makes a long reply feel sluggish.
    node.scrollTo({ top: node.scrollHeight, behavior: busy ? 'auto' : 'smooth' });
  }, [messages, autoScroll, busy]);

  // Grow the composer with its content instead of scrolling a fixed box.
  useEffect(() => {
    const node = composerRef.current;
    if (!node) return;
    node.style.height = 'auto';
    node.style.height = `${Math.min(node.scrollHeight, COMPOSER_MAX_HEIGHT)}px`;
  }, [composer, attachments.length]);

  const stats: Array<[string, string]> = [
    ['Runtime', status.running ? 'running' : 'unknown'],
    ['Provider', data.provider || status.provider || 'n/a'],
    ['Model', data.model || status.model || 'n/a'],
    ['Sessions', String(data.sessions_total ?? status.sessions_total ?? sessions.length)],
    ['Memory', String(data.memory_total ?? status.memory_total ?? 0)],
    ['Tools', String(data.tools_total ?? data.tools_enabled ?? status.tools_builtin_total ?? 0)],
    ['Timeouts (24h)', String(data.timeout_events_24h ?? 0)],
    ['API', effectiveBase],
    ['Socket', socketState],
  ];

  const connectionLabel = connected
    ? busy ? 'Working' : 'Connected'
    : socketState === 'connecting' ? 'Connecting' : socketState === 'error' ? 'Error' : 'Offline';
  const connectionTone = connected ? 'ok' : socketState === 'error' ? 'err' : 'idle';

  const composerBox = (
    <div className="composer">
      {commandMatches.length > 0 ? (
        <div className="command-palette" role="listbox" aria-label="Commands">
          {commandMatches.map((spec, index) => (
            <button
              type="button"
              role="option"
              aria-selected={index === Math.min(commandIndex, commandMatches.length - 1)}
              className={`command-option ${index === Math.min(commandIndex, commandMatches.length - 1) ? 'active' : ''}`}
              key={spec.name}
              onMouseEnter={() => setCommandIndex(index)}
              onClick={() => applyCommand(spec)}
            >
              <span className="command-usage">{spec.usage}</span>
              <span className="command-desc">{spec.description}</span>
            </button>
          ))}
        </div>
      ) : null}
      {attachments.length > 0 && (
        <div className="attachments-preview">
          {attachments.map((item, index) => (
            <div className={`attachment-item ${item.status}`} key={item.id}>
              {item.type === 'image' ? <img src={item.url} alt={item.name} /> : <div className="file-icon"><IconFile /></div>}
              <span className="attachment-name">{item.name}</span>
              <span className="attachment-status" title={item.error || ''}>
                {item.status === 'uploading' ? 'uploading…' : item.status === 'failed' ? 'failed' : ''}
              </span>
              <button type="button" className="remove-attachment" onClick={() => removeAttachment(index)} title="Remove">
                <IconClose />
              </button>
            </div>
          ))}
        </div>
      )}
      <textarea
        ref={composerRef}
        value={composer}
        onChange={(event) => {
          commandDismissedRef.current = false;
          setCommandIndex(0);
          setComposer(event.target.value);
        }}
        onKeyDown={handleComposerKey}
        placeholder={connected ? 'Message LuckyAgent…' : 'Connect to the runtime to start chatting'}
        rows={1}
        spellCheck={false}
      />
      <div className="composer-row">
        <div className="composer-actions">
          <input
            ref={fileInputRef}
            type="file"
            multiple
            accept="image/*,application/pdf,.txt,.json,.xml,.csv"
            style={{ display: 'none' }}
            onChange={handleFileSelect}
          />
          <button className="icon-button" type="button" title="Attach files" onClick={() => fileInputRef.current?.click()}>
            <IconClip />
          </button>
          <label className="auto-scroll-toggle" title="Follow the newest message">
            <input type="checkbox" checked={autoScroll} onChange={(event) => setAutoScroll(event.target.checked)} />
            <span>Auto-scroll</span>
          </label>
        </div>
        <div className="composer-trailing">
          <span className="composer-model">{data.model || status.model || 'runtime default'}</span>
          {busy ? (
            <button className="send-button stop" type="button" onClick={stopRun} title="Stop the current run">
              <IconStop />
            </button>
          ) : (
            <button
              className="send-button"
              type="button"
              onClick={connected ? sendMessage : connect}
              disabled={connected && (uploading || (!composer.trim() && attachments.length === 0))}
              title={connected ? (uploading ? 'Waiting for uploads' : 'Send (Enter)') : 'Connect'}
            >
              {connected ? <IconArrowUp /> : <IconPlug />}
            </button>
          )}
        </div>
      </div>
    </div>
  );

  return (
    <div className={`app ${sidebarOpen ? '' : 'sidebar-hidden'} ${inspectorOpen ? 'inspector-open' : ''}`}>
      <aside className="sidebar">
        <div className="sidebar-head">
          <div className="brand">
            <span className="brand-mark"><LogoMark /></span>
            <span className="brand-name">LuckyAgent</span>
          </div>
          <button className="icon-button" type="button" title="Hide sidebar" onClick={() => setSidebarOpen(false)}>
            <IconPanel />
          </button>
        </div>

        <button className="new-chat" type="button" onClick={() => void createSession()}>
          <IconPlus />
          <span>New chat</span>
        </button>

        <nav className="nav">
          <button className={`nav-item ${view === 'chat' ? 'active' : ''}`} type="button" onClick={() => setView('chat')}>
            <IconChat />
            <span>Chat</span>
          </button>
          <button className={`nav-item ${view === 'trajectory' ? 'active' : ''}`} type="button" onClick={() => setView('trajectory')}>
            <IconRoute />
            <span>Trajectory</span>
          </button>
          <button className={`nav-item ${view === 'gateways' ? 'active' : ''}`} type="button" onClick={() => setView('gateways')}>
            <IconPlug />
            <span>Gateways</span>
          </button>
          <button className={`nav-item ${view === 'skills' ? 'active' : ''}`} type="button" onClick={() => setView('skills')}>
            <IconSkill />
            <span>Skills</span>
          </button>
          <button className={`nav-item ${view === 'settings' ? 'active' : ''}`} type="button" onClick={() => setView('settings')}>
            <IconGear />
            <span>Settings</span>
          </button>
          <button className={`nav-item ${view === 'memory' ? 'active' : ''}`} type="button" onClick={() => setView('memory')}>
            <IconGraph />
            <span>Memory graph</span>
          </button>
        </nav>

        <div className="sidebar-search">
          <IconSearch />
          <input
            value={sessionQuery}
            onChange={(event) => setSessionQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') void loadSessions();
            }}
            placeholder="Search chats"
            spellCheck={false}
          />
        </div>

        <div className="sidebar-label">
          <span>Recents</span>
          <button className="text-button" type="button" onClick={() => void loadSessions()}>
            {sessionsLoading ? 'Syncing' : 'Sync'}
          </button>
        </div>

        <div className="chat-list">
          {sessionsError ? <div className="empty-line error-text">{sessionsError}</div> : null}
          {!sessionsLoading && !sessions.length ? <div className="empty-line">No chats yet</div> : null}
          {sessions.slice(0, 20).map((item) => (
            <div className={`chat-item ${item.id === session ? 'active' : ''}`} key={item.id}>
              {renamingSession === item.id ? (
                <div className="chat-rename">
                  <input
                    type="text"
                    value={renameValue}
                    onChange={(event) => setRenameValue(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') void renameSession(item.id, renameValue);
                      else if (event.key === 'Escape') {
                        setRenamingSession(null);
                        setRenameValue('');
                      }
                    }}
                    autoFocus
                    placeholder="New name"
                  />
                  <button className="icon-button tiny" type="button" title="Save" onClick={() => void renameSession(item.id, renameValue)}>
                    <IconCheck />
                  </button>
                  <button
                    className="icon-button tiny"
                    type="button"
                    title="Cancel"
                    onClick={() => {
                      setRenamingSession(null);
                      setRenameValue('');
                    }}
                  >
                    <IconClose />
                  </button>
                </div>
              ) : (
                <>
                  <button className="chat-main" type="button" onClick={() => void loadSessionHistory(item.id)}>
                    <span className="chat-title">{item.title || item.id}</span>
                    <span className="chat-meta">
                      {item.message_count ?? 0} messages · {sessionAge(item.updated_at || item.created_at)}
                    </span>
                  </button>
                  <button
                    className="icon-button tiny chat-rename-button"
                    type="button"
                    title="Rename chat"
                    onClick={() => {
                      setRenamingSession(item.id);
                      setRenameValue(item.title || item.id);
                    }}
                  >
                    <IconPencil />
                  </button>
                </>
              )}
            </div>
          ))}
        </div>

        <div className="sidebar-foot">
          <button className={`status-chip ${connectionTone}`} type="button" onClick={() => setConnectionOpen(true)}>
            <span className="status-dot" />
            <span>{connectionLabel}</span>
          </button>
          <button
            className="icon-button"
            type="button"
            title={theme === 'dark' ? 'Light theme' : 'Dark theme'}
            onClick={() => setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))}
          >
            {theme === 'dark' ? <IconSun /> : <IconMoon />}
          </button>
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div className="topbar-left">
            {!sidebarOpen ? (
              <button className="icon-button" type="button" title="Show sidebar" onClick={() => setSidebarOpen(true)}>
                <IconPanel />
              </button>
            ) : null}
            <div className="topbar-title">
              <h1>{view === 'chat' ? chatTitle : VIEW_TITLES[view]}</h1>
              {view === 'chat' && chatTitle !== (session || DEFAULT_SESSION) ? (
                <span className="topbar-sub">{session || DEFAULT_SESSION}</span>
              ) : null}
            </div>
          </div>
          <div className="topbar-right">
            <button className={`status-chip ${connectionTone}`} type="button" onClick={() => setConnectionOpen((prev) => !prev)}>
              <span className="status-dot" />
              <span>{connectionLabel}</span>
            </button>
            <button className="icon-button" type="button" title="Refresh runtime data" onClick={() => void loadDashboard()} disabled={loadingDashboard}>
              <IconRefresh />
            </button>
            <button
              className={`icon-button ${inspectorOpen ? 'active' : ''}`}
              type="button"
              title="Activity panel"
              onClick={() => setInspectorOpen((prev) => !prev)}
            >
              <IconActivity />
            </button>
          </div>
        </header>

        {connectionOpen ? (
          <>
            <div className="popover-backdrop" onClick={() => setConnectionOpen(false)} />
            <div className="popover connection-popover">
              <div className="popover-head">
                <strong>Runtime connection</strong>
                <button className="icon-button tiny" type="button" title="Close" onClick={() => setConnectionOpen(false)}>
                  <IconClose />
                </button>
              </div>
              <label className="field">
                <span>API base</span>
                <input value={apiBase} onChange={(event) => setApiBase(event.target.value)} spellCheck={false} />
              </label>
              <label className="field">
                <span>Session id</span>
                <input value={session} onChange={(event) => setSession(event.target.value)} spellCheck={false} />
              </label>
              <div className="popover-actions">
                <button className="ghost" type="button" onClick={() => void loadSessionHistory()}>Load history</button>
                <button className="ghost" type="button" onClick={() => void createSession()}>New session</button>
                <button className="primary" type="button" onClick={connected ? () => disconnect() : connect}>
                  {connected ? 'Disconnect' : 'Connect'}
                </button>
              </div>
              <div className="popover-feed">
                {feed.length ? feed.map((item) => <span key={item}>{item}</span>) : <span className="muted">No socket events yet</span>}
              </div>
            </div>
          </>
        ) : null}

        {view === 'chat' ? (
          <>
            <div
              className="thread"
              ref={streamRef}
              onClick={(event) => {
                // One delegated handler covers both attachment thumbnails and
                // images inside rendered Markdown.
                const target = event.target as HTMLElement;
                if (target.tagName !== 'IMG') return;
                const image = target as HTMLImageElement;
                if (!image.src) return;
                event.preventDefault();
                setLightbox({ src: image.src, alt: image.alt || 'image' });
              }}
            >
              {messages.length === 0 ? (
                <div className="hero">
                  <div className="hero-mark"><LogoMark /></div>
                  <h2>{greeting()}</h2>
                  <p>Connect to the runtime, or open a previous chat from the sidebar.</p>
                  <div className="hero-suggestions">
                    {SUGGESTIONS.map((item) => (
                      <button
                        className="suggestion"
                        type="button"
                        key={item}
                        onClick={() => {
                          setComposer(item);
                          composerRef.current?.focus();
                        }}
                      >
                        {item}
                      </button>
                    ))}
                  </div>
                </div>
              ) : (
                <div className="thread-inner">
                  {historyHasMore ? (
                    <div className="thread-more">
                      <button className="ghost" type="button" onClick={() => void loadEarlierMessages()} disabled={loadingEarlier}>
                        {loadingEarlier ? 'Loading…' : `Load ${HISTORY_PAGE} earlier messages`}
                      </button>
                    </div>
                  ) : null}
                  {messages.map((msg) => {
                    if (msg.role === 'user') {
                      return (
                        <div className="turn user" key={msg.id}>
                          <div className="user-bubble">
                            <Markdown source={msg.body} />
                            {msg.attachments?.length ? (
                              <div className="message-attachments">
                                {msg.attachments.map((att, index) => (
                                  <div className="message-attachment" key={`${att.name}-${index}`}>
                                    {att.type === 'image' ? (
                                      <img src={att.url} alt={att.name} />
                                    ) : (
                                      <a href={att.url} download={att.name} className="file-attachment">
                                        <IconFile />
                                        {att.name}
                                      </a>
                                    )}
                                  </div>
                                ))}
                              </div>
                            ) : null}
                          </div>
                        </div>
                      );
                    }

                    if (msg.role === 'reasoning') {
                      return (
                        <details className="turn step thought" key={msg.id}>
                          <summary>
                            <span className="step-icon"><IconThought /></span>
                            <span className="step-name">{msg.title}</span>
                            <span className="step-meta">{msg.meta}</span>
                          </summary>
                          <div className="step-body">
                            <Markdown source={msg.body} />
                          </div>
                        </details>
                      );
                    }

                    if (msg.role === 'tool_call') {
                      const tool = msg.tool;
                      const state = !tool?.done ? 'running' : tool.success === false ? 'failed' : 'ok';
                      return (
                        <details className={`turn step tool-step ${state}`} key={msg.id}>
                          <summary>
                            <span className="step-icon"><IconTool /></span>
                            <span className="step-name">{tool?.name || msg.title}</span>
                            <span className={`step-state ${state}`}>
                              {state === 'running' ? 'running' : state === 'failed' ? 'failed' : 'done'}
                            </span>
                          </summary>
                          <div className="step-body">
                            {tool?.args ? (
                              <div className="step-payload">
                                <span>Input</span>
                                <pre>{tool.args}</pre>
                              </div>
                            ) : null}
                            {tool?.output ? (
                              <div className="step-payload">
                                <span>{tool.success === false ? 'Error' : 'Result'}</span>
                                <pre>{tool.output}</pre>
                              </div>
                            ) : null}
                            {!tool?.args && !tool?.output ? <p className="muted">No payload reported.</p> : null}
                          </div>
                        </details>
                      );
                    }

                    if (msg.role === 'assistant') {
                      const streaming = msg.meta === 'streaming';
                      return (
                        <div className="turn assistant" key={msg.id}>
                          <div className="assistant-avatar"><LogoMark /></div>
                          <div className="assistant-body">
                            {msg.body ? <Markdown source={msg.body} /> : <span className="typing"><i /><i /><i /></span>}
                            {msg.body && !streaming ? (
                              <div className="turn-actions">
                                <button className="icon-button tiny" type="button" title="Copy" onClick={() => void copyMessage(msg.id, msg.body)}>
                                  {copiedId === msg.id ? <IconCheck /> : <IconCopy />}
                                </button>
                              </div>
                            ) : null}
                          </div>
                        </div>
                      );
                    }

                    return (
                      <div className={`turn note ${msg.role}`} key={msg.id}>
                        <div className="note-card">
                          <div className="note-head">
                            <span>{msg.title}</span>
                            <small>{msg.meta}</small>
                          </div>
                          <div className="note-body">
                            <Markdown source={msg.body} />
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            <div className="composer-dock">
              {!connected ? (
                <div className="composer-notice">
                  <span>Not connected to the runtime.</span>
                  <button className="text-button" type="button" onClick={connect}>Connect now</button>
                </div>
              ) : null}
              {composerBox}
              <div className="composer-hint">Enter to send · Shift + Enter for a new line</div>
            </div>
          </>
        ) : (
          <div className="view-scroll">
            <div className="view-inner">
              {view === 'settings' ? (
                <Settings fetchRuntime={fetchRuntime} pushActivity={pushActivity} />
              ) : view === 'memory' ? (
                <MemoryGraph fetchRuntime={fetchRuntime} pushActivity={pushActivity} liveTraces={memoryTraces} />
              ) : view === 'gateways' ? (
                <Gateways fetchRuntime={fetchRuntime} pushActivity={pushActivity} pushFeed={pushFeed} />
              ) : view === 'skills' ? (
                <Skills fetchRuntime={fetchRuntime} pushActivity={pushActivity} />
              ) : (
                <Trajectory
                  session={session}
                  fetchRuntime={fetchRuntime}
                  pushActivity={pushActivity}
                  onOpenChat={() => setView('chat')}
                />
              )}
            </div>
          </div>
        )}
      </main>

      {lightbox ? (
        <div className="lightbox" role="dialog" aria-modal="true" aria-label={lightbox.alt} onClick={() => setLightbox(null)}>
          <img src={lightbox.src} alt={lightbox.alt} onClick={(event) => event.stopPropagation()} />
          <button className="lightbox-close" type="button" title="Close" onClick={() => setLightbox(null)}>
            <IconClose />
          </button>
        </div>
      ) : null}

      <aside className="inspector">
        <div className="inspector-head">
          <strong>Activity</strong>
          <div className="inspector-head-actions">
            <button className="text-button" type="button" onClick={() => setActivity([])}>Clear</button>
            <button className="icon-button tiny" type="button" title="Close panel" onClick={() => setInspectorOpen(false)}>
              <IconClose />
            </button>
          </div>
        </div>

        <div className="inspector-body">
          <section className="inspector-section">
            <h3>Runtime</h3>
            <div className="kv-list">
              {stats.map(([key, value]) => (
                <div className="kv" key={key}>
                  <span>{key}</span>
                  <strong>{formatValue(value)}</strong>
                </div>
              ))}
            </div>
            {data.timeout_last_error ? (
              <div className="muted-tag">
                Last timeout: {data.timeout_last_error.layer || 'unknown'} · {data.timeout_last_error.config_path || 'n/a'}
              </div>
            ) : null}
          </section>

          <section className="inspector-section">
            <h3>Events</h3>
            <div className="activity-list">
              {activity.length === 0 ? <div className="empty-line">No activity yet</div> : null}
              {activity.map((item) => (
                <article className={`activity ${item.kind}`} key={item.id}>
                  <div className="activity-head">
                    <span>{item.title}</span>
                    <small>{item.meta}</small>
                  </div>
                  <p>{item.body}</p>
                </article>
              ))}
            </div>
          </section>

          <section className="inspector-section">
            <h3>Raw data</h3>
            <pre className="raw">{rawLog}</pre>
          </section>
        </div>
      </aside>
    </div>
  );
}
