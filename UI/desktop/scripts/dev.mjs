#!/usr/bin/env node
import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import http from 'node:http';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const desktopRoot = path.resolve(__dirname, '..');
const uiRoot = path.resolve(desktopRoot, '..');

const DEV_URL = process.env.LH_ELECTRON_DEV_URL || 'http://127.0.0.1:5173';
const WAIT_MS = Number(process.env.LH_ELECTRON_WAIT_MS || 60000);

function waitForUrl(url, timeoutMs) {
  const started = Date.now();
  return new Promise((resolve, reject) => {
    const tick = () => {
      const req = http.get(url, (res) => {
        res.resume();
        resolve(true);
      });
      req.on('error', () => {
        if (Date.now() - started > timeoutMs) {
          reject(new Error(`timed out waiting for ${url}`));
          return;
        }
        setTimeout(tick, 400);
      });
    };
    tick();
  });
}

function spawnInherit(command, args, opts = {}) {
  return spawn(command, args, {
    stdio: 'inherit',
    env: { ...process.env, ...(opts.env || {}) },
    cwd: opts.cwd,
    shell: process.platform === 'win32',
  });
}

const children = [];

function shutdown(code = 0) {
  for (const child of children) {
    if (!child.killed) {
      try {
        child.kill('SIGTERM');
      } catch {
        /* ignore */
      }
    }
  }
  process.exit(code);
}

process.on('SIGINT', () => shutdown(0));
process.on('SIGTERM', () => shutdown(0));

console.log('[desktop] starting GUI vite dev server...');
const vite = spawnInherit(
  process.platform === 'win32' ? 'npm.cmd' : 'npm',
  ['run', 'dev', '--workspace', 'GUI'],
  { cwd: uiRoot },
);
children.push(vite);

try {
  await waitForUrl(DEV_URL, WAIT_MS);
} catch (err) {
  console.error(`[desktop] ${err.message}`);
  shutdown(1);
}

console.log(`[desktop] GUI ready at ${DEV_URL}, launching Electron...`);

const electronCli = path.join(desktopRoot, 'node_modules', 'electron', 'cli.js');
const electron = spawnInherit(process.execPath, [electronCli, '.'], {
  cwd: desktopRoot,
  env: {
    LH_ELECTRON_LOAD: 'dev',
    LH_ELECTRON_DEV_URL: DEV_URL,
  },
});
children.push(electron);

electron.on('exit', (code) => shutdown(code ?? 0));
vite.on('exit', (code) => {
  if (code && code !== 0) shutdown(code);
});
