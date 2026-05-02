package model

import "time"

// Identity is an OIDC credential link between a user and a provider.
// Stored one row per (user, provider) in the user_identities table.
// See ADR-0001 and CONTEXT.md → "Identity".
type Identity struct {
	ID          string
	UserID      string
	Provider    string // "google" | "github" | "generic"
	Issuer      string
	Subject     string
	Email       *string
	LinkedAt    time.Time
	LastLoginAt *time.Time
}
