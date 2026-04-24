import { defineConfig } from "vite"
import { devtools } from "@tanstack/devtools-vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
import viteTsConfigPaths from "vite-tsconfig-paths"
import tailwindcss from "@tailwindcss/vite"

// Proxies dev-server requests for server-owned paths straight to the Go
// backend on :6060 so cookies + SSE + OPDS keep working unchanged.
const BACKEND = "http://localhost:6060"
// grafana/otel-lgtm exposes OTLP HTTP on 4318. Browser OTLP posts go
// through this proxy in dev so the page stays same-origin and CORS
// never enters the picture.
const OTLP_HTTP = "http://localhost:4318"

const config = defineConfig({
  plugins: [
    devtools(),
    viteTsConfigPaths({
      projects: ["./tsconfig.json"],
    }),
    tailwindcss(),
    tanstackStart({
      spa: { enabled: true },
    }),
    viteReact(),
  ],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // Fail loudly if 5173 is taken instead of silently drifting to 5174 —
    // anything assuming :5173 (e2e tests, browser bookmarks, docs) breaks
    // in confusing ways otherwise.
    strictPort: true,
    proxy: {
      "/api": { target: BACKEND, changeOrigin: true },
      "/opds": { target: BACKEND, changeOrigin: true },
      "/events": { target: BACKEND, changeOrigin: true, ws: true },
      "/v1": { target: OTLP_HTTP, changeOrigin: true },
    },
  },
  // TanStack Start's SPA-mode build spins up a Vite preview server and
  // fetches its own routes to prerender the `_shell.html`. By default
  // that server binds to `localhost`, which resolves to ::1 first under
  // Bun-in-Docker while Bun's fetch hits 127.0.0.1 → ECONNREFUSED
  // (TanStack/router#6275). Pinning preview to 127.0.0.1 sidesteps the
  // mismatch. Safe in dev too — the preview server is a build-time tool.
  preview: {
    host: "127.0.0.1",
  },
})

export default config
