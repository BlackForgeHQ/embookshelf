// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import "time"

// MarkdownRenditionState is the lifecycle of one book's Markdown
// rendition (ADR-0033). Far simpler than the audiobook's: conversion is
// one HTTP call, so there is no cancel, no seal, no segment machinery.
type MarkdownRenditionState string

const (
	MarkdownRenditionPending MarkdownRenditionState = "pending"
	MarkdownRenditionRunning MarkdownRenditionState = "running"
	MarkdownRenditionReady   MarkdownRenditionState = "ready"
	MarkdownRenditionFailed  MarkdownRenditionState = "failed"
)

// MarkdownRendition mirrors one book_markdown_renditions row: the
// tracking record for the markdown produced from a Convertible-format
// book by the converter extension.
type MarkdownRendition struct {
	BookID string
	State  MarkdownRenditionState
	// Error is the loud-failure channel, surfaced verbatim. Empty while
	// nothing is wrong.
	Error string
	// Location is the storage key the markdown lives at, inside the
	// book's own library. Empty until first ready.
	Location  string
	SizeBytes int64
	// SourceContentHash is the primary file the markdown was converted
	// from. A mismatch with the book's current file means stale —
	// labelled, never auto-invalidated.
	SourceContentHash []byte
	ConverterVersion  string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
