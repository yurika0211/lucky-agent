'use strict';

const { app, BrowserWindow, shell, Menu, ipcMain } = require('electron');
const path = require('node:path');
const fs = require('node:fs');

/** @type {BrowserWindow | null} */
let mainWindow = null;

const DEFAULT_DEV_URL = process.env.LH_ELECTRON_DEV_URL || 'http://127.0.0.1:5173';
const DEFAULT_API_BASE = process.env.LH_API_BASE || 'http://127.0.0.1:9090';
const WINDOW_RADIUS = Number(process.env.LH_ELECTRON_RADIUS || 16);

function guiDistIndex() {
  return path.resolve(__dirname, '..', 'GUI', 'dist', 'index.html');
}

function shouldLoadDist() {
  if (process.env.LH_ELECTRON_LOAD === 'dist') return true;
  if (process.env.LH_ELECTRON_LOAD === 'dev') return false;
  return fs.existsSync(guiDistIndex());
}

function injectRoundedChrome() {
  if (!mainWindow || mainWindow.isDestroyed()) return;
  const radius = WINDOW_RADIUS;
  const css = `
    /* Electron shell: soft rounded outer frame */
    html, body {
      background: transparent !important;
      border-radius: ${radius}px !important;
      overflow: hidden !important;
    }
    body {
      box-shadow: 0 18px 48px rgba(0, 0, 0, 0.28);
    }
    /* Keep app content clipped to the rounded shell */
    #root, .app-shell, .app-root, #app {
      border-radius: ${radius}px !important;
      overflow: hidden !important;
    }
    /* Floating custom titlebar (frameless window) */
    #lh-electron-titlebar {
      position: fixed;
      top: 0;
      left: 0;
      right: 0;
      height: 36px;
      z-index: 2147483646;
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 10px 0 14px;
      pointer-events: none;
      background: linear-gradient(
        to bottom,
        rgba(15, 18, 12, 0.55),
        rgba(15, 18, 12, 0.18) 70%,
        rgba(15, 18, 12, 0)
      );
      -webkit-app-region: drag;
      app-region: drag;
      user-select: none;
      color: rgba(255, 255, 255, 0.86);
      font: 600 12px/1 ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      letter-spacing: 0.02em;
    }
    #lh-electron-titlebar .lh-title {
      pointer-events: none;
      opacity: 0.9;
    }
    #lh-electron-titlebar .lh-window-controls {
      display: flex;
      gap: 6px;
      pointer-events: auto;
      -webkit-app-region: no-drag;
      app-region: no-drag;
    }
    #lh-electron-titlebar button {
      width: 12px;
      height: 12px;
      border: 0;
      border-radius: 999px;
      padding: 0;
      cursor: pointer;
      opacity: 0.92;
    }
    #lh-electron-titlebar button:hover { filter: brightness(1.08); }
    #lh-electron-titlebar .lh-btn-close { background: #ff5f57; }
    #lh-electron-titlebar .lh-btn-min { background: #febc2e; }
    #lh-electron-titlebar .lh-btn-max { background: #28c840; }
    /* Give the top of the GUI a little room so content isn't under traffic lights */
    body.lh-electron {
      padding-top: 0 !important;
    }
    body.lh-electron #root {
      min-height: 100vh;
    }
  `;

  const js = `
    (() => {
      try {
        document.documentElement.classList.add('lh-electron');
        document.body.classList.add('lh-electron');
        if (document.getElementById('lh-electron-titlebar')) return;
        const bar = document.createElement('div');
        bar.id = 'lh-electron-titlebar';
        bar.innerHTML = \`
          <div class="lh-title">LuckyAgent</div>
          <div class="lh-window-controls">
            <button class="lh-btn-close" title="Close" aria-label="Close"></button>
            <button class="lh-btn-min" title="Minimize" aria-label="Minimize"></button>
            <button class="lh-btn-max" title="Maximize" aria-label="Maximize"></button>
          </div>
        \`;
        // Traffic-light order feels more natural on the right for Windows/Linux custom chrome.
        // Keep close/min/max accessible without OS frame.
        const [closeBtn, minBtn, maxBtn] = bar.querySelectorAll('button');
        closeBtn.addEventListener('click', () => window.luckyDesktop?.windowControl('close'));
        minBtn.addEventListener('click', () => window.luckyDesktop?.windowControl('minimize'));
        maxBtn.addEventListener('click', () => window.luckyDesktop?.windowControl('maximize'));
        document.documentElement.appendChild(bar);
      } catch (err) {
        console.warn('[desktop] failed to inject titlebar', err);
      }
    })();
  `;

  void mainWindow.webContents.insertCSS(css);
  void mainWindow.webContents.executeJavaScript(js, true);
}

function createWindow() {
  const isMac = process.platform === 'darwin';

  mainWindow = new BrowserWindow({
    width: 1280,
    height: 840,
    minWidth: 960,
    minHeight: 640,
    title: 'LuckyAgent',
    backgroundColor: '#00000000',
    transparent: true,
    hasShadow: true,
    frame: false,
    titleBarStyle: isMac ? 'hidden' : undefined,
    trafficLightPosition: isMac ? { x: 14, y: 12 } : undefined,
    roundedCorners: true,
    thickFrame: false,
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  // Soften the OS chrome where supported. Linux/Windows need transparent+CSS
  // radius; macOS also benefits from hidden title bar + roundedCorners.
  try {
    if (process.platform === 'win32') {
      // Keep DWM shadow without the hard rectangular frame.
      mainWindow.setBackgroundMaterial?.('acrylic');
    }
  } catch {
    /* older Electron builds may not expose material APIs */
  }

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });

  mainWindow.webContents.on('did-finish-load', () => {
    injectRoundedChrome();
  });

  if (shouldLoadDist()) {
    const index = guiDistIndex();
    if (!fs.existsSync(index)) {
      console.error(`[desktop] GUI dist not found: ${index}`);
      console.error('[desktop] Run: npm run build --workspace GUI');
      app.quit();
      return;
    }
    void mainWindow.loadFile(index);
  } else {
    void mainWindow.loadURL(DEFAULT_DEV_URL);
  }

  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

function buildMenu() {
  const template = [
    {
      label: app.name,
      submenu: [
        { role: 'about' },
        { type: 'separator' },
        { role: 'services' },
        { type: 'separator' },
        { role: 'hide' },
        { role: 'hideOthers' },
        { role: 'unhide' },
        { type: 'separator' },
        { role: 'quit' },
      ],
    },
    {
      label: 'Edit',
      submenu: [
        { role: 'undo' },
        { role: 'redo' },
        { type: 'separator' },
        { role: 'cut' },
        { role: 'copy' },
        { role: 'paste' },
        { role: 'selectAll' },
      ],
    },
    {
      label: 'View',
      submenu: [
        { role: 'reload' },
        { role: 'forceReload' },
        { role: 'toggleDevTools' },
        { type: 'separator' },
        { role: 'resetZoom' },
        { role: 'zoomIn' },
        { role: 'zoomOut' },
        { type: 'separator' },
        { role: 'togglefullscreen' },
      ],
    },
    {
      label: 'Window',
      submenu: [{ role: 'minimize' }, { role: 'close' }],
    },
  ];

  if (process.platform !== 'darwin') {
    template[0] = {
      label: 'File',
      submenu: [{ role: 'quit' }],
    };
  }

  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

ipcMain.handle('desktop:window-control', (event, action) => {
  const win = BrowserWindow.fromWebContents(event.sender);
  if (!win) return false;
  switch (action) {
    case 'close':
      win.close();
      return true;
    case 'minimize':
      win.minimize();
      return true;
    case 'maximize':
      if (win.isMaximized()) win.unmaximize();
      else win.maximize();
      return true;
    default:
      return false;
  }
});

app.whenReady().then(() => {
  app.setName('LuckyAgent');
  process.env.LH_API_BASE = DEFAULT_API_BASE;
  buildMenu();
  createWindow();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
