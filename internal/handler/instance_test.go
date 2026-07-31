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

	"github.com/blackforge/embookshelf/internal/jobs"
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
// expressible without starting River. The brief guessed Enqueue took a
// queue.Job — there is no such type. The real interface (queue.go:51-55)
// is Enqueue(ctx, jobs.Args) error, Start(ctx) error, Stop(ctx) error.
type stubQueue struct{}

func (stubQueue) Enqueue(context.Context, jobs.Args) error { return nil }
func (stubQueue) Start(context.Context) error              { return nil }
func (stubQueue) Stop(context.Context) error               { return nil }
