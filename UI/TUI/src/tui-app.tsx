import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Box, Text, useApp, useInput } from 'ink';
import TextInput from 'ink-text-input';
import { execFile, execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { existsSync } from 'node:fs';
import path from 'node:path';

type WsPayload = { type: string; data?: Record<string, unknown> };
type StreamItemKind = 'user' | 'assistant' | 'meta' | 'error' | 'reasoning' | 'tool_call' | 'tool_result' | 'status';

type StreamItem = { id: string; kind: StreamItemKind; title: string; body: string; createdAt?: number };
type AppProps = { apiBase: string; session: string; model: string };
type SessionInfo = { id: string; title: string; message_count: number; created_at: string; updated_at: string };
type SessionsResponse = { sessions?: SessionInfo[]; count?: number; total?: number; limit?: number; offset?: number; has_more?: boolean };
type SessionDetailResponse = { id?: string; title?: string; messages?: Array<Record<string, unknown>> };
type ApiSessionResponse = SessionInfo & { id?: string };
type PickerMode = 'resume' | 'commands' | 'none';
type LineDeco = { color: string; gutter?: string; gutterColor?: string; dim?: boolean; bold?: boolean; italic?: boolean };
type RenderLine = { id: string; text: string } & LineDeco;
type BackgroundJobStatus = 'running' | 'stopping' | 'exited' | 'failed';
type BackgroundJob = {
  id: string;
  title: string;
  display: string;
  args: string[];
  status: BackgroundJobStatus;
  pid?: number;
  startedAt: number;
  endedAt?: number;
  exitCode?: number | null;
  signal?: NodeJS.Signals | null;
  error?: string;
  logs: string[];
  child: ChildProcess;
  stopTimer?: NodeJS.Timeout;
};

type CommandSpec = {
  name: string;
  help: string;
  aliases: string[];
};

// Clover-green palette (truecolor). Greens lead, slate for dims, amber for tools, rose for errors.
const THEME = {
  accent: '#34d399', // emerald-400 — primary clover green
  accentBright: '#6ee7b7', // emerald-300 — highlights / user marker
  accentDim: '#0f9b75', // emerald-600 — borders, subdued accent
  user: '#86efac', // green-300 — user input text
  assistant: '#dbe4dd', // soft mint-white — assistant body
  reasoning: '#8aa1a8', // muted slate-teal — thinking
  tool: '#fbbf24', // amber-400 — tool calls / results
  error: '#fb7185', // rose-400 — errors
  meta: '#b8c4cc', // slate-300 — meta / command output
  border: '#2b6a57', // deep teal-green — idle borders
  muted: '#7c8a93', // slate — hints, footer, secondary
  title: '#a7f3d0', // mint — wordmark
} as const;

// 5-row block font, only the glyphs the wordmark needs. Composed at load so we never hand-transcribe block art.
const WORDMARK_FONT: Record<string, readonly [string, string, string, string, string]> = {
  L: ['█   ', '█   ', '█   ', '█   ', '████'],
  U: ['█  █', '█  █', '█  █', '█  █', '████'],
  C: ['████', '█   ', '█   ', '█   ', '████'],
  K: ['█  █', '█ █ ', '██  ', '█ █ ', '█  █'],
  Y: ['█  █', '█  █', ' ██ ', ' █  ', ' █  '],
  H: ['█  █', '█  █', '████', '█  █', '█  █'],
  A: [' ██ ', '█  █', '████', '█  █', '█  █'],
  R: ['███ ', '█  █', '███ ', '█ █ ', '█  █'],
  N: ['█  █', '██ █', '█ ██', '█  █', '█  █'],
  E: ['████', '█   ', '███ ', '█   ', '████'],
  S: [' ███', '█   ', ' ██ ', '   █', '███ '],
  ' ': ['  ', '  ', '  ', '  ', '  '],
};

function bigText(text: string): string[] {
  const rows = ['', '', '', '', ''];
  for (const ch of text.toUpperCase()) {
    const glyph = WORDMARK_FONT[ch] ?? WORDMARK_FONT[' '];
    for (let r = 0; r < 5; r++) rows[r] += `${glyph[r]} `;
  }
  return rows.map((row) => row.replace(/\s+$/, ''));
}

const WORDMARK_ART = bigText('LUCKY HARNESS');
const WORDMARK_COLORS = [THEME.accentBright, THEME.accentBright, THEME.accent, THEME.accent, THEME.accentDim] as const;

const COMMANDS: CommandSpec[] = [
  { name: '/review', help: '查看工作区状态和最近提交', aliases: ['review'] },
  { name: '/resume', help: '恢复历史会话', aliases: ['resume', 'switch'] },
  { name: '/rename', help: '重命名当前会话', aliases: ['rename'] },
  { name: '/sessions', help: '刷新会话列表', aliases: ['sessions'] },
  { name: '/new', help: '创建新会话', aliases: ['new'] },
  { name: '/clear', help: '清空当前 TUI transcript', aliases: ['clear'] },
  { name: '/exit', help: '退出 TUI', aliases: ['exit', 'quit', 'q'] },
  { name: '/help', help: '列出 TUI 与 lh CLI 命令', aliases: ['help'] },
  { name: '/jobs', help: '查看/停止 TUI 托管的后台任务', aliases: ['jobs'] },
  { name: '/lh', help: '执行短时 lh CLI 命令，例如 /lh config list', aliases: ['lh'] },
  { name: '/init', help: 'lh init 初始化 LuckyAgent 主目录', aliases: ['init'] },
  { name: '/chat', help: 'lh chat [message] 单次本地调试对话', aliases: ['chat'] },
  { name: '/config', help: 'lh config get/set/list 管理配置', aliases: ['config'] },
  { name: '/soul', help: 'lh soul show/list/switch 管理 SOUL', aliases: ['soul'] },
  { name: '/version', help: 'lh version 显示版本', aliases: ['version'] },
  { name: '/serve', help: 'lh serve 由 TUI 托管为后台任务', aliases: ['serve'] },
  { name: '/dashboard', help: 'lh dashboard status/start/stop 管理 Web Dashboard', aliases: ['dashboard'] },
  { name: '/msg-gateway', help: 'lh msg-gateway status/stop/start 管理消息网关', aliases: ['msg-gateway', 'gateway'] },
  { name: '/rag', help: 'lh rag index/search/stats 管理知识库', aliases: ['rag'] },
  { name: '/model', help: 'lh chat /model 查看或切换 CLI 子进程模型', aliases: ['model'] },
  { name: '/models', help: 'lh chat /models 列出可用模型', aliases: ['models'] },
  { name: '/tools', help: 'lh chat /tools 列出工具', aliases: ['tools'] },
  { name: '/skills', help: 'lh chat /skills <dir> 加载 Skill 插件', aliases: ['skills'] },
  { name: '/mcp', help: 'lh chat /mcp <name> <url> 连接 MCP Server', aliases: ['mcp'] },
  { name: '/approve', help: 'lh chat /approve <tool> 设置工具自动批准', aliases: ['approve'] },
  { name: '/deny', help: 'lh chat /deny <tool> 禁止工具', aliases: ['deny'] },
  { name: '/remember', help: 'lh chat /remember <content> 保存中期记忆', aliases: ['remember'] },
  { name: '/remember-long', help: 'lh chat /remember-long <content> 保存长期记忆', aliases: ['remember-long'] },
  { name: '/recall', help: 'lh chat /recall <query> 搜索记忆', aliases: ['recall'] },
  { name: '/memstats', help: 'lh chat /memstats 查看记忆统计', aliases: ['memstats'] },
  { name: '/memdecay', help: 'lh chat /memdecay 执行记忆衰减', aliases: ['memdecay'] },
  { name: '/promote', help: 'lh chat /promote <id> 提升记忆层级', aliases: ['promote'] },
  { name: '/cron', help: 'lh chat /cron add/list/remove/pause/resume/start/stop', aliases: ['cron'] },
  { name: '/watch', help: 'lh chat /watch add/list/remove/start/stop', aliases: ['watch'] },
  { name: '/profile', help: 'lh chat /profile list/switch', aliases: ['profile'] },
  { name: '/context', help: 'lh chat /context 查看上下文窗口状态', aliases: ['context'] },
  { name: '/fc', help: 'lh chat /fc tools/history/clear', aliases: ['fc'] },
  { name: '/embedder', help: 'lh chat /embedder list/switch/test', aliases: ['embedder'] },
];

const LH_TOP_LEVEL_ALIASES = new Set([
  'init',
  'chat',
  'config',
  'soul',
  'version',
  'serve',
  'dashboard',
  'msg-gateway',
  'gateway',
  'rag',
]);

const LH_CHAT_SLASH_ALIASES = new Set([
  'model',
  'models',
  'tools',
  'skills',
  'mcp',
  'approve',
  'deny',
  'remember',
  'remember-long',
  'recall',
  'memstats',
  'memdecay',
  'promote',
  'cron',
  'watch',
  'profile',
  'context',
  'fc',
  'embedder',
]);

const CLI_TIMEOUT_MS = 45_000;
const CLI_MAX_BUFFER = 1024 * 1024;
const PICKER_MENU_LIMIT = 8;
const SESSION_PAGE_LIMIT = 50;
const BACKGROUND_JOB_LOG_LINES = 200;
const BACKGROUND_JOB_STOP_GRACE_MS = 2_000;
const TRANSCRIPT_SCROLL_STEP_MIN = 5;
const TRANSCRIPT_SCROLL_STEP_MAX = 12;

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
    title: kind === 'user' ? 'You' : kind === 'assistant' ? 'LuckyAgent' : 'Tool',
    body,
    createdAt: safeDate(String(message.created_at || message.createdAt || message.timestamp || '')) || undefined,
  };
}

function kindColor(kind: StreamItemKind): string {
  switch (kind) {
    case 'user':
      return THEME.user;
    case 'assistant':
      return THEME.assistant;
    case 'reasoning':
      return THEME.reasoning;
    case 'tool_call':
    case 'tool_result':
      return THEME.tool;
    case 'error':
      return THEME.error;
    default:
      return THEME.meta;
  }
}

function kindMarker(kind: StreamItemKind): { glyph: string; color: string } {
  switch (kind) {
    case 'user':
      return { glyph: '›', color: THEME.accentBright };
    case 'assistant':
      return { glyph: '◆', color: THEME.accent };
    case 'reasoning':
      return { glyph: '·', color: THEME.reasoning };
    case 'tool_call':
      return { glyph: '▸', color: THEME.tool };
    case 'tool_result':
      return { glyph: '↳', color: THEME.tool };
    case 'status':
      return { glyph: '•', color: THEME.muted };
    case 'error':
      return { glyph: '✗', color: THEME.error };
    default:
      return { glyph: '•', color: THEME.meta };
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
  return [...lines.slice(0, maxLines), `... ${hidden} more lines folded`];
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

function isCommandOutput(item: StreamItem): boolean {
  if (item.kind !== 'meta') return false;
  const title = item.title.trim().toLowerCase();
  const firstLine = firstMeaningfulLine(item.body);
  return title === 'command' || title === 'review' || title === 'lh' || title.startsWith('lh ') || firstLine.startsWith('$ ');
}

function auxLineLimit(item: StreamItem): number {
  if (isCommandOutput(item)) return 18;
  switch (item.kind) {
    case 'status':
      return 0;
    case 'reasoning':
      return 2;
    case 'tool_call':
      return 6;
    case 'tool_result':
      return 8;
    case 'meta':
      return 6;
    case 'error':
      return 10;
    default:
      return 4;
  }
}

function normalizeCommand(raw: string): string {
  return raw.trim().replace(/\s+/g, ' ');
}

function parseCommandParts(raw: string): { command: string; arg: string } {
  const normalized = raw.trimStart();
  const firstSpace = normalized.search(/\s/u);
  if (firstSpace < 0) {
    return { command: normalizeCommand(normalized).slice(1).toLowerCase(), arg: '' };
  }
  return {
    command: normalizeCommand(normalized.slice(0, firstSpace)).slice(1).toLowerCase(),
    arg: normalized.slice(firstSpace + 1).trim(),
  };
}

function slashCommandDraft(raw: string): { active: boolean; completed: boolean; query: string } {
  const normalized = raw.trimStart();
  if (!normalized.startsWith('/')) return { active: false, completed: false, query: '' };
  const withoutSlash = normalized.slice(1);
  const firstSpace = withoutSlash.search(/\s/u);
  if (firstSpace < 0) return { active: true, completed: false, query: withoutSlash };
  return { active: true, completed: true, query: withoutSlash.slice(0, firstSpace) };
}

function parseArgs(raw: string): string[] {
  const args: string[] = [];
  let current = '';
  let quote: '"' | "'" | null = null;
  let escaping = false;

  for (const ch of raw.trim()) {
    if (escaping) {
      current += ch;
      escaping = false;
      continue;
    }
    if (ch === '\\') {
      escaping = true;
      continue;
    }
    if (quote) {
      if (ch === quote) quote = null;
      else current += ch;
      continue;
    }
    if (ch === '"' || ch === "'") {
      quote = ch;
      continue;
    }
    if (/\s/.test(ch)) {
      if (current) {
        args.push(current);
        current = '';
      }
      continue;
    }
    current += ch;
  }

  if (escaping) current += '\\';
  if (current) args.push(current);
  return args;
}

function maskCliArg(arg: string, previous?: string): string {
  const lower = arg.toLowerCase();
  const previousLower = previous?.toLowerCase() || '';
  if (previousLower.includes('key') || previousLower.includes('token') || previousLower.includes('secret')) return '***';
  if (/^(sk-|xox|ghp_|gho_|ghu_|ghs_|ya29\.|eyJ)/.test(arg)) return `${arg.slice(0, 4)}...`;
  if (/^(--?(?:api[-_]?key|token|secret|appsecret|qq-appsecret)=)/i.test(arg)) {
    const [name] = arg.split('=', 1);
    return `${name}=***`;
  }
  if (lower.includes('api_key=') || lower.includes('token=') || lower.includes('secret=')) {
    return arg.replace(/=(.*)$/u, '=***');
  }
  return arg;
}

function maskCliArgs(args: string[]): string {
  return args.map((arg, index) => maskCliArg(arg, args[index - 1])).join(' ');
}

function redactSecrets(text: string): string {
  return text
    .replace(/\bsk-[A-Za-z0-9_-]{8,}\b/g, 'sk-***')
    .replace(/\b(ghp|gho|ghu|ghs)_[A-Za-z0-9_]{8,}\b/g, '$1_***')
    .replace(/\b(token|api[_-]?key|app[_-]?secret|secret)(\s*[:=]\s*)([^\s]+)/gi, '$1$2***');
}

function limitOutput(text: string, maxChars = 12000): string {
  if (text.length <= maxChars) return text;
  const hidden = text.length - maxChars;
  return `${text.slice(0, maxChars)}\n... ${hidden} chars hidden`;
}

function repoRoot(): string {
  let current = process.cwd();
  for (let depth = 0; depth < 5; depth++) {
    if (existsSync(path.join(current, 'cmd', 'lh', 'main.go'))) return current;
    const parent = path.dirname(current);
    if (parent === current) break;
    current = parent;
  }
  return path.resolve(process.cwd(), '..');
}

function resolveLhCommand(): { command: string; args: string[]; display: string } {
  const localEntrypoint = path.join(repoRoot(), 'cmd', 'lh', 'main.go');
  if (existsSync(localEntrypoint)) {
    return { command: 'go', args: ['run', './cmd/lh'], display: 'go run ./cmd/lh' };
  }
  return { command: 'lh', args: [], display: 'lh' };
}

function shouldRunAsBackgroundJob(args: string[]): boolean {
  const [first, second] = args;
  if (first === 'serve') return true;
  if (first === 'dashboard' && second === 'start') return true;
  if (first === 'msg-gateway' && second === 'start') return true;
  return false;
}

function isBlockedInteractiveCommand(args: string[]): string | null {
  const [first, second] = args;
  if (first === 'chat' && args.length <= 1) return 'lh chat 无消息时会进入交互式 REPL。TUI 内请使用 /chat <message> 或普通聊天输入。';
  if (first === 'msg-gateway' && second === 'weixin-login') {
    return [
      'msg-gateway weixin-login 是扫码/轮询类交互流程，当前 TUI 尚未提供专用扫码面板。',
      '请暂时在外部终端运行：lh msg-gateway weixin-login',
      '后续应接入独立 PTY 或二维码交互视图。',
    ].join('\n');
  }
  return null;
}

function isRunningBackgroundJob(job: BackgroundJob): boolean {
  return job.status === 'running' || job.status === 'stopping';
}

function formatDuration(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  const mins = Math.floor(seconds / 60);
  const secs = seconds % 60;
  if (mins <= 0) return `${secs}s`;
  const hours = Math.floor(mins / 60);
  const remMins = mins % 60;
  if (hours <= 0) return `${mins}m${secs.toString().padStart(2, '0')}s`;
  return `${hours}h${remMins.toString().padStart(2, '0')}m`;
}

function backgroundJobServiceFromArgs(args: string[]): 'serve' | 'dashboard' | 'msg-gateway' | null {
  const [first, second] = args;
  if (first === 'serve') return 'serve';
  if (first === 'dashboard' && second === 'start') return 'dashboard';
  if (first === 'msg-gateway' && second === 'start') return 'msg-gateway';
  return null;
}

function backgroundJobService(job: BackgroundJob): 'serve' | 'dashboard' | 'msg-gateway' | null {
  return backgroundJobServiceFromArgs(job.args);
}

function lhServiceFromArgs(args: string[]): 'serve' | 'dashboard' | 'msg-gateway' | null {
  const [first] = args;
  if (first === 'serve') return 'serve';
  if (first === 'dashboard') return 'dashboard';
  if (first === 'msg-gateway') return 'msg-gateway';
  return null;
}

function serviceStartHint(service: 'serve' | 'dashboard' | 'msg-gateway'): string {
  switch (service) {
    case 'serve':
      return '/serve';
    case 'dashboard':
      return '/dashboard start';
    case 'msg-gateway':
      return '/msg-gateway start';
  }
}

function helpText(): string {
  return [
    'Type / to browse commands. Enter/Tab inserts; Enter again runs.',
    'Core: /resume /new /rename /sessions /review /clear /exit',
    'CLI: /lh /config /rag /dashboard /msg-gateway /version',
    'Jobs: /serve /dashboard start /msg-gateway start /jobs',
    'More: /model /tools /remember /cron /watch /context /fc /embedder',
  ].join('\n');
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

function ellipsize(text: string, width: number): string {
  const max = Math.max(0, width);
  if (text.length <= max) return text;
  if (max <= 1) return '…';
  return `${text.slice(0, max - 1)}…`;
}

function padRight(text: string, width: number): string {
  if (text.length >= width) return text;
  return `${text}${' '.repeat(width - text.length)}`;
}

function clamped(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function windowedSlice<T>(items: T[], selected: number, size: number): { items: T[]; start: number; end: number } {
  if (items.length === 0) return { items: [], start: 0, end: 0 };
  const safeSelected = clamped(selected, 0, items.length - 1);
  let start = safeSelected - Math.floor(size / 2);
  start = clamped(start, 0, Math.max(0, items.length - size));
  const end = Math.min(items.length, start + size);
  return { items: items.slice(start, end), start, end };
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
  const marker = kindMarker(item.kind);
  const contentWidth = Math.max(28, width - 4);

  if (item.kind === 'user') {
    const content = renderMarkdown(item.body, contentWidth);
    for (const [index, line] of content.entries()) {
      lines.push({
        id: `${item.id}-user-${index}`,
        text: line || ' ',
        color: THEME.user,
        gutter: index === 0 ? `${marker.glyph} ` : '  ',
        gutterColor: marker.color,
        bold: index === 0,
      });
    }
    lines.push({ id: `${item.id}-gap`, text: ' ', color: THEME.user });
    return lines;
  }

  if (item.kind === 'assistant') {
    const content = renderMarkdown(item.body, contentWidth);
    for (const [index, line] of content.entries()) {
      lines.push({
        id: `${item.id}-assistant-${index}`,
        text: line || ' ',
        color: THEME.assistant,
        gutter: index === 0 ? `${marker.glyph} ` : '  ',
        gutterColor: marker.color,
      });
    }
    lines.push({ id: `${item.id}-gap`, text: ' ', color: THEME.assistant });
    return lines;
  }

  const summary = firstMeaningfulLine(item.body);
  const headerText = summary ? `${eventKind(item)}: ${summary}` : eventKind(item);
  lines.push({
    id: `${item.id}-head`,
    text: divyTitle(headerText, Math.max(24, width - 2)),
    color,
    gutter: `${marker.glyph} `,
    gutterColor: marker.color,
    bold: item.kind === 'error',
  });
  const rendered = renderMarkdown(item.body, contentWidth);
  const limit = auxLineLimit(item);
  const bodyLines = limit <= 0 ? [] : clampLines(rendered, limit);
  const dim = item.kind === 'reasoning';
  for (const [index, line] of bodyLines.entries()) {
    lines.push({
      id: `${item.id}-body-${index}`,
      text: line || ' ',
      color,
      gutter: '  ',
      dim,
      italic: dim,
    });
  }
  if (item.kind === 'meta' || item.kind === 'error') {
    lines.push({ id: `${item.id}-gap`, text: ' ', color });
  }
  return lines;
}

function socketDot(state: 'idle' | 'connecting' | 'connected' | 'error'): { glyph: string; color: string; label: string } {
  switch (state) {
    case 'connected':
      return { glyph: '●', color: THEME.accent, label: 'connected' };
    case 'connecting':
      return { glyph: '◐', color: THEME.tool, label: 'connecting' };
    case 'error':
      return { glyph: '●', color: THEME.error, label: 'socket error' };
    default:
      return { glyph: '○', color: THEME.muted, label: 'disconnected' };
  }
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
  const [sessionsHasMore, setSessionsHasMore] = useState(false);
  const [sessionsTotal, setSessionsTotal] = useState(0);
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
  const [backgroundJobs, setBackgroundJobs] = useState<BackgroundJob[]>([]);
  const socketRef = useRef<WebSocket | null>(null);
  const draftRef = useRef('');
  const draftIdRef = useRef<string | null>(null);
  const pendingMessagesRef = useRef<string[]>([]);
  const activeSessionRef = useRef(activeSession);
  const sessionsRef = useRef<SessionInfo[]>([]);
  const backgroundJobsRef = useRef<BackgroundJob[]>([]);
  const jobSeqRef = useRef(0);
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
  const sessionWindow = useMemo(() => windowedSlice(filteredSessions, resumeSelection, PICKER_MENU_LIMIT), [filteredSessions, resumeSelection]);
  const commandWindow = useMemo(() => windowedSlice(filteredCommands, commandSelection, PICKER_MENU_LIMIT), [filteredCommands, commandSelection]);

  useEffect(() => {
    activeSessionRef.current = activeSession;
  }, [activeSession]);

  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);

  useEffect(() => {
    backgroundJobsRef.current = backgroundJobs;
  }, [backgroundJobs]);

  useEffect(() => {
    return () => {
      for (const job of backgroundJobsRef.current) {
        if (job.stopTimer) clearTimeout(job.stopTimer);
        if (isRunningBackgroundJob(job)) job.child.kill('SIGINT');
      }
    };
  }, []);

  useEffect(() => {
    setCommandSelection(0);
  }, [commandQuery]);

  useEffect(() => {
    setCommandSelection((prev) => clamped(prev, 0, Math.max(0, filteredCommands.length - 1)));
  }, [filteredCommands.length]);

  useEffect(() => {
    setResumeSelection((prev) => clamped(prev, 0, Math.max(0, filteredSessions.length - 1)));
  }, [filteredSessions.length]);

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

  function normalizeSessions(items: SessionInfo[]): SessionInfo[] {
    return normalizeSessionList(
      items
        .map((item) => ({
          id: String(item.id || '').trim(),
          title: String(item.title || '').trim(),
          message_count: Number(item.message_count || 0),
          created_at: String(item.created_at || ''),
          updated_at: String(item.updated_at || ''),
        }))
        .filter((item) => item.id),
    );
  }

  async function loadSessionsPage(offset: number, replace: boolean, query = sessionFilter.trim()) {
    const url = new URL(effectiveBase);
    url.pathname = '/api/v1/sessions';
    url.search = '';
    url.searchParams.set('limit', String(SESSION_PAGE_LIMIT));
    url.searchParams.set('offset', String(offset));
    if (query) {
      url.searchParams.set('q', query);
    }
    setSessionLoading(true);
    try {
      const resp = await fetch(url.toString());
      if (!resp.ok) throw new Error(`failed to load sessions: ${resp.status}`);
      const data = (await resp.json()) as SessionsResponse;
      const page = normalizeSessions(data.sessions || []);
      setSessions((prev) => {
        const merged = replace ? page : normalizeSessions([...prev, ...page]);
        const seen = new Set<string>();
        return merged.filter((item) => {
          if (seen.has(item.id)) return false;
          seen.add(item.id);
          return true;
        });
      });
      setSessionsTotal(Number(data.total || page.length));
      setSessionsHasMore(Boolean(data.has_more));
      return page;
    } catch {
      return [];
    } finally {
      setSessionLoading(false);
    }
  }

  async function refreshSessions() {
    return loadSessionsPage(0, true);
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
    const wasResumePicker = pickerMode === 'resume' || inputModeRef.current === 'resume';
    setPickerOpen(false);
    setPickerMode('none');
    setCommandQuery('');
    if (wasResumePicker) setSessionFilter('');
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

  function appendJobLog(id: string, chunk: string) {
    const lines = redactSecrets(chunk)
      .replace(/\r\n/g, '\n')
      .replace(/\r/g, '\n')
      .split('\n')
      .map((line) => line.trimEnd())
      .filter(Boolean);
    if (lines.length === 0) return;
    setBackgroundJobs((prev) =>
      prev.map((job) =>
        job.id === id ? { ...job, logs: [...job.logs, ...lines].slice(-BACKGROUND_JOB_LOG_LINES) } : job,
      ),
    );
  }

  function updateBackgroundJob(id: string, patch: Partial<BackgroundJob>) {
    setBackgroundJobs((prev) => prev.map((job) => (job.id === id ? { ...job, ...patch } : job)));
  }

  function startBackgroundJob(args: string[], title: string) {
    const lh = resolveLhCommand();
    const commandArgs = [...lh.args, ...args];
    const display = `${lh.display} ${maskCliArgs(args)}`;
    const id = `bg${jobSeqRef.current + 1}`;
    jobSeqRef.current += 1;

    const child = spawn(lh.command, commandArgs, { cwd: repoRoot(), stdio: ['ignore', 'pipe', 'pipe'] });
    const startedAt = Date.now();
    const job: BackgroundJob = {
      id,
      title,
      display,
      args,
      status: 'running',
      pid: child.pid,
      startedAt,
      logs: [],
      child,
    };

    setBackgroundJobs((prev) => [...prev, job]);
    pushExistingItem({
      id: makeId('job'),
      kind: 'meta',
      title,
      body: [
        `$ ${display}`,
        `job ${id} started`,
        `pid ${child.pid || 'pending'}`,
        'status running',
        `Use /jobs logs ${id} to inspect output.`,
        `Use /jobs stop ${id} to stop it.`,
      ].join('\n'),
      createdAt: startedAt,
    });
    setStatus(`Started ${id}: ${display}`);

    child.stdout?.on('data', (data: Buffer) => appendJobLog(id, data.toString('utf8')));
    child.stderr?.on('data', (data: Buffer) => appendJobLog(id, data.toString('utf8')));
    child.on('error', (error) => {
      updateBackgroundJob(id, { status: 'failed', endedAt: Date.now(), error: error.message });
      appendJobLog(id, `error: ${error.message}`);
      pushItem('error', title, [`$ ${display}`, `job ${id} failed`, error.message].join('\n'));
      setStatus(`Failed ${id}`);
    });
    child.on('exit', (code, signal) => {
      const endedAt = Date.now();
      const latest = backgroundJobsRef.current.find((item) => item.id === id);
      if (latest?.stopTimer) clearTimeout(latest.stopTimer);
      const failed = typeof code === 'number' && code !== 0 && signal === null;
      updateBackgroundJob(id, {
        status: failed ? 'failed' : 'exited',
        endedAt,
        exitCode: code,
        signal,
        stopTimer: undefined,
      });
      const result = signal ? `signal ${signal}` : `exit ${code ?? 0}`;
      appendJobLog(id, `process ended: ${result}`);
      pushItem(failed ? 'error' : 'meta', title, [`$ ${display}`, `job ${id} ended`, result, `runtime ${formatDuration(endedAt - startedAt)}`].join('\n'));
      setStatus(`Ended ${id}: ${result}`);
    });
  }

  function findBackgroundJob(id: string): BackgroundJob | undefined {
    const normalized = id.trim().toLowerCase();
    return backgroundJobsRef.current.find((job) => job.id.toLowerCase() === normalized);
  }

  function findRunningServiceJob(service: 'serve' | 'dashboard' | 'msg-gateway'): BackgroundJob | undefined {
    return backgroundJobsRef.current.find((job) => isRunningBackgroundJob(job) && backgroundJobService(job) === service);
  }

  function backgroundJobLine(job: BackgroundJob): string {
    const runtime = formatDuration((job.endedAt || Date.now()) - job.startedAt);
    const pid = job.pid ? `pid ${job.pid}` : 'pid pending';
    const exit =
      job.status === 'exited' || job.status === 'failed'
        ? job.signal
          ? `signal ${job.signal}`
          : `exit ${job.exitCode ?? 0}`
        : runtime;
    return `${job.id}  ${job.status.padEnd(8)}  ${pid}  ${exit}  ${job.display}`;
  }

  function listBackgroundJobs() {
    const jobs = backgroundJobsRef.current;
    if (jobs.length === 0) {
      pushItem('meta', 'Jobs', 'No TUI-managed background jobs.\nStart one with /serve, /dashboard start, or /msg-gateway start.');
      setStatus('No background jobs');
      return;
    }
    const running = jobs.filter(isRunningBackgroundJob).length;
    const lines = [`jobs ${jobs.length} · running ${running}`];
    for (const job of jobs) {
      lines.push(backgroundJobLine(job));
      const last = job.logs.at(-1);
      if (last) lines.push(`  last: ${ellipsize(last, 140)}`);
    }
    pushItem('meta', 'Jobs', lines.join('\n'));
    setStatus(`Jobs ${jobs.length}, running ${running}`);
  }

  function showBackgroundJobLogs(id: string) {
    const job = findBackgroundJob(id);
    if (!job) {
      pushItem('error', 'Jobs', `Job not found: ${id}`);
      setStatus(`Job not found: ${id}`);
      return;
    }
    const lines = [
      backgroundJobLine(job),
      '',
      ...(job.logs.length > 0 ? job.logs.slice(-80) : ['(no logs yet)']),
    ];
    pushItem('meta', `Jobs ${job.id}`, lines.join('\n'));
    setStatus(`Logs ${job.id}`);
  }

  function showServiceJobStatus(service: 'serve' | 'dashboard' | 'msg-gateway') {
    const jobs = backgroundJobsRef.current.filter((job) => backgroundJobService(job) === service);
    if (jobs.length === 0) {
      pushItem('meta', 'Jobs', `No TUI-managed ${service} job.\nStart one with ${serviceStartHint(service)}.`);
      setStatus(`No ${service} job`);
      return;
    }
    pushItem('meta', 'Jobs', jobs.map(backgroundJobLine).join('\n'));
    setStatus(`${service} jobs ${jobs.length}`);
  }

  function stopBackgroundJob(id: string) {
    const job = findBackgroundJob(id);
    if (!job) {
      pushItem('error', 'Jobs', `Job not found: ${id}`);
      setStatus(`Job not found: ${id}`);
      return;
    }
    if (!isRunningBackgroundJob(job)) {
      pushItem('meta', 'Jobs', `${job.id} is already ${job.status}.\n${backgroundJobLine(job)}`);
      setStatus(`${job.id} already ${job.status}`);
      return;
    }
    if (job.stopTimer) clearTimeout(job.stopTimer);
    updateBackgroundJob(job.id, { status: 'stopping' });
    appendJobLog(job.id, 'stop requested: SIGINT');
    const signaled = job.child.kill('SIGINT');
    const stopTimer = setTimeout(() => {
      const latest = findBackgroundJob(job.id);
      if (!latest || !isRunningBackgroundJob(latest)) return;
      appendJobLog(job.id, 'still running after grace period: SIGTERM');
      latest.child.kill('SIGTERM');
    }, BACKGROUND_JOB_STOP_GRACE_MS);
    updateBackgroundJob(job.id, { status: 'stopping', stopTimer });
    pushItem('meta', 'Jobs', `${job.id} stopping${signaled ? '' : ' (process may already be gone)'}.\n${backgroundJobLine({ ...job, status: 'stopping' })}`);
    setStatus(`Stopping ${job.id}`);
  }

  function stopAllBackgroundJobs() {
    const running = backgroundJobsRef.current.filter(isRunningBackgroundJob);
    if (running.length === 0) {
      pushItem('meta', 'Jobs', 'No running TUI-managed background jobs.');
      setStatus('No running jobs');
      return;
    }
    for (const job of running) {
      if (job.stopTimer) clearTimeout(job.stopTimer);
      updateBackgroundJob(job.id, { status: 'stopping' });
      appendJobLog(job.id, 'stop requested: SIGINT');
      job.child.kill('SIGINT');
      const stopTimer = setTimeout(() => {
        const latest = findBackgroundJob(job.id);
        if (!latest || !isRunningBackgroundJob(latest)) return;
        appendJobLog(job.id, 'still running after grace period: SIGTERM');
        latest.child.kill('SIGTERM');
      }, BACKGROUND_JOB_STOP_GRACE_MS);
      updateBackgroundJob(job.id, { status: 'stopping', stopTimer });
    }
    pushItem('meta', 'Jobs', `Stopping ${running.length} TUI-managed background job(s): ${running.map((job) => job.id).join(', ')}`);
    setStatus(`Stopping ${running.length} jobs`);
  }

  function runServiceJobControl(args: string[], title: string): boolean {
    const service = lhServiceFromArgs(args);
    const [first, second] = args;
    const action = second?.toLowerCase() || '';
    if (!service) return false;
    if (action === 'stop') {
      const job = findRunningServiceJob(service);
      if (!job) {
        pushItem('meta', title, `No running TUI-managed ${service} job.\nExternal shell processes cannot be stopped from this TUI instance.`);
        setStatus(`No running ${service} job`);
        return true;
      }
      stopBackgroundJob(job.id);
      return true;
    }
    if (action === 'logs') {
      const job = findRunningServiceJob(service);
      if (!job) {
        pushItem('meta', title, `No running TUI-managed ${service} job.\nUse /jobs to inspect previous jobs.`);
        setStatus(`No running ${service} job`);
        return true;
      }
      showBackgroundJobLogs(job.id);
      return true;
    }
    if (action === 'status' && (first === 'serve' || first === 'msg-gateway' || findRunningServiceJob(service))) {
      showServiceJobStatus(service);
      return true;
    }
    return false;
  }

  function runJobsCommand(arg: string) {
    const args = parseArgs(arg);
    const subcommand = (args[0] || 'list').toLowerCase();
    if (subcommand === 'list') {
      listBackgroundJobs();
      return;
    }
    if (subcommand === 'logs') {
      if (!args[1]) {
        pushItem('error', 'Jobs', 'Usage: /jobs logs <id>');
        setStatus('Jobs logs needs an id');
        return;
      }
      showBackgroundJobLogs(args[1]);
      return;
    }
    if (subcommand === 'stop') {
      if (!args[1]) {
        pushItem('error', 'Jobs', 'Usage: /jobs stop <id|all>');
        setStatus('Jobs stop needs an id');
        return;
      }
      if (args[1].toLowerCase() === 'all') stopAllBackgroundJobs();
      else stopBackgroundJob(args[1]);
      return;
    }
    pushItem('error', 'Jobs', ['Unknown jobs command: ' + subcommand, 'Usage:', '/jobs list', '/jobs logs <id>', '/jobs stop <id|all>'].join('\n'));
    setStatus(`Unknown jobs command: ${subcommand}`);
  }

  async function runLhCommand(args: string[], title = 'lh') {
    const normalizedArgs = args.map((arg) => arg.trim()).filter(Boolean);
    if (normalizedArgs.length === 0) {
      pushItem('meta', 'lh help', helpText());
      setStatus('lh help');
      return;
    }

    const blocked = isBlockedInteractiveCommand(normalizedArgs);
    if (blocked) {
      pushItem('meta', title, blocked);
      setStatus('Interactive command blocked');
      return;
    }

    if (runServiceJobControl(normalizedArgs, title)) {
      return;
    }

    if (shouldRunAsBackgroundJob(normalizedArgs)) {
      const targetService = backgroundJobServiceFromArgs(normalizedArgs);
      const duplicate = backgroundJobsRef.current.find(
        (job) => isRunningBackgroundJob(job) && backgroundJobService(job) === targetService,
      );
      if (duplicate) {
        const duplicateService = backgroundJobService(duplicate);
        pushItem(
          'meta',
          title,
          [
            `A ${duplicateService} job is already running: ${duplicate.id}`,
            backgroundJobLine(duplicate),
            `Use /jobs logs ${duplicate.id} to inspect it.`,
            `Use /jobs stop ${duplicate.id} before starting another ${serviceStartHint(duplicateService || 'serve')}.`,
          ].join('\n'),
        );
        setStatus(`Already running ${duplicate.id}`);
        return;
      }
      startBackgroundJob(normalizedArgs, title);
      return;
    }

    const lh = resolveLhCommand();
    const commandArgs = [...lh.args, ...normalizedArgs];
    const display = `${lh.display} ${maskCliArgs(normalizedArgs)}`;
    const itemId = makeId('cli');
    setStatus(`Running ${display}`);
    pushExistingItem({ id: itemId, kind: 'meta', title, body: `$ ${display}\nRunning...`, createdAt: Date.now() });

    execFile(lh.command, commandArgs, { cwd: repoRoot(), timeout: CLI_TIMEOUT_MS, maxBuffer: CLI_MAX_BUFFER }, (error, stdout, stderr) => {
      const output = limitOutput(redactSecrets([stdout, stderr].filter(Boolean).join('\n').trim() || '(no output)'));
      const body = [`$ ${display}`, output].join('\n');
      if (error) {
        setItems((prev) =>
          prev.map((item) =>
            item.id === itemId ? { ...item, kind: 'error', title, body: sanitize(`${body}\n\nexit: ${(error as NodeJS.ErrnoException).message}`) } : item,
          ),
        );
        transcriptVersionRef.current += 1;
        setStatus(`Failed ${display}`);
        return;
      }
      updateItem(itemId, body);
      setStatus(`Done ${display}`);
    });
  }

  function runLhAlias(command: string, arg: string) {
    const mappedCommand = command === 'gateway' ? 'msg-gateway' : command;
    void runLhCommand([mappedCommand, ...parseArgs(arg)], `lh ${mappedCommand}`);
  }

  function runLhChatSlash(command: string, arg: string) {
    const slashArg = arg ? `/${command} ${arg}` : `/${command}`;
    void runLhCommand(['chat', slashArg], `lh chat /${command}`);
  }

  function confirmCommandSelection() {
    const selected = filteredCommands[commandSelection];
    if (!selected) return;
    const nextInput = `${selected.name} `;
    closePicker();
    setInput(nextInput);
    setStatus(`Inserted ${selected.name}`);
  }

  async function refreshAndMaybeSearch(query: string) {
    setSessionFilter(query);
    await loadSessionsPage(0, true, query.trim());
  }

  function updateCommandPickerFromInput(next: string) {
    const draftCommand = slashCommandDraft(next);
    if (draftCommand.active && !draftCommand.completed) {
      setCommandQuery(draftCommand.query);
      if (pickerMode !== 'commands') openPicker('commands');
      return;
    }
    if (pickerMode === 'commands') closePicker();
  }

  function handleInputChange(next: string) {
    setInput(next);
    if (pickerMode !== 'resume') updateCommandPickerFromInput(next);
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

  function pushExistingItem(item: StreamItem) {
    transcriptVersionRef.current += 1;
    setItems((prev) => [...prev, { ...item, body: sanitize(item.body), createdAt: item.createdAt || Date.now() }].slice(-220));
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
        title: 'LuckyAgent',
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
        else if (finalText) pushItem('assistant', 'LuckyAgent', finalText);
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
        setSessionFilter('');
        if (sessions.length === 0 && !sessionLoading) {
          void refreshSessions();
        }
        openPicker('resume');
        return;
      case 'sessions':
        void refreshSessions();
        pushItem('meta', 'Command', 'Session list refreshed');
        return;
      case 'clear':
        setItems([]);
        transcriptVersionRef.current += 1;
        setStatus('Transcript cleared');
        return;
      case 'quit':
      case 'exit':
      case 'q':
        exit();
        return;
      case 'new':
        void createSession();
        return;
      case 'help':
        pushItem('meta', 'Command', helpText());
        return;
      case 'jobs':
        runJobsCommand(arg);
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
      case 'lh':
        void runLhCommand(parseArgs(arg));
        return;
      default:
        if (LH_TOP_LEVEL_ALIASES.has(command)) {
          runLhAlias(command, arg);
          return;
        }
        if (LH_CHAT_SLASH_ALIASES.has(command)) {
          runLhChatSlash(command, arg);
          return;
        }
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

  function handleSubmit() {
    if (pickerOpen && pickerMode === 'commands') {
      if (filteredCommands.length > 0) {
        confirmCommandSelection();
        return;
      }
      closePicker();
      send();
      return;
    }
    if (pickerOpen && pickerMode === 'resume') {
      commitResumeSelection();
      return;
    }
    send();
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

  function maybeLoadMoreSessions(nextSelection: number) {
    if (!sessionsHasMore || sessionLoading) return;
    if (nextSelection < Math.max(0, filteredSessions.length - 3)) return;
    void loadSessionsPage(sessions.length, false);
  }

  function moveScroll(nextOffset: number, followBottom: boolean) {
    setAutoFollowBottom(followBottom);
    setScrollOffset(clamped(nextOffset, 0, Math.max(0, transcriptLines.length - transcriptHeight)));
  }

  useInput((inputChar, key) => {
    if (key.ctrl && inputChar === 'c') {
      exit();
      return;
    }
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
          setResumeSelection((prev) => {
            const next = Math.min(Math.max(0, filteredSessions.length - 1), prev + 1);
            maybeLoadMoreSessions(next);
            return next;
          });
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
        if (key.return || key.tab) {
          confirmCommandSelection();
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
        moveScroll(normalizedScrollOffset - transcriptScrollStep, false);
        return;
      }
      if (key.downArrow && input.length === 0) {
        const next = normalizedScrollOffset + transcriptScrollStep;
        const bottom = Math.max(0, transcriptLines.length - transcriptHeight);
        moveScroll(next, next >= bottom);
        return;
      }
      if (key.escape) {
        setInput('');
        setStatus('Input cleared');
        return;
      }
      if (key.tab) {
        const draftCommand = slashCommandDraft(input);
        if (!draftCommand.active || draftCommand.completed) return;
        updateCommandPickerFromInput(input);
        return;
      }
    }
  });

  const dot = socketDot(socketState);
  const pickerBodyRows =
    pickerMode === 'resume'
      ? Math.max(1, sessionWindow.items.length) + (filteredSessions.length > sessionWindow.items.length ? 1 : 0)
      : pickerMode === 'commands'
        ? Math.max(1, commandWindow.items.length) + (filteredCommands.length > commandWindow.items.length ? 1 : 0)
        : 0;
  const pickerHeight = pickerOpen ? Math.min(PICKER_MENU_LIMIT + 5, pickerBodyRows + 4) : 0;
  // Hero (bordered) + input chrome are fixed; the transcript absorbs the slack. Keep the whole frame
  // strictly shorter than the viewport (safety row) so the alt-screen never scrolls and drags the hero.
  const heroBodyRows = (viewportWidth >= 100 ? WORDMARK_ART.length : 1) + 3;
  const heroRows = heroBodyRows + 2; // round border top + bottom
  const chromeRows = heroRows + 6; // transcript margin + input margin + input box (3) + footer
  const transcriptHeight = Math.max(6, viewportHeight - chromeRows - 1 - pickerHeight - (pickerOpen ? 1 : 0));
  const transcriptLines = useMemo(
    () => items.flatMap((item) => renderItemLines(item, viewportWidth)),
    [items, viewportWidth],
  );
  const maxScrollOffset = Math.max(0, transcriptLines.length - transcriptHeight);
  const normalizedScrollOffset = autoFollowBottom ? maxScrollOffset : Math.min(scrollOffset, maxScrollOffset);
  const transcriptScrollStep = clamped(Math.floor(transcriptHeight / 3), TRANSCRIPT_SCROLL_STEP_MIN, TRANSCRIPT_SCROLL_STEP_MAX);
  const visibleTranscriptLines = transcriptLines.slice(normalizedScrollOffset, normalizedScrollOffset + transcriptHeight);

  useEffect(() => {
    const nextTotal = transcriptLines.length;
    const prevTotal = totalTranscriptLinesRef.current;
    totalTranscriptLinesRef.current = nextTotal;
    if (autoFollowBottom) {
      setScrollOffset(items.length === 0 ? 0 : Math.max(0, nextTotal - transcriptHeight));
      return;
    }
    if (nextTotal < prevTotal) {
      setScrollOffset((prev) => Math.min(prev, Math.max(0, nextTotal - transcriptHeight)));
    }
  }, [autoFollowBottom, items.length, transcriptHeight, transcriptLines.length]);

  return (
    <Box flexDirection="column" paddingX={1} paddingY={0}>
      <Box borderStyle="round" borderColor={THEME.accentDim} paddingX={1} justifyContent="space-between">
        <Box flexDirection="column">
          {viewportWidth >= 100 ? (
            WORDMARK_ART.map((line, index) => (
              <Text key={`wm-${index}`} color={WORDMARK_COLORS[index]} wrap="truncate-end">
                {line}
              </Text>
            ))
          ) : (
            <Text color={THEME.title} bold wrap="truncate-end">L U C K Y   H A R N E S S</Text>
          )}
          <Text color={THEME.muted} wrap="truncate-end">agent runtime · memory · tools · gateways</Text>
          <Text color={THEME.accent} wrap="truncate-end">{currentModelLabel}</Text>
          <Text wrap="truncate-end">
            <Text color={dot.color}>{dot.glyph} </Text>
            <Text color={THEME.muted}>{dot.label}</Text>
          </Text>
        </Box>
        <Box flexDirection="column" alignItems="flex-end">
          <Text color={THEME.muted} wrap="truncate-end">{ellipsize(currentSessionLabel, Math.max(12, Math.floor(viewportWidth / 3)))}</Text>
          <Text color={autoFollowBottom ? THEME.accent : THEME.muted}>
            {autoFollowBottom ? '● live' : `${normalizedScrollOffset + 1}-${Math.min(transcriptLines.length, normalizedScrollOffset + transcriptHeight)} / ${transcriptLines.length}`}
          </Text>
        </Box>
      </Box>

      <Box flexDirection="column" flexGrow={1} marginTop={1}>
        <Box flexDirection="column" minHeight={transcriptHeight}>
          {visibleTranscriptLines.map((line) => (
            <Text key={line.id}>
              {line.gutter ? (
                <Text color={line.gutterColor ?? line.color} bold>
                  {line.gutter}
                </Text>
              ) : null}
              <Text color={line.color} dimColor={line.dim} bold={line.bold} italic={line.italic}>
                {line.text || (line.gutter ? '' : ' ')}
              </Text>
            </Text>
          ))}
          {items.length === 0 && normalizedScrollOffset === 0 ? (
            <>
              <Text>
                <Text color={THEME.accent} bold>◆ </Text>
                <Text color={THEME.assistant}>Ready. Ask a question or run a slash command.</Text>
              </Text>
              <Text color={THEME.muted}>  Type / for commands · /resume to switch sessions · /review for repo context.</Text>
            </>
          ) : null}
          {visibleTranscriptLines.length < transcriptHeight
            ? Array.from({ length: Math.max(0, transcriptHeight - visibleTranscriptLines.length - (items.length === 0 && normalizedScrollOffset === 0 ? 2 : 0)) }, (_, index) => (
                <Text key={`pad-${index}`}>{' '}</Text>
              ))
            : null}
        </Box>
      </Box>

      {pickerOpen ? (
        <Box borderStyle="round" borderColor={THEME.accentDim} flexDirection="column" paddingX={1} paddingY={0} marginTop={1}>
          <Box justifyContent="space-between">
            <Text color={THEME.title} bold>{pickerMode === 'resume' ? 'Resume session' : 'Command palette'}</Text>
            <Text color={THEME.muted}>{pickerMode === 'resume' ? '↑↓ browse · ⏎ resume · esc close' : '↑↓ browse · ⏎/⇥ insert · esc close'}</Text>
          </Box>
          <Text color={THEME.muted}>
            {pickerMode === 'resume'
              ? sessionFilter
                ? `filter ${sessionFilter}`
                : 'filter sessions'
              : commandQuery
                ? `filter /${commandQuery}`
                : 'type /command to filter'}
          </Text>
          <Box flexDirection="column" marginTop={1}>
            {pickerMode === 'resume' ? (
              sessionWindow.items.length === 0 ? (
                <Text color={THEME.muted}>No sessions available</Text>
              ) : (
                sessionWindow.items.map((item, index) => {
                  const absoluteIndex = sessionWindow.start + index;
                  const selected = absoluteIndex === resumeSelection;
                  const title = ellipsize(item.title || item.id, Math.max(16, Math.floor(viewportWidth * 0.35)));
                  const row = `${title}  ${item.id}`;
                  return (
                    <Text key={item.id} color={selected ? THEME.accentBright : THEME.meta} bold={selected}>
                      {selected ? '›' : ' '} {ellipsize(row, Math.max(24, viewportWidth - 8))}
                    </Text>
                  );
                })
              )
            ) : commandWindow.items.length === 0 ? (
              <Text color={THEME.muted}>No matching commands</Text>
            ) : (
              commandWindow.items.map((item, index) => {
                const absoluteIndex = commandWindow.start + index;
                const selected = absoluteIndex === commandSelection;
                const nameColumn = padRight(item.name, 16);
                const row = `${nameColumn} ${item.help}`;
                return (
                  <Text key={item.name} color={selected ? THEME.accentBright : THEME.meta} bold={selected}>
                    {selected ? '›' : ' '} {ellipsize(row, Math.max(24, viewportWidth - 8))}
                  </Text>
                );
              })
            )}
            {pickerMode === 'resume' && filteredSessions.length > 0 ? (
              <Text color={THEME.muted}>
                {`  showing ${sessionWindow.start + 1}-${sessionWindow.end} / ${filteredSessions.length}${sessionsTotal ? ` · loaded ${sessions.length}/${sessionsTotal}` : ''}${sessionLoading ? ' · loading...' : sessionsHasMore ? ' · more on Down' : ''}`}
              </Text>
            ) : null}
            {pickerMode === 'commands' && filteredCommands.length > 0 ? (
              <Text color={THEME.muted}>{`  showing ${commandWindow.start + 1}-${commandWindow.end} / ${filteredCommands.length}`}</Text>
            ) : null}
          </Box>
        </Box>
      ) : null}

      <Box flexDirection="column" marginTop={1}>
        <Box borderStyle="round" borderColor={pickerMode === 'resume' ? THEME.border : THEME.accent} paddingX={1}>
          <Text color={THEME.accent} bold>› </Text>
          <TextInput value={input} focus={pickerMode !== 'resume'} onChange={handleInputChange} onSubmit={handleSubmit} placeholder="message or /command" />
        </Box>
        <Box justifyContent="space-between" paddingX={1}>
          <Text color={THEME.muted} wrap="truncate-end">{ellipsize(status, Math.max(16, Math.floor(viewportWidth * 0.5)))}</Text>
          <Text color={THEME.muted} wrap="truncate-end">⏎ send · / cmds · ⇅ scroll · ^C quit</Text>
        </Box>
      </Box>
    </Box>
  );
}
