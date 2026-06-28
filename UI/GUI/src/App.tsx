import { useEffect, useMemo, useRef, useState } from 'react';
import type {
  ActivityNote,
  ChatMessage,
  DashboardData,
  DashboardStatus,
  ProviderMessage,
  RuntimeSession,
  SessionHistory,
  SessionsResponse,
  WsPayload,
} from './types';
import { Markdown } from './components/Markdown';
import { Gateways } from './components/Gateways';

type ThemeMode = 'light' | 'dark';
type WorkspaceView = 'chat' | 'gateways';

type Bubble = ChatMessage;

const DEFAULT_API_BASE = 'http://127.0.0.1:9090';
const DEFAULT_SESSION = 'dashboard-main';
const MAX_MESSAGES = 120;
const MAX_ACTIVITY = 48;

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
  const [feed, setFeed] = useState<string[]>([]);
  const [sessions, setSessions] = useState<RuntimeSession[]>([]);
  const [sessionQuery, setSessionQuery] = useState('');
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [sessionsError, setSessionsError] = useState('');
  const [composer, setComposer] = useState('');
  const [rawLog, setRawLog] = useState('Waiting for runtime data...');
  const [loadingDashboard, setLoadingDashboard] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const assistantDraftRef = useRef('');
  const assistantBubbleRef = useRef<string | null>(null);
  const streamRef = useRef<HTMLDivElement | null>(null);

  const effectiveBase = useMemo(() => normalizeApiBase(apiBase) || DEFAULT_API_BASE, [apiBase]);

  function pushBubble(role: Bubble['role'], title: string, body: string, meta?: string): string {
    const next: Bubble = { id: makeId(role), role, title, body, meta: meta || nowLabel() };
    setMessages((prev) => [...prev, next].slice(-MAX_MESSAGES));
    return next.id;
  }

  function updateBubble(id: string, body: string, meta?: string) {
    setMessages((prev) => prev.map((item) => (item.id === id ? { ...item, body, meta: meta ?? item.meta } : item)));
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

  async function loadSessionHistory(id = session) {
    const target = id.trim();
    if (!target) return;
    try {
      const response = await fetchRuntime(`/v1/sessions/${encodeURIComponent(target)}`);
      if (!response.ok) throw new Error(`session ${response.status}`);
      const payload = (await response.json()) as SessionHistory;
      setSession(target);
      setMessages(historyToBubbles(payload.messages));
      assistantDraftRef.current = '';
      assistantBubbleRef.current = null;
      pushActivity('socket', 'Session loaded', `${payload.title || target} · ${payload.message_count ?? 0} messages`);
    } catch (error) {
      pushActivity('error', 'Load session failed', String(error));
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
      setMessages([]);
      setActivity([]);
      pushActivity('socket', 'New session', next.id);
      await loadSessions('');
    } catch (error) {
      pushActivity('error', 'New session failed', String(error));
    }
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
        pushActivity('error', 'Protocol parse failed', String(event.data).slice(0, 200));
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
        const summary = String(payload.summary || '').trim();
        if (summary) pushActivity('reasoning', `Reasoning${payload.round ? ` round ${payload.round}` : ''}`, summary);
        break;
      }
      case 'tool_call': {
        const name = String(payload.name || 'tool');
        pushActivity('tool', `Calling ${name}`, preview(payload.display || payload.args || payload.params), String(payload.phase || 'start'));
        break;
      }
      case 'tool_result': {
        const name = String(payload.name || 'tool');
        pushActivity(payload.success === false ? 'error' : 'tool', `Result ${name}`, preview(payload.display || payload.output), 'done');
        break;
      }
      case 'stream_chunk': {
        const chunk = String(payload.content || '');
        if (!chunk) break;
        assistantDraftRef.current += chunk;
        updateBubble(ensureAssistantBubble(), assistantDraftRef.current, 'streaming');
        break;
      }
      case 'stream_end': {
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
    if (!text) return;
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) {
      pushActivity('error', 'Not connected', 'Connect to the runtime before sending.');
      return;
    }
    pushBubble('user', 'You', text);
    setComposer('');
    assistantDraftRef.current = '';
    assistantBubbleRef.current = pushBubble('assistant', 'LuckyAgent', '', 'streaming');
    setSocketState('running');
    pushFeed('sent');
    wsRef.current.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
  }

  function handleComposerKey(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
      event.preventDefault();
      sendMessage();
    }
  }

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
    return () => {
      if (wsRef.current) wsRef.current.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    streamRef.current?.scrollTo({ top: streamRef.current.scrollHeight, behavior: 'smooth' });
  }, [messages]);

  const stats = [
    ['Runtime', status.running ? 'running' : 'unknown'],
    ['Provider', data.provider || status.provider || 'n/a'],
    ['Model', data.model || status.model || 'n/a'],
    ['Sessions', String(data.sessions_total ?? status.sessions_total ?? sessions.length)],
    ['Memory', String(data.memory_total ?? status.memory_total ?? 0)],
    ['Tools', String(data.tools_total ?? data.tools_enabled ?? status.tools_builtin_total ?? 0)],
  ];

  const runtimeRows = [
    ['API', effectiveBase],
    ['Route', '/api/v1/ws'],
    ['Session', session || DEFAULT_SESSION],
    ['Socket', socketState],
  ];

  return (
    <div className="dashboard-shell">
      <aside className="rail">
        <div className="brand-mark">LH</div>
        <button
          className={`rail-button ${view === 'chat' ? 'active' : ''}`}
          type="button"
          title="Chat workspace"
          onClick={() => setView('chat')}
        >
          C
        </button>
        <button
          className={`rail-button ${view === 'gateways' ? 'active' : ''}`}
          type="button"
          title="Gateways"
          onClick={() => setView('gateways')}
        >
          G
        </button>
        <div className="rail-spacer" />
        <button
          className="rail-button"
          type="button"
          title={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
          onClick={() => setTheme((prev) => (prev === 'dark' ? 'light' : 'dark'))}
        >
          {theme === 'dark' ? '☀' : '☾'}
        </button>
        <button className="rail-button muted" type="button" title="Refresh" onClick={() => void loadDashboard()}>
          ⟳
        </button>
        <div className="avatar">GUI</div>
      </aside>

      <main className="workspace">
        <section className="topbar panel">
          <div className="topbar-title">
            <span className="eyebrow">LuckyAgent GUI</span>
            <h1>{view === 'gateways' ? 'Messaging gateways' : 'Agent runtime workspace'}</h1>
          </div>
          <div className="topbar-actions">
            <span className={`connection-pill ${connected ? 'ok' : socketState === 'error' ? 'err' : ''}`}>
              {connected ? 'Connected' : socketState === 'connecting' ? 'Connecting' : 'Offline'}
            </span>
            <button className="ghost" type="button" onClick={() => void loadDashboard()} disabled={loadingDashboard}>
              Refresh
            </button>
            <button className="primary" type="button" onClick={connected ? () => disconnect() : connect}>
              {connected ? 'Disconnect' : 'Connect'}
            </button>
            <button className="danger" type="button" onClick={stopRun} disabled={!connected && socketState !== 'connecting'}>
              Stop
            </button>
          </div>
          <div className="topbar-controls">
            <label>
              <span>API Base</span>
              <input value={apiBase} onChange={(event) => setApiBase(event.target.value)} spellCheck={false} />
            </label>
            <label>
              <span>Session</span>
              <input value={session} onChange={(event) => setSession(event.target.value)} spellCheck={false} />
            </label>
            <button className="ghost" type="button" onClick={() => void loadSessionHistory()}>
              Load
            </button>
            <button className="ghost" type="button" onClick={() => void createSession()}>
              New
            </button>
          </div>
        </section>

        <section className={`content ${view === 'gateways' ? 'single' : ''}`}>
          {view === 'gateways' ? (
            <Gateways fetchRuntime={fetchRuntime} pushActivity={pushActivity} pushFeed={pushFeed} />
          ) : (
          <>
          <aside className="left-col">
            <section className="panel">
              <div className="panel-head">
                <h2>Runtime</h2>
                <span className={`status-dot ${connected ? 'ok' : socketState === 'error' ? 'err' : 'idle'}`} />
              </div>
              <div className="panel-body">
                {stats.map(([key, value]) => (
                  <div className="kv" key={key}>
                    <span>{key}</span>
                    <strong>{formatValue(value)}</strong>
                  </div>
                ))}
              </div>
            </section>

            <section className="panel sessions-panel">
              <div className="panel-head">
                <h2>Sessions</h2>
                <button className="mini-button" type="button" onClick={() => void loadSessions()}>
                  Sync
                </button>
              </div>
              <div className="session-search">
                <input
                  value={sessionQuery}
                  onChange={(event) => setSessionQuery(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') void loadSessions();
                  }}
                  placeholder="Search sessions"
                  spellCheck={false}
                />
              </div>
              <div className="session-list">
                {sessionsLoading ? <div className="empty-line">Loading sessions</div> : null}
                {sessionsError ? <div className="empty-line error-text">{sessionsError}</div> : null}
                {!sessionsLoading && !sessions.length ? <div className="empty-line">No sessions</div> : null}
                {sessions.slice(0, 12).map((item) => (
                  <button
                    className={`session-item ${item.id === session ? 'active' : ''}`}
                    key={item.id}
                    type="button"
                    onClick={() => void loadSessionHistory(item.id)}
                  >
                    <span>{item.title || item.id}</span>
                    <small>
                      {item.message_count ?? 0} msgs · {sessionAge(item.updated_at || item.created_at)}
                    </small>
                  </button>
                ))}
              </div>
            </section>

            <section className="panel">
              <div className="panel-head">
                <h2>Connection</h2>
              </div>
              <div className="panel-body">
                {runtimeRows.map(([key, value]) => (
                  <div className="kv" key={key}>
                    <span>{key}</span>
                    <strong>{formatValue(value)}</strong>
                  </div>
                ))}
              </div>
            </section>
          </aside>

          <section className="chat-panel">
            <div className="chat-header">
              <div>
                <div className="eyebrow">Conversation</div>
                <h2>{session || DEFAULT_SESSION}</h2>
              </div>
              <div className="chat-status">{socketState.toUpperCase()}</div>
            </div>

            <div className="message-stream" ref={streamRef}>
              {messages.length === 0 ? (
                <div className="empty-state">
                  <div className="empty-title">Ready</div>
                  <p>Connect to the runtime or load a previous session.</p>
                </div>
              ) : (
                messages.map((msg) => (
                  <article className={`bubble ${msg.role}`} key={msg.id}>
                    <div className="bubble-head">
                      <span>{msg.title}</span>
                      <span>{msg.meta}</span>
                    </div>
                    <div className="bubble-body">
                      <Markdown source={msg.body} />
                    </div>
                  </article>
                ))
              )}
            </div>

            <div className="composer">
              <textarea
                value={composer}
                onChange={(event) => setComposer(event.target.value)}
                onKeyDown={handleComposerKey}
                placeholder="Type a message"
                spellCheck={false}
                title="Ctrl+Enter or Cmd+Enter sends"
              />
              <div className="composer-row">
                <div className="feed">
                  {feed.map((item) => (
                    <span key={item}>{item}</span>
                  ))}
                </div>
                <button className="primary" type="button" onClick={sendMessage} disabled={!connected || !composer.trim()}>
                  Send
                </button>
              </div>
            </div>
          </section>

          <aside className="right-col">
            <section className="panel activity-panel">
              <div className="panel-head">
                <h2>Activity</h2>
                <button className="mini-button" type="button" onClick={() => setActivity([])}>
                  Clear
                </button>
              </div>
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

            <section className="panel raw-panel">
              <div className="panel-head">
                <h2>Raw Data</h2>
              </div>
              <pre className="raw">{rawLog}</pre>
            </section>
          </aside>
          </>
          )}
        </section>
      </main>
    </div>
  );
}
