'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('luckyDesktop', {
  platform: process.platform,
  apiBase: process.env.LH_API_BASE || 'http://127.0.0.1:9090',
  isElectron: true,
  windowControl(action) {
    return ipcRenderer.invoke('desktop:window-control', action);
  },
});
