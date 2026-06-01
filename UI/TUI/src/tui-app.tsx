import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, useApp, useInput } from 'ink';
import TextInput from 'ink-text-input';
import { execFileSync } from 'node:child_process';

type WsPayload = { type: string; data?: Record<string, unknown> };
type StreamItemKind = 'user' | 'assistant' | 'meta' | 'error' | 'reasoning' | 'tool_call' | 'tool_result' | 'status';

type StreamItem = { id: string; kind: StreamItemKind; title: string; body: string };
type AppProps = { apiBase: string; session: string; model: string };
type SessionInfo = { id: string; title: string; message_count: number; created_at: string; updated_at: string };
type SessionsResponse = { sessions?: SessionInfo[] };
type SessionDetailResponse = { id?: string; title?: string; messages?: Array<Record<string, unknown>> };
type ApiSessionResponse = SessionInfo & { id?: string };
type PickerMode = 'resume' | 'commands' | 'none';

type CommandSpec = {
  name: string;
  help: string;
  aliases: string[];
};

const COMMANDS: CommandSpec[] = [
  { name: '/review', help: '查看工作区状态和最近提交', aliases: ['review'] },
  { name: '/resume', help: '恢复历史会话', aliases: ['resume', 'switch'] },
  { name: '/rename', help: '重命名当前会话', aliases: ['rename'] },
  { name: '/help', help: '列出可用命令', aliases: ['help'] },
  { name: '/sessions', help: '刷新会话列表', aliases: ['sessions'] },
  { name: '/new', help: '创建新会话', aliases: ['new'] },
];

const CLOVER = [
  '   .-.-.   ',
  '  (  *  )  ',
  '   `-+-´   ',
  '  .-.-.-.  ',
];

function normalizeApiBase(value: string): string {
  const raw = value.trim();
  const fallback = 'http://127.0.0.1:9090';
  if (!raw) return fallback;
  const defaultHost = '127.0.0.1';
  let target = raw;
  if (/^\d+$/.test(target)) target = `${defaultHost}:${target}`;
  else if (/^:\d+$/.test(target)) target = `${defaultHost}${target}`;
  else if (/^\/\//.test(target)) target = `http:${target}`;
  else if (/^wss?:\/\//i.test(target)) target = target.replace(/^ws/i, 'http');
  else if (!/^https?:\/\//i.test(target)) target = `http://${target}`;
  try {
    const url = new URL(target);
    if (!url.hostname || url.hostname === '0.0.0.0' || url.hostname === '::') url.hostname = defaultHost;
    return `${url.protocol}//${url.host}`.replace(/\/+$/, '');
  } catch {
    return fallback;
  }
}

function makeId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function sanitize(text: string): string {
  return text.replace(/\r\n/g, '\n').replace(/\u0000/g, '').trimEnd();
}

function asText(value: unknown): string {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function safeDate(value: string): number {
  const ts = Date.parse(value);
  return Number.isFinite(ts) ? ts : 0;
}

function normalizeSessionList(items: SessionInfo[]): SessionInfo[] {
  return [...items].sort((a, b) => safeDate(b.updated_at || '') - safeDate(a.updated_at || ''));
}

function sessionMessageToItem(message: Record<string, unknown>, index: number): StreamItem {
  const role = String(message.role || 'meta');
  const kind: StreamItemKind =
    role === 'user' ? 'user' : role === 'assistant' ? 'assistant' : role === 'tool' ? 'tool_result' : 'meta';
  const body =
    sanitize(asText(message.content || message.text || message.message || message.reasoning_content || '')) || ' ';
  return {
    id: makeId(`history-${index}`),
    kind,
    title: kind === 'user' ? 'You' : kind === 'assistant' ? 'LuckyHarness' : 'Tool',
    body,
  };
}

function kindColor(kind: StreamItemKind): 'greenBright' | 'cyanBright' | 'gray' | 'yellowBright' | 'redBright' | 'whiteBright' {
  switch (kind) {
    case 'user':
      return 'greenBright';
    case 'assistant':
      return 'cyanBright';
    case 'reasoning':
      return 'gray';
    case 'tool_call':
    case 'tool_result':
      return 'yellowBright';
    case 'error':
      return 'redBright';
    default:
      return 'whiteBright';
  }
}

function kindLabel(kind: StreamItemKind): string {
  switch (kind) {
    case 'reasoning':
      return 'Thinking';
    case 'tool_call':
      return 'Tool call';
    case 'tool_result':
      return 'Tool result';
    case 'status':
      return 'Status';
    case 'error':
      return 'Error';
    case 'meta':
      return 'Meta';
    default:
      return '';
  }
}

function stripMarkdown(text: string): string {
  return text
    .replace(/\r\n/g, '\n')
    .replace(/^>\s?/gm, '')
    .replace(/^#{1,6}\s+/gm, '')
    .replace(/\*\*(.*?)\*\*/g, '$1')
    .replace(/__(.*?)__/g, '$1')
    .replace(/\*(.*?)\*/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/^\s*[-*+]\s+/gm, '• ')
    .replace(/^\s*\d+\.\s+/gm, '• ')
    .replace(/^---+$/gm, '─'.repeat(24))
    .trimEnd();
}

function wrapLine(line: string, width: number): string[] {
  if (width <= 1) return [line];
  if (!line) return [''];
  const out: string[] = [];
  let remaining = line;
  while (remaining.length > width) {
    let breakAt = remaining.lastIndexOf(' ', width);
    if (breakAt < Math.max(8, width - 20)) breakAt = width;
    out.push(remaining.slice(0, breakAt).trimEnd());
    remaining = remaining.slice(breakAt).trimStart();
  }
  out.push(remaining);
  return out;
}

function wrapText(text: string, width: number): string {
  return text
    .split('\n')
    .flatMap((line) => (line.trim() === '' ? [''] : wrapLine(line, width)))
    .join('\n');
}

function formatItemBody(item: StreamItem, width: number): string {
  const maxWidth = Math.max(24, width - 6);
  const body = stripMarkdown(item.body);
  return body ? wrapText(body, maxWidth) : ' ';
}

function renderDividerLabel(label: string, width: number): string {
  const clean = label.trim();
  const target = Math.max(32, width);
  const labelText = clean ? ` ${clean} ` : ' ';
  if (target <= labelText.length) return labelText;
  const remaining = target - labelText.length;
  const left = Math.floor(remaining / 2);
  const right = remaining - left;
  return `${'-'.repeat(left)}${labelText}${'-'.repeat(right)}`;
}

function renderFullDivider(width: number): string {
  return '-'.repeat(Math.max(32, width));
}

function isBubbleKind(kind: StreamItemKind): boolean {
  return kind === 'user' || kind === 'assistant';
}

function compact(lines: string[]): string {
  return lines.filter(Boolean).join(' · ');
}

function normalizeCommand(raw: string): string {
  return raw.trim().replace(/\s+/g, ' ');
}

function parseCommandParts(raw: string): { command: string; arg: string } {
  const normalized = normalizeCommand(raw);
  const firstSpace = normalized.indexOf(' ');
  if (firstSpace < 0) {
    return { command: normalized.slice(1).toLowerCase(), arg: '' };
  }
  return {
    command: normalized.slice(1, firstSpace).toLowerCase(),
    arg: normalized.slice(firstSpace + 1).trim(),
  };
}

function fallbackModel(model: string, status: string): string {
  if (model.trim()) return model.trim();
  const match = status.match(/model[:=]\s*([^\s|]+)/i);
  return match?.[1] || 'unknown';
}

function readGitSnippet(args: string[]): string {
  try {
    return execFileSync('git', args, { cwd: process.cwd(), encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();
  } catch (error) {
    const output = asText((error as { stdout?: unknown; stderr?: unknown }).stdout || (error as { stderr?: unknown }).stderr || error);
    return output.trim() || 'git unavailable';
  }
}

function parseTitle(raw: string): string {
  return raw.trim().replace(/\s+/g, ' ');
}

function markdownSegments(text: string): string[] {
  const normalized = text.replace(/\r\n/g, '\n');
  const lines = normalized.split('\n');
  const out: string[] = [];
  let codeFence = false;
  let codeBuffer: string[] = [];

  for (const line of lines) {
    if (/^```/.test(line.trim())) {
      if (!codeFence) {
        codeFence = true;
        codeBuffer = [];
        continue;
      }
      codeFence = false;
      out.push(`\`\`\`\n${codeBuffer.join('\n')}\n\`\`\``);
      continue;
    }
    if (codeFence) {
      codeBuffer.push(line);
      continue;
    }
    out.push(line);
  }

  if (codeFence && codeBuffer.length > 0) {
    out.push(`\`\`\`\n${codeBuffer.join('\n')}\n\`\`\``);
  }

  return out;
}

function renderMarkdown(text: string, width: number): string[] {
  const body = stripMarkdown(text);
  if (!body.trim()) return [' '];
  return markdownSegments(body).flatMap((line) => {
    if (/^```/.test(line)) return line.split('\n').map((part) => wrapText(part, Math.max(24, width - 8)));
    if (/^ {0,3}[-*+]\s+/.test(line) || /^\s*\d+\.\s+/.test(line)) return wrapLine(line, Math.max(24, width - 4));
    return wrapText(line, Math.max(24, width - 4)).split('\n');
  });
}

function divyTitle(text: string, width: number): string {
  const max = Math.max(0, width - 2);
  if (text.length <= max) return text;
  if (max <= 1) return '…';
  return `${text.slice(0, max - 1)}…`;
}

function clamped(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function createStatusItem(message: string): StreamItem {
  return { id: makeId('status'), kind: 'status', title: 'Status', body: message };
}

function isToolCallLike(value: unknown): value is {
  name?: unknown;
  arguments?: unknown;
  args?: unknown;
  function?: { name?: unknown; arguments?: unknown };
} {
  return typeof value === 'object' && value !== null;
}

export function App({ apiBase, session, model }: AppProps) {
  const { exit } = useApp();
  const [input, setInput] = useState('');
  const [items, setItems] = useState<StreamItem[]>([]);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [activeSession, setActiveSession] = useState(session);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerIndex, setPickerIndex] = useState(0);
  const [pickerMode, setPickerMode] = useState<PickerMode>('none');
  const [socketState, setSocketState] = useState<'idle' | 'connecting' | 'connected' | 'error'>('idle');
  const [status, setStatus] = useState('Booting');
  const [sessionLoading, setSessionLoading] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [viewportWidth, setViewportWidth] = useState(process.stdout.columns || 120);
  const [draft, setDraft] = useState('');
  const [draftId, setDraftId] = useState<string | null>(null);
  const [commandQuery, setCommandQuery] = useState('');
  const [commandSelection, setCommandSelection] = useState(0);
  const [sessionFilter, setSessionFilter] = useState('');
  const [resumeSelection, setResumeSelection] = useState(0);
  const [reviewOutput, setReviewOutput] = useState<string[]>([]);
  const [headerTick, setHeaderTick] = useState(0);
  const [runtimeModel, setRuntimeModel] = useState(model);
  const socketRef = useRef<WebSocket | null>(null);
  const draftRef = useRef('');
  const draftIdRef = useRef<string | null>(null);
  const pendingMessagesRef = useRef<string[]>([]);
  const activeSessionRef = useRef(activeSession);
  const sessionsRef = useRef<SessionInfo[]>([]);
  const inputModeRef = useRef<'command' | 'resume' | 'normal'>('normal');

  const effectiveBase = useMemo(() => normalizeApiBase(apiBase), [apiBase]);
  const currentSessionLabel = useMemo(
    () => sessions.find((item) => item.id === activeSession)?.title?.trim() || activeSession,
    [activeSession, sessions],
  );
  const currentModelLabel = useMemo(() => (runtimeModel.trim() ? runtimeModel.trim() : fallbackModel(model, status)), [model, runtimeModel, status]);
  const filteredSessions = useMemo(() => {
    const query = sessionFilter.trim().toLowerCase();
    if (!query) return sessions;
    return sessions.filter((item) => {
      const text = `${item.title || ''} ${item.id} ${item.message_count}`.toLowerCase();
      return text.includes(query);
    });
  }, [sessionFilter, sessions]);
  const filteredCommands = useMemo(() => {
    const query = commandQuery.trim().toLowerCase();
    if (!query) return COMMANDS;
    return COMMANDS.filter((item) => {
      const hay = `${item.name} ${item.help} ${item.aliases.join(' ')}`.toLowerCase();
      return hay.includes(query);
    });
  }, [commandQuery]);

  useEffect(() => {
    activeSessionRef.current = activeSession;
  }, [activeSession]);

  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);

  useEffect(() => {
    const onResize = () => setViewportWidth(process.stdout.columns || 120);
    process.stdout?.on?.('resize', onResize);
    onResize();
    return () => {
      process.stdout?.off?.('resize', onResize);
    };
  }, []);

  useEffect(() => {
    const timer = setInterval(() => setHeaderTick((value) => (value + 1) % CLOVER.length), 1200);
    return () => clearInterval(timer);
  }, []);

  async function refreshSessions() {
    const url = new URL(effectiveBase);
    url.pathname = '/api/v1/sessions';
    url.search = '';
    if (sessionFilter.trim()) {
      url.searchParams.set('q', sessionFilter.trim());
    }
    setSessionLoading(true);
    try {
      const resp = await fetch(url.toString());
      if (!resp.ok) throw new Error(`failed to load sessions: ${resp.status}`);
      const data = (await resp.json()) as SessionsResponse;
      const next = normalizeSessionList(
        (data.sessions || [])
          .map((item) => ({
            id: String(item.id || '').trim(),
            title: String(item.title || '').trim(),
            message_count: Number(item.message_count || 0),
            created_at: String(item.created_at || ''),
            updated_at: String(item.updated_at || ''),
          }))
          .filter((item) => item.id),
      );
      setSessions(next);
      return next;
    } catch {
      return [];
    } finally {
      setSessionLoading(false);
    }
  }

  async function loadSessionHistory(sessionId: string) {
    const url = new URL(effectiveBase);
    url.pathname = `/api/v1/sessions/${encodeURIComponent(sessionId)}`;
    url.search = '';
    setHistoryLoading(true);
    try {
      const resp = await fetch(url.toString());
      if (!resp.ok) throw new Error(`failed to load session ${sessionId}: ${resp.status}`);
      const data = (await resp.json()) as SessionDetailResponse;
      const history = (data.messages || []).flatMap((message, index) => {
        const base = sessionMessageToItem(message, index);
        const reasoning = String(message.reasoning_content || '').trim();
        const toolCalls = Array.isArray(message.tool_calls) ? message.tool_calls : [];
        const extras: StreamItem[] = [];
        if (reasoning) {
          extras.push({
            id: makeId(`history-reasoning-${index}`),
            kind: 'reasoning',
            title: 'Thinking',
            body: reasoning,
          });
        }
        for (const [toolIndex, tool] of toolCalls.entries()) {
          const toolCall = isToolCallLike(tool) ? tool : {};
          extras.push({
            id: makeId(`history-tool-${index}-${toolIndex}`),
            kind: 'tool_call',
            title: `Tool: ${String(toolCall.name || toolCall.function?.name || 'unknown')}`,
            body: asText(toolCall.arguments || toolCall.args || toolCall.function?.arguments || toolCall),
          });
        }
        return [base, ...extras];
      });
      setItems(history);
      setDraft('');
      setDraftId(null);
      draftRef.current = '';
      draftIdRef.current = null;
    } catch {
      setItems([]);
    } finally {
      setHistoryLoading(false);
    }
  }

  async function loadRuntimeStatus() {
    const url = new URL(effectiveBase);
    url.pathname = '/api/v1/stats';
    url.search = '';
    try {
      const resp = await fetch(url.toString());
      if (!resp.ok) return;
      const data = (await resp.json()) as Record<string, unknown>;
      const provider = String(data.provider || '').trim();
      const modelName = String(data.model || '').trim();
      if (modelName) setRuntimeModel(modelName);
      if (provider || modelName) {
        setStatus(`Ready ${provider || 'provider'} / ${modelName || 'model'}`);
      }
    } catch {
      // ignore: top bar still falls back to the passed model prop
    }
  }

  function switchSession(nextSession: string) {
    const trimmed = nextSession.trim();
    if (!trimmed || trimmed === activeSessionRef.current) return;
    setActiveSession(trimmed);
    setStatus(`Resumed ${trimmed}`);
    setInput('');
    setDraft('');
    setDraftId(null);
    draftRef.current = '';
    draftIdRef.current = null;
    setItems([]);
  }

  function openPicker(mode: PickerMode) {
    setPickerMode(mode);
    setPickerOpen(true);
    inputModeRef.current = mode === 'resume' ? 'resume' : mode === 'commands' ? 'command' : 'normal';
    if (mode === 'resume') {
      setResumeSelection(clamped(filteredSessions.findIndex((item) => item.id === activeSessionRef.current), 0, Math.max(0, filteredSessions.length - 1)));
      setStatus('Choose a session');
    } else if (mode === 'commands') {
      setCommandSelection(0);
      setStatus('Choose a command');
    }
  }

  function closePicker() {
    setPickerOpen(false);
    setPickerMode('none');
    setCommandQuery('');
    setSessionFilter('');
    inputModeRef.current = 'normal';
  }

  async function createSession() {
    const url = new URL(effectiveBase);
    url.pathname = '/api/v1/sessions';
    url.search = '';
    try {
      const resp = await fetch(url.toString(), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'New session' }),
      });
      if (!resp.ok) throw new Error(`failed to create session: ${resp.status}`);
      const data = (await resp.json()) as ApiSessionResponse;
      const created = String(data.id || '').trim();
      if (!created) throw new Error('missing session id');
      setActiveSession(created);
      setItems([]);
      await refreshSessions();
      await loadSessionHistory(created);
    } catch {
      const fallback = `lh-${Date.now()}`;
      setActiveSession(fallback);
      setItems([]);
      await refreshSessions();
    }
  }

  async function renameSession() {
    const currentInput = input.trim();
    const arg = currentInput.startsWith('/') ? parseCommandParts(currentInput).arg : currentInput;
    const title = parseTitle(arg);
    if (!title) {
      pushItem('error', 'Command', 'Rename needs a title');
      setStatus('Rename needs a title');
      return;
    }
    const current = activeSessionRef.current;
    const url = new URL(effectiveBase);
    url.pathname = '/api/v1/sessions';
    url.search = '';
    try {
      const resp = await fetch(url.toString(), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: current, title }),
      });
      if (!resp.ok) throw new Error(`failed to rename session: ${resp.status}`);
      setStatus(`Renamed ${current} -> ${title}`);
      await refreshSessions();
    } catch (err) {
      pushItem('error', 'Command', asText(err));
      setStatus(`Rename failed: ${title}`);
    }
  }

  async function renameSessionByTitle(titleText: string) {
    const title = parseTitle(titleText);
    if (!title) {
      pushItem('error', 'Command', 'Rename needs a title');
      setStatus('Rename needs a title');
      return;
    }
    const current = activeSessionRef.current;
    const url = new URL(effectiveBase);
    url.pathname = '/api/v1/sessions';
    url.search = '';
    try {
      const resp = await fetch(url.toString(), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: current, title }),
      });
      if (!resp.ok) throw new Error(`failed to rename session: ${resp.status}`);
      setStatus(`Renamed ${current} -> ${title}`);
      await refreshSessions();
    } catch (err) {
      pushItem('error', 'Command', asText(err));
      setStatus(`Rename failed: ${title}`);
    }
  }

  async function runReview() {
    const lines: string[] = [];
    try {
      const statusUrl = new URL(effectiveBase);
      statusUrl.pathname = '/api/v1/stats';
      const sessionsUrl = new URL(effectiveBase);
      sessionsUrl.pathname = '/api/v1/sessions';
      const [statusResp, sessionsResp] = await Promise.all([fetch(statusUrl.toString()), fetch(sessionsUrl.toString())]);
      const statusData = statusResp.ok ? ((await statusResp.json()) as Record<string, unknown>) : {};
      const sessionsData = sessionsResp.ok ? ((await sessionsResp.json()) as SessionsResponse) : {};
      const recent = (sessionsData.sessions || []).slice(0, 5);
      lines.push(`workspace ${process.cwd()}`);
      lines.push(`provider ${String(statusData.provider || 'unknown')}`);
      lines.push(`model ${String(statusData.model || currentModelLabel)}`);
      lines.push('git status');
      const gitStatus = readGitSnippet(['status', '--short', '--branch']);
      lines.push(...(gitStatus ? gitStatus.split('\n').slice(0, 8) : ['clean']));
      lines.push('recent commits');
      const gitLog = readGitSnippet(['log', '--oneline', '--decorate=short', '-n', '5']);
      lines.push(...(gitLog ? gitLog.split('\n').slice(0, 5) : ['no commits']));
      lines.push(`sessions ${recent.length}`);
      for (const sess of recent) {
        lines.push(`# ${sess.title || sess.id} (${sess.message_count})`);
      }
    } catch (err) {
      lines.push(`review failed: ${asText(err)}`);
    }
    setReviewOutput(lines);
    pushItem('meta', 'Review', lines.join('\n'));
    setStatus('Workspace reviewed');
  }

  function confirmCommandSelection() {
    const selected = filteredCommands[commandSelection];
    if (!selected) return;
    const name = selected.name;
    closePicker();
    if (name === '/review') {
      void runReview();
    } else if (name === '/resume') {
      openPicker('resume');
    } else if (name === '/help') {
      pushItem('meta', 'Command', `Commands: ${COMMANDS.map((item) => item.name).join(', ')}`);
    } else if (name === '/new') {
      void createSession();
    } else if (name === '/rename') {
      setInput('/rename ');
    } else {
      setInput(`${name} `);
    }
  }

  async function refreshAndMaybeSearch(query: string) {
    setSessionFilter(query);
    await refreshSessions();
  }

  useEffect(() => {
    void refreshSessions();
    void loadRuntimeStatus();
  }, [effectiveBase]);

  useEffect(() => {
    void loadSessionHistory(activeSession);
  }, [activeSession, effectiveBase]);

  useEffect(() => {
    const wsUrl = new URL(effectiveBase);
    wsUrl.protocol = wsUrl.protocol === 'https:' ? 'wss:' : 'ws:';
    wsUrl.pathname = '/api/v1/ws';
    wsUrl.search = new URLSearchParams({ session: activeSession }).toString();

    setSocketState('connecting');
    const socket = new WebSocket(wsUrl.toString());
    socketRef.current = socket;

    socket.addEventListener('open', () => {
      setSocketState('connected');
      setStatus('Connected');
      setItems((prev) => [...prev, createStatusItem(`Connected to ${wsUrl.host}`)].slice(-220));
      const pending = pendingMessagesRef.current.splice(0);
      for (const text of pending) {
        socket.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
        pushItem('user', 'You', text);
      }
    });

    socket.addEventListener('close', () => {
      setSocketState('idle');
      setStatus('Disconnected');
      if (socketRef.current === socket) socketRef.current = null;
    });

    socket.addEventListener('error', () => {
      setSocketState('error');
      setStatus('Socket error');
    });

    socket.addEventListener('message', (event) => {
      let payload: WsPayload;
      try {
        payload = JSON.parse(event.data) as WsPayload;
      } catch {
        return;
      }
      handleWsMessage(payload);
    });

    return () => {
      socket.close();
      if (socketRef.current === socket) socketRef.current = null;
    };
  }, [effectiveBase, activeSession]);

  function pushItem(kind: StreamItemKind, title: string, body: string) {
    const item: StreamItem = { id: makeId(kind), kind, title, body: sanitize(body) };
    setItems((prev) => [...prev, item].slice(-220));
  }

  function updateItem(id: string, body: string) {
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, body: sanitize(body) } : item)));
  }

  function insertAfter(id: string | null, nextItem: StreamItem) {
    setItems((prev) => {
      if (!id) return [...prev, nextItem].slice(-220);
      const index = prev.findIndex((item) => item.id === id);
      if (index < 0) return [...prev, nextItem].slice(-220);
      return [...prev.slice(0, index + 1), nextItem, ...prev.slice(index + 1)].slice(-220);
    });
  }

  function handleWsMessage(msg: WsPayload) {
    const data = (msg.data || {}) as Record<string, unknown>;
    switch (msg.type) {
      case 'status':
        if (typeof data.state === 'string') setStatus(data.state);
        if (data.message || data.text || data.summary || data.state) {
          pushItem('status', 'Status', asText(data.message || data.text || data.summary || data.state));
        }
        break;
      case 'reasoning':
        insertAfter(draftIdRef.current, {
          id: makeId('reasoning'),
          kind: 'reasoning',
          title: 'Thinking',
          body: asText(data.summary || data.text || data.reasoning || data.content || data),
        });
        break;
      case 'tool_call':
        insertAfter(draftIdRef.current, {
          id: makeId('tool_call'),
          kind: 'tool_call',
          title: `Tool: ${String(data.name || 'unknown')}`,
          body: asText(data.display || data.args || data.params || data),
        });
        break;
      case 'tool_result':
        insertAfter(draftIdRef.current, {
          id: makeId('tool_result'),
          kind: 'tool_result',
          title: `Result: ${String(data.name || 'unknown')}`,
          body: asText(data.display || data.output || data.result || data),
        });
        break;
      case 'stream_chunk': {
        const next = draftRef.current + String(data.content || '');
        draftRef.current = next;
        setDraft(next);
        if (!draftIdRef.current) {
          const id = makeId('assistant');
          draftIdRef.current = id;
          setDraftId(id);
          pushItem('assistant', 'LuckyHarness', next);
        } else {
          updateItem(draftIdRef.current, next);
        }
        break;
      }
      case 'stream_end': {
        const finalText = sanitize(String(data.full_response || draftRef.current || ''));
        if (!draftIdRef.current) pushItem('assistant', 'LuckyHarness', finalText);
        else updateItem(draftIdRef.current, finalText);
        draftRef.current = '';
        draftIdRef.current = null;
        setDraft('');
        setDraftId(null);
        void refreshSessions();
        break;
      }
      case 'error':
        pushItem('error', 'Runtime Error', String(data.message || 'unknown error'));
        draftRef.current = '';
        draftIdRef.current = null;
        setDraft('');
        setDraftId(null);
        break;
      default:
        pushItem('meta', msg.type, asText(data));
        break;
    }
  }

  function runCommand(raw: string): void {
    const { command, arg } = parseCommandParts(raw);
    switch (command) {
      case 'resume':
      case 'switch':
        if (arg) {
          const target = sessions.find((item) => item.id === arg || item.title === arg);
          if (!target) {
            pushItem('error', 'Command', `Session not found: ${arg}`);
            setStatus(`Session not found: ${arg}`);
            return;
          }
          switchSession(target.id);
          return;
        }
        if (sessions.length === 0) {
          pushItem('error', 'Command', 'No sessions available');
          setStatus('No sessions available');
          return;
        }
        setSessionFilter('');
        openPicker('resume');
        return;
      case 'sessions':
        void refreshSessions();
        pushItem('meta', 'Command', 'Session list refreshed');
        return;
      case 'new':
        void createSession();
        return;
      case 'help':
        pushItem('meta', 'Command', `Commands: ${COMMANDS.map((item) => item.name).join(', ')}`);
        return;
      case 'rename':
        if (arg) {
          void renameSessionByTitle(arg);
          return;
        }
        void renameSession();
        return;
      case 'review':
        void runReview();
        return;
      default:
        pushItem('error', 'Command', `Unknown command: ${raw}`);
        setStatus(`Unknown command: ${raw}`);
    }
  }

  function send() {
    const text = input.trim();
    if (!text) return;
    setInput('');
    if (text.startsWith('/')) {
      runCommand(text);
      return;
    }
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      pendingMessagesRef.current.push(text);
      setStatus('Socket reconnecting, queued message');
      return;
    }
    pushItem('user', 'You', text);
    draftRef.current = '';
    draftIdRef.current = null;
    setDraft('');
    setDraftId(null);
    socket.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
  }

  function commitResumeSelection() {
    const target = filteredSessions[resumeSelection];
    if (target) switchSession(target.id);
    closePicker();
  }

  function updateSessionFilter(next: string) {
    setSessionFilter(next);
    void refreshAndMaybeSearch(next);
  }

  useInput((inputChar, key) => {
    if (pickerOpen) {
      if (key.escape) {
        closePicker();
        return;
      }
      if (pickerMode === 'resume') {
        if (key.upArrow) {
          setResumeSelection((prev) => Math.max(0, prev - 1));
          return;
        }
        if (key.downArrow) {
          setResumeSelection((prev) => Math.min(Math.max(0, filteredSessions.length - 1), prev + 1));
          return;
        }
        if (key.return) {
          commitResumeSelection();
          return;
        }
        if (key.backspace || key.delete) {
          updateSessionFilter(sessionFilter.slice(0, -1));
          return;
        }
        if (inputChar) {
          updateSessionFilter(sessionFilter + inputChar);
          return;
        }
      }
      if (pickerMode === 'commands') {
        if (key.upArrow) {
          setCommandSelection((prev) => Math.max(0, prev - 1));
          return;
        }
        if (key.downArrow) {
          setCommandSelection((prev) => Math.min(Math.max(0, filteredCommands.length - 1), prev + 1));
          return;
        }
        if (key.return) {
          confirmCommandSelection();
          return;
        }
        if (key.backspace || key.delete) {
          setCommandQuery((prev) => prev.slice(0, -1));
          return;
        }
        if (inputChar) {
          setCommandQuery((prev) => `${prev}${inputChar}`);
          return;
        }
      }
    } else {
      if (key.escape) {
        setInput('');
        setStatus('Input cleared');
        return;
      }
      if (key.tab && input.startsWith('/')) {
        openPicker('commands');
        return;
      }
      if (key.ctrl && inputChar === 'c') {
        exit();
      }
    }
  });

  const bannerLine = CLOVER[headerTick % CLOVER.length];
  const topLineLeft = compact(['LuckyHarness', currentSessionLabel, socketState.toUpperCase()]);
  const topLineRight = compact([currentModelLabel, activeSession || 'no-session']);
  const feedLines = [
    `session ${currentSessionLabel}`,
    `socket ${socketState}`,
    `state ${status}${sessionLoading ? ' | sessions loading' : ''}${historyLoading ? ' | history loading' : ''}`,
    draft ? `draft ${sanitize(draft).slice(0, 80)}` : '',
  ].filter(Boolean);
  const timelineWidth = Math.max(32, viewportWidth - 4);

  return (
    <Box flexDirection="column" paddingX={1} paddingY={0}>
      <Box justifyContent="space-between">
        <Text color="greenBright">{divyTitle(topLineLeft, Math.max(12, Math.floor(viewportWidth / 2) - 6))}</Text>
        <Text color="greenBright">{bannerLine}</Text>
        <Text color="greenBright">{divyTitle(topLineRight, Math.max(12, Math.floor(viewportWidth / 2) - 6))}</Text>
      </Box>
      <Text dimColor>{renderFullDivider(viewportWidth)}</Text>
      <Box justifyContent="space-between">
        <Text dimColor>{`API ${effectiveBase}`}</Text>
        <Text dimColor>{`/review /resume /rename /help  Esc clears  Ctrl+C quits`}</Text>
      </Box>
      <Text dimColor>{renderFullDivider(viewportWidth)}</Text>

      <Box flexDirection="row" marginBottom={1}>
        <Box flexDirection="column" width={Math.max(26, Math.min(42, Math.floor(viewportWidth * 0.3)))}>
          <Text color="whiteBright">Sessions</Text>
          <Text dimColor>{sessionFilter ? `filter ${sessionFilter}` : sessionLoading ? 'loading...' : 'latest'}</Text>
          {filteredSessions.length === 0 ? (
            <Text dimColor>no sessions</Text>
          ) : (
            filteredSessions.slice(0, 8).map((item, index) => (
              <Text key={item.id} color={item.id === activeSession ? 'greenBright' : index === resumeSelection ? 'cyanBright' : 'white'}>
                {item.id === activeSession ? '▸' : index === resumeSelection ? '›' : ' '} {item.title || item.id} ({item.message_count})
              </Text>
            ))
          )}
        </Box>
        <Box flexDirection="column" flexGrow={1} paddingLeft={2}>
          <Text color="whiteBright">Status</Text>
          {feedLines.map((line) => (
            <Text key={line} dimColor>
              {line}
            </Text>
          ))}
          {reviewOutput.length > 0 ? (
            <>
              <Text dimColor>{renderFullDivider(Math.max(32, Math.floor(viewportWidth * 0.6)))}</Text>
              {reviewOutput.map((line) => (
                <Text key={line} dimColor>
                  {line}
                </Text>
              ))}
            </>
          ) : null}
        </Box>
      </Box>

      <Text dimColor>{renderFullDivider(viewportWidth)}</Text>

      <Box flexDirection="column" flexGrow={1}>
        {items.length === 0 ? (
          <Box paddingY={1} flexDirection="column">
            <Text>Use the prompt below to talk to LuckyHarness.</Text>
            <Text dimColor>Reasoning, tool calls, and tool results appear inline in the same stream.</Text>
          </Box>
        ) : (
          items.map((item) => (
            <Box key={item.id} width="100%" flexDirection="column" marginBottom={1}>
              {isBubbleKind(item.kind) ? (
                <Box width="100%" flexDirection="row" justifyContent={item.kind === 'user' ? 'flex-end' : 'flex-start'}>
                  <Box width={Math.max(40, Math.floor(viewportWidth * 0.92))} flexDirection="column">
                    <Box
                      width="100%"
                      flexDirection="column"
                      backgroundColor={item.kind === 'user' ? 'black' : undefined}
                      paddingX={1}
                      paddingY={0}
                    >
                      {renderMarkdown(item.body, viewportWidth).map((line, index) => (
                        <Text key={`${item.id}-${index}`} color={kindColor(item.kind)}>
                          {line || ' '}
                        </Text>
                      ))}
                    </Box>
                  </Box>
                </Box>
              ) : (
                <Box flexDirection="column" width="100%">
                  <Text dimColor>{renderDividerLabel(item.kind === 'status' ? item.body : kindLabel(item.kind) || item.title, timelineWidth)}</Text>
                  {formatItemBody(item, viewportWidth).split('\n').map((line, index) => (
                    <Text key={`${item.id}-${index}`} color={kindColor(item.kind)}>
                      {line || ' '}
                    </Text>
                  ))}
                  <Text dimColor>{renderFullDivider(timelineWidth)}</Text>
                </Box>
              )}
            </Box>
          ))
        )}
      </Box>

      {pickerOpen ? (
        <Box
          position="absolute"
          top={5}
          left={Math.max(2, Math.floor((viewportWidth - Math.min(80, viewportWidth - 4)) / 2))}
          width={Math.min(80, viewportWidth - 4)}
          flexDirection="column"
          paddingX={1}
          paddingY={0}
        >
          <Text color="greenBright">{pickerMode === 'resume' ? 'Resume session' : 'Command palette'}</Text>
          <Text dimColor>{pickerMode === 'resume' ? 'Type to filter, arrows to move, Enter to resume' : 'Type to filter, arrows to move, Enter to run'}</Text>
          {pickerMode === 'resume' ? <Text dimColor>{sessionFilter ? `search ${sessionFilter}` : 'search sessions'}</Text> : null}
          {pickerMode === 'commands' ? <Text dimColor>{commandQuery ? `search ${commandQuery}` : 'search commands'}</Text> : null}
          <Box marginTop={1} flexDirection="column">
            {pickerMode === 'resume' ? (
              filteredSessions.length === 0 ? (
                <Text dimColor>No sessions available</Text>
              ) : (
                filteredSessions.slice(0, 12).map((item, index) => (
                  <Text key={item.id} color={index === resumeSelection ? 'greenBright' : 'white'}>
                    {index === resumeSelection ? '▸' : ' '} {item.title || item.id}  {item.id}
                  </Text>
                ))
              )
            ) : (
              filteredCommands.map((item, index) => (
                <Text key={item.name} color={index === commandSelection ? 'greenBright' : 'white'}>
                  {index === commandSelection ? '▸' : ' '} {item.name} - {item.help}
                </Text>
              ))
            )}
          </Box>
        </Box>
      ) : null}

      <Text dimColor>{renderFullDivider(viewportWidth)}</Text>
      <Box flexDirection="column">
        <Text color="greenBright">Prompt</Text>
        <TextInput value={input} onChange={setInput} onSubmit={send} placeholder="Type a message or /resume" />
        <Box marginTop={1} justifyContent="space-between">
          <Text dimColor>Enter sends</Text>
          <Text dimColor>Tab command palette</Text>
        </Box>
      </Box>
    </Box>
  );
}
