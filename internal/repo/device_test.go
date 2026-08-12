// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// user_devices had no tests at all until the projection conversion
// (#313), and it carries the Column-order coupling hazard's classic
// shape: kind, name and secret are three adjacent TEXT columns, so a
// crossed pair compiles, runs, and sends a device push to the wrong
// endpoint with the wrong secret. Every field below carries a value
// distinct from every other field of its type — verified to fail on a
// deliberate crossing before the projection was introduced.
func TestDeviceRepo_CreateReadRoundTrip(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	devices := repo.NewDeviceRepo(d)
	ctx := context.Background()

	owner, err := users.Create(ctx, "device-owner@example.com", "Owner", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	created, err := devices.Create(ctx, model.Device{
		UserID: owner.ID,
		Kind:   model.DeviceKind("remarkable"),
		Name:   "living room tablet",
		Secret: "device-token-value",
		Config: map[string]any{"folder": "/books", "resolution": "1872"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty id")
	}

	got, err := devices.GetForUser(ctx, owner.ID, created.ID)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if got.UserID != owner.ID {
		t.Errorf("UserID = %q, want %q", got.UserID, owner.ID)
	}
	if got.Kind != "remarkable" {
		t.Errorf("Kind = %q, want remarkable (crossed with a neighbouring text column?)", got.Kind)
	}
	if got.Name != "living room tablet" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Secret != "device-token-value" {
		t.Errorf("Secret = %q", got.Secret)
	}
	if got.Config["folder"] != "/books" || got.Config["resolution"] != "1872" {
		t.Errorf("Config = %v, want the stored JSONB document", got.Config)
	}
	if got.LastSentAt != nil {
		t.Errorf("LastSentAt = %v, want nil before any send", got.LastSentAt)
	}
	if got.LastError != "" {
		t.Errorf("LastError = %q, want empty before any send", got.LastError)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps not scanned")
	}

	list, err := devices.ListForUser(ctx, owner.ID)
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("ListForUser = (%v, %v), want the one device", list, err)
	}
}

// A NULL config document reads as an empty, non-nil map — callers index
// it without a guard.
func TestDeviceRepo_NilConfigReadsAsEmptyMap(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	devices := repo.NewDeviceRepo(d)
	ctx := context.Background()

	owner, err := users.Create(ctx, "nilcfg@example.com", "Owner", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	created, err := devices.Create(ctx, model.Device{
		UserID: owner.ID, Kind: "kindle", Name: "paperwhite", Secret: "s",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := devices.GetForUser(ctx, owner.ID, created.ID)
	if err != nil {
		t.Fatalf("GetForUser: %v", err)
	}
	if got.Config == nil {
		t.Fatal("Config = nil, want an empty map")
	}
	if len(got.Config) != 0 {
		t.Fatalf("Config = %v, want empty", got.Config)
	}
}

// MarkSendResult's two arms: success stamps last_sent_at and clears the
// error; failure records the message (truncated) and leaves last_sent_at
// alone.
func TestDeviceRepo_MarkSendResult(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	devices := repo.NewDeviceRepo(d)
	ctx := context.Background()

	owner, err := users.Create(ctx, "sendresult@example.com", "Owner", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	created, err := devices.Create(ctx, model.Device{
		UserID: owner.ID, Kind: "kindle", Name: "pw", Secret: "s",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := devices.MarkSendResult(ctx, owner.ID, created.ID, errors.New("dial tcp: refused")); err != nil {
		t.Fatalf("MarkSendResult(err): %v", err)
	}
	got, _ := devices.GetForUser(ctx, owner.ID, created.ID)
	if got.LastError != "dial tcp: refused" {
		t.Errorf("LastError = %q", got.LastError)
	}
	if got.LastSentAt != nil {
		t.Errorf("LastSentAt = %v, want nil after a failure", got.LastSentAt)
	}

	if err := devices.MarkSendResult(ctx, owner.ID, created.ID, nil); err != nil {
		t.Fatalf("MarkSendResult(nil): %v", err)
	}
	got, _ = devices.GetForUser(ctx, owner.ID, created.ID)
	if got.LastError != "" {
		t.Errorf("LastError = %q, want cleared on success", got.LastError)
	}
	if got.LastSentAt == nil {
		t.Error("LastSentAt = nil, want stamped on success")
	}
}
