// SPDX-License-Identifier: AGPL-3.0-or-later

// Package app is the composition root. Everything the process needs is
// wired here, in three phases that are deliberately separate:
//
//   - Build prepares, then constructs. The prepare stage writes what the
//     constructors read back — schema migrations, the storage_v2
//     backfill, the shared-S3 backend reconcile — and every other stage
//     only assembles: no seed, no goroutine, no queue start. Failure is
//     a returned error, so a test can call Build and inspect the wiring
//     without booting — knowing that Build has migrated the database it
//     was pointed at.
//   - Start runs the runtime side effects — seeds, the reloads that read
//     them, the queue, and the background sweepers.
//   - Close shuts the whole thing down along one path.
//
// The split exists because construction and side effects used to
// interleave in main, which meant the only way to find out whether a seam
// had been left unassigned was to boot the process and wait for the
// nil dereference (#196).
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/handler"
	"github.com/blackforge/embookshelf/internal/ingest"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/provider"
	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/task"
)

// App holds the wired process as the stage bundles that built it. Start
// and Close read them, and so does the boot test, which is the point —
// a seam a stage forgets is a failing assertion rather than a
// production nil dereference. The bundles are named fields rather than
// embedded because repos and services deliberately reuse names (r.lib
// the repo, s.lib the service), which embedding would turn into
// ambiguous selectors.
type App struct {
	// w carries the process-level primitives every stage shares,
	// including the config, the pool, and the late-bound enqueuer —
	// see wiring in build.go.
	w wiring
	// r is the repository tier; s the service tier over it. The seam
	// walk in the boot test descends into all three bundles.
	r repos
	s services

	// Background queue. Started by Start together with resolving
	// w.enq — see startQueue.
	queue queue.Client

	// Bookdrop watchers; constructed by buildWatchers, run by Start.
	// Both are always non-nil — Run self-disables when unconfigured.
	watcher   *ingest.Watcher
	s3Watcher *ingest.S3Watcher

	handler *handler.Handler
	engine  *gin.Engine

	// The background work Start launches and Close waits for. Not a
	// Build seam and deliberately not a pointer: there is nothing to
	// cancel or wait for until Start runs, so its zero value is the
	// correct state for a built-but-unstarted App.
	bg background
}

// background is the registry Close consults. Holding a context in a
// struct is the exception this file makes on purpose: it is the
// application's own lifetime, one value that every registered task must
// share, and threading it through goBackground's callers instead would
// let one of them pass a different one.
//
// cancel is nil until Start derives the context — Close on an App that
// was never started has nothing to stop, and must not panic saying so.
type background struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// goBackground starts fn as a named background task and records it, so
// Close knows what it is waiting for.
//
// Every loop Start launches goes through here. A goroutine started with
// a bare `go` is one Close cannot see: it keeps running past the pool
// close, and the next query it makes fails on a pool that is already
// gone — the shutdown race the two backfills used to lose by detaching
// onto context.Background() (#224). The name reaches the log line below,
// which is what identifies a task that outstayed the shutdown budget.
//
// Start-only. It reads the context Start derived, so a call made before
// Start would hand fn a nil one.
func (a *App) goBackground(name string, fn func(context.Context)) {
	a.bg.wg.Add(1)
	go func() {
		defer a.bg.wg.Done()
		fn(a.bg.ctx)
		slog.Debug("background task returned", "task", name)
	}()
}

// Build wires the process and returns it unstarted.
//
// The first stage, prepare, writes what the constructors read back —
// schema migrations, the storage_v2 backfill, the shared-S3 backend
// reconcile. Every stage after it only assembles: no seed, no reload,
// no goroutine, no queue start.
func Build(ctx context.Context, cfg config.Config, version, commit string) (*App, error) {
	dbh, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	// Every failure past this point owns the pool: the caller gets an
	// error rather than an App, so it has no handle to close.
	built := false
	defer func() {
		if !built {
			_ = dbh.Close()
		}
	}()

	p, err := prepare(ctx, dbh, cfg)
	if err != nil {
		return nil, err
	}

	r, err := buildRepos(p, cfg)
	if err != nil {
		return nil, err
	}

	w, err := buildWiring(p, cfg)
	if err != nil {
		return nil, err
	}

	s, err := buildServices(ctx, w, r)
	if err != nil {
		return nil, err
	}
	q, err := buildQueue(ctx, w, r, s)
	if err != nil {
		return nil, fmt.Errorf("queue: %w", err)
	}
	watcher, s3Watcher := buildWatchers(ctx, w, r, s)

	h := buildHTTP(w, r, s, q, version, commit)

	built = true
	return &App{
		w: w, r: r, s: s,
		queue:   q,
		watcher: watcher, s3Watcher: s3Watcher,
		handler: h,
		engine:  h.Engine(),
	}, nil
}

// Engine is the HTTP handler main serves. Built by Build so a failure to
// register a route surfaces before the listener opens.
func (a *App) Engine() *gin.Engine { return a.engine }

// Start runs every boot-time side effect, in the order the process has
// always run them: seed the settings rows, apply what they say, bring the
// queue up, then launch the background loops.
//
// Only a failed queue start is fatal. The rest degrade: a settings row
// that cannot be seeded leaves the feature at its defaults, which is the
// same state a fresh install has.
func (a *App) Start(ctx context.Context) error {
	// The context every background task runs under. Derived from the
	// caller's so a cancelled signal context still stops them, but owned
	// here so Close can cancel it itself: "Close is the single shutdown
	// path" has to hold for a caller that only calls Close.
	a.bg.ctx, a.bg.cancel = context.WithCancel(ctx)

	// Seed provider_settings on first boot using catalog defaults.
	// ON CONFLICT DO NOTHING means subsequent restarts leave admin
	// toggles alone — the DB is authoritative after the initial seed.
	defaults := make(map[string]bool, len(provider.Catalog))
	for _, c := range provider.Catalog {
		defaults[string(c.ID)] = c.DefaultEnabled
	}
	if err := a.s.providerSettingsRepo.SeedIfAbsent(ctx, defaults); err != nil {
		slog.Warn("seed provider settings", "err", err)
	}
	// Push stored per-provider config (API keys, language, …) into the
	// running provider instances. Failure here is non-fatal — providers
	// fall back to their no-config defaults.
	if err := a.s.providerCfg.LoadConfigs(ctx); err != nil {
		slog.Warn("load provider configs", "err", err)
	}

	// Every app_settings row the registry declares, seeded so the admin
	// UI has something to render on first boot: OIDC empty, forward-auth,
	// reading guides and audiobooks disabled. One call, because boot is
	// the wrong place to keep a list of domains — a new one that forgot
	// to be added here cost nothing at runtime and an empty settings
	// panel to notice (#237).
	if err := a.r.appSettings.SeedAll(ctx); err != nil {
		slog.Warn("seed settings", "err", err)
	}

	if n, err := a.s.auth.PurgeExpiredSessions(ctx); err != nil {
		slog.Warn("purge sessions", "err", err)
	} else if n > 0 {
		slog.Info("purged expired sessions", "count", n)
	}

	// Reload after the seed so the notifier picks up the persisted EMAIL
	// row rather than the state it was constructed with. ADR-0020.
	if err := a.s.notifier.Reload(ctx); err != nil {
		slog.Warn("email subsystem disabled — reload failed", "err", err)
	} else if !a.s.notifier.Enabled() {
		slog.Info("email subsystem disabled — configure under admin settings to enable")
	}

	if err := a.startQueue(ctx); err != nil {
		return err
	}

	// Staging for abandoned failed or cancelled runs is dead weight after
	// a week. Hourly loop, same shape as the missing-file and
	// orphaned-key sweepers.
	a.goBackground("audiobook staging sweep", func(ctx context.Context) {
		a.w.staging.LoopSweep(ctx, a.r.audiobook)
	})

	// Requeue anything still mid-flight from a previous process.
	ingest.DiscoverOnStartup(ctx, a.r.bdrop, a.queue)

	// Boot-time files backfill: hash any files rows that are still missing a
	// content_hash. Runs in the background so it doesn't block startup.
	// 1-hour timeout is generous; real deployments have hundreds of files at most.
	a.goBackground("files backfill", func(ctx context.Context) {
		slog.Info("files backfill starting")
		// The ceiling derives from the application context rather than
		// replacing it: it bounds a pathological library, it is not a
		// second lifetime that outlives shutdown.
		backfillCtx, cancel := context.WithTimeout(ctx, 1*time.Hour)
		defer cancel()
		if err := task.RunFilesBackfill(backfillCtx, task.FilesBackfillDeps{
			Files:    a.r.file,
			LibStore: a.s.libStore,
		}); err != nil {
			slog.Warn("files backfill", "err", err)
		}
	})

	// Boot-time covers backfill: migrate legacy book-id-keyed covers to the
	// hash-keyed path (covers/<hash[0:2]>/<hash>.<ext>). Idempotent and
	// best-effort — errors per-book are logged and retried on next boot.
	a.goBackground("covers backfill", func(ctx context.Context) {
		backfillCoversCtx, cancel := context.WithTimeout(ctx, 1*time.Hour)
		defer cancel()
		if err := task.RunCoversBackfill(backfillCoversCtx, task.CoversBackfillDeps{
			Books:  a.r.book,
			Covers: a.w.covers,
		}); err != nil {
			slog.Warn("covers backfill", "err", err)
		}
	})

	// Missing-files purge sweeper: deletes files rows whose missing_since
	// is older than 24h. Runs hourly until the application shuts down.
	a.goBackground("missing purge", func(ctx context.Context) {
		task.LoopMissingPurge(ctx, a.r.file, time.Hour)
	})

	// Orphaned-keys sweeper: drains pending_orphans rows whose grace
	// window has passed, deleting the underlying storage keys. Sources:
	// post-rename old keys (full RenameGrace) and rollback half-rename
	// new keys (RenameRollbackGrace). ADR-0005.
	a.goBackground("orphaned keys", func(ctx context.Context) {
		task.LoopOrphanedKeys(ctx, task.OrphanedKeysDeps{
			Orphans:  a.r.pendingOrphans,
			Libs:     a.r.lib,
			Resolver: a.w.resolver,
		}, time.Hour)
	})

	// File watcher goroutine.
	a.goBackground("bookdrop watcher", a.watcher.Run)
	a.goBackground("s3 bookdrop watcher", a.s3Watcher.Run)

	return nil
}

// startQueue resolves the deferred enqueuer and starts the queue.
//
// These are one operation, not two steps that happen to be adjacent.
// River begins draining jobs the moment Start returns, so a leftover job
// can reach a worker immediately; if enq were still unresolved at that
// instant the worker's follow-on dispatch would fail with ErrNoQueue and
// the run would stall with no error to explain it (#184). Holding both
// calls in one method is what keeps a later edit from putting anything
// between them.
func (a *App) startQueue(ctx context.Context) error {
	a.w.enq.Resolve(a.queue)
	if err := a.queue.Start(ctx); err != nil {
		return fmt.Errorf("queue start: %w", err)
	}
	return nil
}

// backgroundStopTimeout bounds how long Close waits for the tasks
// goBackground registered.
//
// Every one of them is a cooperative loop that checks the context
// between items, so a healthy shutdown finishes in milliseconds and
// never spends this. The bound exists for the task that is wedged in a
// syscall — a storage backend that stopped answering — where the cost
// must be a slow shutdown rather than one that never completes. 5s is
// generous for cooperative work and keeps Close's worst case at 20s,
// this plus the 15s drain below, inside the grace period an orchestrator
// allows before it sends SIGKILL.
const backgroundStopTimeout = 5 * time.Second

// Close is the single shutdown path: stop the background tasks, drain
// the queue, then close the database. Errors are joined rather than
// returned at the first failure — a queue that refuses to drain must not
// leave the pool open.
func (a *App) Close(ctx context.Context) error {
	var errs []error

	// Background work goes first because all of it holds repos backed by
	// the pool closed at the bottom of this function. Close cancels the
	// context itself rather than trusting the caller to have cancelled
	// the one it passed Start: that is what makes this the single
	// shutdown path rather than the second half of one.
	//
	// A nil cancel means Build ran but Start did not, so there is
	// nothing registered and nothing to wait for.
	if a.bg.cancel != nil {
		a.bg.cancel()
	}
	stopped := make(chan struct{})
	go func() {
		a.bg.wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(backgroundStopTimeout):
		errs = append(errs, fmt.Errorf("background tasks still running after %s", backgroundStopTimeout))
	}

	// The 15s budget is detached from ctx because ctx is normally the
	// signal context, already cancelled by the time we get here — a
	// derived context would expire instantly and the drain would be no
	// drain at all. WithoutCancel keeps the values (trace context) and
	// drops only the cancellation.
	//
	// river.Client.Stop tolerates a client that was never started
	// (returns nil rather than blocking or erroring), so this runs
	// unconditionally even when Start failed before reaching the queue.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := a.queue.Stop(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("queue stop: %w", err))
	}

	if err := a.w.dbh.Close(); err != nil {
		errs = append(errs, fmt.Errorf("db close: %w", err))
	}

	return errors.Join(errs...)
}

// RunMigrations applies every pending schema migration using the embedded
// migration files. Idempotent — a no-op when the DB is already up-to-date.
//
// Used by Build and by `import-sqlite` — the dedicated-connection dance
// below is load bearing, so both paths must go through here rather than
// reaching for migrator.New directly.
func RunMigrations(d *db.DB) error {
	// Open a short-lived, dedicated connection for the migrator so that
	// m.Close() (which golang-migrate's Postgres driver calls sql.DB.Close on)
	// does not close the shared application pool. Skipping this deadlocks
	// db.Close(): the migrator keeps a pool connection checked out and
	// pgxpool.Close waits for it forever.
	migDB, err := d.OpenMigrationDB()
	if err != nil {
		return fmt.Errorf("migration db: %w", err)
	}

	m, err := migrator.New(d.Dialect, migDB)
	if err != nil {
		// migDB not yet owned by migrator — close it ourselves.
		_ = migDB.Close()
		return err
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate source close", "err", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate db close", "err", dbErr)
		}
	}()
	v, dirty, _ := m.Version()
	if err := migrator.Up(m); err != nil {
		return err
	}
	newV, _, _ := m.Version()
	if newV != v {
		slog.Info("migrations applied", "from", v, "to", newV, "dirty", dirty)
	}
	return nil
}
