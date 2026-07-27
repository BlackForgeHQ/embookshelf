// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"github.com/google/uuid"
)

// NewID returns a fresh canonical UUID string. Repos call this for every
// INSERT instead of relying on Postgres' gen_random_uuid() default, so the
// generated id is known to the caller without a RETURNING round-trip.
func NewID() string {
	return uuid.NewString()
}
