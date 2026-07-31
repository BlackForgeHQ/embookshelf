# Settings › Instance Status Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Settings **About** panel with an **Instance** panel — a five-row status board answering "is this instance healthy, and if not, which subsystem is wrong?"

**Architecture:** The backend adds what the app currently cannot observe at all (connection-pool pressure, schema version, queue attachment, build commit, uptime) as new fields on the existing admin `GET /api/v1/settings/instance` endpoint, behind a narrow `platformProbe` seam so handler tests need no database. The frontend derives every row through pure functions in `instanceStatus.ts` — that is where the logic and the tests live — and a thin `InstancePanel` renders them as a ledger.

**Tech Stack:** Go 1.x + Gin + pgx/pgxpool + golang-migrate; React 19 + TanStack Router/Query + Tailwind v4 + Vitest + Testing Library.

## Global Constraints

- **Postgres only (ADR-0023).** No dialect branches, no SQLite migrations. `db.DB.PG` is non-nil in every serving process.
- **Every new `.go` file starts with `// SPDX-License-Identifier: AGPL-3.0-or-later`** as its first line, followed by a blank line and the `package` clause.
- **Go tests use the stdlib `testing` package only.** No testify, no gomega. Table-driven where there is more than one case.
- **Postgres-backed Go tests require `TEST_DATABASE_URL`** and must fail (not skip) when it is unset — follow `internal/repo/repotest/repotest.go:75`.
- **UI tests are Vitest.** A test needing a DOM starts with the `// @vitest-environment jsdom` pragma on line 1.
- **Timestamps cross the wire as RFC3339 strings**, matching `providerInfoDTO`. The client relativizes them.
- **Never edit** `internal/staticfs/dist/` or `ui/src/routeTree.gen.ts` — both are generated.
- **Verification commands:** `make test` (Go), `make ui-test` (Vitest), `make ui-typecheck` (tsc), `make ui-lint` (Biome), `make go-lint` (golangci-lint).
- **Commit messages:** no `Co-Authored-By` or other trailers.
- Work happens on branch `spec/instance-status-panel`, which already holds the design doc.

---

## File Structure

**Backend — create**

- `internal/migrator/current.go` — reads the recorded migration version and dirty flag. Lives in `migrator` because `schema_migrations` is that package's table.
- `internal/migrator/current_test.go`
- `internal/service/platform.go` — `PlatformService.Probe`: the one place that turns a `*db.DB` into process-health facts.
- `internal/handler/platform.go` — the `platformProbe` interface (the handler tier's narrow view of the above).
- `internal/handler/instance_test.go` — `InstanceInfo` driven with a fake probe.
- `internal/handler/health_test.go`

**Backend — modify**

- `internal/handler/instance.go` — new DTO fields, new nil guards.
- `internal/handler/handler.go` — `Handler.startedAt`, `Handler.platform`; `PlatformDeps` + `NewPlatformDeps` gain the probe.
- `internal/handler/health.go` — real database ping.
- `internal/app/app.go:507` — pass the probe at the composition root.

**Frontend — create**

- `ui/src/components/settings/instanceStatus.ts` — `StatusRow`, `StatusTone`, and the five pure derivers. **All the logic lives here.**
- `ui/src/components/settings/__tests__/instanceStatus.test.ts`
- `ui/src/components/settings/StatusLedger.tsx` — presentational two-line ledger row list.
- `ui/src/components/settings/InstancePanel.tsx` — three queries, map to rows, render (replaces `AboutPanel.tsx`).
- `ui/src/components/settings/__tests__/InstancePanel.test.tsx`

**Frontend — modify**

- `ui/src/api/settings.ts` — `InstanceInfo` gains the new fields.
- `ui/src/lib/format.ts` — gains `relativeTime`.
- `ui/src/components/settings/ProvidersPanel.tsx` — imports `relativeTime` instead of defining it.
- `ui/src/components/settings/sections.tsx` — key/label/panel rename.
- `ui/src/components/__tests__/SettingsShell.test.tsx` — mock path and label update.

**Frontend — delete**

- `ui/src/components/settings/AboutPanel.tsx`

---

### Task 1: `migrator.Current`

Reads the migration version the database records. On demand, not captured at boot — boot capture is wrong when `MigrateOnStart` is false or someone migrates out of band.

**Files:**
- Create: `internal/migrator/current.go`
- Test: `internal/migrator/current_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func migrator.Current(ctx context.Context, sqlDB *sql.DB) (version int, dirty bool, err error)`. A database with no `schema_migrations` row returns `(0, false, nil)` — a fresh, never-migrated database is a fact, not an error.

- [ ] **Step 1: Write the failing test**

Create `internal/migrator/current_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/blackforge/embookshelf/internal/migrator"
)

// openTestDB connects to the Postgres named by TEST_DATABASE_URL. A
// missing variable is a hard failure rather than a skip, matching
// repotest: a silently skipped migration test is how a broken schema
// read reaches production.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal(`TEST_DATABASE_URL is not set — this test needs Postgres.

  export TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'`)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

func TestCurrentReportsRecordedVersion(t *testing.T) {
	ctx := context.Background()
	sqlDB := openTestDB(t)

	// A scratch schema keeps the assertion independent of whatever the
	// shared test database happens to be migrated to.
	if _, err := sqlDB.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS migrator_current_test`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sqlDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS migrator_current_test CASCADE`)
	})
	if _, err := sqlDB.ExecContext(ctx, `SET search_path TO migrator_current_test`); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	// No table at all — the caller gets an error, not a silent zero.
	if _, _, err := migrator.Current(ctx, sqlDB); err == nil {
		t.Error("Current with no schema_migrations table returned nil error; want a read failure")
	}

	if _, err := sqlDB.ExecContext(ctx, `
		CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// An empty table means "never migrated", which is a fact.
	v, dirty, err := migrator.Current(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Current on empty table: %v", err)
	}
	if v != 0 || dirty {
		t.Errorf("Current on empty table = (%d, %v), want (0, false)", v, dirty)
	}

	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, dirty) VALUES (38, true)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	v, dirty, err = migrator.Current(ctx, sqlDB)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if v != 38 {
		t.Errorf("version = %d, want 38", v)
	}
	if !dirty {
		t.Error("dirty = false, want true — a dirty row must survive the read")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' go test ./internal/migrator/ -run TestCurrentReportsRecordedVersion -v`

Expected: FAIL to compile — `undefined: migrator.Current`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/migrator/current.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Current reports the migration version the database records and whether
// the last attempt left it dirty.
//
// Read on demand rather than captured at boot: app.RunMigrations already
// reads the version and discards it, but that value is stale the moment
// MigrateOnStart is false, and absent entirely when an operator migrates
// out of band with the CLI.
//
// No rows means the table exists but nothing has been applied — a fresh
// database, which is a fact rather than a failure, so it reports version
// zero. A missing table is a genuine read failure and is returned as one.
func Current(ctx context.Context, sqlDB *sql.DB) (version int, dirty bool, err error) {
	row := sqlDB.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`)
	if err := row.Scan(&version, &dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("read schema_migrations: %w", err)
	}
	return version, dirty, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `TEST_DATABASE_URL='postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable' go test ./internal/migrator/ -run TestCurrentReportsRecordedVersion -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/migrator/current.go internal/migrator/current_test.go
git commit -m "feat(migrator): read the recorded schema version on demand

app.RunMigrations reads the version at boot and throws it away, which is
stale as soon as MigrateOnStart is false and absent when an operator
migrates with the CLI. Current asks the database instead."
```

---

### Task 2: `service.PlatformService`

The one place that turns a `*db.DB` into process-health facts.

**Files:**
- Create: `internal/service/platform.go`

**Interfaces:**
- Consumes: `migrator.Current` (Task 1).
- Produces:
  - `type service.SchemaStatus struct { Version int; Dirty bool }`
  - `type service.PlatformStatus struct { PingMs float64; InUse, Idle, MaxConns int32; Schema *SchemaStatus }`
  - `func service.NewPlatformService(d *db.DB) *service.PlatformService`
  - `func (*service.PlatformService) Probe(ctx context.Context) (service.PlatformStatus, error)`

`Probe` returns an error **only** when there is no usable handle or the ping fails. A `schema_migrations` read failure leaves `Schema` nil and is logged — the pool facts are still true and a caller must not lose them to an unrelated failure.

There is no unit test for this task: every line of it is a `pgxpool` or `database/sql` call, so a test would assert that pgx works. Its behaviour is covered where it matters — Task 1 tests the schema read, Task 4 tests the handler's mapping through a fake, and Task 3 tests the ping's effect on `/healthcheck`.

- [ ] **Step 1: Write the implementation**

Create `internal/service/platform.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
)

// SchemaStatus is what schema_migrations records.
type SchemaStatus struct {
	Version int
	Dirty   bool
}

// PlatformStatus is the process-health picture: how hard the connection
// pool is working, how quickly the database answers, and which schema
// version is in force.
//
// Deliberately not a "reachable" boolean. If Postgres is unreachable the
// admin endpoint that would report it never answers — session lookup
// fails first — so a green "reachable" row is tautological. What can vary
// while the app still serves is pool pressure and latency.
type PlatformStatus struct {
	// PingMs is the round trip for a single pool ping, in milliseconds.
	PingMs float64
	// InUse, Idle and MaxConns describe pool pressure. InUse at MaxConns
	// is a request queue nothing else in the app surfaces.
	InUse    int32
	Idle     int32
	MaxConns int32
	// Schema is nil when schema_migrations could not be read. The pool
	// facts above are still valid in that case.
	Schema *SchemaStatus
}

// PlatformService answers process-health questions about the database
// handle. It holds the whole *db.DB because it is the only consumer that
// legitimately needs both halves — the pgx pool for statistics and the
// database/sql handle for the migration table.
type PlatformService struct {
	db *db.DB
}

func NewPlatformService(d *db.DB) *PlatformService {
	return &PlatformService{db: d}
}

// ErrNoDatabaseHandle is what Probe returns when it was built without a
// usable Postgres handle. Distinct from a ping failure so a caller can
// tell "never wired" from "wired and down".
var ErrNoDatabaseHandle = errors.New("platform probe has no Postgres handle")

// Probe measures the database. It fails only when there is no handle or
// the ping does not come back; a schema read that fails leaves Schema nil
// and is logged, because losing the pool numbers to an unrelated failure
// would hide the thing most likely to be wrong.
func (s *PlatformService) Probe(ctx context.Context) (PlatformStatus, error) {
	if s == nil || s.db == nil || s.db.PG == nil {
		return PlatformStatus{}, ErrNoDatabaseHandle
	}

	start := time.Now()
	if err := s.db.PG.Ping(ctx); err != nil {
		return PlatformStatus{}, err
	}
	stat := s.db.PG.Stat()
	out := PlatformStatus{
		PingMs:   float64(time.Since(start).Microseconds()) / 1000,
		InUse:    stat.AcquiredConns(),
		Idle:     stat.IdleConns(),
		MaxConns: stat.MaxConns(),
	}

	version, dirty, err := migrator.Current(ctx, s.db.SQL)
	if err != nil {
		slog.Warn("read schema version", "err", err)
		return out, nil
	}
	out.Schema = &SchemaStatus{Version: version, Dirty: dirty}
	return out, nil
}
```

- [ ] **Step 2: Verify it builds and nothing imports in a cycle**

Run: `go build ./... && go vet ./internal/service/`

Expected: no output. (`migrator` imports `db`; neither imports `service`, so there is no cycle.)

- [ ] **Step 3: Commit**

```bash
git add internal/service/platform.go
git commit -m "feat(service): probe pool pressure, latency and schema version

One place turns the database handle into process-health facts. Not a
reachable flag: if Postgres is down the admin endpoint that would report
it never answers, so what is worth measuring is pressure and latency."
```

---

### Task 3: Wire the probe into the handler and make `/healthcheck` honest

`/api/v1/healthcheck` currently returns a static `{"status":"ok"}` — it lies to any orchestrator watching it.

**Files:**
- Create: `internal/handler/platform.go`
- Create: `internal/handler/health_test.go`
- Modify: `internal/handler/handler.go:21-83` (Handler struct), `:97-108` (`PlatformDeps`), `:238-260` (`New`)
- Modify: `internal/handler/health.go`
- Modify: `internal/app/app.go:507`

**Interfaces:**
- Consumes: `service.PlatformStatus`, `service.PlatformService` (Task 2).
- Produces:
  - `type platformProbe interface { Probe(ctx context.Context) (service.PlatformStatus, error) }` (unexported, package `handler`)
  - `Handler.platform platformProbe` and `Handler.startedAt time.Time`
  - `handler.NewPlatformDeps(cfg config.Config, static embed.FS, version, commit string, hub *sse.Hub, platform platformProbe) PlatformDeps` — **the signature gains a sixth positional argument**, so the composition root fails to build until it supplies one.

- [ ] **Step 1: Write the failing test**

Create `internal/handler/health_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/service"
)

// fakeProbe stands in for service.PlatformService so the handler tier is
// testable without a Postgres.
type fakeProbe struct {
	status service.PlatformStatus
	err    error
}

func (f *fakeProbe) Probe(context.Context) (service.PlatformStatus, error) {
	return f.status, f.err
}

var _ platformProbe = (*fakeProbe)(nil)

func TestHealthcheckReportsDatabaseFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name  string
		probe platformProbe
		want  int
	}{
		{
			name:  "healthy database",
			probe: &fakeProbe{status: service.PlatformStatus{PingMs: 1.5, MaxConns: 25}},
			want:  http.StatusOK,
		},
		{
			name:  "ping fails",
			probe: &fakeProbe{err: errors.New("connection refused")},
			want:  http.StatusServiceUnavailable,
		},
		{
			name:  "no probe wired",
			probe: nil,
			want:  http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{platform: tt.probe}

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/healthcheck", nil)

			h.Healthcheck(c)

			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestHealthcheckReportsDatabaseFailure -v`

Expected: FAIL to compile — `undefined: platformProbe` and `Handler has no field platform`.

- [ ] **Step 3: Create the seam**

Create `internal/handler/platform.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"

	"github.com/blackforge/embookshelf/internal/service"
)

// platformProbe is the handler tier's view of process health, declared
// here for the same reason appSettingsStore is declared in settings.go:
// an interface the endpoints can be driven against, rather than a
// *db.DB, which would drag a live Postgres into every test of them.
//
// One method on purpose. The handler asks "how is the platform doing"
// and does not get to reach past that into the pool or the schema table.
type platformProbe interface {
	Probe(ctx context.Context) (service.PlatformStatus, error)
}

var _ platformProbe = (*service.PlatformService)(nil)
```

- [ ] **Step 4: Add the fields and the dependency**

In `internal/handler/handler.go`, add `"time"` to the imports. Inside the `Handler` struct, after the `queue queue.Client` field, add:

```go
	// platform answers the Instance panel's process-health questions and
	// backs the healthcheck's database ping. nil disables both: the
	// healthcheck reports unavailable and the panel shows an unknown row.
	platform platformProbe
	// startedAt is process start, for the Instance panel's uptime. Set in
	// New rather than at package init so a test can construct a Handler
	// with a chosen instant.
	startedAt time.Time
```

Change `PlatformDeps` to carry it:

```go
type PlatformDeps struct {
	cfg      config.Config
	static   embed.FS
	version  string
	commit   string
	hub      *sse.Hub
	platform platformProbe
}

// NewPlatform builds the platform group.
func NewPlatformDeps(
	cfg config.Config,
	static embed.FS,
	version, commit string,
	hub *sse.Hub,
	platform platformProbe,
) PlatformDeps {
	return PlatformDeps{
		cfg: cfg, static: static, version: version, commit: commit,
		hub: hub, platform: platform,
	}
}
```

In `New`, change the platform line to:

```go
		cfg: p.cfg, static: p.static, version: p.version, commit: p.commit, hub: p.hub,
		platform: p.platform, startedAt: time.Now(),
```

- [ ] **Step 5: Make the healthcheck honest**

Replace the whole body of `internal/handler/health.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Healthcheck answers whether this process can serve. It used to return
// a static ok, which told an orchestrator nothing it did not already know
// from the port being open — a process with a dead database passed.
func (h *Handler) Healthcheck(c *gin.Context) {
	if h.platform == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "no database probe configured",
		})
		return
	}
	if _, err := h.platform.Probe(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"reason": "database unreachable",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
```

The error detail is deliberately not echoed: `/healthcheck` is unauthenticated, and a database DSN or hostname in a connection error is not something to hand out.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/handler/ -run TestHealthcheckReportsDatabaseFailure -v`

Expected: PASS, all three subtests.

- [ ] **Step 7: Fix the composition root**

Run: `go build ./...`

Expected: FAIL at `internal/app/app.go:507` — not enough arguments to `NewPlatformDeps`.

In `internal/app/app.go`, change line 507 to:

```go
		handler.NewPlatformDeps(cfg, staticfs.FS, version, commit, hub, service.NewPlatformService(dbh)),
```

`service` and `dbh` are both already in scope in that function.

- [ ] **Step 8: Verify the build and the package**

Run: `go build ./... && go test ./internal/handler/`

Expected: build clean, handler package PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/handler/platform.go internal/handler/health.go internal/handler/health_test.go internal/handler/handler.go internal/app/app.go
git commit -m "feat(handler): ping the database in the healthcheck

The endpoint returned a static ok, so a process whose database had gone
away still passed — it told an orchestrator nothing the open port had
not. It now goes through the platform probe, which arrives as a required
positional seam so the composition root cannot forget it."
```

---

### Task 4: New `instanceInfoDTO` fields

**Files:**
- Modify: `internal/handler/instance.go:26-34` (DTO), `:178-210` (handler)
- Create: `internal/handler/instance_test.go`

**Interfaces:**
- Consumes: `platformProbe`, `Handler.platform`, `Handler.startedAt` (Task 3); `service.PlatformStatus` (Task 2). The `fakeProbe` type from `health_test.go` is reused — same package, do not redeclare it.
- Produces: the JSON contract Task 6 types in TypeScript:

```json
{
  "commit": "a3f19c2",
  "startedAt": "2026-07-31T09:14:00Z",
  "queueAttached": true,
  "database": { "pingMs": 1.4, "inUse": 3, "idle": 5, "maxConns": 25 },
  "schema": { "version": 38, "dirty": false }
}
```

`database` and `schema` are both omitted when unknown — `omitempty` on pointer fields.

- [ ] **Step 1: Write the failing test**

Create `internal/handler/instance_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/queue"
	"github.com/blackforge/embookshelf/internal/service"
)

// instanceBody is the subset of the payload these tests assert on.
type instanceBody struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	StartedAt     string `json:"startedAt"`
	QueueAttached bool   `json:"queueAttached"`
	Database      *struct {
		PingMs   float64 `json:"pingMs"`
		InUse    int32   `json:"inUse"`
		Idle     int32   `json:"idle"`
		MaxConns int32   `json:"maxConns"`
	} `json:"database"`
	Schema *struct {
		Version int  `json:"version"`
		Dirty   bool `json:"dirty"`
	} `json:"schema"`
}

func getInstance(t *testing.T, h *Handler) (int, instanceBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/instance", nil)

	h.InstanceInfo(c)

	var body instanceBody
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

func TestInstanceInfoReportsPlatformFacts(t *testing.T) {
	started := time.Date(2026, 7, 31, 9, 14, 0, 0, time.UTC)
	h := &Handler{
		version:   "1.4.2",
		commit:    "a3f19c2",
		startedAt: started,
		queue:     stubQueue{},
		platform: &fakeProbe{status: service.PlatformStatus{
			PingMs:   1.4,
			InUse:    3,
			Idle:     5,
			MaxConns: 25,
			Schema:   &service.SchemaStatus{Version: 38, Dirty: false},
		}},
	}

	code, body := getInstance(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body.Commit != "a3f19c2" {
		t.Errorf("commit = %q, want %q — it is on the Handler and was never serialized", body.Commit, "a3f19c2")
	}
	if body.StartedAt != started.Format(time.RFC3339) {
		t.Errorf("startedAt = %q, want %q", body.StartedAt, started.Format(time.RFC3339))
	}
	if !body.QueueAttached {
		t.Error("queueAttached = false despite a queue being wired")
	}
	if body.Database == nil {
		t.Fatal("database is absent despite a successful probe")
	}
	if body.Database.InUse != 3 || body.Database.MaxConns != 25 {
		t.Errorf("pool = %d/%d, want 3/25", body.Database.InUse, body.Database.MaxConns)
	}
	if body.Schema == nil || body.Schema.Version != 38 {
		t.Errorf("schema = %+v, want version 38", body.Schema)
	}
}

// A failing probe must degrade the payload, not the request. The panel
// that reads this endpoint owns the whole About surface — losing the
// version and the counts because the pool was briefly busy would turn a
// warning row into a blank page.
func TestInstanceInfoSurvivesProbeFailure(t *testing.T) {
	h := &Handler{
		version:   "1.4.2",
		startedAt: time.Now(),
		platform:  &fakeProbe{err: errors.New("connection refused")},
	}

	code, body := getInstance(t, h)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a probe failure must not fail the request", code)
	}
	if body.Version != "1.4.2" {
		t.Errorf("version = %q, want 1.4.2 — the rest of the payload must survive", body.Version)
	}
	if body.Database != nil {
		t.Errorf("database = %+v, want absent when the probe failed", body.Database)
	}
	if body.QueueAttached {
		t.Error("queueAttached = true with no queue wired")
	}
}

// stubQueue satisfies queue.Client so "a queue is attached" is
// expressible without starting River.
type stubQueue struct{}

func (stubQueue) Enqueue(context.Context, queue.Job) error { return nil }
func (stubQueue) Start(context.Context) error              { return nil }
func (stubQueue) Stop(context.Context) error               { return nil }
```

**Before running:** open `internal/queue/queue.go:51` and read the real `queue.Client` interface. Make `stubQueue`'s method set match it exactly — signatures, names, and the `context` import. If the interface has moved on from `Enqueue/Start/Stop`, fix the stub, not the interface.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestInstanceInfo -v`

Expected: FAIL — `body.Commit` empty, `body.Database` nil, `startedAt` empty.

- [ ] **Step 3: Extend the DTO**

In `internal/handler/instance.go`, add the new fields to `instanceInfoDTO` and the two new nested types beneath it:

```go
type instanceInfoDTO struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	GoVersion string `json:"goVersion"`
	// StartedAt is process start as RFC3339, so the client relativizes it
	// into an uptime the same way it does provider health timestamps.
	StartedAt           string            `json:"startedAt"`
	AllowedOrigins      []string          `json:"allowedOrigins"`
	BookDropPath        string            `json:"bookDropPath"`
	DataPath            string            `json:"dataPath"`
	MigrateOnStart      bool              `json:"migrateOnStart"`
	EnrichmentProviders []providerInfoDTO `json:"enrichmentProviders"`
	Counts              instanceCountsDTO `json:"counts"`
	// QueueAttached is false when no worker pool was wired, which means
	// no scan, enrichment or narration job is ever dispatched. Nothing
	// else in the interface says so — the seam surfaces it only as a 503
	// on the endpoint that tried.
	QueueAttached bool `json:"queueAttached"`
	// Database and Schema are nil when the probe could not answer. The
	// panel renders an unknown row rather than pretending.
	Database *instanceDatabaseDTO `json:"database,omitempty"`
	Schema   *instanceSchemaDTO   `json:"schema,omitempty"`
}

type instanceDatabaseDTO struct {
	PingMs   float64 `json:"pingMs"`
	InUse    int32   `json:"inUse"`
	Idle     int32   `json:"idle"`
	MaxConns int32   `json:"maxConns"`
}

type instanceSchemaDTO struct {
	Version int  `json:"version"`
	Dirty   bool `json:"dirty"`
}
```

- [ ] **Step 4: Fill them in, and guard the services**

Replace the body of `InstanceInfo` in `internal/handler/instance.go`:

```go
func (h *Handler) InstanceInfo(c *gin.Context) {
	ctx := c.Request.Context()

	providers := make([]providerInfoDTO, 0)
	if h.providerCfg != nil {
		infos, err := h.providerCfg.ListProviders(ctx)
		if err != nil {
			slog.Warn("list providers", "err", err)
		}
		for _, p := range infos {
			providers = append(providers, toProviderInfoDTO(p))
		}
	}

	counts := instanceCountsDTO{}
	if h.lib != nil {
		if libs, err := h.lib.List(ctx); err == nil {
			counts.Libraries = len(libs)
			for _, l := range libs {
				counts.Books += l.BookCount
			}
		}
	}
	if h.auth != nil {
		if users, err := h.auth.ListUsers(ctx); err == nil {
			counts.Users = len(users)
		}
	}

	out := instanceInfoDTO{
		Version:             h.appVersion(),
		Commit:              h.commit,
		GoVersion:           runtime.Version(),
		StartedAt:           h.startedAt.Format(time.RFC3339),
		AllowedOrigins:      h.cfg.AllowedOrigins,
		BookDropPath:        h.cfg.BookDropPath,
		DataPath:            h.cfg.DataPath.String(),
		MigrateOnStart:      h.cfg.MigrateOnStart,
		EnrichmentProviders: providers,
		Counts:              counts,
		QueueAttached:       h.queue != nil,
	}

	// A probe failure degrades the payload rather than the request: this
	// endpoint backs the whole Instance panel, and answering 500 because
	// the pool was busy would blank the version, the paths and the
	// provider health along with it.
	if h.platform != nil {
		if st, err := h.platform.Probe(ctx); err != nil {
			slog.Warn("platform probe", "err", err)
		} else {
			out.Database = &instanceDatabaseDTO{
				PingMs: st.PingMs, InUse: st.InUse, Idle: st.Idle, MaxConns: st.MaxConns,
			}
			if st.Schema != nil {
				out.Schema = &instanceSchemaDTO{Version: st.Schema.Version, Dirty: st.Schema.Dirty}
			}
		}
	}

	c.JSON(http.StatusOK, out)
}
```

The nil guards on `h.lib`, `h.auth` and `h.providerCfg` are what make a struct-literal `Handler` testable — the same gap `settings_providers_test.go:36` documents, where a nil seam reached a live request.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/handler/ -run TestInstanceInfo -v`

Expected: PASS, both tests.

- [ ] **Step 6: Run the whole Go suite**

Run: `make test`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/handler/instance.go internal/handler/instance_test.go
git commit -m "feat(handler): report commit, uptime, pool and schema on /settings/instance

Five facts the panel needs and no endpoint carried: the commit was on the
Handler and never serialized, uptime was nowhere, and pool pressure,
schema version and queue attachment were invisible outside the logs. A
probe failure degrades the payload instead of the request."
```

---

### Task 5: Extract `relativeTime`

**Files:**
- Modify: `ui/src/lib/format.ts`
- Modify: `ui/src/components/settings/ProvidersPanel.tsx:560-571`
- Test: `ui/src/lib/__tests__/format.test.ts` (create if absent; if present, append the describe block)

**Interfaces:**
- Produces: `export function relativeTime(ms: number): string` from `@/lib/format`. `0` returns `"—"`. A future timestamp returns `"in the future"`. Otherwise the largest whole unit under `s`/`m`/`h`/`d`, suffixed `" ago"`.

- [ ] **Step 1: Write the failing test**

Create or append to `ui/src/lib/__tests__/format.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest"

import { relativeTime } from "@/lib/format"

describe("relativeTime", () => {
  const now = Date.parse("2026-07-31T12:00:00Z")

  it("reports the largest whole unit", () => {
    vi.setSystemTime(now)
    expect(relativeTime(now - 5_000)).toBe("5s ago")
    expect(relativeTime(now - 90_000)).toBe("1m ago")
    expect(relativeTime(now - 3 * 3_600_000)).toBe("3h ago")
    expect(relativeTime(now - 2 * 86_400_000)).toBe("2d ago")
    vi.useRealTimers()
  })

  it("has an em dash for a missing timestamp and a phrase for a future one", () => {
    vi.setSystemTime(now)
    expect(relativeTime(0)).toBe("—")
    expect(relativeTime(now + 60_000)).toBe("in the future")
    vi.useRealTimers()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && bun run vitest run src/lib/__tests__/format.test.ts`

Expected: FAIL — `relativeTime` is not exported from `@/lib/format`.

- [ ] **Step 3: Move the function**

Append to `ui/src/lib/format.ts`:

```ts
/**
 * How long ago a timestamp was, in the largest whole unit.
 *
 * Lived privately inside ProvidersPanel until the Instance panel needed
 * the same phrasing three times — for uptime, for how stale the board is,
 * and for a failing provider's last error.
 */
export function relativeTime(ms: number): string {
  if (!ms) return "—"
  const diff = Date.now() - ms
  if (diff < 0) return "in the future"
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24)
  return `${d}d ago`
}
```

In `ui/src/components/settings/ProvidersPanel.tsx`, delete the local `function relativeTime(ms: number): string { … }` definition (around line 560) and add `relativeTime` to the existing import from `@/lib/format`, or add the import if there is none:

```ts
import { relativeTime } from "@/lib/format"
```

Leave the local `truncate` helper alone — it has one caller.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && bun run vitest run src/lib/__tests__/format.test.ts && bun run lint && bunx tsc --noEmit`

Expected: PASS, no lint errors, no type errors.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/format.ts ui/src/lib/__tests__/format.test.ts ui/src/components/settings/ProvidersPanel.tsx
git commit -m "refactor(ui): relativeTime moves to lib/format

Three more callers are about to want the same phrasing, and a second
private copy is how two of them drift apart."
```

---

### Task 6: `InstanceInfo` type gains the new fields

Small and mechanical, but it gates Task 7 — the derivers cannot be written against fields the type does not have.

**Files:**
- Modify: `ui/src/api/settings.ts:146-155`

**Interfaces:**
- Consumes: the JSON contract from Task 4.
- Produces:

```ts
export type InstanceDatabase = { pingMs: number; inUse: number; idle: number; maxConns: number }
export type InstanceSchema = { version: number; dirty: boolean }
```

plus `commit?`, `startedAt`, `queueAttached`, `database?`, `schema?` on `InstanceInfo`.

- [ ] **Step 1: Extend the type**

In `ui/src/api/settings.ts`, replace the `InstanceInfo` type:

```ts
// Connection-pool pressure and round-trip latency. Absent when the
// server could not probe its database.
export type InstanceDatabase = {
  pingMs: number
  inUse: number
  idle: number
  maxConns: number
}

// What schema_migrations records. Absent when it could not be read.
export type InstanceSchema = {
  version: number
  dirty: boolean
}

export type InstanceInfo = {
  version: string
  commit?: string
  goVersion: string
  /** Process start, RFC3339. Relativized client-side into an uptime. */
  startedAt: string
  allowedOrigins: Array<string>
  bookDropPath: string
  dataPath: string
  migrateOnStart: boolean
  enrichmentProviders: Array<ProviderInfo>
  counts: { users: number; libraries: number; books: number }
  /** False means no worker pool: no scan, enrichment or narration runs. */
  queueAttached: boolean
  database?: InstanceDatabase
  schema?: InstanceSchema
}
```

Leave `instanceInfoQuery` and its `INSTANCE_STALE_TIME` alone — the panel's Refresh button calls `refetch()`, which bypasses staleness.

- [ ] **Step 2: Verify types**

Run: `cd ui && bunx tsc --noEmit`

Expected: no errors. (`AboutPanel.tsx` reads only pre-existing fields, so it still compiles.)

- [ ] **Step 3: Commit**

```bash
git add ui/src/api/settings.ts
git commit -m "feat(ui): type the new instance platform fields"
```

---

### Task 7: The status derivers

**This is the task that carries the logic.** Everything after it is rendering.

**Files:**
- Create: `ui/src/components/settings/instanceStatus.ts`
- Test: `ui/src/components/settings/__tests__/instanceStatus.test.ts`

**Interfaces:**
- Consumes: `InstanceInfo`, `SettingsLibrary` from `@/api/settings` (Task 6); `BookDropItem` from `@/api/bookdrop`; `relativeTime` from `@/lib/format` (Task 5).
- Produces:
  - `export type StatusTone = "ok" | "warn" | "idle"`
  - `export type StatusRow = { key: string; label: string; state: string; tone: StatusTone; evidence?: string }`
  - `export type QueryState<T> = { data: T | undefined; error: { message: string } | null }`
  - `export function toQueryState<T>(q: { data: T | undefined; error: { message: string } | null }): QueryState<T>`
  - `export function databaseRow(q: QueryState<InstanceInfo>): StatusRow`
  - `export function queueRow(q: QueryState<InstanceInfo>): StatusRow`
  - `export function providersRow(q: QueryState<InstanceInfo>): StatusRow`
  - `export function librariesRow(q: QueryState<Array<SettingsLibrary>>): StatusRow`
  - `export function bookdropRow(q: QueryState<Array<BookDropItem>>): StatusRow`
  - `export const POOL_PRESSURE_RATIO = 0.8`, `export const SLOW_PING_MS = 250`

- [ ] **Step 1: Write the failing test**

Create `ui/src/components/settings/__tests__/instanceStatus.test.ts`:

```ts
import { describe, expect, it } from "vitest"

import type { InstanceInfo, SettingsLibrary } from "@/api/settings"
import type { BookDropItem } from "@/api/bookdrop"
import {
  bookdropRow,
  databaseRow,
  librariesRow,
  providersRow,
  queueRow,
} from "@/components/settings/instanceStatus"

function info(patch: Partial<InstanceInfo> = {}): InstanceInfo {
  return {
    version: "1.4.2",
    goVersion: "go1.23.4",
    startedAt: "2026-07-31T09:00:00Z",
    allowedOrigins: [],
    bookDropPath: "/data/bookdrop",
    dataPath: "/data",
    migrateOnStart: true,
    enrichmentProviders: [],
    counts: { users: 2, libraries: 1, books: 40 },
    queueAttached: true,
    database: { pingMs: 1.4, inUse: 3, idle: 5, maxConns: 25 },
    schema: { version: 38, dirty: false },
    ...patch,
  }
}

function library(patch: Partial<SettingsLibrary> = {}): SettingsLibrary {
  return {
    id: "lib-1",
    name: "Fiction",
    slug: "fiction",
    bookCount: 40,
    createdAt: "2026-01-01T00:00:00Z",
    path: "/data/fiction",
    lastScannedAt: "2026-07-31T08:00:00Z",
    fileCount: 40,
    discoveredCount: 40,
    ...patch,
  }
}

function dropItem(patch: Partial<BookDropItem> = {}): BookDropItem {
  return {
    id: "bd-1",
    filename: "book.epub",
    path: "/data/bookdrop/book.epub",
    fileSize: 1024,
    format: "epub",
    state: "ready",
    progress: 100,
    hasCover: false,
    discoveredAt: "2026-07-31T09:00:00Z",
    updatedAt: "2026-07-31T09:00:00Z",
    ...patch,
  }
}

const pending = { data: undefined, error: null }
const failed = { data: undefined, error: { message: "network down" } }

describe("unresolved queries", () => {
  it("shows checking while pending", () => {
    expect(databaseRow(pending)).toMatchObject({ state: "checking…", tone: "idle" })
    expect(librariesRow(pending)).toMatchObject({ state: "checking…", tone: "idle" })
  })

  // A board that hides what it could not check reads as all-clear.
  it("warns — never hides — when the query failed", () => {
    expect(bookdropRow(failed)).toMatchObject({
      state: "unknown",
      tone: "warn",
      evidence: "network down",
    })
  })
})

describe("databaseRow", () => {
  it("is healthy on a quiet pool and a clean schema", () => {
    const row = databaseRow({ data: info(), error: null })
    expect(row.tone).toBe("ok")
    expect(row.evidence).toContain("schema 38, clean")
    expect(row.evidence).toContain("pool 3 of 25")
  })

  it("warns on a dirty schema even when the pool is quiet", () => {
    const row = databaseRow({ data: info({ schema: { version: 38, dirty: true } }), error: null })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("Schema dirty")
  })

  it("warns when the pool is near capacity", () => {
    const row = databaseRow({
      data: info({ database: { pingMs: 1, inUse: 20, idle: 0, maxConns: 25 } }),
      error: null,
    })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("Pool under pressure")
  })

  it("warns on a slow ping", () => {
    const row = databaseRow({
      data: info({ database: { pingMs: 800, inUse: 1, idle: 9, maxConns: 25 } }),
      error: null,
    })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("Slow")
  })

  it("warns when the server could not probe at all", () => {
    const row = databaseRow({
      data: info({ database: undefined, schema: undefined }),
      error: null,
    })
    expect(row).toMatchObject({ state: "unknown", tone: "warn" })
  })
})

describe("queueRow", () => {
  it("is ok when attached", () => {
    expect(queueRow({ data: info(), error: null })).toMatchObject({
      state: "Attached",
      tone: "ok",
    })
  })

  it("warns and says what stops when detached", () => {
    const row = queueRow({ data: info({ queueAttached: false }), error: null })
    expect(row.tone).toBe("warn")
    expect(row.evidence).toContain("narration")
  })
})

describe("providersRow", () => {
  it("is idle when none are enabled", () => {
    expect(providersRow({ data: info(), error: null })).toMatchObject({
      state: "None enabled",
      tone: "idle",
    })
  })

  it("ignores a disabled provider that is failing", () => {
    const row = providersRow({
      data: info({
        enrichmentProviders: [
          { id: "a", name: "A", enabled: true, external: true, lastSuccessAt: "2026-07-31T09:00:00Z" },
          { id: "b", name: "B", enabled: false, external: true, lastErrorAt: "2026-07-31T09:30:00Z" },
        ],
      }),
      error: null,
    })
    expect(row).toMatchObject({ state: "1 enabled", tone: "ok" })
  })

  // A provider is failing when its last error is newer than its last
  // success — not merely when it has ever errored.
  it("counts a provider whose error is newer than its success", () => {
    const row = providersRow({
      data: info({
        enrichmentProviders: [
          { id: "a", name: "Open Library", enabled: true, external: true, lastSuccessAt: "2026-07-31T09:30:00Z", lastErrorAt: "2026-07-31T09:00:00Z" },
          { id: "b", name: "Google Books", enabled: true, external: true, lastSuccessAt: "2026-07-31T08:00:00Z", lastErrorAt: "2026-07-31T09:40:00Z", lastError: "429 rate limited" },
        ],
      }),
      error: null,
    })
    expect(row.tone).toBe("warn")
    expect(row.state).toBe("1 of 2 failing")
    expect(row.evidence).toContain("Google Books")
    expect(row.evidence).toContain("429 rate limited")
  })
})

describe("librariesRow", () => {
  it("is idle when a library has never been scanned", () => {
    const row = librariesRow({
      data: [library(), library({ id: "lib-2", name: "Comics", lastScannedAt: null })],
      error: null,
    })
    expect(row).toMatchObject({ state: "1 of 2 scanned", tone: "idle" })
    expect(row.evidence).toContain("Comics")
  })

  it("is ok when every library has been scanned", () => {
    expect(librariesRow({ data: [library()], error: null })).toMatchObject({
      state: "1 scanned",
      tone: "ok",
    })
  })

  it("is idle with no libraries at all", () => {
    expect(librariesRow({ data: [], error: null })).toMatchObject({ tone: "idle" })
  })
})

describe("bookdropRow", () => {
  it("is ok when empty", () => {
    expect(bookdropRow({ data: [], error: null })).toMatchObject({
      state: "Empty",
      tone: "ok",
    })
  })

  it("warns on failed items and counts what is waiting", () => {
    const row = bookdropRow({
      data: [dropItem(), dropItem({ id: "bd-2", state: "failed" })],
      error: null,
    })
    expect(row).toMatchObject({ state: "1 failed", tone: "warn" })
    expect(row.evidence).toContain("1 awaiting review")
  })

  it("is ok when items are only waiting", () => {
    const row = bookdropRow({ data: [dropItem()], error: null })
    expect(row.tone).toBe("ok")
    expect(row.evidence).toContain("1 awaiting review")
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && bun run vitest run src/components/settings/__tests__/instanceStatus.test.ts`

Expected: FAIL — cannot resolve `@/components/settings/instanceStatus`.

- [ ] **Step 3: Write the implementation**

Create `ui/src/components/settings/instanceStatus.ts`:

```ts
import type { BookDropItem } from "@/api/bookdrop"
import type { InstanceInfo, ProviderInfo, SettingsLibrary } from "@/api/settings"
import { relativeTime } from "@/lib/format"

/**
 * The Instance panel as pure functions.
 *
 * Every row on the board is derived here, from an API payload to a
 * StatusRow, with no React and no fetching in sight. The panel that
 * renders these is thirty lines of layout; this is where the judgements
 * live — what counts as pool pressure, when a provider is failing rather
 * than merely having once failed, what a never-scanned library means.
 * Those are the things worth testing, and a test for them needs no DOM.
 */

export type StatusTone = "ok" | "warn" | "idle"

export type StatusRow = {
  key: string
  label: string
  /** The verdict, one short phrase. */
  state: string
  tone: StatusTone
  /** The evidence for the verdict, rendered as a second italic line. */
  evidence?: string
}

/**
 * What a deriver needs to know about its query. Narrower than
 * react-query's result on purpose: a deriver that could see `isFetching`
 * would start making the board flicker.
 *
 * `data === undefined` with no error means still in flight — there is no
 * separate pending flag, because those are the same state to a reader.
 */
export type QueryState<T> = {
  data: T | undefined
  error: { message: string } | null
}

export function toQueryState<T>(q: {
  data: T | undefined
  error: { message: string } | null
}): QueryState<T> {
  return { data: q.data, error: q.error }
}

/** Pool utilisation at or above this reads as pressure. */
export const POOL_PRESSURE_RATIO = 0.8

/** A ping slower than this is worth saying out loud. */
export const SLOW_PING_MS = 250

/**
 * The one place a row handles not-yet and could-not-tell.
 *
 * A failed query becomes a warn row, never a missing one: a board that
 * drops what it could not check reads as all-clear, which is the exact
 * lie it exists to prevent.
 */
function row<T>(
  key: string,
  label: string,
  q: QueryState<T>,
  resolve: (data: T) => Omit<StatusRow, "key" | "label">
): StatusRow {
  if (q.error) {
    return { key, label, state: "unknown", tone: "warn", evidence: q.error.message }
  }
  if (!q.data) {
    return { key, label, state: "checking…", tone: "idle" }
  }
  return { key, label, ...resolve(q.data) }
}

export function databaseRow(q: QueryState<InstanceInfo>): StatusRow {
  return row("database", "Database", q, (info) => {
    const db = info.database
    if (!db) {
      return {
        state: "unknown",
        tone: "warn",
        evidence: "the server could not probe its database",
      }
    }

    const schema = info.schema
    const evidence = [
      schema ? `schema ${schema.version}, ${schema.dirty ? "dirty" : "clean"}` : "schema unknown",
      `pool ${db.inUse} of ${db.maxConns} in use`,
      `ping ${Math.round(db.pingMs)}ms`,
    ].join(" · ")

    if (schema?.dirty) return { state: "Schema dirty", tone: "warn", evidence }
    if (db.maxConns > 0 && db.inUse / db.maxConns >= POOL_PRESSURE_RATIO) {
      return { state: "Pool under pressure", tone: "warn", evidence }
    }
    if (db.pingMs > SLOW_PING_MS) return { state: "Slow", tone: "warn", evidence }
    return { state: "Healthy", tone: "ok", evidence }
  })
}

export function queueRow(q: QueryState<InstanceInfo>): StatusRow {
  return row("queue", "Job queue", q, (info) =>
    info.queueAttached
      ? { state: "Attached", tone: "ok" }
      : {
          state: "Not attached",
          tone: "warn",
          evidence:
            "no worker pool is wired — scans, enrichment and narration will not run",
        }
  )
}

export function providersRow(q: QueryState<InstanceInfo>): StatusRow {
  return row("providers", "Metadata providers", q, (info) => {
    const enabled = info.enrichmentProviders.filter((p) => p.enabled)
    if (enabled.length === 0) {
      return {
        state: "None enabled",
        tone: "idle",
        evidence: "no provider will be consulted when a book is enriched",
      }
    }

    const failing = enabled.filter(isFailing)
    if (failing.length === 0) {
      return { state: `${enabled.length} enabled`, tone: "ok" }
    }

    const worst = failing.reduce((a, b) => (errorAt(b) > errorAt(a) ? b : a))
    return {
      state: `${failing.length} of ${enabled.length} failing`,
      tone: "warn",
      evidence: `${worst.name} — ${worst.lastError ?? "no detail recorded"}, ${relativeTime(errorAt(worst))}`,
    }
  })
}

/**
 * A provider is failing when its last error is newer than its last
 * success — not merely when it has ever errored, which is true of any
 * provider that has run long enough.
 */
function isFailing(p: ProviderInfo): boolean {
  return errorAt(p) > successAt(p)
}

function errorAt(p: ProviderInfo): number {
  return p.lastErrorAt ? Date.parse(p.lastErrorAt) : 0
}

function successAt(p: ProviderInfo): number {
  return p.lastSuccessAt ? Date.parse(p.lastSuccessAt) : 0
}

export function librariesRow(q: QueryState<Array<SettingsLibrary>>): StatusRow {
  return row("libraries", "Libraries", q, (libs) => {
    if (libs.length === 0) {
      return { state: "None", tone: "idle", evidence: "no library has been added yet" }
    }

    const never = libs.filter((l) => !l.lastScannedAt)
    if (never.length === 0) {
      return { state: `${libs.length} scanned`, tone: "ok" }
    }

    // Never-scanned is idle, not a warning: nothing is broken, the work
    // simply has not been asked for. There is deliberately no
    // stale-after-N-days rule — there is no scan schedule to be late
    // against, so a threshold would be a lie with a number on it.
    return {
      state: `${libs.length - never.length} of ${libs.length} scanned`,
      tone: "idle",
      evidence: `${never.map((l) => l.name).join(", ")} — never scanned`,
    }
  })
}

export function bookdropRow(q: QueryState<Array<BookDropItem>>): StatusRow {
  return row("bookdrop", "BookDrop", q, (items) => {
    if (items.length === 0) return { state: "Empty", tone: "ok" }

    const failed = items.filter((i) => i.state === "failed")
    const waiting = items.filter((i) => i.state === "ready")
    const waitingNote = waiting.length > 0 ? `${waiting.length} awaiting review` : undefined

    if (failed.length === 0) {
      return {
        state: `${items.length} ${items.length === 1 ? "item" : "items"}`,
        tone: "ok",
        evidence: waitingNote,
      }
    }
    return {
      state: `${failed.length} failed`,
      tone: "warn",
      evidence: [`of ${items.length} items`, waitingNote].filter(Boolean).join(" · "),
    }
  })
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd ui && bun run vitest run src/components/settings/__tests__/instanceStatus.test.ts && bunx tsc --noEmit && bun run lint`

Expected: PASS, no type errors, no lint errors.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/settings/instanceStatus.ts ui/src/components/settings/__tests__/instanceStatus.test.ts
git commit -m "feat(ui): derive the instance status rows as pure functions

The judgements the board makes — what counts as pool pressure, when a
provider is failing rather than having once failed, what a never-scanned
library means — live in one module with no React in it, so the tests for
them need no DOM. A failed query becomes a warn row and never a missing
one: a board that drops what it could not check reads as all-clear."
```

---

### Task 8: `StatusLedger`, `InstancePanel`, and the rename

**Files:**
- Create: `ui/src/components/settings/StatusLedger.tsx`
- Create: `ui/src/components/settings/InstancePanel.tsx`
- Create: `ui/src/components/settings/__tests__/InstancePanel.test.tsx`
- Delete: `ui/src/components/settings/AboutPanel.tsx`
- Modify: `ui/src/components/settings/sections.tsx:3,15-26,97-102`
- Modify: `ui/src/components/__tests__/SettingsShell.test.tsx:45-48`

**Interfaces:**
- Consumes: everything from Tasks 5–7.
- Produces: `export function StatusLedger({ rows }: { rows: Array<StatusRow> })`, `export function InstancePanel()`, and the section key `"instance"` with label `"Instance"`, still last in `SETTINGS_SECTIONS`.

- [ ] **Step 1: Write the failing test**

Create `ui/src/components/settings/__tests__/InstancePanel.test.tsx`:

```tsx
// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { cleanup, render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { InstancePanel } from "@/components/settings/InstancePanel"

const instance = {
  version: "1.4.2",
  commit: "a3f19c2",
  goVersion: "go1.23.4",
  startedAt: "2026-07-31T09:00:00Z",
  allowedOrigins: [],
  bookDropPath: "/data/bookdrop",
  dataPath: "/data",
  migrateOnStart: true,
  enrichmentProviders: [],
  counts: { users: 2, libraries: 1, books: 40 },
  queueAttached: false,
  database: { pingMs: 1.4, inUse: 3, idle: 5, maxConns: 25 },
  schema: { version: 38, dirty: false },
}

vi.mock("@/api/client", () => ({
  api: vi.fn(async (path: string) => {
    if (path.startsWith("/api/v1/settings/instance")) return instance
    if (path.startsWith("/api/v1/settings/libraries")) return { libraries: [] }
    if (path.startsWith("/api/v1/bookdrop")) return { items: [] }
    throw new Error(`unexpected path ${path}`)
  }),
}))

function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <InstancePanel />
    </QueryClientProvider>
  )
}

afterEach(cleanup)

describe("InstancePanel", () => {
  it("renders every row, and warns about the detached queue", async () => {
    renderPanel()

    for (const key of ["database", "queue", "providers", "libraries", "bookdrop"]) {
      expect(await screen.findByTestId(`status-row-${key}`)).toBeTruthy()
    }

    const queue = await screen.findByTestId("status-row-queue")
    expect(queue.getAttribute("data-tone")).toBe("warn")
    expect(queue.textContent).toContain("Not attached")
    expect(queue.textContent).toContain("narration")
  })

  it("shows the build identity in the header", async () => {
    renderPanel()
    expect(await screen.findByText("a3f19c2")).toBeTruthy()
    expect(await screen.findByText("1.4.2")).toBeTruthy()
  })
})
```

**Before running:** check how other UI tests in this repo stub the network — look at `ui/src/components/settings/__tests__/` and `ui/src/hooks/__tests__/useSettingsDraft.test.tsx`. If the repo already has an MSW server or a shared render helper, use that instead of the `vi.mock("@/api/client")` above and adjust this test to match. Follow the existing pattern; do not introduce a second one.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd ui && bun run vitest run src/components/settings/__tests__/InstancePanel.test.tsx`

Expected: FAIL — cannot resolve `@/components/settings/InstancePanel`.

- [ ] **Step 3: Write the ledger**

Create `ui/src/components/settings/StatusLedger.tsx`:

```tsx
import type { StatusRow } from "@/components/settings/instanceStatus"
import { cn } from "@/lib/utils"

/**
 * The status board's rows.
 *
 * Two lines per row — verdict, then the evidence for it — which is the
 * one thing DefRow has no slot for, so this spells the row out rather
 * than bending that one. Deliberately not a Card: Card stacks its
 * children with a gap, and a ledger's rows meet at a hairline.
 *
 * Tone carries no colour of its own beyond the accent ink a warning
 * already uses elsewhere in settings. There is no status-dot vocabulary
 * here because there is none anywhere else in the app.
 */
export function StatusLedger({ rows }: { rows: Array<StatusRow> }) {
  return (
    <div className="mb-4 rounded-lg border border-border bg-card p-4 shadow-sm">
      {rows.map((r) => (
        <div
          key={r.key}
          data-testid={`status-row-${r.key}`}
          data-tone={r.tone}
          className="flex items-baseline gap-3 border-b border-dashed border-(--color-rule-soft) py-2 last:border-b-0"
        >
          <div className="t-label w-[150px] shrink-0">{r.label}</div>
          <div className="min-w-0 flex-1">
            <div
              className={cn(
                "text-[13.5px] break-words",
                r.tone === "warn" && "text-(--color-accent-ink)",
                r.tone === "idle" && "text-muted-foreground"
              )}
            >
              {r.state}
            </div>
            {r.evidence && (
              <div
                className={cn(
                  "t-small mt-0.5 break-words italic",
                  r.tone === "warn"
                    ? "text-(--color-accent-ink)"
                    : "text-muted-foreground"
                )}
              >
                {r.evidence}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 4: Write the panel**

Create `ui/src/components/settings/InstancePanel.tsx`:

```tsx
import { bookdropQuery } from "@/api/bookdrop"
import { useApiQuery } from "@/api/query"
import { instanceInfoQuery, settingsLibrariesQuery } from "@/api/settings"
import { DefRow } from "@/components/DefRow"
import { Card, PanelHeader, PanelLoading } from "@/components/SettingsShared"
import { StatusLedger } from "@/components/settings/StatusLedger"
import {
  bookdropRow,
  databaseRow,
  librariesRow,
  providersRow,
  queueRow,
  toQueryState,
} from "@/components/settings/instanceStatus"
import { relativeTime } from "@/lib/format"

/**
 * What this instance is, and whether anything about it is wrong.
 *
 * The rows are the subsystems that can fail in a way no other panel
 * surfaces. Email, OIDC, audiobooks and reading guides are absent on
 * purpose: each shows its own state inside its own panel, and
 * "configured: yes" is not news.
 *
 * Nothing polls. A health board that is quietly ten minutes old is worse
 * than none — it says ok and you believe it — so it says how old it is
 * and offers a button instead.
 */
export function InstancePanel() {
  const info = useApiQuery(instanceInfoQuery)
  const libraries = useApiQuery(settingsLibrariesQuery)
  const bookdrop = useApiQuery(bookdropQuery)

  const infoState = toQueryState(info)
  const rows = [
    databaseRow(infoState),
    queueRow(infoState),
    providersRow(infoState),
    librariesRow(toQueryState(libraries)),
    bookdropRow(toQueryState(bookdrop)),
  ]

  const fetchedAt = [info, libraries, bookdrop]
    .map((q) => q.dataUpdatedAt)
    .filter((t) => t > 0)
  const oldest = fetchedAt.length > 0 ? Math.min(...fetchedAt) : 0

  function refreshAll() {
    void Promise.all([info.refetch(), libraries.refetch(), bookdrop.refetch()])
  }

  return (
    <>
      <PanelHeader title="Instance">
        What this server is running, and whether anything about it needs
        attention.
      </PanelHeader>

      {!info.data && !info.error ? (
        <PanelLoading />
      ) : (
        <Card>
          <DefRow label="Version" value={<span className="mono">{info.data?.version ?? "—"}</span>} />
          {info.data?.commit && (
            <DefRow label="Build" value={<span className="mono">{info.data.commit}</span>} />
          )}
          <DefRow label="Runtime" value={<span className="mono">{info.data?.goVersion ?? "—"}</span>} />
          <DefRow
            label="Uptime"
            value={
              <span className="mono">
                {info.data ? relativeTime(Date.parse(info.data.startedAt)).replace(" ago", "") : "—"}
              </span>
            }
          />
          <DefRow label="Data path" value={<span className="mono">{info.data?.dataPath ?? "—"}</span>} />
          <DefRow label="BookDrop path" value={<span className="mono">{info.data?.bookDropPath ?? "—"}</span>} />
          <DefRow
            label="Catalog"
            value={
              info.data
                ? `${info.data.counts.books.toLocaleString()} books · ${info.data.counts.libraries} libraries · ${info.data.counts.users} users`
                : "—"
            }
          />
        </Card>
      )}

      <div className="mt-6 mb-2.5 flex items-baseline justify-between gap-3">
        <div className="t-label">Status</div>
        <div className="flex items-baseline gap-3">
          <span className="t-small text-muted-foreground italic">
            {oldest > 0 ? `as of ${relativeTime(oldest)}` : "not yet read"}
          </span>
          <button
            type="button"
            onClick={refreshAll}
            className="t-micro border border-(--color-rule-soft) px-2 py-1 text-(--color-ink-2) transition-colors hover:bg-(--color-paper-2) hover:text-(--color-ink-1)"
          >
            Refresh
          </button>
        </div>
      </div>

      <StatusLedger rows={rows} />

      <p className="t-small mt-6 italic">
        embookshelf — self-hosted ebook library. AGPL-3.0.
      </p>
    </>
  )
}
```

- [ ] **Step 5: Rewire the section table**

In `ui/src/components/settings/sections.tsx`:

1. Replace the import `import { AboutPanel } from "@/components/settings/AboutPanel"` with `import { InstancePanel } from "@/components/settings/InstancePanel"` (keep the import block alphabetically ordered — `InstancePanel` sorts after `ForwardAuthPanel` and before `InvitesPanel`).
2. In the `SettingsSectionKey` union, replace `| "about"` with `| "instance"`.
3. Replace the last entry of `SETTINGS_SECTIONS` with:

```tsx
  {
    key: "instance",
    label: "Instance",
    adminOnly: true,
    render: () => <InstancePanel />,
  },
```

Keep it **last** in the array.

Then delete the old panel:

```bash
git rm ui/src/components/settings/AboutPanel.tsx
```

In `ui/src/components/__tests__/SettingsShell.test.tsx`, replace the final mock block:

```ts
vi.mock("@/components/settings/InstancePanel", () => ({
  InstancePanel: probe("InstancePanel"),
}))
```

Then run the file and fix any assertion that names `about` or `AboutPanel` — search it for both strings.

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd ui && bun run vitest run && bunx tsc --noEmit && bun run lint`

Expected: PASS across the suite, no type errors, no lint errors. Grep for stragglers if anything fails:

```bash
grep -rn "AboutPanel\|\"about\"" ui/src --include="*.ts" --include="*.tsx"
```

- [ ] **Step 7: Commit**

```bash
git add ui/src/components/settings/StatusLedger.tsx ui/src/components/settings/InstancePanel.tsx ui/src/components/settings/__tests__/InstancePanel.test.tsx ui/src/components/settings/sections.tsx ui/src/components/__tests__/SettingsShell.test.tsx
git rm --cached ui/src/components/settings/AboutPanel.tsx 2>/dev/null || true
git commit -m "feat(ui): About becomes an instance status board

Nine static facts, and the provider health the panel already fetched and
threw away. It now reads the five subsystems that can be wrong in a way
no other panel surfaces, says how old its answer is, and stops calling
itself About."
```

---

### Task 9: Full verification

**Files:** none — this task changes nothing and exists to catch what the per-task runs could not see.

- [ ] **Step 1: Run every CI check**

Run: `make ci-local`

Expected: all checks pass. If `TEST_DATABASE_URL` is unset, the Go suite fails loudly by design — export it and rerun.

- [ ] **Step 2: Build the binary with the UI embedded**

Run: `make build`

Expected: `./tmp/embookshelf` is produced with no error. This is the step that catches a stale `internal/staticfs/dist/`.

- [ ] **Step 3: Exercise it against a running instance**

Run `make up`, sign in as an admin, open **Settings › Instance**, and confirm by eye:

1. The header shows a version, a commit, a Go version and an uptime that grows on refresh.
2. Five rows render.
3. The Database row shows a schema version and a pool figure — not "unknown".
4. Refresh updates the "as of" line.
5. `curl -i localhost:6060/api/v1/healthcheck` returns 200; stop Postgres and confirm it returns 503.

Do not claim this task complete without having run step 3 — every prior test in this plan runs against a fake probe, so this is the only point where a real `pgxpool.Stat()` and a real `schema_migrations` read are exercised together.

- [ ] **Step 4: Commit any fixes and push**

```bash
git push -u origin spec/instance-status-panel
```
