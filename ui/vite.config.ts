import { defineConfig } from "vite"
import { devtools } from "@tanstack/devtools-vite"
import { tanstackStart } from "@tanstack/react-start/plugin/vite"
import viteReact from "@vitejs/plugin-react"
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
    tailwindcss(),
    tanstackStart({
      spa: { enabled: true },
      // A test for a route belongs beside the route, but the router
      // scans this directory for anything exporting a Route and warns
      // about every file that does not. Tests never will, so exclude
      // them rather than teaching each one to look like a non-route.
      router: { routeFileIgnorePattern: "__tests__" },
    }),
    viteReact(),
  ],
  // Vite 7+ reads `paths` from tsconfig.json natively — replaces the
  // vite-tsconfig-paths plugin.
  resolve: {
    tsconfigPaths: true,
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // lucide-react's main barrel re-exports each icon module. Sonner
        // and the shelf-icon static map statically import a handful of
        // those icons, so the main lucide chunk ends up *importing* the
        // individual icon chunks (e.g. circle-check). Each icon chunk in
        // turn imports `createLucideIcon`, which Rollup keeps in the main
        // lucide chunk by default → static-import cycle, and the icon
        // chunk evaluates while `createLucideIcon` is still in TDZ
        // ("t is not a function" at icon-chunk:1).
        //
        // Hoisting the lucide primitives (createLucideIcon, Icon,
        // shared utils) into a sibling chunk breaks the cycle: icon
        // chunks import primitives from `lucide-core`, the main barrel
        // imports the icon chunks, primitives never import back.
        manualChunks(id) {
          if (
            id.includes("lucide-react/dist/esm/createLucideIcon") ||
            id.includes("lucide-react/dist/esm/Icon.mjs") ||
            id.includes("lucide-react/dist/esm/defaultAttributes") ||
            id.includes("lucide-react/dist/esm/context") ||
            id.includes("lucide-react/dist/esm/shared/")
          ) {
            return "lucide-core"
          }
        },
      },
    },
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
