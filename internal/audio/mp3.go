// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audio assembles a finished audiobook out of the per-segment
// MP3s a text-to-speech engine returns.
//
// It exists because the obvious container for an audiobook — M4B — is
// unreachable from here: every engine in the catalog emits MP3 and none
// emits M4B, an M4B wants AAC, and there is no usable pure-Go AAC
// encoder. MP3 has the property that makes the whole feature cheap
// instead: frames concatenate. Given one engine, one voice and one run,
// the per-segment responses join byte-wise into a valid file with no
// transcode, no muxer and no ffmpeg (ADR-0027).
//
// Chapter marks are written as ID3v2 CHAP/CTOC frames so the file stays
// self-describing once it leaves embookshelf. The database keeps its own
// copy for in-app playback; both are written from the same source in the
// same step, so they cannot disagree.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
)

// ErrNoFrames is returned when a payload contains no MPEG audio at all.
// Almost always an engine returning an error page, a rate-limit notice or
// a format nobody asked for — worth failing the segment over rather than
// splicing into a half-gigabyte artifact nobody will listen to twice.
var ErrNoFrames = errors.New("audio: no MPEG frames found")

// Chapter is one chapter mark in the finished file. Times are
// milliseconds from the start of the audiobook.
type Chapter struct {
	Title   string
	StartMS uint32
	EndMS   uint32
}

// Tags are the standard fields written alongside the chapters, so a
// player that has never heard of embookshelf still shows the right thing.
type Tags struct {
	Title     string
	Artist    string
	Album     string
	Genre     string
	Cover     []byte
	CoverMIME string
}

// Payload strips any ID3 tags from one engine response and reports the
// duration of the MPEG frames that remain.
//
// Both halves matter to the caller. The frames are what gets concatenated
// — an ID3 header left in the middle of the stream is a gap or a hard
// stop depending on the player. The duration is what chapter start times
// are accumulated from, and it comes from walking frame headers rather
// than trusting a XING header, because a stitched-together file's XING
// would describe only its first segment.
func Payload(b []byte) ([]byte, int64, error) {
	b = stripID3v2(b)
	b = stripID3v1(b)

	start := -1
	var ms float64
	for i := 0; i+4 <= len(b); {
		size, samples, rate, ok := parseFrameHeader(b[i:])
		if !ok {
			if start >= 0 {
				// Frames ended. Trailing junk after a valid run is common
				// enough — take the run and stop.
				return b[start:i], int64(ms), nil
			}
			i++
			continue
		}
		if start < 0 {
			start = i
		}
		ms += float64(samples) * 1000 / float64(rate)
		i += size
	}
	if start < 0 {
		return nil, 0, ErrNoFrames
	}
	return b[start:], int64(ms), nil
}

// Concat writes every part's frames to w in order and returns each part's
// own duration in milliseconds.
//
// Per-part rather than cumulative on purpose: the caller turns these into
// chapter boundaries by accumulating them, and a running total would make
// a re-synthesized segment impossible to substitute without recomputing
// everything after it.
func Concat(w io.Writer, parts [][]byte) ([]int64, error) {
	durations := make([]int64, 0, len(parts))
	for i, part := range parts {
		frames, ms, err := Payload(part)
		if err != nil {
			return nil, fmt.Errorf("part %d: %w", i, err)
		}
		if _, err := w.Write(frames); err != nil {
			return nil, fmt.Errorf("write part %d: %w", i, err)
		}
		durations = append(durations, ms)
	}
	return durations, nil
}

// ---------------------------------------------------------------------------
// Frame parsing
// ---------------------------------------------------------------------------

// bitrates is the Layer III table, in kbit/s, indexed by the header's
// four-bit field. Index 0 is "free" and index 15 is invalid; both are
// rejected by parseFrameHeader.
var bitrates = map[bool][16]int{
	// MPEG-1
	true: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
	// MPEG-2 and MPEG-2.5
	false: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
}

// sampleRates is indexed by version then by the header's two-bit field.
var sampleRates = map[byte][4]int{
	0b11: {44100, 48000, 32000}, // MPEG-1
	0b10: {22050, 24000, 16000}, // MPEG-2
	0b00: {11025, 12000, 8000},  // MPEG-2.5
}

// parseFrameHeader decodes one MPEG Layer III frame header, returning the
// frame's total size in bytes, its sample count, and its sample rate.
//
// Only Layer III is accepted: it is what every engine emits, and treating
// a stray Layer II sync word as valid would resynchronise the walk onto
// garbage and silently mis-measure the book.
func parseFrameHeader(b []byte) (size, samples, rate int, ok bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return 0, 0, 0, false
	}
	version := (b[1] >> 3) & 0b11
	layer := (b[1] >> 1) & 0b11
	if layer != 0b01 { // Layer III
		return 0, 0, 0, false
	}
	rates, known := sampleRates[version]
	if !known {
		return 0, 0, 0, false
	}

	bitrateIdx := (b[2] >> 4) & 0x0F
	rateIdx := (b[2] >> 2) & 0b11
	padding := int((b[2] >> 1) & 0b1)
	if rateIdx == 0b11 {
		return 0, 0, 0, false
	}

	mpeg1 := version == 0b11
	bitrate := bitrates[mpeg1][bitrateIdx] * 1000
	rate = rates[rateIdx]
	if bitrate == 0 || rate == 0 {
		return 0, 0, 0, false
	}

	// MPEG-1 Layer III carries 1152 samples per frame; MPEG-2 and 2.5
	// carry half that, and their frame-size coefficient halves with it.
	samples = 1152
	coefficient := 144
	if !mpeg1 {
		samples = 576
		coefficient = 72
	}
	size = coefficient*bitrate/rate + padding
	if size < 4 {
		return 0, 0, 0, false
	}
	return size, samples, rate, true
}

// ---------------------------------------------------------------------------
// ID3 stripping
// ---------------------------------------------------------------------------

func stripID3v2(b []byte) []byte {
	if len(b) < 10 || string(b[:3]) != "ID3" {
		return b
	}
	size := unsyncsafe(b[6:10])
	end := 10 + size
	if b[5]&0x10 != 0 {
		end += 10 // footer present
	}
	if end < 0 || end > len(b) {
		return b
	}
	return b[end:]
}

func stripID3v1(b []byte) []byte {
	if len(b) >= 128 && string(b[len(b)-128:len(b)-125]) == "TAG" {
		return b[:len(b)-128]
	}
	return b
}

// ---------------------------------------------------------------------------
// ID3v2.3 writing
// ---------------------------------------------------------------------------

// tocElementID names the single top-level table of contents. Element IDs
// are internal identifiers, never shown to anyone.
const tocElementID = "toc"

// WriteID3 writes an ID3v2.3 tag: the standard text frames, the cover,
// and one CHAP frame per chapter under a CTOC that orders them.
//
// v2.3 rather than v2.4 because it is what players actually read, and
// CHAP is an extension both understand. Text is UTF-16LE with a BOM
// (encoding 0x01) — the only encoding v2.3 offers that can carry a title
// outside Latin-1, which is precisely the long tail worth narrating.
func WriteID3(w io.Writer, t Tags, chapters []Chapter) error {
	var body []byte

	body = append(body, textFrame("TIT2", t.Title)...)
	body = append(body, textFrame("TPE1", t.Artist)...)
	body = append(body, textFrame("TALB", t.Album)...)
	body = append(body, textFrame("TCON", t.Genre)...)
	if len(t.Cover) > 0 && t.CoverMIME != "" {
		body = append(body, apicFrame(t.CoverMIME, t.Cover)...)
	}
	if len(chapters) > 0 {
		ids := make([]string, len(chapters))
		for i, ch := range chapters {
			ids[i] = fmt.Sprintf("chp%d", i)
			body = append(body, chapFrame(ids[i], ch)...)
		}
		body = append(body, ctocFrame(ids)...)
	}

	head := make([]byte, 0, 10)
	head = append(head, 'I', 'D', '3', 0x03, 0x00, 0x00)
	head = append(head, syncsafe(uint32(len(body)))...)

	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// frame wraps a payload in an ID3v2.3 frame header. Frame sizes in v2.3
// are plain big-endian uint32 — only the tag header's size is syncsafe,
// and conflating the two is the classic way to produce a tag that every
// reader silently truncates.
func frame(id string, payload []byte) []byte {
	out := make([]byte, 0, 10+len(payload))
	out = append(out, id...)
	size := make([]byte, 4)
	binary.BigEndian.PutUint32(size, uint32(len(payload)))
	out = append(out, size...)
	out = append(out, 0x00, 0x00) // flags
	return append(out, payload...)
}

func textFrame(id, value string) []byte {
	if value == "" {
		return nil
	}
	payload := append([]byte{0x01}, utf16LEWithBOM(value)...)
	return frame(id, payload)
}

// apicFrame carries the cover. The MIME type is Latin-1 and
// null-terminated regardless of the text encoding byte, per the spec;
// picture type 0x03 is "front cover".
func apicFrame(mime string, data []byte) []byte {
	payload := []byte{0x00}
	payload = append(payload, mime...)
	payload = append(payload, 0x00)
	payload = append(payload, 0x03)
	payload = append(payload, 0x00) // empty Latin-1 description
	return frame("APIC", append(payload, data...))
}

// chapFrame is one chapter: an element id, the time span, and an
// embedded TIT2 sub-frame holding the title a reader actually sees.
//
// The byte offsets are written as 0xFFFFFFFF, the spec's "not present"
// sentinel. Real offsets would be more precise, but they would have to be
// recomputed whenever a single segment is re-synthesized, and every
// player seeks by time anyway.
func chapFrame(elementID string, ch Chapter) []byte {
	payload := make([]byte, 0, 32+len(ch.Title)*2)
	payload = append(payload, elementID...)
	payload = append(payload, 0x00)

	times := make([]byte, 16)
	binary.BigEndian.PutUint32(times[0:4], ch.StartMS)
	binary.BigEndian.PutUint32(times[4:8], ch.EndMS)
	binary.BigEndian.PutUint32(times[8:12], 0xFFFFFFFF)
	binary.BigEndian.PutUint32(times[12:16], 0xFFFFFFFF)
	payload = append(payload, times...)

	payload = append(payload, textFrame("TIT2", ch.Title)...)
	return frame("CHAP", payload)
}

// ctocFrame orders the chapters. Flags 0x03 marks it top-level and
// ordered, which is what makes a player render it as *the* chapter list
// rather than one of several possible groupings.
func ctocFrame(elementIDs []string) []byte {
	payload := make([]byte, 0, 8+len(elementIDs)*8)
	payload = append(payload, tocElementID...)
	payload = append(payload, 0x00)
	payload = append(payload, 0x03)
	payload = append(payload, byte(len(elementIDs)))
	for _, id := range elementIDs {
		payload = append(payload, id...)
		payload = append(payload, 0x00)
	}
	return frame("CTOC", payload)
}

// utf16LEWithBOM encodes text as ID3v2.3's encoding 0x01: a byte-order
// mark followed by UTF-16LE code units, terminated by a null code unit.
func utf16LEWithBOM(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2+len(units)*2+2)
	out = append(out, 0xFF, 0xFE) // little-endian BOM
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return append(out, 0x00, 0x00)
}

// syncsafe encodes a length as four 7-bit bytes, the format the tag
// header uses so a size can never contain a false frame sync.
func syncsafe(n uint32) []byte {
	return []byte{
		byte((n >> 21) & 0x7F),
		byte((n >> 14) & 0x7F),
		byte((n >> 7) & 0x7F),
		byte(n & 0x7F),
	}
}

func unsyncsafe(b []byte) int {
	if len(b) < 4 {
		return 0
	}
	return int(b[0]&0x7F)<<21 | int(b[1]&0x7F)<<14 | int(b[2]&0x7F)<<7 | int(b[3]&0x7F)
}
