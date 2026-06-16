import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import 'katex/dist/katex.min.css';
import './styles.css';

// Resolve the initial theme before first paint to avoid a flash of the wrong palette.
function resolveInitialTheme(): 'light' | 'dark' {
  try {
    const stored = localStorage.getItem('lh-gui-theme');
    if (stored === 'light' || stored === 'dark') return stored;
  } catch {
    /* localStorage unavailable */
  }
  if (window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark';
  return 'light';
}

document.documentElement.dataset.theme = resolveInitialTheme();

const root = ReactDOM.createRoot(document.getElementById('root') as HTMLElement);
root.render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
