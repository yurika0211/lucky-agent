'use strict';

const { contextBridge } = require('electron');

contextBridge.exposeInMainWorld('luckyDesktop', {
  platform: process.platform,
  apiBase: process.env.LH_API_BASE || 'http://127.0.0.1:9090',
  isElectron: true,
});
