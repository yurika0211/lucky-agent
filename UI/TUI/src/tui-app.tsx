import { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, useApp, useInput } from 'ink';
import TextInput from 'ink-text-input';

type WsPayload = {
  type: string;
  data?: Record<string, unknown>;
};

type StreamItem = {
  id: string;
  kind: 'user' | 'assistant' | 'meta' | 'error' | 'reasoning' | 'tool_call' | 'tool_result' | 'status';
  title: string;
  body: string;
};

type AppProps = {
  apiBase: string;
  session: string;
  model: string;
};

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
    if (!url.hostname || url.hostname === '0.0.0.0' || url.hostname === '::') {
      url.hostname = defaultHost;
    }
    return `${url.protocol}//${url.host}`.replace(/\/+$/, '');
  } catch {
    return fallback;
  }
}

function makeId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function sanitize(text: string): string {
  return text.replace(/\r\n/g, '\n').trimEnd();
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

function kindColor(kind: StreamItem['kind']): 'greenBright' | 'cyanBright' | 'gray' | 'yellowBright' | 'redBright' | 'whiteBright' {
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
    case 'status':
    case 'meta':
    default:
      return 'whiteBright';
  }
}

function kindLabel(kind: StreamItem['kind']): string {
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

export function App({ apiBase, session, model }: AppProps) {
  const { exit } = useApp();
  const [input, setInput] = useState('');
  const [items, setItems] = useState<StreamItem[]>([]);
  const [socketState, setSocketState] = useState<'idle' | 'connecting' | 'connected' | 'error'>('idle');
  const [status, setStatus] = useState('Booting');
  const [draft, setDraft] = useState('');
  const [draftId, setDraftId] = useState<string | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const draftRef = useRef('');
  const draftIdRef = useRef<string | null>(null);

  const effectiveBase = useMemo(() => normalizeApiBase(apiBase), [apiBase]);

  useEffect(() => {
    const wsUrl = new URL(effectiveBase);
    wsUrl.protocol = wsUrl.protocol === 'https:' ? 'wss:' : 'ws:';
    wsUrl.pathname = '/api/v1/ws';
    wsUrl.search = new URLSearchParams({ session }).toString();

    setSocketState('connecting');
    const socket = new WebSocket(wsUrl.toString());
    socketRef.current = socket;

    socket.addEventListener('open', () => {
      setSocketState('connected');
      setStatus('Connected');
      const item: StreamItem = {
        id: makeId('meta'),
        kind: 'status',
        title: 'Socket',
        body: `connected to ${wsUrl.toString()}`,
      };
      setItems((prev) => [item, ...prev].slice(0, 120));
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
  }, [effectiveBase, session]);

  useInput((inputChar, key) => {
    if (key.escape) {
      setInput('');
      return;
    }
    if (key.ctrl && inputChar === 'c') {
      exit();
    }
  });

  function pushItem(kind: StreamItem['kind'], title: string, body: string) {
    const item: StreamItem = { id: makeId(kind), kind, title, body: sanitize(body) };
    setItems((prev) => [...prev, item].slice(-120));
  }

  function updateItem(id: string, body: string) {
    setItems((prev) => prev.map((item) => (item.id === id ? { ...item, body: sanitize(body) } : item)));
  }

  function insertAfter(id: string | null, nextItem: StreamItem) {
    setItems((prev) => {
      if (!id) return [...prev, nextItem].slice(-120);
      const index = prev.findIndex((item) => item.id === id);
      if (index < 0) return [...prev, nextItem].slice(-120);
      return [...prev.slice(0, index + 1), nextItem, ...prev.slice(index + 1)].slice(-120);
    });
  }

  function handleWsMessage(msg: WsPayload) {
    const data = (msg.data || {}) as Record<string, unknown>;
    switch (msg.type) {
      case 'status':
        if (typeof data.state === 'string') setStatus(data.state);
        pushItem('status', 'Status', asText(data));
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
          pushItem('assistant', 'Assistant', next);
        } else {
          updateItem(draftIdRef.current, next);
        }
        break;
      }
      case 'stream_end': {
        const finalText = String(data.full_response || draftRef.current);
        if (!draftIdRef.current) {
          pushItem('assistant', 'Assistant', finalText);
        } else {
          updateItem(draftIdRef.current, finalText);
        }
        draftRef.current = '';
        draftIdRef.current = null;
        setDraft('');
        setDraftId(null);
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

  function send() {
    const text = input.trim();
    if (!text || !socketRef.current || socketRef.current.readyState !== WebSocket.OPEN) return;
    pushItem('user', 'You', text);
    setInput('');
    draftRef.current = '';
    draftIdRef.current = null;
    setDraft('');
    setDraftId(null);
    socketRef.current.send(JSON.stringify({ type: 'chat', data: { message: text, stream: true, max_iterations: 8 } }));
  }

  return (
    <Box flexDirection="column" padding={1}>
      <Box borderStyle="round" paddingX={1} paddingY={0} marginBottom={1} flexDirection="row" justifyContent="space-between">
        <Box flexDirection="column">
          <Text color="greenBright">LuckyHarness</Text>
          <Text dimColor>
            {socketState.toUpperCase()} · {status}
          </Text>
        </Box>
        <Box flexDirection="column" alignItems="flex-end">
          <Text dimColor>{model}</Text>
          <Text dimColor>{session}</Text>
        </Box>
      </Box>

      <Box borderStyle="round" paddingX={1} paddingY={0} marginBottom={1} flexDirection="row" justifyContent="space-between">
        <Text dimColor>API {effectiveBase}</Text>
        <Text dimColor>Ctrl+C exits · Esc clears input</Text>
      </Box>

      <Box borderStyle="round" paddingX={1} paddingY={0} flexDirection="column" flexGrow={1}>
        <Text color="whiteBright">Conversation</Text>
        {items.length === 0 ? (
          <Box paddingY={1} flexDirection="column">
            <Text>Use the prompt below to talk to LuckyHarness.</Text>
            <Text dimColor>Reasoning and tool events will appear inline in the same stream.</Text>
          </Box>
        ) : (
          items.map((item) => (
            <Box
              key={item.id}
              width="100%"
              flexDirection="row"
              justifyContent={item.kind === 'user' ? 'flex-end' : 'flex-start'}
              marginTop={1}
            >
              <Box
                width={item.kind === 'user' || item.kind === 'assistant' ? '84%' : '100%'}
                flexDirection="column"
                borderStyle={item.kind === 'error' ? 'double' : item.kind === 'status' ? 'single' : 'round'}
                paddingX={1}
                paddingY={0}
              >
                {item.kind !== 'user' && item.kind !== 'assistant' ? (
                  <Text color={kindColor(item.kind)}>{kindLabel(item.kind)}</Text>
                ) : null}
                <Text color={kindColor(item.kind)}>{item.body || ' '}</Text>
              </Box>
            </Box>
          ))
        )}
      </Box>

      <Box borderStyle="round" paddingX={1} paddingY={0} marginTop={1} flexDirection="column">
        <Text color="greenBright">Prompt</Text>
        <TextInput value={input} onChange={setInput} onSubmit={send} placeholder="Type a message and press Enter" />
        <Box marginTop={1} justifyContent="space-between">
          <Text dimColor>Enter sends</Text>
          <Text dimColor>Ctrl+C quits</Text>
        </Box>
      </Box>
    </Box>
  );
}
