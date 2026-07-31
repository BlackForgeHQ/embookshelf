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
