import { useEffect, useState } from 'react';
import type { ActivityNote, GatewayStatus, GatewaysResponse } from '../types';

type PushActivity = (kind: ActivityNote['kind'], title: string, body: string, meta?: string) => void;

type GatewaysProps = {
  fetchRuntime: (path: string, init?: RequestInit) => Promise<Response>;
  pushActivity: PushActivity;
  pushFeed: (text: string) => void;
};

type ConfigBlock = {
  key: string;
  label: string;
  note: string;
  snippet: string;
};

// LH configures gateways through the `msg_gateway` block in config.json.
// Only Telegram exposes a live start endpoint; qqofficial / napcat / weixin are config-file driven.
const CONFIG_BLOCKS: ConfigBlock[] = [
  {
    key: 'telegram',
    label: 'Telegram',
    note: 'Bot token from @BotFather。可在下方表单实时启动，或写入配置随服务自启。',
    snippet: `"msg_gateway": {
  "platform": "telegram",
  "start_all": false,
  "api_addr": "127.0.0.1:9090",
  "telegram": {
    "token": "<BOT_TOKEN>",
    "proxy": "",
    "chat_timeout_seconds": 600,
    "progress_as_messages": true,
    "show_tool_details_in_result": false
  }
}`,
  },
  {
    key: 'qqofficial',
    label: 'QQ 官方机器人',
    note: 'QQ 开放平台的 app_id / app_secret，sandbox 先用沙箱联调。仅配置文件方式。',
    snippet: `"msg_gateway": {
  "platform": "qqofficial",
  "qqofficial": {
    "app_id": "<APP_ID>",
    "app_secret": "<APP_SECRET>",
    "sandbox": true,
    "allowed_chats": [],
    "allowed_users": [],
    "remove_at": true,
    "heartbeat_sec": 25,
    "intents": ["public_guild_messages", "group_and_c2c_messages"]
  }
}`,
  },
  {
    key: 'napcat',
    label: 'NapCat (OneBot v11)',
    note: 'LH 作为 WS 服务端监听，NapCat 反向连接到 listen_addr + path。仅配置文件方式。',
    snippet: `"msg_gateway": {
  "platform": "napcat",
  "napcat": {
    "listen_addr": "127.0.0.1:6701",
    "path": "/onebot/v11/ws",
    "access_token": "",
    "allowed_chats": [],
    "allowed_users": [],
    "remove_at": true,
    "group_trigger_mode": "mention"
  }
}`,
  },
  {
    key: 'weixin',
    label: '微信 (ilinkai)',
    note: 'token / account_id 来自 ilinkai 平台。group_policy 默认关闭，按需开启。仅配置文件方式。',
    snippet: `"msg_gateway": {
  "platform": "weixin",
  "weixin": {
    "token": "<TOKEN>",
    "account_id": "<ACCOUNT_ID>",
    "base_url": "https://ilinkai.weixin.qq.com",
    "dm_policy": "open",
    "group_policy": "disabled",
    "allowed_users": [],
    "poll_timeout_ms": 35000
  }
}`,
  },
];

function splitList(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

export function Gateways({ fetchRuntime, pushActivity, pushFeed }: GatewaysProps) {
  const [gateways, setGateways] = useState<GatewayStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [token, setToken] = useState('');
  const [allowedChats, setAllowedChats] = useState('');
  const [adminIds, setAdminIds] = useState('');
  const [starting, setStarting] = useState(false);
  const [copied, setCopied] = useState('');

  async function loadGateways() {
    setLoading(true);
    setError('');
    try {
      const response = await fetchRuntime('/v1/gateways');
      if (!response.ok) throw new Error(`gateways ${response.status}`);
      const payload = (await response.json()) as GatewaysResponse;
      setGateways(payload.gateways || []);
    } catch (err) {
      setError(String(err));
      pushActivity('error', 'Gateways unavailable', String(err));
    } finally {
      setLoading(false);
    }
  }

  async function startTelegram() {
    const value = token.trim();
    if (!value) {
      pushActivity('error', 'Token required', 'Enter a Telegram bot token before starting.');
      return;
    }
    setStarting(true);
    try {
      const response = await fetchRuntime('/v1/gateways/telegram/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          token: value,
          allowed_chats: splitList(allowedChats),
          admin_ids: splitList(adminIds),
        }),
      });
      const body = (await response.json().catch(() => ({}))) as Record<string, unknown>;
      if (!response.ok) {
        throw new Error(String(body.message || body.error || `start ${response.status}`));
      }
      pushActivity('socket', 'Telegram started', String(body.message || 'telegram gateway started'));
      pushFeed('telegram started');
      setToken('');
      await loadGateways();
    } catch (err) {
      pushActivity('error', 'Telegram start failed', String(err));
    } finally {
      setStarting(false);
    }
  }

  async function stopGateway(name: string) {
    try {
      const response = await fetchRuntime(`/v1/gateways/${encodeURIComponent(name)}/stop`, { method: 'POST' });
      const body = (await response.json().catch(() => ({}))) as Record<string, unknown>;
      if (!response.ok) throw new Error(String(body.message || body.error || `stop ${response.status}`));
      pushActivity('socket', `${name} stopped`, String(body.message || ''));
      pushFeed(`${name} stopped`);
      await loadGateways();
    } catch (err) {
      pushActivity('error', `Stop ${name} failed`, String(err));
    }
  }

  async function copySnippet(key: string, snippet: string) {
    try {
      await navigator.clipboard.writeText(snippet);
      setCopied(key);
      window.setTimeout(() => setCopied((current) => (current === key ? '' : current)), 1500);
    } catch {
      pushActivity('error', 'Copy failed', 'Clipboard access was blocked.');
    }
  }

  useEffect(() => {
    void loadGateways();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="gateways">
      <header className="gateways-head">
        <div>
          <div className="eyebrow">Messaging</div>
          <h2>Gateways</h2>
        </div>
        <button className="ghost" type="button" onClick={() => void loadGateways()} disabled={loading}>
          {loading ? 'Refreshing' : 'Refresh'}
        </button>
      </header>

      <section className="panel gw-section">
        <div className="panel-head">
          <h2>Live status</h2>
          <span className="muted-tag">{gateways.length} registered</span>
        </div>
        {error ? <div className="empty-line error-text">{error}</div> : null}
        {!error && gateways.length === 0 ? (
          <div className="empty-line">No gateways registered. Start one below or via config.json.</div>
        ) : null}
        <div className="gw-list">
          {gateways.map((gw) => (
            <article className="gw-item" key={gw.name}>
              <div className="gw-item-main">
                <span className={`status-dot ${gw.running ? 'ok' : 'idle'}`} />
                <strong>{gw.name}</strong>
                <span className={`gw-state ${gw.running ? 'on' : 'off'}`}>{gw.running ? 'running' : 'stopped'}</span>
              </div>
              <div className="gw-item-stats">
                <span>↑ {gw.stats?.MessagesSent ?? 0}</span>
                <span>↓ {gw.stats?.MessagesReceived ?? 0}</span>
                <span>⚠ {gw.stats?.Errors ?? 0}</span>
              </div>
              <button className="mini-button" type="button" onClick={() => void stopGateway(gw.name)} disabled={!gw.running}>
                Stop
              </button>
            </article>
          ))}
        </div>
      </section>

      <section className="panel gw-section">
        <div className="panel-head">
          <h2>Start Telegram</h2>
          <span className="muted-tag">live</span>
        </div>
        <div className="gw-form">
          <label>
            <span>Bot token</span>
            <input value={token} onChange={(event) => setToken(event.target.value)} placeholder="123456:ABC-..." spellCheck={false} />
          </label>
          <label>
            <span>Allowed chats</span>
            <input
              value={allowedChats}
              onChange={(event) => setAllowedChats(event.target.value)}
              placeholder="comma or newline separated (optional)"
              spellCheck={false}
            />
          </label>
          <label>
            <span>Admin IDs</span>
            <input
              value={adminIds}
              onChange={(event) => setAdminIds(event.target.value)}
              placeholder="comma or newline separated (optional)"
              spellCheck={false}
            />
          </label>
          <button className="primary" type="button" onClick={() => void startTelegram()} disabled={starting || !token.trim()}>
            {starting ? 'Starting' : 'Start gateway'}
          </button>
        </div>
      </section>

      <section className="panel gw-section">
        <div className="panel-head">
          <h2>Config method</h2>
          <span className="muted-tag">config.json · msg_gateway</span>
        </div>
        <p className="gw-hint">
          LH 通过 <code>config.json</code> 的 <code>msg_gateway</code> 块配置网关。设置 <code>"start_all": true</code>{' '}
          可在服务启动时自动拉起对应平台。qq/napcat/weixin 仅支持此方式。
        </p>
        <div className="gw-config-list">
          {CONFIG_BLOCKS.map((block) => (
            <article className="gw-config" key={block.key}>
              <div className="gw-config-head">
                <strong>{block.label}</strong>
                <button className="mini-button" type="button" onClick={() => void copySnippet(block.key, block.snippet)}>
                  {copied === block.key ? 'Copied' : 'Copy'}
                </button>
              </div>
              <p className="gw-config-note">{block.note}</p>
              <pre className="gw-snippet">{block.snippet}</pre>
            </article>
          ))}
        </div>
      </section>
    </div>
  );
}
