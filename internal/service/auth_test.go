package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// fakeUserStatusRepo is the in-memory test seam. It implements only the
// methods userStatusRepo names; nothing else from UserRepo is needed.
type fakeUserStatusRepo struct {
	users map[string]model.User
}

func (f *fakeUserStatusRepo) GetByID(_ context.Context, id string) (model.User, error) {
	u, ok := f.users[id]
	if !ok {
		return model.User{}, repo.ErrNotFound
	}
	return u, nil
}

func (f *fakeUserStatusRepo) UpdateStatus(_ context.Context, id string, status model.UserStatus) error {
	u, ok := f.users[id]
	if !ok {
		return repo.ErrNotFound
	}
	now := time.Now()
	u.Status = status
	u.StatusChangedAt = &now
	f.users[id] = u
	return nil
}

func (f *fakeUserStatusRepo) CountByRole(_ context.Context, role model.Role) (int, error) {
	n := 0
	for _, u := range f.users {
		if u.Role == role && u.Status == model.UserStatusActive {
			n++
		}
	}
	return n, nil
}

func newApproveTestSetup() (*fakeUserStatusRepo, model.User, model.User) {
	admin := model.User{ID: "admin-1", Email: "a@x", Role: model.RoleAdmin, Status: model.UserStatusActive}
	pending := model.User{ID: "u-2", Email: "p@x", Role: model.RoleUser, Status: model.UserStatusPending}
	return &fakeUserStatusRepo{users: map[string]model.User{
		admin.ID:   admin,
		pending.ID: pending,
	}}, admin, pending
}

func TestApproveUserFlipsPendingToActive(t *testing.T) {
	repo, admin, pending := newApproveTestSetup()

	changed, err := approveUser(context.Background(), repo, pending.ID)
	if err != nil {
		t.Fatalf("approveUser: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true on pending → active")
	}
	got := repo.users[pending.ID]
	if got.Status != model.UserStatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.StatusChangedAt == nil {
		t.Fatalf("status_changed_at not set")
	}
	_ = admin
}

func TestApproveUserIsIdempotentOnActive(t *testing.T) {
	repo, _, pending := newApproveTestSetup()
	pending.Status = model.UserStatusActive
	repo.users[pending.ID] = pending

	changed, err := approveUser(context.Background(), repo, pending.ID)
	if err != nil {
		t.Fatalf("approveUser idempotent: %v", err)
	}
	if changed {
		t.Fatalf("changed = true, want false on already-active")
	}
}

func TestDenyUserFlipsPendingToDenied(t *testing.T) {
	repo, admin, pending := newApproveTestSetup()
	changed, err := denyUser(context.Background(), repo, admin.ID, pending.ID)
	if err != nil {
		t.Fatalf("denyUser: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true on pending → denied")
	}
	if repo.users[pending.ID].Status != model.UserStatusDenied {
		t.Fatalf("status = %q, want denied", repo.users[pending.ID].Status)
	}
}

func TestDenyUserRefusesSelf(t *testing.T) {
	repo, admin, _ := newApproveTestSetup()
	_, err := denyUser(context.Background(), repo, admin.ID, admin.ID)
	if !errors.Is(err, ErrCannotTargetSelf) {
		t.Fatalf("err = %v, want ErrCannotTargetSelf", err)
	}
}

func TestDenyUserRefusesLastAdmin(t *testing.T) {
	repo, admin, _ := newApproveTestSetup()
	other := model.User{ID: "admin-2", Email: "b@x", Role: model.RoleAdmin, Status: model.UserStatusActive}
	repo.users[other.ID] = other

	// Two active admins exist — denying the second admin should succeed.
	if _, err := denyUser(context.Background(), repo, admin.ID, other.ID); err != nil {
		t.Fatalf("denying second admin should succeed: %v", err)
	}
	// Now only `admin` (admin-1) remains active. Denying them via an
	// arbitrary other-admin path should be blocked by the last-admin guard.
	repo.users[admin.ID] = admin
	if _, err := denyUser(context.Background(), repo, "ghost", admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", err)
	}
}
