// SPDX-License-Identifier: AGPL-3.0-or-later

package storagetest_test

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/storage/storagetest"
)

func TestLocalFS_Contract(t *testing.T) {
	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
		fs, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return fs, func() {}
	})
}

// liarEnv selects which lying backend the re-executed test binary runs.
// Empty means "this is the parent process".
const liarEnv = "STORAGETEST_LIAR"

// overpromising advertises every option-gated capability while delegating
// to LocalFS, which refuses all three options outright. It is the backend
// the acceptance criteria describe as "advertises a bit it does not
// honour".
type overpromising struct{ *local.LocalFS }

func (overpromising) Capabilities() storage.Capability {
	return storage.CapRange | storage.CapConditional | storage.CapVersioning
}

// silentlyPermissive keeps LocalFS's honest answer of zero capabilities
// but swallows WithRange instead of refusing it. It is the other half of
// the contract: an option accepted without the bit is as much a lie as a
// bit advertised without the option, because a caller that gates on
// Capabilities() will never learn its range was ignored.
type silentlyPermissive struct{ *local.LocalFS }

func (s silentlyPermissive) Get(ctx context.Context, key string, _ ...storage.GetOption) (io.ReadCloser, error) {
	return s.LocalFS.Get(ctx, key)
}

// TestSuiteCatchesCapabilityLies is a test of the suite rather than of a
// backend: it runs RunSuite against two deliberately inconsistent
// backends and requires it to fail on both. Without this, a capability
// table that asserted nothing would look exactly like one that worked —
// every real backend would still be green.
//
// The lying runs happen in a re-executed copy of this test binary because
// their failures are the expected result; a child process lets the parent
// assert on the exit status instead of the harness reporting real
// failures.
func TestSuiteCatchesCapabilityLies(t *testing.T) {
	newLocal := func(t *testing.T) *local.LocalFS {
		fs, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}

	switch os.Getenv(liarEnv) {
	case "overpromising":
		storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
			return overpromising{newLocal(t)}, func() {}
		})
		return
	case "silentlyPermissive":
		storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
			return silentlyPermissive{newLocal(t)}, func() {}
		})
		return
	}

	for _, liar := range []struct{ mode, lie string }{
		{"overpromising", "advertises CapRange, CapConditional and CapVersioning while refusing all three options"},
		{"silentlyPermissive", "accepts WithRange without advertising CapRange"},
	} {
		t.Run(liar.mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0],
				"-test.run=^TestSuiteCatchesCapabilityLies$", "-test.count=1")
			cmd.Env = append(os.Environ(), liarEnv+"="+liar.mode)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("RunSuite passed a backend that %s — the capability "+
					"bits are documentation, not a contract:\n%s", liar.lie, out)
			}
		})
	}
}
