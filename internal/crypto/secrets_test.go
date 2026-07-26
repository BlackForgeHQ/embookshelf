// SPDX-License-Identifier: AGPL-3.0-or-later

package crypto_test

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/crypto"
)

func upper(s string) (string, error) { return strings.ToUpper(s), nil }

func TestTransformSlotsAppliesToEveryNonEmptySlot(t *testing.T) {
	t.Parallel()

	a, b := "alpha", "beta"
	if err := crypto.TransformSlots(upper, []*string{&a, &b}); err != nil {
		t.Fatalf("TransformSlots returned error: %v", err)
	}
	if a != "ALPHA" || b != "BETA" {
		t.Fatalf("slots = %q, %q; want ALPHA, BETA", a, b)
	}
}

func TestTransformSlotsSkipsEmptySlots(t *testing.T) {
	t.Parallel()

	calls := 0
	count := func(s string) (string, error) {
		calls++
		return s + "!", nil
	}

	empty, set := "", "x"
	if err := crypto.TransformSlots(count, []*string{&empty, &set}); err != nil {
		t.Fatalf("TransformSlots returned error: %v", err)
	}
	if empty != "" {
		t.Errorf("empty slot = %q, want unchanged empty string", empty)
	}
	if calls != 1 {
		t.Errorf("op called %d times, want 1 (empty slot must be skipped)", calls)
	}
}

func TestTransformSlotsSkipsNilPointers(t *testing.T) {
	t.Parallel()

	set := "x"
	if err := crypto.TransformSlots(upper, []*string{nil, &set}); err != nil {
		t.Fatalf("TransformSlots returned error: %v", err)
	}
	if set != "X" {
		t.Errorf("slot = %q, want X", set)
	}
}

// All-or-nothing: a partial failure must not leave some slots
// transformed — a half-encrypted config must never reach the database.
func TestTransformSlotsLeavesSlotsUntouchedOnError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	failOnSecond := func(s string) (string, error) {
		if s == "second" {
			return "", boom
		}
		return strings.ToUpper(s), nil
	}

	first, second := "first", "second"
	err := crypto.TransformSlots(failOnSecond, []*string{&first, &second})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if first != "first" || second != "second" {
		t.Fatalf("slots = %q, %q; want both unmodified", first, second)
	}
}

func TestTransformSlotsEmptySliceIsNoOp(t *testing.T) {
	t.Parallel()

	if err := crypto.TransformSlots(upper, nil); err != nil {
		t.Fatalf("TransformSlots(nil) returned error: %v", err)
	}
}

// Round-trip through the real cipher: what TransformSlots encrypts,
// TransformSlots decrypts.
func TestTransformSlotsRoundTripsThroughAESGCM(t *testing.T) {
	t.Parallel()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := crypto.NewAESGCM(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewAESGCM: %v", err)
	}

	secret := "hunter2"
	if err := crypto.TransformSlots(c.Encrypt, []*string{&secret}); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if secret == "hunter2" {
		t.Fatal("slot still holds plaintext after encrypt")
	}
	if err := crypto.TransformSlots(c.Decrypt, []*string{&secret}); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if secret != "hunter2" {
		t.Fatalf("round-trip = %q, want hunter2", secret)
	}
}
