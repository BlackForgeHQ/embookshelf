// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audiotest builds MPEG audio fixtures for tests in other
// packages.
//
// Its own package rather than an export from internal/audio because a
// frame builder is test support, and audio's job is parsing real files —
// putting a fixture in its production surface would invite production
// use. Same shape as repotest and storagetest.
package audiotest

import "bytes"

// frameHeader is one MPEG-1 Layer III frame header: 128 kbit/s,
// 44.1 kHz, stereo, no CRC. Every engine in the catalog emits exactly
// this shape, which is what makes byte-wise concatenation legal
// (ADR-0027).
var frameHeader = []byte{0xFF, 0xFB, 0x90, 0x00}

// FrameBytes is the size of the frame above: 144 * 128000 / 44100.
const FrameBytes = 417

// Frames builds n back-to-back frames of silence.
func Frames(n int) []byte {
	var b bytes.Buffer
	for range n {
		b.Write(frameHeader)
		b.Write(make([]byte, FrameBytes-len(frameHeader)))
	}
	return b.Bytes()
}
