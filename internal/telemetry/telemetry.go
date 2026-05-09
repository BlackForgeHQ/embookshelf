// SPDX-License-Identifier: AGPL-3.0-or-later

// Package telemetry wires OpenTelemetry traces, metrics, and logs to an
// OTLP-compatible collector (e.g. grafana/otel-lgtm during local dev, or
// an external Grafana / Datadog / Honeycomb backend in production).
//
// Setup is a no-op when cfg.Enabled is false so the binary can ship with
// observability dormant and costing nothing.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// fanoutHandler dispatches each slog record to every wrapped handler.
// Used so logs land on stdout (for human tailing / air) AND OTLP (for
// Grafana) simultaneously.
type fanoutHandler struct{ handlers []slog.Handler }

func (f fanoutHandler) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}
func (f fanoutHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
func (f fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		nh[i] = h.WithAttrs(attrs)
	}
	return fanoutHandler{handlers: nh}
}
func (f fanoutHandler) WithGroup(name string) slog.Handler {
	nh := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		nh[i] = h.WithGroup(name)
	}
	return fanoutHandler{handlers: nh}
}

// Config captures the subset of cfg.Config used by this package so the
// signature stays independent of internal/config.
type Config struct {
	Enabled     bool
	ServiceName string
	Endpoint    string  // host:port for gRPC, base URL for http/protobuf
	Protocol    string  // "grpc" or "http/protobuf"
	Insecure    bool    // skip TLS; true is typical for local dev
	SampleRatio float64 // 0.0–1.0 head-based trace sampler
}

// Shutdown releases all telemetry providers in reverse of Setup's order.
// Safe to call even when Setup returned a no-op: the returned function is
// always non-nil.
type Shutdown func(context.Context) error

// Setup registers global tracer, meter, and logger providers and returns
// a shutdown hook the caller should defer. When cfg.Enabled is false the
// function returns a no-op shutdown and leaves the global providers alone
// so callers that ask for a tracer get the default no-op implementation.
func Setup(ctx context.Context, cfg Config) (Shutdown, error) {
	noop := func(context.Context) error { return nil }
	if !cfg.Enabled {
		return noop, nil
	}

	res, err := buildResource(ctx, cfg.ServiceName)
	if err != nil {
		return noop, fmt.Errorf("telemetry resource: %w", err)
	}

	tp, err := newTracerProvider(ctx, cfg, res)
	if err != nil {
		return noop, fmt.Errorf("telemetry traces: %w", err)
	}

	mp, err := newMeterProvider(ctx, cfg, res)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return noop, fmt.Errorf("telemetry metrics: %w", err)
	}

	lp, err := newLoggerProvider(ctx, cfg, res)
	if err != nil {
		_ = mp.Shutdown(ctx)
		_ = tp.Shutdown(ctx)
		return noop, fmt.Errorf("telemetry logs: %w", err)
	}

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Fan slog out to the existing stdout handler AND the OTel log
	// bridge, so operators still see logs in their terminal while
	// Grafana/Loki gets the same stream with trace correlation.
	stdoutHandler := slog.Default().Handler()
	otelHandler := otelslog.NewHandler(cfg.ServiceName, otelslog.WithLoggerProvider(lp))
	slog.SetDefault(slog.New(fanoutHandler{handlers: []slog.Handler{stdoutHandler, otelHandler}}))

	return func(ctx context.Context) error {
		// Give exporters up to 5s each to flush; a stalled collector
		// should not block process exit forever.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return errors.Join(
			lp.Shutdown(ctx),
			mp.Shutdown(ctx),
			tp.Shutdown(ctx),
		)
	}, nil
}

func buildResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
}

func newTracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	var exp *otlptrace.Exporter
	var err error
	switch cfg.Protocol {
	case "http/protobuf":
		opts := []otlptracehttp.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpointURL(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exp, err = otlptracehttp.New(ctx, opts...)
	default:
		opts := []otlptracegrpc.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exp, err = otlptracegrpc.New(ctx, opts...)
	}
	if err != nil {
		return nil, err
	}

	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	), nil
}

func newMeterProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	var exp sdkmetric.Exporter
	var err error
	switch cfg.Protocol {
	case "http/protobuf":
		opts := []otlpmetrichttp.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlpmetrichttp.WithEndpointURL(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		exp, err = otlpmetrichttp.New(ctx, opts...)
	default:
		opts := []otlpmetricgrpc.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlpmetricgrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		exp, err = otlpmetricgrpc.New(ctx, opts...)
	}
	if err != nil {
		return nil, err
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	), nil
}

func newLoggerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	var exp sdklog.Exporter
	var err error
	switch cfg.Protocol {
	case "http/protobuf":
		opts := []otlploghttp.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlploghttp.WithEndpointURL(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		exp, err = otlploghttp.New(ctx, opts...)
	default:
		opts := []otlploggrpc.Option{}
		if cfg.Endpoint != "" {
			opts = append(opts, otlploggrpc.WithEndpoint(cfg.Endpoint))
		}
		if cfg.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		exp, err = otlploggrpc.New(ctx, opts...)
	}
	if err != nil {
		return nil, err
	}

	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(res),
	), nil
}
