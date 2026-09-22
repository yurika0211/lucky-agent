'use strict';

const { app, BrowserWindow, shell, Menu } = require('electron');
const path = require('node:path');
const fs = require('node:fs');

/** @type {BrowserWindow | null} */
let mainWindow = null;

const DEFAULT_DEV_URL = process.env.LH_ELECTRON_DEV_URL || 'http://127.0.0.1:5173';
const DEFAULT_API_BASE = process.env.LH_API_BASE || 'http://127.0.0.1:9090';

function guiDistIndex() {
  return path.resolve(__dirname, '..', 'GUI', 'dist', 'index.html');
}

function shouldLoadDist() {
  if (process.env.LH_ELECTRON_LOAD === 'dist') return true;
  if (process.env.LH_ELECTRON_LOAD === 'dev') return false;
  // packaged / no explicit mode: prefer built GUI assets when present
  return fs.existsSync(guiDistIndex());
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 840,
    minWidth: 960,
    minHeight: 640,
    title: 'LuckyAgent',
    backgroundColor: '#0b0f14',
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
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
    // Simplify non-macOS app menu
    template[0] = {
      label: 'File',
      submenu: [{ role: 'quit' }],
    };
  }

  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

app.whenReady().then(() => {
  // Expose defaults to renderer via preload bridge.
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
