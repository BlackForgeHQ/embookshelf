import { PHASE_DEVELOPMENT_SERVER } from "next/constants"
import type { NextConfig } from "next"

// Server-owned paths the dev server forwards to the Go backend on :6060,
// so cookies, SSE and OPDS behave exactly as they do behind the binary.
const BACKEND = "http://localhost:6060"
// grafana/otel-lgtm exposes OTLP HTTP on 4318. Browser OTLP posts go
// through the same forwarding in dev so the page stays same-origin and
// CORS never enters the picture.
const OTLP_HTTP = "http://localhost:4318"

const base: NextConfig = {
  reactStrictMode: true,
  // Fail on a stale trailing-slash URL rather than redirecting: the Go
  // server resolves `/library` → `library.html`, and every link the app
  // and the backend emit is slash-free.
  trailingSlash: false,
}

export default function config(phase: string): NextConfig {
  if (phase === PHASE_DEVELOPMENT_SERVER) {
    // Dev runs as a normal Next server: rewrites need it, and the export
    // mode's dynamic-route check would refuse every real book id, since
    // the build only knows the `_` placeholder (see src/lib/routes.ts).
    return {
      ...base,
      // gzip buffers the proxied /events stream until it closes, so SSE
      // events would never reach the page.
      compress: false,
      async rewrites() {
        return [
          { source: "/api/:path*", destination: `${BACKEND}/api/:path*` },
          { source: "/opds/:path*", destination: `${BACKEND}/opds/:path*` },
          { source: "/opds", destination: `${BACKEND}/opds` },
          { source: "/events", destination: `${BACKEND}/events` },
          { source: "/v1/:path*", destination: `${OTLP_HTTP}/v1/:path*` },
        ]
      },
    }
  }
  // Production is a static export embedded in the Go binary
  // (internal/staticfs). No Node server ever runs it.
  return { ...base, output: "export" }
}
