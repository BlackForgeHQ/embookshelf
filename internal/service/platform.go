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
