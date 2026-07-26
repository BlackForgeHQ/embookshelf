// SPDX-License-Identifier: AGPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/task"
)

// probeArgs is a stand-in job type — the registry must work for any
// JobArgs, not just the three this binary ships.
type probeArgs struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (probeArgs) Kind() string { return "test.probe" }

func TestRegisterExposesTheArgsKind(t *testing.T) {
	t.Parallel()

	reg := register(func(context.Context, probeArgs) error { return nil })
	if reg.kind != "test.probe" {
		t.Fatalf("kind = %q, want test.probe", reg.kind)
	}
}

func TestRegisteredHandlerDecodesArgsAndDispatches(t *testing.T) {
	t.Parallel()

	var got probeArgs
	reg := register(func(_ context.Context, a probeArgs) error {
		got = a
		return nil
	})

	if err := reg.sqliteHandler(context.Background(), `{"name":"widget","count":7}`); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if got.Name != "widget" || got.Count != 7 {
		t.Fatalf("decoded args = %+v, want {widget 7}", got)
	}
}

func TestRegisteredHandlerPropagatesWorkerError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	reg := register(func(context.Context, probeArgs) error { return boom })

	err := reg.sqliteHandler(context.Background(), `{"name":"x"}`)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// A malformed payload must fail before the worker runs — a job whose
// args can't be decoded should be recorded as failed, not dispatched
// with a zero-valued struct.
func TestRegisteredHandlerRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	called := false
	reg := register(func(context.Context, probeArgs) error {
		called = true
		return nil
	})

	err := reg.sqliteHandler(context.Background(), `{"name":`)
	if err == nil {
		t.Fatal("want a decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode args") {
		t.Errorf("err = %v, want it to name the decode failure", err)
	}
	if called {
		t.Error("worker ran despite an undecodable payload")
	}
}

// The registry is the single place job types are declared. Every kind
// the binary ships must appear exactly once.
func TestRegistryCoversEveryJobKindExactlyOnce(t *testing.T) {
	t.Parallel()

	want := []string{
		task.BookDropIngestArgs{}.Kind(),
		task.LibraryScanArgs{}.Kind(),
		task.SendToKindleArgs{}.Kind(),
	}

	seen := map[string]int{}
	for _, reg := range registry(Deps{}) {
		seen[reg.kind]++
		if reg.sqliteHandler == nil {
			t.Errorf("kind %q registered without a SQLite handler", reg.kind)
		}
		if reg.addToRiver == nil {
			t.Errorf("kind %q registered without a River worker", reg.kind)
		}
	}

	for _, kind := range want {
		switch seen[kind] {
		case 1:
		case 0:
			t.Errorf("kind %q missing from the registry", kind)
		default:
			t.Errorf("kind %q registered %d times, want exactly 1", kind, seen[kind])
		}
	}
	if len(seen) != len(want) {
		t.Errorf("registry has %d kinds, want %d — a job was added without a test", len(seen), len(want))
	}
}
