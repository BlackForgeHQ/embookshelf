package model

import "time"

// BookDropState is the lifecycle of a single file in the ingest pipeline.
type BookDropState string

const (
	BookDropDiscovered BookDropState = "discovered" // queued, not yet processed
	BookDropProcessing BookDropState = "processing" // worker is running
	BookDropReady      BookDropState = "ready"      // metadata extracted, awaiting review
	BookDropFailed     BookDropState = "failed"     // processor returned an error
	BookDropImported   BookDropState = "imported"   // user approved → books row created
	BookDropRejected   BookDropState = "rejected"   // user dismissed
)

// BookDropItem mirrors a row in bookdrop_items.
type BookDropItem struct {
	ID           string
	Path         string
	FileSize     int64
	Format       string
	State        BookDropState
	Progress     int
	ErrorMsg     string
	Title        string
	Author       string
	Description  string
	Language     string
	HasCover     bool
	CoverMime    string
	BookID       *string
	DiscoveredAt time.Time
	UpdatedAt    time.Time
	// ContentHash is the SHA-256 digest of the file content, computed
	// during ingest. nil (or empty) until Task 9 fills it.
	ContentHash []byte
}

// IsTerminal reports whether no further transitions are expected.
func (s BookDropState) IsTerminal() bool {
	return s == BookDropImported || s == BookDropRejected
}

// NeedsReview reports whether the UI should surface Approve/Reject actions.
func (s BookDropState) NeedsReview() bool {
	return s == BookDropReady || s == BookDropFailed
}
