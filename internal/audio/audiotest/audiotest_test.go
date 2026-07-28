// SPDX-License-Identifier: AGPL-3.0-or-later

package audiotest_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/audio"
	"github.com/blackforge/embookshelf/internal/audio/audiotest"
)

// A fixture the production parser rejects would fail every downstream
// test with a message about the fixture rather than the code under test.
func TestFramesParseAsRealMPEGAudio(t *testing.T) {
	t.Parallel()

	frames, ms, err := audio.Payload(audiotest.Frames(4))
	if err != nil {
		t.Fatalf("audio.Payload rejected the frame fixture: %v", err)
	}
	if want := 4 * audiotest.FrameBytes; len(frames) != want {
		t.Errorf("payload is %d bytes, want %d", len(frames), want)
	}
	if ms <= 0 {
		t.Errorf("duration is %dms, want a positive measurement", ms)
	}
}
