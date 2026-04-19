package model

import "time"

// DeviceKind identifies which backend/protocol handles a device. The
// canonical list lives here so UI copy, driver dispatch, and DB content
// all agree on the same strings.
type DeviceKind string

const (
	// DeviceRemarkablePaperPro pushes via the reMarkable cloud API. The
	// same driver handles every modern reMarkable tablet (RM1/RM2 + Paper
	// Pro) — their cloud protocol is shared.
	DeviceRemarkablePaperPro DeviceKind = "remarkable-paper-pro"
)

// Device is a per-user destination users can push books to. Secret is
// long-lived (pairing token, API key); config carries non-secret per-device
// knobs the driver may want.
type Device struct {
	ID         string
	UserID     string
	Kind       DeviceKind
	Name       string
	Secret     string
	Config     map[string]any
	LastSentAt *time.Time
	LastError  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
