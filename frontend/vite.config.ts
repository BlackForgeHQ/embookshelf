import path from 'node:path';
import { defineConfig } from 'vite';
import { tanstackStart } from '@tanstack/react-start/plugin/vite';
import react from '@vitejs/plugin-react';

// Proxies dev-server requests for server-owned paths straight to the Go
// backend on :6060 so cookies + SSE + OPDS keep working unchanged.
const BACKEND = 'http://localhost:6060';

export default defineConfig({
  plugins: [
    tanstackStart({
      spa: { enabled: true },
    }),
    react(),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, 'src'),
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: BACKEND, changeOrigin: true },
      '/opds': { target: BACKEND, changeOrigin: true },
      '/events': { target: BACKEND, changeOrigin: true, ws: true },
    },
  },
});
