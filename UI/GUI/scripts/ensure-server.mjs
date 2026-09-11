#!/usr/bin/env node
// Ensures `la serve` is running before the GUI dev server starts. If the
// API is already up, this is a no-op. Otherwise it spawns a detached
// `la serve` process and waits briefly for it to become healthy, but
// never blocks `vite`/`vite preview` from starting regardless of outcome.
import { spawn } from 'node:child_process';
import { openSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';

const API_BASE = 'http://127.0.0.1:9090';
const HEALTH_URL = `${API_BASE}/api/v1/health/live`;

async function isHealthy() {
  try {
    const res = await fetch(HEALTH_URL, { signal: AbortSignal.timeout(1500) });
    return res.ok;
  } catch {
    return false;
  }
}

function resolveLaBinary() {
  const candidate = join(homedir(), 'go', 'bin', 'la');
  return candidate;
}

async function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function main() {
  if (await isHealthy()) {
    return;
  }

  const logDir = join(homedir(), '.luckyagent', 'logs');
  await mkdir(logDir, { recursive: true });
  const logPath = join(logDir, 'serve-autostart.log');
  const logFd = openSync(logPath, 'a');

  const laBin = resolveLaBinary();
  const child = spawn(laBin, ['serve', '--addr', '0.0.0.0:9090'], {
    detached: true,
    stdio: ['ignore', logFd, logFd],
  });
  child.on('error', () => {
    // Fall back silently; vite will still start and surface connection
    // errors to the user if the API never comes up.
  });
  child.unref();

  console.log(`检测到 API 服务未运行，已自动启动 la serve (pid=${child.pid})，等待就绪...`);

  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (await isHealthy()) {
      console.log('la serve 已就绪。');
      return;
    }
    await sleep(300);
  }
  console.warn(`la serve 在 15s 内未就绪，请检查 ${logPath}`);
}

await main();
