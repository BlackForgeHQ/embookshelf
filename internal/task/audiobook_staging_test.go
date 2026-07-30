// SPDX-License-Identifier: AGPL-3.0-or-later

package task_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/task"
)

// A deployment with no data root has nowhere to stage, and the staging
// value says so at every door rather than at some of them.
//
// This is the shape of #207 and of the bypass that followed it. Three
// deleters removed staging — the finalize worker's, the sweeper's, and an
// inline os.RemoveAll at the composition root — and only the first two
// went through the helper carrying this guard. The third survived on an
// accident: os.RemoveAll("") returns nil, so deleting an unconfigured
// staging area looked exactly like deleting an empty one. With the
// operations behind one value the accident is not reachable, and this
// test is what keeps it that way: every operation refuses, and the
// refusal is config.ErrDataRootUnset rather than each caller's own idea
// of what an empty path means.
func TestStagingRefusesEveryOperationWhenNoDataRootIsConfigured(t *testing.T) {
	staging := task.NewStaging(config.DataRoot{})

	if staging.Configured() {
		t.Error("an unset data root produced a configured staging area")
	}
	if _, err := staging.Dir("b1"); !errors.Is(err, config.ErrDataRootUnset) {
		t.Errorf("Dir err = %v, want ErrDataRootUnset", err)
	}
	if _, err := staging.SegmentPath("b1", 1, 0); !errors.Is(err, config.ErrDataRootUnset) {
		t.Errorf("SegmentPath err = %v, want ErrDataRootUnset", err)
	}

	// The write is the one that cost money the last time it went wrong: an
	// empty root joined to audiobooks/{book_id} is a *relative* path, so
	// the segment worker created it under the process working directory
	// and wrote hundreds of megabytes into it (#207).
	if _, err := staging.WriteSegment("b1", 1, 0, []byte("frames")); !errors.Is(err, config.ErrDataRootUnset) {
		t.Errorf("WriteSegment err = %v, want ErrDataRootUnset", err)
	}
	if _, err := os.Stat(filepath.Join("audiobooks", "b1")); err == nil {
		t.Error("a relative staging directory was created in the working directory")
	}

	// Clean is deliberately silent — it is called on paths that may never
	// have existed — so what it must not do is the assertion: no root
	// means nothing of ours is on disk to remove.
	staging.Clean("b1")

	// And the sweep declines to run at all, rather than asking the
	// database for runs whose staging it could not locate.
	n, err := staging.Sweep(context.Background(), refuseToList{t})
	if err != nil {
		t.Errorf("Sweep on an unconfigured staging area: %v", err)
	}
	if n != 0 {
		t.Errorf("Sweep reclaimed %d run(s) with no staging area configured", n)
	}
}

// One run's segments cannot land on another's, because the generation is
// a directory level in the path.
//
// Two things make this more than tidiness. WriteSegment is os.WriteFile,
// which is not atomic, so a superseded worker writing over a live
// segment can leave a truncated file that finalize concatenates without
// complaint. And the two plans can genuinely differ — a regeneration may
// pick a different voice, engine or segmentation cap — so the same seq of
// two generations is not the same audio, and generation-1 bytes at
// generation-2's path would ship the wrong voice silently (ADR-0031).
//
// What must keep working is the book directory: Clean and Sweep operate
// on the whole of it, and the segment rows carry their own staged_path,
// so finalize never derives a path at all.
func TestStagingKeepsEachGenerationsSegmentsApart(t *testing.T) {
	staging := tempStaging(t)

	first, err := staging.WriteSegment("b1", 1, 7, []byte("older"))
	if err != nil {
		t.Fatalf("WriteSegment gen 1: %v", err)
	}
	second, err := staging.WriteSegment("b1", 2, 7, []byte("newer"))
	if err != nil {
		t.Fatalf("WriteSegment gen 2: %v", err)
	}
	if first == second {
		t.Fatalf("both generations staged seq 7 at %s — a superseded worker can overwrite live audio", first)
	}
	if got, err := os.ReadFile(first); err != nil || string(got) != "older" {
		t.Errorf("generation 1's segment = %q/%v, want it untouched by generation 2", got, err)
	}

	// Both live under the book's own directory, which is what the per-run
	// clean and the hourly sweep take.
	dir, err := staging.Dir("b1")
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	for _, path := range []string{first, second} {
		if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
			t.Errorf("%s is outside the book's staging directory %s; Clean and Sweep would miss it", path, dir)
		}
	}
	staging.Clean("b1")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the book's staging directory survived Clean: %v", err)
	}
}

// refuseToList fails the test if the sweep asks it for anything.
type refuseToList struct{ t *testing.T }

func (r refuseToList) ListStaleStaging(context.Context, int) ([]string, error) {
	r.t.Error("the sweep queried for stale runs with no staging area configured")
	return nil, nil
}
