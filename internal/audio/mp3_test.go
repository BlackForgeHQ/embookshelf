// SPDX-License-Identifier: AGPL-3.0-or-later

package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// mpeg1Frame is one MPEG-1 Layer III frame header: 128 kbit/s, 44.1 kHz,
// stereo, no CRC. Every engine in the catalog emits exactly this shape,
// which is what makes byte-wise concatenation legal.
//
//	0xFF 0xFB — sync, MPEG-1, Layer III, no CRC
//	0x90      — bitrate index 9 (128k), sample-rate index 0 (44.1k)
//	0x00      — stereo
var mpeg1Frame = []byte{0xFF, 0xFB, 0x90, 0x00}

// frameBytes is the size of the frame above: 144 * 128000 / 44100 = 417.
const frameBytes = 417

// frameMS is its duration: 1152 samples / 44100 Hz = 26.122ms.
var frameMS = 1152 * 1000.0 / 44100.0

// mp3Frames builds n back-to-back frames of silence.
func mp3Frames(n int) []byte {
	var b bytes.Buffer
	for range n {
		b.Write(mpeg1Frame)
		b.Write(make([]byte, frameBytes-len(mpeg1Frame)))
	}
	return b.Bytes()
}

// withID3v2 prefixes an ID3v2 tag of the given payload size, the way a
// real engine response arrives.
func withID3v2(payloadSize int, body []byte) []byte {
	head := []byte{'I', 'D', '3', 0x03, 0x00, 0x00}
	head = append(head, syncsafe(uint32(payloadSize))...)
	head = append(head, make([]byte, payloadSize)...)
	return append(head, body...)
}

// withID3v1 appends the 128-byte trailer some encoders still write.
func withID3v1(body []byte) []byte {
	tail := make([]byte, 128)
	copy(tail, "TAG")
	return append(append([]byte{}, body...), tail...)
}

// ---------------------------------------------------------------------------
// Payload — strip the tags, measure the frames
// ---------------------------------------------------------------------------

func TestPayloadMeasuresFrameDuration(t *testing.T) {
	t.Parallel()

	frames, ms, err := Payload(mp3Frames(100))
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if len(frames) != 100*frameBytes {
		t.Errorf("frames = %d bytes, want %d", len(frames), 100*frameBytes)
	}
	// 100 frames at 26.122ms. Independent arithmetic, not the code's.
	if want := int64(100 * frameMS); ms < want-2 || ms > want+2 {
		t.Errorf("duration = %dms, want ~%dms", ms, want)
	}
}

// An engine's response carries its own ID3 header. Concatenating those
// verbatim would drop a tag into the middle of the stream, which players
// either render as a gap or choke on outright.
func TestPayloadStripsLeadingID3v2Tag(t *testing.T) {
	t.Parallel()

	frames, ms, err := Payload(withID3v2(64, mp3Frames(10)))
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if len(frames) != 10*frameBytes {
		t.Errorf("frames = %d bytes, want %d — the ID3 tag survived", len(frames), 10*frameBytes)
	}
	if ms <= 0 {
		t.Errorf("duration = %d, want positive", ms)
	}
}

func TestPayloadStripsTrailingID3v1Tag(t *testing.T) {
	t.Parallel()

	frames, _, err := Payload(withID3v1(mp3Frames(10)))
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	if len(frames) != 10*frameBytes {
		t.Errorf("frames = %d bytes, want %d — the ID3v1 trailer survived", len(frames), 10*frameBytes)
	}
}

// Bytes that are not MPEG audio at all mean the engine returned an error
// page or a format we did not ask for. Better to fail the segment than to
// splice garbage into a 500 MB artifact.
func TestPayloadRejectsNonMPEGBytes(t *testing.T) {
	t.Parallel()

	if _, _, err := Payload([]byte("<html>rate limited</html>")); err == nil {
		t.Fatal("want an error for non-MPEG bytes, got nil")
	}
}

// ---------------------------------------------------------------------------
// Concat
// ---------------------------------------------------------------------------

func TestConcatJoinsFramesAndReportsPerPartDurations(t *testing.T) {
	t.Parallel()

	parts := [][]byte{
		withID3v2(32, mp3Frames(10)),
		mp3Frames(20),
		withID3v1(mp3Frames(30)),
	}

	var out bytes.Buffer
	durations, err := Concat(&out, parts)
	if err != nil {
		t.Fatalf("Concat: %v", err)
	}
	if len(durations) != 3 {
		t.Fatalf("got %d durations, want 3", len(durations))
	}
	if got, want := out.Len(), 60*frameBytes; got != want {
		t.Errorf("output = %d bytes, want %d (frames only, no tags)", got, want)
	}
	// Each part's duration must be its own, not the running total —
	// chapter start times are built by accumulating these.
	for i, wantFrames := range []int{10, 20, 30} {
		want := int64(float64(wantFrames) * frameMS)
		if durations[i] < want-2 || durations[i] > want+2 {
			t.Errorf("durations[%d] = %dms, want ~%dms", i, durations[i], want)
		}
	}
	// And the joined stream must still parse as one continuous run.
	if _, total, err := Payload(out.Bytes()); err != nil {
		t.Errorf("concatenated output does not parse: %v", err)
	} else if want := int64(60 * frameMS); total < want-3 || total > want+3 {
		t.Errorf("joined duration = %dms, want ~%dms", total, want)
	}
}

// ---------------------------------------------------------------------------
// ID3 — the tag that makes the file self-describing off-instance
// ---------------------------------------------------------------------------

func TestWriteID3EmitsAParseableTagWithStandardFields(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := WriteID3(&out, Tags{
		Title:  "Woman in the Dunes",
		Artist: "Kōbō Abe",
		Album:  "Woman in the Dunes",
		Genre:  "Audiobook",
	}, nil)
	if err != nil {
		t.Fatalf("WriteID3: %v", err)
	}

	b := out.Bytes()
	if string(b[:3]) != "ID3" || b[3] != 0x03 {
		t.Fatalf("header = %x, want an ID3v2.3 tag", b[:5])
	}
	// The declared size must match what actually follows, or every reader
	// downstream mis-locates the first audio frame.
	if got, want := unsyncsafe(b[6:10]), len(b)-10; got != want {
		t.Errorf("declared tag size = %d, want %d", got, want)
	}
	for _, id := range []string{"TIT2", "TPE1", "TALB", "TCON"} {
		if !bytes.Contains(b, []byte(id)) {
			t.Errorf("tag is missing the %s frame", id)
		}
	}
}

// The chapter frames are the whole reason the file is worth downloading:
// without them a phone sees one eight-hour blob.
func TestWriteID3EmitsChapterAndTableOfContentsFrames(t *testing.T) {
	t.Parallel()

	chapters := []Chapter{
		{Title: "The Ruined Map", StartMS: 0, EndMS: 1_800_000},
		{Title: "Woman in the Dunes", StartMS: 1_800_000, EndMS: 3_600_000},
	}

	var out bytes.Buffer
	if err := WriteID3(&out, Tags{Title: "Book"}, chapters); err != nil {
		t.Fatalf("WriteID3: %v", err)
	}
	b := out.Bytes()

	if n := bytes.Count(b, []byte("CHAP")); n != 2 {
		t.Errorf("got %d CHAP frames, want 2", n)
	}
	if n := bytes.Count(b, []byte("CTOC")); n != 1 {
		t.Errorf("got %d CTOC frames, want 1", n)
	}
	if got, want := unsyncsafe(b[6:10]), len(b)-10; got != want {
		t.Errorf("declared tag size = %d, want %d", got, want)
	}

	// The second chapter's start time must appear verbatim, big-endian —
	// a player seeking to chapter 2 reads exactly these four bytes.
	start := make([]byte, 4)
	binary.BigEndian.PutUint32(start, 1_800_000)
	if !bytes.Contains(b, start) {
		t.Error("chapter 2 start time is not present as a big-endian uint32")
	}
}

func TestWriteID3EmbedsCoverArt(t *testing.T) {
	t.Parallel()

	cover := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'j', 'p', 'e', 'g'}
	var out bytes.Buffer
	err := WriteID3(&out, Tags{
		Title:     "Book",
		Cover:     cover,
		CoverMIME: "image/jpeg",
	}, nil)
	if err != nil {
		t.Fatalf("WriteID3: %v", err)
	}

	b := out.Bytes()
	if !bytes.Contains(b, []byte("APIC")) {
		t.Fatal("tag is missing the APIC frame")
	}
	if !bytes.Contains(b, []byte("image/jpeg")) {
		t.Error("APIC frame does not declare the cover MIME type")
	}
	if !bytes.Contains(b, cover) {
		t.Error("APIC frame does not carry the cover bytes")
	}
}

// A title outside Latin-1 is the common case for exactly the books people
// most want narrated, and ID3v2.3 has one encoding that can carry it.
func TestWriteID3CarriesNonLatinTitles(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := WriteID3(&out, Tags{Title: "砂の女"}, nil); err != nil {
		t.Fatalf("WriteID3: %v", err)
	}
	b := out.Bytes()

	// UTF-16LE with a byte-order mark: encoding byte 0x01, then FF FE.
	if !bytes.Contains(b, []byte{0x01, 0xFF, 0xFE}) {
		t.Error("text frames are not UTF-16 with a BOM, so non-Latin titles cannot round-trip")
	}
	want := utf16LEWithBOM("砂の女")
	if !bytes.Contains(b, want) {
		t.Error("the title is not present as UTF-16LE")
	}
}
