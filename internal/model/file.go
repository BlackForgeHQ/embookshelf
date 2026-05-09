// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// File mirrors the files row: a physical artifact in storage tied to
// a logical book. content_hash is sha256 (32 bytes) and may be nil
// while the boot-time backfill is still running.
type File struct {
	ID          string
	LibraryID   string
	BookID      string // empty when the file is orphaned (not implemented yet)
	Location    string // relative to library.root; slash-separated
	Size        int64
	Mtime       time.Time
	ETag        string // empty for local FS
	ContentHash []byte // nil until hashed
	Format      string // canonical: "EPUB" | "PDF" | "CBZ" | "MP3" | "M4B"
	LastScanned time.Time
	// MissingSince is set when a scan can't find the file in storage.
	// Files stay flagged for the missing TTL (24h) before purge; nil
	// means "present (or never observed missing)".
	MissingSince *time.Time
}
