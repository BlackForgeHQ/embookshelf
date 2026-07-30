// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestNormalizeEndpoint(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"fsn1.your-objectstorage.com", "https://fsn1.your-objectstorage.com"},
		{"https://s3.example.com", "https://s3.example.com"},
		{"http://localhost:9000", "http://localhost:9000"},
		{"localhost:9000", "https://localhost:9000"},
		{"  s3.example.com ", "https://s3.example.com"},
	}
	for _, c := range cases {
		if got := normalizeEndpoint(c.in); got != c.want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// readUpTo is the hinge Put turns on: whether the reader is exhausted,
// not whether the buffer is full, decides between a single request and a
// multipart upload. The two are indistinguishable at exactly one part,
// which is the case a `n < len(buf)` test gets wrong.
func TestReadUpTo(t *testing.T) {
	const limit = 64

	cases := []struct {
		name      string
		size      int
		wantEOF   bool
		chunkSize int // 0 = read as much as asked
	}{
		{name: "empty", size: 0, wantEOF: true},
		{name: "one byte", size: 1, wantEOF: true},
		{name: "one under the limit", size: limit - 1, wantEOF: true},
		{name: "exactly the limit", size: limit, wantEOF: false},
		{name: "over the limit", size: limit + 1, wantEOF: false},
		{name: "dribbled in", size: limit - 3, wantEOF: true, chunkSize: 7},
		{name: "dribbled past the limit", size: limit * 3, wantEOF: false, chunkSize: 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := bytes.Repeat([]byte("abcdefgh"), (c.size/8)+1)[:c.size]
			var r io.Reader = bytes.NewReader(src)
			if c.chunkSize > 0 {
				r = &dribble{r: r, n: c.chunkSize}
			}

			got, atEOF, err := readUpTo(r, limit)
			if err != nil {
				t.Fatalf("readUpTo: %v", err)
			}
			if atEOF != c.wantEOF {
				t.Errorf("atEOF = %v, want %v (size %d, limit %d)",
					atEOF, c.wantEOF, c.size, limit)
			}
			want := src
			if len(want) > limit {
				want = want[:limit]
			}
			if !bytes.Equal(got, want) {
				t.Errorf("read %q, want %q", got, want)
			}
			if !atEOF {
				// The multipart branch refills this exact buffer with
				// io.ReadFull, so it has to come back part-sized.
				if len(got) != limit || cap(got) != limit {
					t.Errorf("len/cap = %d/%d, want %d/%d — the caller reuses "+
						"this buffer for every subsequent part",
						len(got), cap(got), limit, limit)
				}
			}
		})
	}
}

// A read error must surface rather than being mistaken for the end of
// the object, which would silently truncate an upload.
func TestReadUpToPropagatesError(t *testing.T) {
	boom := errors.New("disk fell over")
	r := io.MultiReader(strings.NewReader("some bytes"), errReader{boom})
	if _, _, err := readUpTo(r, 64); !errors.Is(err, boom) {
		t.Fatalf("readUpTo = %v, want %v", err, boom)
	}
}

// dribble hands out at most n bytes per Read, the way a network or pipe
// source does.
type dribble struct {
	r io.Reader
	n int
}

func (d *dribble) Read(b []byte) (int, error) {
	if len(b) > d.n {
		b = b[:d.n]
	}
	return d.r.Read(b)
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
