import { useEffect, useMemo, useRef, useState } from 'react';
import type { ChatMessage, DashboardData, DashboardStatus, WsPayload } from './types';

type Bubble = ChatMessage;

const DEFAULT_API_BASE = 'http://127.0.0.1:9090';
const DEFAULT_SESSION = 'dashboard-main';

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

export function App() {
  const [apiBase, setApiBase] = useState(DEFAULT_API_BASE);
  const [session, setSession] = useState(DEFAULT_SESSION);
  const [status, setStatus] = useState<DashboardStatus>({});
  const [data, setData] = useState<DashboardData>({});
  const [connected, setConnected] = useState(false);
  const [socketState, setSocketState] = useState<'idle' | 'connecting' | 'connected' | 'error'>('idle');
  const [messages, setMessages] = useState<Bubble[]>([]);
  const [feed, setFeed] = useState<string[]>([]);
  const [composer, setComposer] = useState('');
  const [rawLog, setRawLog] = useState('Waiting for runtime data...');
  const [assistantDraft, setAssistantDraft] = useState('');
  const [assistantId, setAssistantId] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);

  const effectiveBase = useMemo(() => normalizeApiBase(apiBase) || DEFAULT_API_BASE, [apiBase]);

  useEffect(() => {
    let active = true;
    async function load() {
      try {
        const [statusRes, dataRes] = await Promise.all([fetch('/api/status'), fetch('/api/data')]);
        const nextStatus = (await statusRes.json()) as DashboardStatus;
        const nextData = (await dataRes.json()) as DashboardData;
        if (!active) return;
        setStatus(nextStatus);
        setData(nextData);
        setRawLog(JSON.stringify({ status: nextStatus, data: nextData }, null, 2));
        const preferred = normalizeApiBase(String(nextData.api_addr || nextStatus.addr || ''));
        if (preferred) setApiBase(preferred);
      } catch (error) {
        if (!active) return;
        setRawLog(String(error));
      }
    }
    void load();
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!effectiveBase) return;

    const wsUrl = new URL(effectiveBase);
    wsUrl.protocol = wsUrl.protocol === 'https:' ? 'wss:' : 'ws:';
    wsUrl.pathname = '/api/v1/ws';
    wsUrl.search = new URLSearchParams({ session }).toString();

    if (wsRef.current) wsRef.current.close();

    setSocketState('connecting');
    const socket = new WebSocket(wsUrl.toString());
    wsRef.current = socket;

    socket.addEventListener('open', () => {
      setConnected(true);
      setSocketState('connected');
      setFeed((prev) => [`connected to ${wsUrl.toString()}`, ...prev].slice(0, 8));
    });

    socket.addEventListener('close', () => {
      setConnected(false);
      setSocketState('idle');
      if (wsRef.current === socket) wsRef.current = null;
    });

    socket.addEventListener('error', () => {
      setSocketState('error');
      setFeed((prev) => ['WebSocket error', ...prev].slice(0, 8));
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
      if (wsRef.current === socket) wsRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [effectiveBase, session]);

  function pushBubble(role: Bubble['role'], title: string, body: string, meta?: string) {
    const next: Bubble = { id: makeId(role), role, title, body, meta: meta || nowLabel() };
    setMessages((prev) => [...prev, next].slice(-80));
  }

  function updateBubble(id: string, body: string, meta?: string) {
    setMessages((prev) => prev.map((item) => (item.id === id ? { ...item, body, meta: meta ?? item.meta } : item)));
  }

  function handleWsMessage(msg: WsPayload) {
    const payload = (msg.data || {}) as Record<string, unknown>;
    switch (msg.type) {
      case 'status':
        setFeed((prev) => [`status: ${JSON.stringify(payload)}`, ...prev].slice(0, 8));
        break;
      case 'reasoning':
        setFeed((prev) => [`reasoning: ${String(payload.summary || '')}`, ...prev].slice(0, 8));
        break;
      case 'tool_call':
        setFeed((prev) => [`tool_call: ${String(payload.name || 'unknown')}`, ...prev].slice(0, 8));
        break;
      case 'tool_result':
        setFeed((prev) => [`tool_result: ${String(payload.name || 'unknown')}`, ...prev].slice(0, 8));
        break;
      case 'stream_chunk': {
        const chunk = String(payload.content || '');
        const next = assistantDraft + chunk;
        setAssistantDraft(next);
        if (!assistantId) {
          const id = makeId('assistant');
          setAssistantId(id);
          pushBubble('assistant', 'LuckyHarness', next, 'streaming');
        } else {
          updateBubble(assistantId, next, 'streaming');
        }
        break;
      }
      case 'stream_end': {
        const finalText = String(payload.full_response || assistantDraft);
        if (!assistantId) {
          pushBubble('assistant', 'LuckyHarness', finalText, 'done');
        } else {
          updateBubble(assistantId, finalText, 'done');
        }
        setAssistantDraft('');
        setAssistantId(null);
        break;
      }
      case 'error':
        pushBubble('error', 'Runtime Error', String(payload.message || 'unknown error'));
        setAssistantDraft('');
        setAssistantId(null);
        break;
      default:
        setFeed((prev) => [`${msg.type}: ${JSON.stringify(payload)}`, ...prev].slice(0, 8));
        break;
    }
  }

  function refresh() {
    void (async () => {
      const [statusRes, dataRes] = await Promise.all([fetch('/api/status'), fetch('/api/data')]);
      const nextStatus = (await statusRes.json()) as DashboardStatus;
      const nextData = (await dataRes.json()) as DashboardData;
      setStatus(nextStatus);
      setData(nextData);
      setRawLog(JSON.stringify({ status: nextStatus, data: nextData }, null, 2));
    })();
  }

  function sendMessage() {
    const text = composer.trim();
    if (!text || !wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return;
    pushBubble('user', 'You', text);
    setComposer('');
    setAssistantDraft('');
    setAssistantId(null);
    wsRef.current.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
  }

  const stats = [
    ['Status', status.running ? 'running' : 'stopped'],
    ['Provider', data.provider || status.provider || 'n/a'],
    ['Model', data.model || status.model || 'n/a'],
    ['Sessions', String(data.sessions_total ?? status.sessions_total ?? 0)],
    ['Memory', String(data.memory_total ?? status.memory_total ?? 0)],
    ['Tools', String(data.tools_total ?? data.tools_enabled ?? status.tools_builtin_total ?? 0)],
  ];

  const sidebar = [
    ['Route', '/api/v1/ws'],
    ['Session', session],
    ['Socket', socketState],
    ['API', effectiveBase],
  ];

  return (
    <div className="dashboard-shell">
      <aside className="rail">
        <div className="brand-mark">LH</div>
        <button className="rail-button active" type="button">C</button>
        <button className="rail-button" type="button">S</button>
        <button className="rail-button" type="button">W</button>
        <button className="rail-button" type="button">T</button>
        <div className="rail-spacer" />
        <button className="rail-button muted" type="button">⋯</button>
        <div className="avatar">UI</div>
      </aside>

      <main className="app-grid">
        <section className="hero">
          <div className="hero-top">
            <div className="pill">LuckyHarness Dashboard</div>
            <div className="pill muted">Session {session}</div>
          </div>
          <div className="hero-copy">
            <h1>OpenAI-style runtime chat.</h1>
            <p>
              A focused dashboard for session chat, live runtime state, and streamed tool activity.
            </p>
          </div>
          <div className="hero-controls">
            <label>
              API Base
              <input value={apiBase} onChange={(e) => setApiBase(e.target.value)} spellCheck={false} />
            </label>
            <label>
              Session
              <input value={session} onChange={(e) => setSession(e.target.value)} spellCheck={false} />
            </label>
            <button className="primary" type="button" onClick={sendMessage} disabled={!connected}>
              Send
            </button>
          </div>
        </section>

        <section className="content">
          <div className="left-col">
            <div className="panel">
              <div className="panel-head">
                <h2>Runtime</h2>
                <span className={`status-dot ${connected ? 'ok' : socketState === 'error' ? 'err' : 'idle'}`} />
              </div>
              <div className="panel-body">
                {stats.map(([k, v]) => (
                  <div className="kv" key={k}>
                    <span>{k}</span>
                    <strong>{v}</strong>
                  </div>
                ))}
              </div>
            </div>

            <div className="panel">
              <div className="panel-head">
                <h2>Navigation</h2>
              </div>
              <div className="panel-body">
                {sidebar.map(([k, v]) => (
                  <div className="kv" key={k}>
                    <span>{k}</span>
                    <strong>{v}</strong>
                  </div>
                ))}
              </div>
            </div>
          </div>

          <section className="chat-panel">
            <div className="chat-header">
              <div>
                <div className="eyebrow">Conversation</div>
                <h2>OpenAI-style chat workspace</h2>
              </div>
              <div className="chat-status">{socketState.toUpperCase()}</div>
            </div>

            <div className="message-stream">
              {messages.length === 0 ? (
                <div className="empty-state">
                  <div className="empty-title">Ready</div>
                  <p>Connect to the runtime and start a session.</p>
                </div>
              ) : (
                messages.map((msg) => (
                  <article className={`bubble ${msg.role}`} key={msg.id}>
                    <div className="bubble-head">
                      <span>{msg.title}</span>
                      <span>{msg.meta}</span>
                    </div>
                    <div className="bubble-body">
                      {msg.body.split('\n').map((line) => (
                        <div key={`${msg.id}-${line.slice(0, 16)}`}>{line || '\u00a0'}</div>
                      ))}
                    </div>
                  </article>
                ))
              )}
            </div>

            <div className="composer">
              <textarea
                value={composer}
                onChange={(e) => setComposer(e.target.value)}
                placeholder="Type a message. Ctrl+Enter sends in the runtime; here just click Send."
                spellCheck={false}
              />
              <div className="composer-row">
                <div className="feed">
                  {feed.slice(0, 4).map((item) => (
                    <span key={item}>{item}</span>
                  ))}
                </div>
                <button className="primary" type="button" onClick={sendMessage} disabled={!connected}>
                  Send Message
                </button>
              </div>
            </div>
          </section>

          <div className="right-col">
            <div className="panel tall">
              <div className="panel-head">
                <h2>Data</h2>
                <button className="ghost" type="button" onClick={refresh}>
                  Refresh
                </button>
              </div>
              <div className="panel-body">
                <pre className="raw">{rawLog}</pre>
              </div>
            </div>
          </div>
        </section>
      </main>
    </div>
  );
}
