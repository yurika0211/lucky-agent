/// <reference types="vite/client" />

export {};

declare global {
  interface LuckyDesktopBridge {
    platform: string;
    apiBase: string;
    isElectron: boolean;
    windowControl?: (action: 'close' | 'minimize' | 'maximize') => Promise<boolean>;
  }

  interface Window {
    luckyDesktop?: LuckyDesktopBridge;
  }
}
