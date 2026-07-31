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
