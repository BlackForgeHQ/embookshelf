package model

import "time"

// PendingOrphan is a storage key queued for deletion by the
// LoopOrphanedKeys sweeper after EligibleAt has passed. Created by
// the S3 edit-time folder rename pipeline (ADR-0005) for old keys
// that the rename moved past, and for new keys left behind by a
// half-failed rename.
type PendingOrphan struct {
	ID         int64
	LibraryID  string
	Key        string
	EligibleAt time.Time
	Reason     string
	BookID     *string
	CreatedAt  time.Time
}
