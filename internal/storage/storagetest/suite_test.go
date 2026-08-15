// SPDX-License-Identifier: AGPL-3.0-or-later

package storagetest_test

import (
	"bytes"
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

// TestLocalFS_ContractAtDeployedRoot runs the same contract against the
// local backend in the only rooting that ships: storageloader builds it
// with local.New("/") on both its paths, and ADR-0030 §1 keeps it there.
//
// The arm above is the one production never builds. Rooting per subtest
// at t.TempDir() gives the backend a keyspace it owns, so every key it
// sees is relative and there is exactly one shape of them. A "/"-rooted
// backend gets neither: its keys are whole filesystem paths, and it is
// handed them both with and without the leading slash — from the shim on
// one side, from its own List output and unshimmed callers on the other.
// That asymmetry is what produced #168, #201 and #202, and the suite could
// not fail on it while the only local arm was rooted somewhere else.
func TestLocalFS_ContractAtDeployedRoot(t *testing.T) {
	storagetest.RunArm(t, storagetest.Arm{
		Root: storagetest.RootGlobal,
		New: func(t *testing.T) storagetest.Instance {
			fs, err := local.New("/")
			if err != nil {
				t.Fatal(err)
			}
			// The subtree, not the root, is what makes each subtest start
			// clean — the root is the machine's filesystem and is shared
			// with everything on it.
			return storagetest.Instance{
				Storage: fs,
				Subtree: t.TempDir(),
				Close:   func() {},
			}
		},
	})
}

// bufferingPut is the defect #266 fixed in the S3 adapter, reproduced on
// a backend the suite can run anywhere: it reads the whole body into
// memory and only then writes it. Every byte round-trips, every size
// "works" — which is precisely why a conformance case phrased as a size
// would call it correct.
type bufferingPut struct{ *local.LocalFS }

func (b bufferingPut) Put(ctx context.Context, key string, r io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return storage.PutResult{}, err
	}
	return b.LocalFS.Put(ctx, key, bytes.NewReader(body), opts...)
}

// bufferEnv selects the buffering backend in the re-executed binary.
const bufferEnv = "STORAGETEST_BUFFERING_PUT"

// TestSuiteCatchesABufferingBackend proves the streaming case can fail.
//
// It is the same idea as the capability liars, aimed at the defect #266
// found in the S3 adapter: a Put that reads the whole body before writing
// it. Every byte round-trips and every size "works", so a conformance
// case phrased as a size would pass it — which is how the real one
// survived review. This requires the suite to fail it.
func TestSuiteCatchesABufferingBackend(t *testing.T) {
	if os.Getenv(bufferEnv) != "" {
		storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
			fs, err := local.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return bufferingPut{fs}, func() {}
		})
		return
	}

	requireChildFails(t, "TestSuiteCatchesABufferingBackend",
		"RunSuite passed a backend that reads the whole body into memory "+
			"before writing it — the streaming expectation is a comment, not "+
			"a contract",
		bufferEnv+"=1")
}

// armEnv selects which malformed Arm the re-executed test binary hands
// RunArm. Empty means "this is the parent process".
const armEnv = "STORAGETEST_BAD_ARM"

// TestSuiteRejectsMalformedArms is the root-shape half of the same idea:
// it hands RunArm arms that do not say — or contradict — what their
// backend is rooted at, and requires each to be refused.
//
// Without this the declaration would be a comment. An Arm that left Root
// at its zero value would look exactly like one that thought about it,
// which is how the local backend spent its whole life being graded in a
// rooting no deployment builds (ADR-0030 §1). The two Subtree cases are
// the same gate from the other side: scratch space is what a RootGlobal
// arm needs and what a RootScoped arm cannot mean.
func TestSuiteRejectsMalformedArms(t *testing.T) {
	newLocal := func(t *testing.T) storage.Storage {
		fs, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}

	switch os.Getenv(armEnv) {
	case "undeclaredRoot":
		storagetest.RunArm(t, storagetest.Arm{
			// Root deliberately left at its zero value.
			New: func(t *testing.T) storagetest.Instance {
				return storagetest.Instance{Storage: newLocal(t)}
			},
		})
		return
	case "scopedWithSubtree":
		storagetest.RunArm(t, storagetest.Arm{
			Root: storagetest.RootScoped,
			New: func(t *testing.T) storagetest.Instance {
				return storagetest.Instance{Storage: newLocal(t), Subtree: t.TempDir()}
			},
		})
		return
	case "globalWithoutSubtree":
		storagetest.RunArm(t, storagetest.Arm{
			Root: storagetest.RootGlobal,
			New: func(t *testing.T) storagetest.Instance {
				fs, err := local.New("/")
				if err != nil {
					t.Fatal(err)
				}
				return storagetest.Instance{Storage: fs}
			},
		})
		return
	}

	for _, bad := range []struct{ mode, arm string }{
		{"undeclaredRoot", "never declared what its backend is rooted at"},
		{"scopedWithSubtree", "called itself scoped yet asked for a scratch subtree"},
		{"globalWithoutSubtree", "called itself global yet supplied no scratch subtree, " +
			"which would have run the contract against the real filesystem root"},
	} {
		t.Run(bad.mode, func(t *testing.T) {
			requireChildFails(t, "TestSuiteRejectsMalformedArms",
				"RunArm accepted an Arm that "+bad.arm+" — the root shape is "+
					"documentation, not a gate",
				armEnv+"="+bad.mode)
		})
	}
}

// requireChildFails re-executes this test binary with the given
// "KEY=VALUE" environment, running only testName, and requires it to
// fail. The child process is what lets the parent assert on a failure
// that is the expected result, instead of the harness reporting it as a
// real one.
func requireChildFails(t *testing.T, testName, because string, env ...string) {
	t.Helper()
	out, err := runChild(t, testName, env...)
	if err == nil {
		t.Fatalf("%s:\n%s", because, out)
	}
}

// runChild re-executes this test binary running only testName, with env
// entries appended to the current environment. Later entries win — a
// "KEY=" therefore clears a variable the parent run was started with.
func runChild(t *testing.T, testName string, env ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), env...)
	return cmd.CombinedOutput()
}
