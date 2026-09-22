'use strict';

const { app, BrowserWindow, shell, Menu, Tray, ipcMain, nativeImage } = require('electron');
const path = require('node:path');
const fs = require('node:fs');
const http = require('node:http');
const { spawn, execFileSync } = require('node:child_process');

/** @type {BrowserWindow | null} */
let mainWindow = null;
/** @type {Tray | null} */
let tray = null;
/** @type {import('node:child_process').ChildProcess | null} */
let runtimeChild = null;

const DEFAULT_DEV_URL = process.env.LH_ELECTRON_DEV_URL || 'http://127.0.0.1:5173';
const DEFAULT_API_BASE = process.env.LH_API_BASE || 'http://127.0.0.1:9090';
const WINDOW_RADIUS = Number(process.env.LH_ELECTRON_RADIUS || 16);
const gotSingleInstanceLock = app.requestSingleInstanceLock();




function packagedRoot() {
  if (process.env.LH_APP_ROOT) return path.resolve(process.env.LH_APP_ROOT);
  const besideResources = path.resolve(process.resourcesPath || '', '..');
  if (fs.existsSync(path.join(besideResources, 'lh')) || fs.existsSync(path.join(besideResources, 'lh.exe'))) {
    return besideResources;
  }
  return path.resolve(__dirname, '..', '..');
}

function runtimeBinary() {
  const root = packagedRoot();
  const name = process.platform === 'win32' ? 'lh.exe' : 'lh';
  const candidates = [
    process.env.LH_BINARY,
    path.join(root, name),
    path.join(root, 'dist', name),
  ].filter(Boolean);
  return candidates.find((candidate) => fs.existsSync(candidate));
}

function healthURL(apiBase) {
  return new URL('/api/v1/health/live', apiBase.endsWith('/') ? apiBase : `${apiBase}/`).toString();
}

function isAPIHealthy(apiBase) {
  return new Promise((resolve) => {
    const req = http.get(healthURL(apiBase), { timeout: 1200 }, (res) => {
      res.resume();
      resolve(res.statusCode >= 200 && res.statusCode < 300);
    });
    req.on('timeout', () => {
      req.destroy();
      resolve(false);
    });
    req.on('error', () => resolve(false));
  });
}

function waitForAPI(apiBase, timeoutMs) {
  const started = Date.now();
  return new Promise((resolve) => {
    const tick = async () => {
      if (await isAPIHealthy(apiBase)) {
        resolve(true);
        return;
      }
      if (Date.now() - started > timeoutMs) {
        resolve(false);
        return;
      }
      setTimeout(tick, 300);
    };
    tick();
  });
}

async function ensureLocalRuntime() {
  if (process.env.LH_ELECTRON_AUTOSTART === '0') return;
  const apiBase = process.env.LH_API_BASE || DEFAULT_API_BASE;
  if (await isAPIHealthy(apiBase)) return;

  const binary = runtimeBinary();
  if (!binary) {
    console.warn('[desktop] local lh binary was not found; start it manually');
    return;
  }

  const port = new URL(apiBase).port || '9090';
  const logDir = path.join(app.getPath('home'), '.luckyagent', 'logs');
  fs.mkdirSync(logDir, { recursive: true });
  const log = fs.openSync(path.join(logDir, 'desktop-serve.log'), 'a');
  runtimeChild = spawn(binary, ['serve', '--addr', `127.0.0.1:${port}`], {
    detached: true,
    stdio: ['ignore', log, log],
    env: process.env,
  });
  runtimeChild.unref();
  const ready = await waitForAPI(apiBase, 20000);
  if (!ready) console.warn(`[desktop] lh serve did not become healthy; see ${path.join(logDir, 'desktop-serve.log')}`);
}

function iconForTray(icon) {
  const source = icon?.image && !icon.image.isEmpty() ? icon.image : nativeImage.createFromPath(icon?.path || '');
  if (!source || source.isEmpty()) return undefined;
  return source.resize({ width: 24, height: 24, quality: 'best' });
}

function ensureTray(icon) {
  if (tray || process.env.LH_ELECTRON_TRAY === '0') return;
  const image = iconForTray(icon);
  if (!image) return;
  tray = new Tray(image);
  tray.setToolTip('LuckyAgent');
  const menu = Menu.buildFromTemplate([
    { label: 'Show LuckyAgent', click: () => showMainWindow() },
    { type: 'separator' },
    { label: 'Quit', click: () => app.quit() },
  ]);
  tray.setContextMenu(menu);
  tray.on('click', () => showMainWindow());
}

function showMainWindow() {
  if (!mainWindow || mainWindow.isDestroyed()) {
    createWindow();
    return;
  }
  if (mainWindow.isMinimized()) mainWindow.restore();
  mainWindow.show();
  mainWindow.focus();
}

function resolveAppIcon() {
  const candidates = [
    path.join(__dirname, 'assets', 'icon.png'),
    path.join(__dirname, 'assets', 'icon.ico'),
    path.resolve(__dirname, '..', 'GUI', 'public', 'favicon.png'),
    path.resolve(__dirname, '..', 'GUI', 'public', 'icon.png'),
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) return candidate;
  }
  return undefined;
}

function loadAppIconImage() {
  const iconPath = resolveAppIcon();
  if (!iconPath) return { path: undefined, image: undefined };
  try {
    const image = nativeImage.createFromPath(iconPath);
    if (!image || image.isEmpty()) return { path: iconPath, image: undefined };
    // Linux taskbars often ignore a single 1024px PNG. A small size set is
    // what actually lands in _NET_WM_ICON.
    const sizes = [16];
    const representations = sizes
      .map((size) => image.resize({ width: size, height: size, quality: 'best' }))
      .filter((item) => item && !item.isEmpty());
    if (representations.length) {
      image.addRepresentation({ scaleFactor: 1, width: image.getSize().width, height: image.getSize().height });
      for (const representation of representations) {
        const size = representation.getSize();
        image.addRepresentation({
          scaleFactor: 1,
          width: size.width,
          height: size.height,
          buffer: representation.toPNG(),
        });
      }
    }
    return { path: iconPath, image };
  } catch (err) {
    console.warn('[desktop] failed to load app icon', err);
    return { path: iconPath, image: undefined };
  }
}


function publishLinuxWindowIcon(win, icon) {
  if (process.platform !== 'linux') return false;
  if (!win || win.isDestroyed() || !icon?.image || icon.image.isEmpty()) return false;
  const handle = win.getNativeWindowHandle();
  if (!handle || handle.length < 4) return false;
  const wid = handle.readUInt32LE(0);
  if (!wid) return false;

  // xprop -set truncates CARDINAL lists at 64 values, so a real icon has to
  // be written through XChangeProperty. Keep the helper local and tiny.
  const helper = path.join(__dirname, 'scripts', 'set-x11-icon.py');
  if (!fs.existsSync(helper)) return false;
  const blob = path.join(app.getPath('temp'), `luckyagent-net-wm-icon-${wid}.rgba`);
  try {
    const size = 48;
    const resized = icon.image.resize({ width: size, height: size, quality: 'best' });
    if (!resized || resized.isEmpty()) return false;
    const { width, height } = resized.getSize();
    fs.writeFileSync(blob, Buffer.concat([
      Buffer.from(Uint32Array.of(width, height).buffer),
      resized.toBitmap(),
    ]));
    execFileSync('python3', [helper, String(wid), blob], {
      stdio: 'ignore',
      timeout: 4000,
      env: { ...process.env, DISPLAY: process.env.DISPLAY || ':0' },
    });
    return true;
  } catch (err) {
    console.warn('[desktop] failed to publish linux window icon', err?.message || err);
    return false;
  } finally {
    try { fs.unlinkSync(blob); } catch { /* ignore */ }
  }
}

function applyWindowIcon(win, icon) {
  if (!win || win.isDestroyed() || (!icon?.image && !icon?.path)) return false;
  const payload = icon.image && !icon.image.isEmpty() ? icon.image : icon.path;
  let ok = false;
  try {
    win.setIcon(payload);
    ok = true;
  } catch (err) {
    console.warn('[desktop] setIcon failed', err);
  }
  // Frameless transparent windows on this Linux/X11 stack do not retain
  // Electron's setIcon result, so publish the icon property directly.
  if (publishLinuxWindowIcon(win, icon)) ok = true;
  return ok;
}


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

  const appIcon = loadAppIconImage();

  mainWindow = new BrowserWindow({
    width: 1280,
    height: 840,
    minWidth: 960,
    minHeight: 640,
    title: 'LuckyAgent',
    show: false,
    ...(appIcon.image || appIcon.path ? { icon: appIcon.image || appIcon.path } : {}),
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

  applyWindowIcon(mainWindow, appIcon);

  const revealWindow = () => {
    if (!mainWindow || mainWindow.isDestroyed()) return;
    // Linux drops _NET_WM_ICON when the icon is set before the window is mapped.
    applyWindowIcon(mainWindow, appIcon);
    if (!mainWindow.isVisible()) mainWindow.show();
    applyWindowIcon(mainWindow, appIcon);
  };

  mainWindow.once('ready-to-show', revealWindow);
  // Fallback show if ready-to-show is delayed on some Linux setups.
  setTimeout(() => {
    if (mainWindow && !mainWindow.isDestroyed() && !mainWindow.isVisible()) {
      revealWindow();
    }
  }, 1200);

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

if (!gotSingleInstanceLock) {
  app.quit();
} else {
  app.on('second-instance', () => showMainWindow());

  app.whenReady().then(async () => {
    const boot = loadAppIconImage();
    if (process.platform === 'darwin' && (boot.image || boot.path)) {
      try { app.dock?.setIcon?.(boot.image || boot.path); } catch { /* ignore */ }
    }
    if (process.platform === 'linux' && boot.image && !boot.image.isEmpty()) {
      try { app.setIcon?.(boot.image); } catch { /* ignore */ }
    }

    app.setName('LuckyAgent');
    process.env.LH_API_BASE = DEFAULT_API_BASE;
    app.setLoginItemSettings({
      openAtLogin: process.env.LH_ELECTRON_OPEN_AT_LOGIN === '1',
      path: process.execPath,
    });
    ensureTray(boot);
    buildMenu();
    await ensureLocalRuntime();
    createWindow();

    app.on('activate', () => {
      if (BrowserWindow.getAllWindows().length === 0) createWindow();
    });
  });
}

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});
