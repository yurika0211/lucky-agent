import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react-swc';
import { resolve } from 'node:path';

export default defineConfig({
  // Relative base keeps packaged Electron file:// loads working.
  // Web/Go static hosting also tolerates './' asset paths.
  base: process.env.LH_GUI_BASE || './',
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: false,
    host: '127.0.0.1',
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:9090',
        changeOrigin: true,
        ws: true,
      },
      '/lh-api': {
        target: 'http://127.0.0.1:9090',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/lh-api/, '/api'),
      },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
});
