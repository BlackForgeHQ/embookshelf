// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"

	"github.com/dhowden/tag"

	"github.com/blackforge/embookshelf/internal/storage"
)

// AudioProcessor handles single-file audiobooks: MP3 (ID3v2-tagged) and
// M4B/M4A (iTunes-style atoms). Multi-file folder-as-book is a separate
// design that's out of scope for now — each file is one book.
//
// Tag/cover parsing uses github.com/dhowden/tag. Duration is derived
// in this package: for MP3 we look for an XING/Info header in the first
// audio frame; for MP4 we parse the mvhd atom out of the moov box. Both
// are best-effort — a missing header leaves DurationSeconds nil and the
// reader UI just shows "—" instead.
type AudioProcessor struct{}

func (AudioProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	_ = ctx

	// Wrap the Source as an io.ReadSeeker via SectionReader so tag.ReadFrom
	// and the duration parsers can seek without consuming the full object.
	f := io.NewSectionReader(src, 0, src.Size())

	m := Metadata{Format: "MP3"} // default; overridden below if we know the format

	// Tags + cover are best-effort. ReadFrom seeks the file; on bare MP3
	// streams without an ID3v2 tag it can return an error — fall through
	// without erroring out.
	if t, err := tag.ReadFrom(f); err == nil {
		applyAudioTags(&m, t)
		// Refine the format from the parsed tag type when available.
		switch t.FileType() {
		case tag.MP3:
			m.Format = "MP3"
		case tag.M4A, tag.M4B, tag.M4P:
			m.Format = "M4B"
		}
	}

	// Duration: re-seek the file because tag.ReadFrom moved the cursor.
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		switch m.Format {
		case "MP3":
			if secs, ok := mp3Duration(f); ok {
				m.DurationSeconds = &secs
			}
		case "M4B":
			if secs, ok := mp4Duration(f); ok {
				m.DurationSeconds = &secs
			}
		}
	}

	// Title fallback: without a filename we leave Title empty rather than
	// deriving something meaningless. Callers can supply a filename-derived
	// title via the sidecar layer.

	return m, nil
}

func applyAudioTags(m *Metadata, t tag.Metadata) {
	if title := strings.TrimSpace(t.Title()); title != "" {
		m.Title = title
	} else if alb := strings.TrimSpace(t.Album()); alb != "" {
		// Audiobook conventions: Album = book title, Artist = author or
		// narrator depending on the tagger. When Title is empty (rare
		// for audiobooks but happens in podcast-style tagging) the album
		// is the closer match.
		m.Title = alb
	}
	// Author is the audiobook's writer; many taggers store it as
	// "AlbumArtist" with the narrator in "Artist". Fall back to Artist
	// when AlbumArtist is empty.
	if aa := strings.TrimSpace(t.AlbumArtist()); aa != "" {
		m.Author = aa
	} else if ar := strings.TrimSpace(t.Artist()); ar != "" {
		m.Author = ar
	}
	// Narrator: when both AlbumArtist and Artist are populated and
	// distinct, the Artist field is the narrator.
	aa := strings.TrimSpace(t.AlbumArtist())
	ar := strings.TrimSpace(t.Artist())
	if aa != "" && ar != "" && aa != ar {
		m.Narrator = ar
	}
	if c := strings.TrimSpace(t.Comment()); c != "" {
		m.Description = c
	}

	// pic.MIMEType is a string the tagger wrote into the APIC frame, and
	// the cover route serves it back as the response Content-Type — a
	// frame declaring "text/html" over a document was stored XSS (#330).
	// Sniffing the picture data instead also retires the hardcoded
	// "image/jpeg" default for frames with no declared type: those are
	// almost always JPEG, and now they are typed as such only when they
	// actually are.
	if pic := t.Picture(); pic != nil && len(pic.Data) > 0 {
		if mime := SniffImageMime(pic.Data); mime != "" {
			m.HasCover = true
			m.CoverBytes = pic.Data
			m.CoverMime = mime
		}
	}
}

// ---------------------------------------------------------------------------
// MP3 duration via XING/Info header.
// ---------------------------------------------------------------------------

// mp3Duration tries to read the XING / Info VBR header from the first
// MPEG audio frame and compute total duration in seconds.
//
// Returns (0, false) when:
//   - the file isn't a parseable MPEG stream
//   - the first frame has no XING/Info header (CBR file, or stripped tag)
//
// We deliberately don't fall back to "scan every frame" — for a long
// audiobook that's tens of MB of I/O on every ingest. Operators who
// care can re-tag with a XING-aware encoder.
func mp3Duration(f io.ReadSeeker) (int, bool) {
	// Skip ID3v2 if present.
	id3Hdr := make([]byte, 10)
	if _, err := io.ReadFull(f, id3Hdr); err != nil {
		return 0, false
	}
	if string(id3Hdr[:3]) == "ID3" {
		// Synchsafe size at bytes 6..9 (each byte uses 7 bits).
		size := int(id3Hdr[6]&0x7f)<<21 |
			int(id3Hdr[7]&0x7f)<<14 |
			int(id3Hdr[8]&0x7f)<<7 |
			int(id3Hdr[9]&0x7f)
		if _, err := f.Seek(int64(size), io.SeekCurrent); err != nil {
			return 0, false
		}
	} else {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, false
		}
	}

	// Read up to 4KB and scan for the next MPEG sync (0xFFE_).
	scan := make([]byte, 4096)
	n, _ := io.ReadFull(f, scan)
	if n < 4 {
		return 0, false
	}
	frameStart := -1
	for i := 0; i+1 < n; i++ {
		if scan[i] == 0xFF && (scan[i+1]&0xE0) == 0xE0 {
			frameStart = i
			break
		}
	}
	if frameStart < 0 || frameStart+4 > n {
		return 0, false
	}

	hdr := scan[frameStart : frameStart+4]
	mpegVer := (hdr[1] >> 3) & 0x03 // 0=v2.5, 2=v2, 3=v1
	layer := (hdr[1] >> 1) & 0x03   // 1=layer3
	if mpegVer == 1 || layer != 1 { // reserved version, or non-layer-3
		return 0, false
	}
	sampleRateIdx := (hdr[2] >> 2) & 0x03
	if sampleRateIdx == 3 {
		return 0, false
	}
	channelMode := (hdr[3] >> 6) & 0x03

	// Sample rates by version.
	var srTable [4]int
	switch mpegVer {
	case 3: // MPEG-1
		srTable = [4]int{44100, 48000, 32000, 0}
	case 2: // MPEG-2
		srTable = [4]int{22050, 24000, 16000, 0}
	case 0: // MPEG-2.5
		srTable = [4]int{11025, 12000, 8000, 0}
	}
	sampleRate := srTable[sampleRateIdx]
	if sampleRate == 0 {
		return 0, false
	}

	// Samples per frame: MPEG-1 layer III = 1152, MPEG-2/2.5 layer III = 576.
	samplesPerFrame := 1152
	if mpegVer != 3 {
		samplesPerFrame = 576
	}

	// XING / Info header offset within the frame depends on version + channel.
	var xingOffset int
	switch {
	case mpegVer == 3 && channelMode == 3: // MPEG-1 mono
		xingOffset = 4 + 17
	case mpegVer == 3: // MPEG-1 stereo / joint / dual
		xingOffset = 4 + 32
	case channelMode == 3: // MPEG-2/2.5 mono
		xingOffset = 4 + 9
	default: // MPEG-2/2.5 stereo
		xingOffset = 4 + 17
	}

	// Need at least frameStart + xingOffset + 8 bytes (tag + flags + frames).
	end := frameStart + xingOffset + 8
	if end > n {
		return 0, false
	}
	tagBytes := scan[frameStart+xingOffset : frameStart+xingOffset+4]
	if string(tagBytes) != "Xing" && string(tagBytes) != "Info" {
		return 0, false
	}
	flags := binary.BigEndian.Uint32(scan[frameStart+xingOffset+4 : frameStart+xingOffset+8])
	if flags&0x01 == 0 {
		// "frames" field absent — can't compute duration without scanning.
		return 0, false
	}
	if frameStart+xingOffset+12 > n {
		return 0, false
	}
	totalFrames := binary.BigEndian.Uint32(scan[frameStart+xingOffset+8 : frameStart+xingOffset+12])
	if totalFrames == 0 {
		return 0, false
	}
	secs := int(int64(totalFrames) * int64(samplesPerFrame) / int64(sampleRate))
	if secs <= 0 {
		return 0, false
	}
	return secs, true
}

// ---------------------------------------------------------------------------
// MP4 (M4B/M4A) duration via mvhd atom.
// ---------------------------------------------------------------------------

// mp4Duration walks the top-level atoms of an MP4 container looking for
// moov.mvhd, then computes duration_seconds = mvhd.duration / mvhd.timescale.
//
// We only descend into the moov atom — that's where mvhd lives — so the
// I/O cost is bounded to a few hundred KB even on multi-GB audiobooks.
func mp4Duration(f io.ReadSeeker) (int, bool) {
	const maxAtomSearch = 64 // safety: never walk past the first 64 atoms
	r := io.ReadSeeker(f)
	for atomCount := 0; atomCount < maxAtomSearch; atomCount++ {
		size, name, ok := readAtomHeader(r)
		if !ok {
			return 0, false
		}
		switch name {
		case "moov":
			// Recurse into moov; size is the full atom size including the
			// 8-byte header we already consumed. Pass size-8 as the body
			// length so we know when to stop reading children.
			return findMvhd(r, int64(size)-8)
		case "mdat":
			// Audiobook data — large. Skip.
			fallthrough
		default:
			// Skip body; if size is 0 it means "to EOF" which means we
			// won't find mvhd anyway.
			if size <= 8 {
				return 0, false
			}
			if _, err := r.Seek(int64(size)-8, io.SeekCurrent); err != nil {
				return 0, false
			}
		}
	}
	return 0, false
}

// findMvhd walks one level of children inside the moov atom (bounded by
// `body` bytes) looking for the mvhd atom. Returns duration in seconds
// when found.
func findMvhd(r io.ReadSeeker, body int64) (int, bool) {
	for body > 8 {
		size, name, ok := readAtomHeader(r)
		if !ok {
			return 0, false
		}
		body -= int64(size)
		if name == "mvhd" {
			// mvhd payload layout:
			//   v=0: version(1) flags(3) created(4) modified(4) timescale(4) duration(4) ...
			//   v=1: version(1) flags(3) created(8) modified(8) timescale(4) duration(8) ...
			payload := make([]byte, size-8)
			if _, err := io.ReadFull(r, payload); err != nil {
				return 0, false
			}
			if len(payload) < 1 {
				return 0, false
			}
			version := payload[0]
			var timescale uint32
			var duration uint64
			switch version {
			case 0:
				if len(payload) < 4+4+4+4+4 {
					return 0, false
				}
				timescale = binary.BigEndian.Uint32(payload[4+4+4 : 4+4+4+4])
				duration = uint64(binary.BigEndian.Uint32(payload[4+4+4+4 : 4+4+4+4+4]))
			case 1:
				if len(payload) < 4+8+8+4+8 {
					return 0, false
				}
				timescale = binary.BigEndian.Uint32(payload[4+8+8 : 4+8+8+4])
				duration = binary.BigEndian.Uint64(payload[4+8+8+4 : 4+8+8+4+8])
			default:
				return 0, false
			}
			if timescale == 0 || duration == 0 {
				return 0, false
			}
			secs := int(duration / uint64(timescale))
			if secs <= 0 {
				return 0, false
			}
			return secs, true
		}
		// Skip non-mvhd children.
		if size <= 8 {
			return 0, false
		}
		if _, err := r.Seek(int64(size)-8, io.SeekCurrent); err != nil {
			return 0, false
		}
	}
	return 0, false
}

// readAtomHeader reads an 8-byte MP4 atom header (size + 4-char type).
// Returns false on EOF or short read. Doesn't handle extended 64-bit sizes
// (size==1) — those are vanishingly rare in audiobooks.
func readAtomHeader(r io.Reader) (size uint32, name string, ok bool) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(r, hdr); err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, "", false
		}
		return 0, "", false
	}
	size = binary.BigEndian.Uint32(hdr[:4])
	name = string(hdr[4:8])
	return size, name, true
}
