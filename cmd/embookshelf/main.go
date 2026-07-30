// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/blackforge/embookshelf/internal/app"
	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/telemetry"
)

// Build-time identity. Overridden via
//
//	-ldflags "-X main.version=$VERSION -X main.commit=$COMMIT"
//
// in the Dockerfile and goreleaser. Defaults keep `go run` readable.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// Subcommands run and exit; no argument means "serve".
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "import-sqlite":
			os.Exit(importSQLiteCmd(os.Args[2:]))
		case "recover-misplaced":
			os.Exit(recoverMisplacedCmd(os.Args[2:]))
		case "-h", "--help", "help":
			fmt.Fprintf(os.Stderr, `embookshelf %s (%s)

Usage:
  embookshelf                        serve (default)
  embookshelf import-sqlite ...      import an existing SQLite library into Postgres
  embookshelf recover-misplaced      find book files written outside their library
                                     by the v0.3.1–v0.6.2 placer bug (dry run;
                                     --apply to repair)
`, version, commit)
			os.Exit(0)
		}
	}

	// Default to release mode (silences debug logs, skips trusted-proxy
	// warnings). Set GIN_MODE=debug to opt back into route logs.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// OpenTelemetry — must be set up before services start so spans and
	// metrics from DB, queue, and HTTP paths end up in the pipeline.
	// No-op when OTEL_ENABLED is false.
	otelShutdown, err := telemetry.Setup(ctx, telemetry.Config{
		Enabled:     cfg.OTELEnabled,
		ServiceName: cfg.OTELServiceName,
		Endpoint:    cfg.OTELEndpoint,
		Protocol:    cfg.OTELProtocol,
		Insecure:    cfg.OTELInsecure,
		SampleRatio: cfg.OTELSampleRatio,
	})
	if err != nil {
		slog.Error("telemetry setup", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := otelShutdown(shutdownCtx); err != nil {
			slog.Warn("telemetry shutdown", "err", err)
		}
	}()
	if cfg.OTELEnabled {
		slog.Info("OpenTelemetry enabled", "endpoint", cfg.OTELEndpoint, "protocol", cfg.OTELProtocol, "service", cfg.OTELServiceName)
	}

	a, err := app.Build(ctx, cfg, version, commit)
	if err != nil {
		// The ADR-0023 refusal itself lives in db.Open, which every entry
		// point shares and which rejects the DSN before opening it — so no
		// empty database file is created on the way here. What is left for
		// this binary is turning that refusal into the instructions an
		// operator of the *server* needs.
		if errors.Is(err, db.ErrSQLiteUnsupported) {
			slog.Error("SQLite is no longer supported — embookshelf requires Postgres")
			fmt.Fprintf(os.Stderr, `
DATABASE_URL points at SQLite, which this version cannot serve (ADR-0023).

Migrate the library into Postgres with:

  DATABASE_URL='postgres://user:pass@host:5432/embookshelf' \
    embookshelf import-sqlite --from <path to your .db file>

Then set DATABASE_URL to that Postgres DSN and start again. The target
database must be empty; migrations are applied to it automatically.
`)
			os.Exit(1)
		}
		slog.Error("build", "err", err)
		os.Exit(1)
	}

	if err := a.Start(ctx); err != nil {
		slog.Error("start", "err", err)
		if cerr := a.Close(ctx); cerr != nil {
			slog.Warn("shutdown", "err", cerr)
		}
		os.Exit(1)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           a.Engine(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Track whether ListenAndServe died on its own (bind error, etc.) so
	// we can propagate a non-zero exit — otherwise air sees exit 0 and
	// won't flag the failure, and the operator only learns the port is
	// taken from a silent stop.
	var serveErr error
	go func() {
		slog.Info("server starting", "addr", srv.Addr, "bookdrop", cfg.BookDropPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			serveErr = err
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "err", err)
	}

	if err := a.Close(ctx); err != nil {
		slog.Warn("shutdown", "err", err)
	}

	if serveErr != nil {
		os.Exit(1)
	}
}
