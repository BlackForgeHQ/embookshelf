// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import "testing"

func TestNewID(t *testing.T) {
	id := NewID()
	if len(id) != 36 {
		t.Fatalf("got %q (len=%d), want 36-char UUID", id, len(id))
	}
	if id == NewID() {
		t.Fatal("two NewID() calls returned the same value")
	}
}
