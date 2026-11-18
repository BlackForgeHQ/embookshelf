// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// APIToken is a personal access token's metadata (ADR-0039). The token
// itself is never held after creation — only its sha256 is stored — so
// nothing here can authenticate.
type APIToken struct {
	ID     string
	UserID string
	Name   string
	// Prefix is the first characters after "ebk_", for recognising a
	// token in a list without being able to use it.
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	// ExpiresAt nil means the token never expires.
	ExpiresAt *time.Time
}
