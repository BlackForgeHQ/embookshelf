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
			requireChildFails(t, "TestSuiteCatchesCapabilityLies", liarEnv, liar.mode,
				"RunSuite passed a backend that "+liar.lie+" — the capability "+
					"bits are documentation, not a contract")
		})
	}
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
			requireChildFails(t, "TestSuiteRejectsMalformedArms", armEnv, bad.mode,
				"RunArm accepted an Arm that "+bad.arm+" — the root shape is "+
					"documentation, not a gate")
		})
	}
}

// requireChildFails re-executes this test binary with env=value, running
// only testName, and requires it to fail. The child process is what lets
// the parent assert on a failure that is the expected result, instead of
// the harness reporting it as a real one.
func requireChildFails(t *testing.T, testName, env, value, because string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), env+"="+value)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("%s:\n%s", because, out)
	}
}
