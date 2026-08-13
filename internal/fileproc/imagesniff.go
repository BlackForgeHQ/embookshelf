// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

// sniffImageMime sniffs a blob's magic bytes and returns its image MIME
// type, or "" when the bytes don't match any recognized image format.
//
// This is the shared trust boundary for every processor in this package
// that reads a cover (or cover-shaped record) out of attacker-controlled
// container data: MOBI/AZW3 records carry no type of their own, and FB2's
// <binary content-type> attribute is a value the file's author chose, not
// something this package parsed out of image data. Both processors sniff
// the decoded bytes here and use the sniffed type rather than any
// declared one — a container claiming its cover is "image/png" (or,
// maliciously, "text/html") does not get to decide what content type is
// later served for it.
func sniffImageMime(b []byte) string {
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
