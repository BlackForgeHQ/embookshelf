/// <reference types="vite/client" />
// Browser OpenTelemetry wiring: spans for document load, user interactions
// (clicks), and fetch calls (incl. traceparent propagation to the Go API
// so browser spans link to backend spans in Grafana/Tempo).
//
// Gated by VITE_OTEL_ENABLED so the bundle keeps the SDK tree-shakeable
// for production builds that don't need it. Call setupTelemetry() once,
// as early as possible in the client entry.

import { context as otelContext, trace } from "@opentelemetry/api"
import { ZoneContextManager } from "@opentelemetry/context-zone"
import {
  BatchSpanProcessor,
  WebTracerProvider,
} from "@opentelemetry/sdk-trace-web"
import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http"
import { resourceFromAttributes } from "@opentelemetry/resources"
import {
  ATTR_SERVICE_NAME,
  ATTR_SERVICE_VERSION,
} from "@opentelemetry/semantic-conventions"
import { registerInstrumentations } from "@opentelemetry/instrumentation"
import { DocumentLoadInstrumentation } from "@opentelemetry/instrumentation-document-load"
import { FetchInstrumentation } from "@opentelemetry/instrumentation-fetch"
import { UserInteractionInstrumentation } from "@opentelemetry/instrumentation-user-interaction"

let initialized = false

export function setupTelemetry() {
  if (initialized) return
  if (typeof window === "undefined") return

  const enabled = import.meta.env.VITE_OTEL_ENABLED === "true"
  if (!enabled) return

  // Same-origin /v1/traces so Vite (dev) and the Go server (prod) can
  // forward to the collector without running into CORS. See vite.config.ts
  // for the dev proxy target.
  const endpoint = import.meta.env.VITE_OTEL_OTLP_ENDPOINT || "/v1/traces"
  const serviceName =
    import.meta.env.VITE_OTEL_SERVICE_NAME || "embookshelf-frontend"

  const resource = resourceFromAttributes({
    [ATTR_SERVICE_NAME]: serviceName,
    [ATTR_SERVICE_VERSION]: import.meta.env.VITE_APP_VERSION || "dev",
    "deployment.environment": import.meta.env.MODE,
  })

  const provider = new WebTracerProvider({
    resource,
    spanProcessors: [
      new BatchSpanProcessor(new OTLPTraceExporter({ url: endpoint })),
    ],
  })

  provider.register({
    contextManager: new ZoneContextManager(),
  })

  registerInstrumentations({
    instrumentations: [
      new DocumentLoadInstrumentation(),
      new UserInteractionInstrumentation(),
      new FetchInstrumentation({
        // Inject traceparent into outbound API calls so browser→server
        // spans stitch together in Tempo.
        propagateTraceHeaderCorsUrls: [/.*/],
        clearTimingResources: true,
      }),
    ],
  })

  // Handy in the console when debugging: `window.__otel.tracer.startSpan(...)`.
  ;(window as unknown as { __otel?: unknown }).__otel = {
    provider,
    tracer: trace.getTracer(serviceName),
    context: otelContext,
  }

  initialized = true
}
