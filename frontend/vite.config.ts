import path from 'node:path';
import { defineConfig } from 'vite';
import { tanstackStart } from '@tanstack/react-start/plugin/vite';
import react from '@vitejs/plugin-react';

// Proxies dev-server requests for server-owned paths straight to the Go
// backend on :6060 so cookies + SSE + OPDS keep working unchanged.
const BACKEND = 'http://localhost:6060';
// grafana/otel-lgtm exposes OTLP HTTP on 4318. Browser OTLP posts go
// through this proxy in dev so the page stays same-origin and CORS
// never enters the picture.
const OTLP_HTTP = 'http://localhost:4318';

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
    // Fail loudly if 5173 is taken instead of silently drifting to 5174 —
    // anything assuming :5173 (e2e tests, browser bookmarks, docs) breaks
    // in confusing ways otherwise.
    strictPort: true,
    proxy: {
      '/api': { target: BACKEND, changeOrigin: true },
      '/opds': { target: BACKEND, changeOrigin: true },
      '/events': { target: BACKEND, changeOrigin: true, ws: true },
      // OTLP HTTP receiver paths — /v1/traces, /v1/metrics, /v1/logs.
      '/v1': { target: OTLP_HTTP, changeOrigin: true },
    },
  },
});
