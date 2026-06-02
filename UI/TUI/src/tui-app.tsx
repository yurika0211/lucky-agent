import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, useApp, useInput } from 'ink';
import TextInput from 'ink-text-input';
import { execFileSync } from 'node:child_process';

type WsPayload = { type: string; data?: Record<string, unknown> };
type StreamItemKind = 'user' | 'assistant' | 'meta' | 'error' | 'reasoning' | 'tool_call' | 'tool_result' | 'status';

type StreamItem = { id: string; kind: StreamItemKind; title: string; body: string; createdAt?: number };
type AppProps = { apiBase: string; session: string; model: string };
type SessionInfo = { id: string; title: string; message_count: number; created_at: string; updated_at: string };
type SessionsResponse = { sessions?: SessionInfo[] };
type SessionDetailResponse = { id?: string; title?: string; messages?: Array<Record<string, unknown>> };
type ApiSessionResponse = SessionInfo & { id?: string };
type PickerMode = 'resume' | 'commands' | 'none';
type RenderLine = { id: string; text: string; color: ReturnType<typeof kindColor> };

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
    createdAt: safeDate(String(message.created_at || message.createdAt || message.timestamp || '')) || undefined,
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

function stripMarkdownLine(line: string): string {
  return line
    .replace(/^>\s?/g, '| ')
    .replace(/^#{1,6}\s+/g, '')
    .replace(/\*\*(.*?)\*\*/g, '$1')
    .replace(/__(.*?)__/g, '$1')
    .replace(/\*(.*?)\*/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/^\s*[-*+]\s+/g, '- ')
    .replace(/^\s*\d+\.\s+/g, '- ')
    .replace(/^---+$/g, '-'.repeat(24));
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

function wrapCodeLine(line: string, width: number): string[] {
  const target = Math.max(8, width);
  if (!line) return [''];
  const out: string[] = [];
  for (let offset = 0; offset < line.length; offset += target) {
    out.push(line.slice(offset, offset + target));
  }
  return out.length ? out : [''];
}

function wrapText(text: string, width: number): string {
  return text
    .split('\n')
    .flatMap((line) => (line.trim() === '' ? [''] : wrapLine(line, width)))
    .join('\n');
}

function clampLines(lines: string[], maxLines: number): string[] {
  if (lines.length <= maxLines) return lines;
  const hidden = lines.length - maxLines;
  return [...lines.slice(0, maxLines), `... ${hidden} more lines hidden in this view`];
}

function eventKind(item: StreamItem): string {
  switch (item.kind) {
    case 'reasoning':
      return 'thinking';
    case 'tool_call':
      return item.title.replace(/^Tool:\s*/i, 'tool');
    case 'tool_result':
      return item.title.replace(/^Result:\s*/i, 'result');
    case 'status':
      return 'status';
    case 'error':
      return 'error';
    case 'meta':
      return item.title || 'meta';
    default:
      return kindLabel(item.kind).toLowerCase();
  }
}

function firstMeaningfulLine(text: string): string {
  return sanitize(text)
    .split('\n')
    .map((line) => stripMarkdownLine(line).trim())
    .find(Boolean) || '';
}

function compactEventHeader(item: StreamItem, width: number): string {
  const prefix = ['•', eventKind(item)].filter(Boolean).join(' ');
  const summary = firstMeaningfulLine(item.body);
  const text = summary ? `${prefix}: ${summary}` : prefix;
  return divyTitle(text, Math.max(24, width));
}

function auxLineLimit(kind: StreamItemKind): number {
  switch (kind) {
    case 'status':
      return 0;
    case 'reasoning':
      return 2;
    case 'tool_call':
    case 'tool_result':
      return 4;
    case 'meta':
      return 3;
    case 'error':
      return 3;
    default:
      return 4;
  }
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
  const body = sanitize(text);
  if (!body.trim()) return [' '];
  return markdownSegments(body).flatMap((line) => {
    const lineWidth = Math.max(24, width);
    if (/^```/.test(line)) {
      const codeLines = line.split('\n');
      return codeLines.flatMap((part, index) => {
        if (index === 0 || index === codeLines.length - 1) return [part];
        return wrapCodeLine(`  ${part}`, Math.max(12, lineWidth - 2));
      });
    }
    const cleaned = stripMarkdownLine(line);
    if (/^\s*[-*+]\s+/.test(cleaned)) return wrapLine(cleaned, Math.max(24, lineWidth - 2));
    return wrapText(cleaned, lineWidth).split('\n');
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

function isToolCallLike(value: unknown): value is {
  name?: unknown;
  arguments?: unknown;
  args?: unknown;
  function?: { name?: unknown; arguments?: unknown };
} {
  return typeof value === 'object' && value !== null;
}

function renderItemLines(item: StreamItem, width: number): RenderLine[] {
  const lines: RenderLine[] = [];
  const color = kindColor(item.kind);
  const contentWidth = Math.max(28, width - 4);
  if (item.kind === 'user') {
    const content = renderMarkdown(item.body, contentWidth);
    for (const [index, line] of content.entries()) {
      lines.push({
        id: `${item.id}-user-${index}`,
        text: `${index === 0 ? '› ' : '  '}${line || ' '}`,
        color: 'greenBright',
      });
    }
    lines.push({ id: `${item.id}-gap`, text: ' ', color: 'whiteBright' });
    return lines;
  }

  if (item.kind === 'assistant') {
    lines.push({
      id: `${item.id}-head`,
      text: 'assistant',
      color: 'cyanBright',
    });
    const content = renderMarkdown(item.body, contentWidth);
    for (const [index, line] of content.entries()) {
      lines.push({
        id: `${item.id}-assistant-${index}`,
        text: line || ' ',
        color,
      });
    }
    lines.push({ id: `${item.id}-gap`, text: ' ', color: 'whiteBright' });
    return lines;
  }

  lines.push({
    id: `${item.id}-head`,
    text: compactEventHeader(item, width - 2),
    color,
  });
  const rendered = renderMarkdown(item.body, contentWidth);
  const limit = auxLineLimit(item.kind);
  const bodyLines = limit <= 0 ? [] : clampLines(rendered.slice(1), limit);
  for (const [index, line] of bodyLines.entries()) {
    lines.push({
      id: `${item.id}-body-${index}`,
      text: `  ${line || ' '}`,
      color,
    });
  }
  return lines;
}

export function App({ apiBase, session, model }: AppProps) {
  const { exit } = useApp();
  const [input, setInput] = useState('');
  const [items, setItems] = useState<StreamItem[]>([]);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [activeSession, setActiveSession] = useState(session);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerMode, setPickerMode] = useState<PickerMode>('none');
  const [socketState, setSocketState] = useState<'idle' | 'connecting' | 'connected' | 'error'>('idle');
  const [status, setStatus] = useState('Booting');
  const [sessionLoading, setSessionLoading] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [viewportWidth, setViewportWidth] = useState(process.stdout.columns || 120);
  const [viewportHeight, setViewportHeight] = useState(process.stdout.rows || 40);
  const [draft, setDraft] = useState('');
  const [draftId, setDraftId] = useState<string | null>(null);
  const [commandQuery, setCommandQuery] = useState('');
  const [commandSelection, setCommandSelection] = useState(0);
  const [sessionFilter, setSessionFilter] = useState('');
  const [resumeSelection, setResumeSelection] = useState(0);
  const [runtimeModel, setRuntimeModel] = useState(model);
  const [scrollOffset, setScrollOffset] = useState(0);
  const [autoFollowBottom, setAutoFollowBottom] = useState(true);
  const socketRef = useRef<WebSocket | null>(null);
  const draftRef = useRef('');
  const draftIdRef = useRef<string | null>(null);
  const pendingMessagesRef = useRef<string[]>([]);
  const activeSessionRef = useRef(activeSession);
  const sessionsRef = useRef<SessionInfo[]>([]);
  const inputModeRef = useRef<'command' | 'resume' | 'normal'>('normal');
  const totalTranscriptLinesRef = useRef(0);
  const streamingRef = useRef(false);
  const transcriptVersionRef = useRef(0);

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
    const onResize = () => {
      setViewportWidth(process.stdout.columns || 120);
      setViewportHeight(process.stdout.rows || 40);
    };
    process.stdout?.on?.('resize', onResize);
    onResize();
    return () => {
      process.stdout?.off?.('resize', onResize);
    };
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
    const startedAtVersion = transcriptVersionRef.current;
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
      if (
        activeSessionRef.current !== sessionId ||
        transcriptVersionRef.current !== startedAtVersion ||
        streamingRef.current ||
        draftIdRef.current
      ) {
        return;
      }
      transcriptVersionRef.current += 1;
      setItems(history);
      setDraft('');
      setDraftId(null);
      draftRef.current = '';
      draftIdRef.current = null;
    } catch {
      if (
        activeSessionRef.current !== sessionId ||
        transcriptVersionRef.current !== startedAtVersion ||
        streamingRef.current ||
        draftIdRef.current
      ) {
        return;
      }
      transcriptVersionRef.current += 1;
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
    activeSessionRef.current = trimmed;
    transcriptVersionRef.current += 1;
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
      activeSessionRef.current = created;
      transcriptVersionRef.current += 1;
      setActiveSession(created);
      setItems([]);
      await refreshSessions();
      await loadSessionHistory(created);
    } catch {
      const fallback = `lh-${Date.now()}`;
      activeSessionRef.current = fallback;
      transcriptVersionRef.current += 1;
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
      const pending = pendingMessagesRef.current.splice(0);
      for (const text of pending) {
        socket.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
        streamingRef.current = true;
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
    const item: StreamItem = { id: makeId(kind), kind, title, body: sanitize(body), createdAt: Date.now() };
    transcriptVersionRef.current += 1;
    setItems((prev) => [...prev, item].slice(-220));
  }

  function updateItem(id: string, body: string) {
    transcriptVersionRef.current += 1;
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, body: sanitize(body) } : item)));
  }

  function upsertAssistantDraft(id: string, body: string) {
    const clean = sanitize(body);
    transcriptVersionRef.current += 1;
    setItems((prev) => {
      const index = prev.findIndex((item) => item.id === id);
      if (index >= 0) {
        const next = [...prev];
        next[index] = { ...next[index], body: clean };
        return next;
      }
      const item: StreamItem = {
        id,
        kind: 'assistant',
        title: 'LuckyHarness',
        body: clean,
        createdAt: Date.now(),
      };
      return [...prev, item].slice(-220);
    });
  }

  function insertAfter(id: string | null, nextItem: StreamItem) {
    const item = nextItem.createdAt ? nextItem : { ...nextItem, createdAt: Date.now() };
    transcriptVersionRef.current += 1;
    setItems((prev) => {
      if (!id) return [...prev, item].slice(-220);
      const index = prev.findIndex((item) => item.id === id);
      if (index < 0) return [...prev, item].slice(-220);
      return [...prev.slice(0, index + 1), item, ...prev.slice(index + 1)].slice(-220);
    });
  }

  function handleWsMessage(msg: WsPayload) {
    const data = (msg.data || {}) as Record<string, unknown>;
    switch (msg.type) {
      case 'status':
        if (data.message || data.text || data.summary || data.state) {
          setStatus(asText(data.message || data.text || data.summary || data.state));
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
        }
        upsertAssistantDraft(draftIdRef.current, next);
        break;
      }
      case 'stream_end': {
        const finalText = sanitize(String(data.full_response || draftRef.current || ''));
        if (draftIdRef.current) upsertAssistantDraft(draftIdRef.current, finalText);
        else if (finalText) pushItem('assistant', 'LuckyHarness', finalText);
        streamingRef.current = false;
        draftRef.current = '';
        draftIdRef.current = null;
        setDraft('');
        setDraftId(null);
        void refreshSessions();
        break;
      }
      case 'error':
        pushItem('error', 'Runtime Error', String(data.message || 'unknown error'));
        streamingRef.current = false;
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
    streamingRef.current = true;
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

  function moveScroll(nextOffset: number, followBottom: boolean) {
    setAutoFollowBottom(followBottom);
    setScrollOffset(clamped(nextOffset, 0, Math.max(0, transcriptLines.length - transcriptHeight)));
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
      if (key.pageUp) {
        moveScroll(normalizedScrollOffset - transcriptHeight + 2, false);
        return;
      }
      if (key.pageDown) {
        const next = normalizedScrollOffset + transcriptHeight - 2;
        const bottom = Math.max(0, transcriptLines.length - transcriptHeight);
        moveScroll(next, next >= bottom);
        return;
      }
      if (key.home) {
        moveScroll(0, false);
        return;
      }
      if (key.end) {
        moveScroll(Math.max(0, transcriptLines.length - transcriptHeight), true);
        return;
      }
      if (key.upArrow && input.length === 0) {
        moveScroll(normalizedScrollOffset - 1, false);
        return;
      }
      if (key.downArrow && input.length === 0) {
        const next = normalizedScrollOffset + 1;
        const bottom = Math.max(0, transcriptLines.length - transcriptHeight);
        moveScroll(next, next >= bottom);
        return;
      }
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

  const headerLeft = compact(['LuckyHarness', currentModelLabel, socketState]);
  const headerRight = compact([currentSessionLabel, status]);
  const transcriptHeight = Math.max(8, viewportHeight - (pickerOpen ? 12 : 6));
  const transcriptLines = useMemo(() => items.flatMap((item) => renderItemLines(item, viewportWidth)), [items, viewportWidth]);
  const maxScrollOffset = Math.max(0, transcriptLines.length - transcriptHeight);
  const normalizedScrollOffset = autoFollowBottom ? maxScrollOffset : Math.min(scrollOffset, maxScrollOffset);
  const visibleTranscriptLines = transcriptLines.slice(normalizedScrollOffset, normalizedScrollOffset + transcriptHeight);

  useEffect(() => {
    const nextTotal = transcriptLines.length;
    const prevTotal = totalTranscriptLinesRef.current;
    totalTranscriptLinesRef.current = nextTotal;
    if (autoFollowBottom) {
      setScrollOffset(Math.max(0, nextTotal - transcriptHeight));
      return;
    }
    if (nextTotal < prevTotal) {
      setScrollOffset((prev) => Math.min(prev, Math.max(0, nextTotal - transcriptHeight)));
    }
  }, [autoFollowBottom, transcriptHeight, transcriptLines.length]);

  return (
    <Box flexDirection="column" paddingX={1} paddingY={0}>
      <Box justifyContent="space-between">
        <Text color="greenBright">{divyTitle(headerLeft, Math.max(12, Math.floor(viewportWidth / 2) - 2))}</Text>
        <Text dimColor>{divyTitle(headerRight, Math.max(12, Math.floor(viewportWidth / 2) - 2))}</Text>
      </Box>
      <Box justifyContent="space-between">
        <Text dimColor>{effectiveBase}</Text>
        <Text dimColor>{autoFollowBottom ? 'live' : `scroll ${normalizedScrollOffset + 1}-${Math.min(transcriptLines.length, normalizedScrollOffset + transcriptHeight)} / ${transcriptLines.length}`}</Text>
      </Box>

      <Box flexDirection="column" flexGrow={1} marginTop={1}>
        {items.length === 0 ? (
          <Box paddingY={1} flexDirection="column" minHeight={transcriptHeight}>
            <Text color="cyanBright">assistant</Text>
            <Text>Ready. Ask a question or run a slash command.</Text>
            <Text dimColor>/resume to switch sessions, /review for repo context, Tab after / for commands.</Text>
          </Box>
        ) : (
          <Box flexDirection="column" minHeight={transcriptHeight}>
            {visibleTranscriptLines.map((line) => (
              <Text key={line.id} color={line.color}>
                {line.text || ' '}
              </Text>
            ))}
            {visibleTranscriptLines.length < transcriptHeight
              ? Array.from({ length: transcriptHeight - visibleTranscriptLines.length }, (_, index) => (
                  <Text key={`pad-${index}`}>{' '}</Text>
                ))
              : null}
          </Box>
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

      <Box flexDirection="column" marginTop={1}>
        <Box>
          <Text color="greenBright">› </Text>
          <TextInput value={input} onChange={setInput} onSubmit={send} placeholder="message or /command" />
        </Box>
        <Box justifyContent="space-between">
          <Text dimColor>Enter send · Esc clear · Ctrl+C quit</Text>
          <Text dimColor>PgUp/PgDn scroll · Tab commands</Text>
        </Box>
      </Box>
    </Box>
  );
}
