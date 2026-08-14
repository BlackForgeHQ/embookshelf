// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

// SniffImageMime sniffs a blob's magic bytes and returns its image MIME
// type, or "" when the bytes don't match any recognized image format.
//
// This is the one image sniffer in the codebase, and the trust boundary
// for every cover that reaches a browser. A cover's content type is
// whatever the file's author wrote — an EPUB manifest's media-type, an
// FB2 <binary content-type>, an ID3 APIC MIME field, a comic archive's
// entry name — and the cover routes hand that string straight back as
// the response's Content-Type. A container claiming its cover is
// "image/png" (or, maliciously, "text/html") therefore does not get to
// decide what content type is later served for it: the bytes do.
//
// Formats are the four a book cover is actually shipped in. Anything
// else — SVG most pointedly, which is a script-capable document — is not
// recognized, and normalizeCover degrades it to no cover at all.
func SniffImageMime(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// normalizeCover re-types a Metadata's cover from its own bytes and
// drops the cover entirely when those bytes are not an image.
//
// Called once, from ExtractBook — the single seam every processor's
// cover crosses on its way to the database. Individual processors sniff
// too, but that is defence in depth: a processor is free to forget, and
// several derive the type from something the file's author named (a
// manifest attribute, an archive entry's extension). Doing it here is
// what makes "the declared type is never persisted" a property of the
// package rather than a habit each extractor has to keep.
func normalizeCover(m Metadata) Metadata {
	mime := SniffImageMime(m.CoverBytes)
	if mime == "" {
		m.HasCover = false
		m.CoverBytes = nil
		m.CoverMime = ""
		return m
	}
	m.HasCover = true
	m.CoverMime = mime
	return m
}
