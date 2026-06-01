import { createElement } from 'react';
import { render } from 'ink';
import { App } from './tui-app';

function parseArg(name: string, fallback: string): string {
  const idx = process.argv.indexOf(name);
  if (idx >= 0 && process.argv[idx + 1]) {
    return process.argv[idx + 1];
  }
  return fallback;
}

const apiBase = parseArg('--api-base', 'http://127.0.0.1:9090');
const session = parseArg('--session', 'dashboard-main');
const model = parseArg('--model', '');

if (!process.stdin.isTTY) {
  console.error('Ink raw mode is not supported in this terminal. Run TUI from an interactive PowerShell / terminal window.');
  process.exit(1);
}

render(createElement(App, { apiBase, session, model }));
