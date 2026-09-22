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
    /* Soft rounded shell without a fake title strip */
    html, body {
      background: transparent !important;
      border-radius: ${radius}px !important;
      overflow: hidden !important;
    }
    #root, .app {
      border-radius: ${radius}px !important;
      overflow: hidden !important;
      min-height: 100vh;
      background: var(--bg);
    }
    body.lh-electron {
      box-shadow: none !important;
    }

    /* Use the real GUI chrome as the drag surface */
    body.lh-electron .sidebar-head,
    body.lh-electron .topbar {
      -webkit-app-region: drag;
      app-region: drag;
    }

    /* Keep interactive controls clickable */
    body.lh-electron .sidebar-head button,
    body.lh-electron .sidebar-head a,
    body.lh-electron .sidebar-head input,
    body.lh-electron .topbar button,
    body.lh-electron .topbar a,
    body.lh-electron .topbar input,
    body.lh-electron .topbar select,
    body.lh-electron .nav,
    body.lh-electron .nav-item,
    body.lh-electron .new-chat,
    body.lh-electron .sidebar-search,
    body.lh-electron .chat-list,
    body.lh-electron .lh-window-controls,
    body.lh-electron .lh-window-controls * {
      -webkit-app-region: no-drag;
      app-region: no-drag;
    }

    /* Native-feeling window controls tucked into the topbar */
    body.lh-electron .lh-window-controls {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      margin-left: 4px;
      padding: 4px 6px;
      border-radius: 999px;
      background: color-mix(in srgb, var(--hover-strong) 70%, transparent);
    }
    body.lh-electron .lh-window-controls button {
      width: 11px;
      height: 11px;
      border: 0;
      border-radius: 999px;
      padding: 0;
      cursor: pointer;
      opacity: 0.78;
      transition: opacity 120ms ease, transform 120ms ease, filter 120ms ease;
    }
    body.lh-electron .lh-window-controls button:hover {
      opacity: 1;
      transform: scale(1.06);
      filter: brightness(1.04);
    }
    body.lh-electron .lh-btn-close { background: #d96863; }
    body.lh-electron .lh-btn-min { background: #c9a24b; }
    body.lh-electron .lh-btn-max { background: #6aa35d; }

    /* Slightly quieter top chrome in desktop mode */
    body.lh-electron .topbar {
      height: 52px;
      padding-right: 10px;
    }
    body.lh-electron .sidebar {
      padding-top: 10px;
    }
  `;

  const js = `
    (() => {
      try {
        document.documentElement.classList.add('lh-electron');
        document.body.classList.add('lh-electron');

        // Remove the old deliberate black title strip if a previous build injected it.
        document.getElementById('lh-electron-titlebar')?.remove();

        const mountControls = () => {
          if (document.querySelector('.lh-window-controls')) return true;
          const host =
            document.querySelector('.topbar-right') ||
            document.querySelector('.topbar') ||
            document.querySelector('.sidebar-head');
          if (!host) return false;

          const controls = document.createElement('div');
          controls.className = 'lh-window-controls';
          controls.setAttribute('role', 'group');
          controls.setAttribute('aria-label', 'Window controls');
          controls.innerHTML = \`
            <button class="lh-btn-close" type="button" title="Close" aria-label="Close"></button>
            <button class="lh-btn-min" type="button" title="Minimize" aria-label="Minimize"></button>
            <button class="lh-btn-max" type="button" title="Maximize" aria-label="Maximize"></button>
          \`;
          const [closeBtn, minBtn, maxBtn] = controls.querySelectorAll('button');
          closeBtn.addEventListener('click', (event) => {
            event.stopPropagation();
            window.luckyDesktop?.windowControl('close');
          });
          minBtn.addEventListener('click', (event) => {
            event.stopPropagation();
            window.luckyDesktop?.windowControl('minimize');
          });
          maxBtn.addEventListener('click', (event) => {
            event.stopPropagation();
            window.luckyDesktop?.windowControl('maximize');
          });

          // Prefer the real topbar action cluster so it feels like part of the nav.
          if (host.classList.contains('topbar-right')) host.appendChild(controls);
          else host.prepend(controls);
          return true;
        };

        if (!mountControls()) {
          const obs = new MutationObserver(() => {
            if (mountControls()) obs.disconnect();
          });
          obs.observe(document.documentElement, { childList: true, subtree: true });
          setTimeout(() => obs.disconnect(), 8000);
        }
      } catch (err) {
        console.warn('[desktop] failed to inject natural chrome', err);
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
