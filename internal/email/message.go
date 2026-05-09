// SPDX-License-Identifier: AGPL-3.0-or-later

// Package email is the transport seam for outbound mail. The Sender
// interface hides the SMTP/HTTP/etc choice from the service layer so
// domain code never reaches into a vendor SDK. The shipping
// implementation is SMTP via github.com/wneessen/go-mail; ADR-0020
// records why there is no provider catalog mirroring
// internal/provider.
package email

import "io"

// Message is the transport-agnostic payload handed to a Sender. Text
// is required (some clients drop HTML; accessibility tooling reads
// text/plain). HTML is optional — when set, the message goes out as
// multipart/alternative.
type Message struct {
	To          string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

// Attachment carries one file body alongside a Message. Reader is
// drained once during send; callers must not reuse it. ContentType
// may be empty — the SMTP adapter falls back to
// application/octet-stream which Send-to-Kindle accepts for EPUB and
// PDF alike.
type Attachment struct {
	Filename    string
	ContentType string
	Reader      io.Reader
}
