import { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, useApp, useInput } from 'ink';
import TextInput from 'ink-text-input';

type WsPayload = { type: string; data?: Record<string, unknown> };
type StreamItemKind = 'user' | 'assistant' | 'meta' | 'error' | 'reasoning' | 'tool_call' | 'tool_result' | 'status';

type StreamItem = { id: string; kind: StreamItemKind; title: string; body: string };
type AppProps = { apiBase: string; session: string; model: string };
type SessionInfo = { id: string; title: string; message_count: number; created_at: string; updated_at: string };
type SessionsResponse = { sessions?: SessionInfo[] };
type SessionDetailResponse = { id?: string; title?: string; messages?: Array<Record<string, unknown>> };
type ApiSessionResponse = SessionInfo & { id?: string };
type PickerMode = 'resume' | 'new' | 'none';

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
  return {
    id: makeId(`history-${index}`),
    kind,
    title: kind === 'user' ? 'You' : kind === 'assistant' ? 'LuckyHarness' : 'Tool',
    body: sanitize(asText(message.content || message.text || message.message || '')) || ' ',
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
    .replace(/^---+$/gm, '─'.repeat(20))
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
  const maxWidth = Math.max(24, width - 4);
  const body = stripMarkdown(item.body);
  return body ? wrapText(body, maxWidth) : ' ';
}

function compactHeader(lines: string[]): string {
  return lines.filter(Boolean).join(' · ');
}

function createStatusItem(message: string): StreamItem {
  return { id: makeId('status'), kind: 'status', title: 'Status', body: message };
}

function renderDividerLabel(label: string, width: number): string {
  const clean = label.trim();
  const target = Math.max(24, width);
  const labelText = clean ? ` ${clean} ` : ' ';
  if (target <= labelText.length) return labelText;
  const remaining = target - labelText.length;
  const left = Math.floor(remaining / 2);
  const right = remaining - left;
  return `${'-'.repeat(left)}${labelText}${'-'.repeat(right)}`;
}

function renderFullDivider(width: number): string {
  return '-'.repeat(Math.max(24, width));
}

function isBubbleKind(kind: StreamItemKind): boolean {
  return kind === 'user' || kind === 'assistant';
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
  const socketRef = useRef<WebSocket | null>(null);
  const draftRef = useRef('');
  const draftIdRef = useRef<string | null>(null);
  const pendingMessagesRef = useRef<string[]>([]);
  const activeSessionRef = useRef(activeSession);

  const effectiveBase = useMemo(() => normalizeApiBase(apiBase), [apiBase]);
  const currentSessionLabel = useMemo(() => sessions.find((item) => item.id === activeSession)?.title?.trim() || activeSession, [activeSession, sessions]);

  useEffect(() => {
    activeSessionRef.current = activeSession;
  }, [activeSession]);

  useEffect(() => {
    const onResize = () => setViewportWidth(process.stdout.columns || 120);
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
      const history = (data.messages || []).map((message, index) => sessionMessageToItem(message, index));
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
  }

  function openPicker(mode: PickerMode) {
    setPickerMode(mode);
    setPickerOpen(true);
    setPickerIndex(Math.max(0, sessions.findIndex((item) => item.id === activeSessionRef.current)));
    setStatus(mode === 'resume' ? 'Choose a session' : 'Choose action');
  }

  function closePicker() {
    setPickerOpen(false);
    setPickerMode('none');
  }

  function confirmPicker() {
    if (pickerMode === 'resume') {
      const target = sessions[pickerIndex];
      if (target) switchSession(target.id);
    } else if (pickerMode === 'new') {
      void createSession();
    }
    closePicker();
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
    } catch {
      const fallback = `lh-${Date.now()}`;
      setActiveSession(fallback);
      setItems([]);
      await refreshSessions();
    }
  }

  useEffect(() => {
    void refreshSessions();
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
      setItems((prev) => [...prev, createStatusItem(`Connected to ${wsUrl.host}`)].slice(-180));
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
    setItems((prev) => [...prev, item].slice(-180));
  }

  function updateItem(id: string, body: string) {
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, body: sanitize(body) } : item)));
  }

  function insertAfter(id: string | null, nextItem: StreamItem) {
    setItems((prev) => {
      if (!id) return [...prev, nextItem].slice(-180);
      const index = prev.findIndex((item) => item.id === id);
      if (index < 0) return [...prev, nextItem].slice(-180);
      return [...prev.slice(0, index + 1), nextItem, ...prev.slice(index + 1)].slice(-180);
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
    const parts = raw.trim().split(/\s+/);
    const command = parts[0]?.slice(1).toLowerCase();
    const arg = parts.slice(1).join(' ').trim();
    switch (command) {
      case 'resume':
      case 'switch':
        if (arg) {
          if (!sessions.some((item) => item.id === arg)) {
            pushItem('error', 'Command', `Session not found: ${arg}`);
            setStatus(`Session not found: ${arg}`);
            return;
          }
          switchSession(arg);
          return;
        }
        if (sessions.length === 0) {
          pushItem('error', 'Command', 'No sessions available');
          setStatus('No sessions available');
          return;
        }
        openPicker('resume');
        return;
      case 'sessions':
        void refreshSessions();
        pushItem('meta', 'Command', 'Session list refreshed');
        return;
      case 'new':
        if (sessions.length === 0) {
          void createSession();
          return;
        }
        openPicker('new');
        return;
      case 'help':
        pushItem('meta', 'Command', 'Commands: /resume [session_id], /sessions, /new, /help');
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

  useInput((inputChar, key) => {
    if (pickerOpen) {
      if (key.escape) {
        closePicker();
        return;
      }
      if (key.upArrow) {
        setPickerIndex((prev) => Math.max(0, prev - 1));
        return;
      }
      if (key.downArrow) {
        setPickerIndex((prev) => Math.min(Math.max(0, sessions.length - 1), prev + 1));
        return;
      }
      if (key.return) {
        confirmPicker();
        return;
      }
    } else {
      if (key.escape) {
        setInput('');
        setStatus('Input cleared');
        return;
      }
      if (key.ctrl && inputChar === 'c') {
        exit();
      }
    }
  });

  const leftWidth = Math.max(24, Math.min(38, Math.floor(viewportWidth * 0.28)));
  const rightWidth = Math.max(40, viewportWidth - leftWidth - 6);
  const feedLines = [
    `session ${currentSessionLabel}`,
    `socket ${socketState}`,
    `state ${status}${sessionLoading ? ' | sessions loading' : ''}${historyLoading ? ' | history loading' : ''}`,
    draft ? `draft ${sanitize(draft).slice(0, 80)}` : '',
  ].filter(Boolean);

  return (
    <Box flexDirection="column" padding={1}>
      <Box borderStyle="round" paddingX={1} paddingY={0} marginBottom={1} flexDirection="row" justifyContent="space-between">
        <Box flexDirection="column">
          <Text color="greenBright">LuckyHarness</Text>
          <Text dimColor>{compactHeader([socketState.toUpperCase(), status, activeSession || 'no-session'])}</Text>
        </Box>
        <Box flexDirection="column" alignItems="flex-end">
          <Text dimColor>{model || 'unknown model'}</Text>
          <Text dimColor>{currentSessionLabel}</Text>
        </Box>
      </Box>

      <Box borderStyle="round" paddingX={1} paddingY={0} marginBottom={1} justifyContent="space-between">
        <Text dimColor>API {effectiveBase}</Text>
        <Text dimColor>/resume [session_id] · /sessions · /new · Esc clears · Ctrl+C quits</Text>
      </Box>

      <Box flexDirection="row" marginBottom={1}>
        <Box borderStyle="round" paddingX={1} paddingY={0} width={leftWidth} flexDirection="column" marginRight={1}>
          <Text color="whiteBright">Sessions</Text>
          {sessions.length === 0 ? (
            <Text dimColor>{sessionLoading ? 'loading...' : 'no sessions'}</Text>
          ) : (
            sessions.slice(0, 8).map((item) => (
              <Text key={item.id} color={item.id === activeSession ? 'greenBright' : 'white'}>
                {item.id === activeSession ? '▸' : ' '} {item.title || item.id} ({item.message_count})
              </Text>
            ))
          )}
        </Box>

        <Box borderStyle="round" paddingX={1} paddingY={0} width={rightWidth} flexDirection="column">
          <Text color="whiteBright">Feed</Text>
          {feedLines.map((line) => (
            <Text key={line} dimColor>
              {line}
            </Text>
          ))}
        </Box>
      </Box>

      <Box borderStyle="round" paddingX={1} paddingY={0} flexDirection="column" flexGrow={1}>
        {items.length === 0 ? (
          <Box paddingY={1} flexDirection="column">
            <Text>Use the prompt below to talk to LuckyHarness.</Text>
            <Text dimColor>Reasoning, tool calls, and tool results appear inline in the same stream.</Text>
          </Box>
        ) : (
          items.map((item) => (
            <Box key={item.id} width="100%" flexDirection="column" marginTop={1}>
              {isBubbleKind(item.kind) ? (
                <Box width="100%" flexDirection="row" justifyContent={item.kind === 'user' ? 'flex-end' : 'flex-start'}>
                  <Box
                    width="92%"
                    flexDirection="column"
                    borderStyle={item.kind === 'assistant' ? 'round' : 'double'}
                    paddingX={1}
                    paddingY={0}
                  >
                    {formatItemBody(item, viewportWidth).split('\n').map((line, index) => (
                      <Text key={`${item.id}-${index}`} color={kindColor(item.kind)}>
                        {line || ' '}
                      </Text>
                    ))}
                  </Box>
                </Box>
              ) : (
                <Box flexDirection="column" width="100%">
                  <Text dimColor>{renderDividerLabel(item.kind === 'status' ? item.body : kindLabel(item.kind) || item.title, viewportWidth)}</Text>
                  {formatItemBody(item, viewportWidth).split('\n').map((line, index) => (
                    <Text key={`${item.id}-${index}`} color={kindColor(item.kind)}>
                      {line || ' '}
                    </Text>
                  ))}
                  <Text dimColor>{renderFullDivider(viewportWidth)}</Text>
                </Box>
              )}
            </Box>
          ))
        )}
      </Box>

      {pickerOpen ? (
        <Box
          position="absolute"
          top={6}
          left={Math.max(2, Math.floor((viewportWidth - Math.min(72, viewportWidth - 4)) / 2))}
          width={Math.min(72, viewportWidth - 4)}
          borderStyle="double"
          flexDirection="column"
          paddingX={1}
          paddingY={0}
        >
          <Text color="greenBright">{pickerMode === 'resume' ? 'Resume session' : 'Create session'}</Text>
          <Text dimColor>Use up/down and Enter</Text>
          <Box marginTop={1} flexDirection="column">
            {pickerMode === 'resume' ? (
              sessions.length === 0 ? (
                <Text dimColor>No sessions available</Text>
              ) : (
                sessions.map((item, index) => (
                  <Text key={item.id} color={index === pickerIndex ? 'greenBright' : 'white'}>
                    {index === pickerIndex ? '▸' : ' '} {item.title || item.id}  {item.id}
                  </Text>
                ))
              )
            ) : (
              <Text dimColor>Press Enter to create a new session</Text>
            )}
          </Box>
        </Box>
      ) : null}

      <Box borderStyle="round" paddingX={1} paddingY={0} marginTop={1} flexDirection="column">
        <Text color="greenBright">Prompt</Text>
        <TextInput value={input} onChange={setInput} onSubmit={send} placeholder="Type a message or /resume" />
        <Box marginTop={1} justifyContent="space-between">
          <Text dimColor>Enter sends</Text>
          <Text dimColor>Ctrl+C quits</Text>
        </Box>
      </Box>
    </Box>
  );
}
