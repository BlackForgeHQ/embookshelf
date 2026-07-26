// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ---------------------------------------------------------------------------
// Fakes — in-memory stand-ins for the Provisioner's narrow interfaces.
// ---------------------------------------------------------------------------

type fakeProvSettings struct {
	details repo.OIDCAutoProvisionDetails
}

func (f *fakeProvSettings) GetOIDCAutoProvision(context.Context) (repo.OIDCAutoProvisionDetails, error) {
	return f.details, nil
}

type fakeProvUsers struct {
	byID    map[string]model.User
	byEmail map[string]model.User
	count   int

	created        []model.User
	createdPending []model.User
	deleted        []string
	lastSeen       []string

	createErr error
}

func newFakeProvUsers() *fakeProvUsers {
	return &fakeProvUsers{byID: map[string]model.User{}, byEmail: map[string]model.User{}}
}

func (f *fakeProvUsers) add(u model.User) {
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	f.count++
}

func (f *fakeProvUsers) GetByID(_ context.Context, id string) (model.User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return model.User{}, repo.ErrNotFound
}

func (f *fakeProvUsers) GetByEmail(_ context.Context, email string) (model.User, error) {
	if u, ok := f.byEmail[email]; ok {
		return u, nil
	}
	return model.User{}, repo.ErrNotFound
}

func (f *fakeProvUsers) Count(context.Context) (int, error) { return f.count, nil }

func (f *fakeProvUsers) CreateOIDC(_ context.Context, email, name string, role model.Role) (model.User, error) {
	if f.createErr != nil {
		return model.User{}, f.createErr
	}
	u := model.User{ID: "u-new", Email: email, Name: name, Role: role, Status: model.UserStatusActive}
	f.created = append(f.created, u)
	return u, nil
}

func (f *fakeProvUsers) CreateOIDCPending(_ context.Context, email, name string, role model.Role) (model.User, error) {
	if f.createErr != nil {
		return model.User{}, f.createErr
	}
	u := model.User{ID: "u-pending", Email: email, Name: name, Role: role, Status: model.UserStatusPending}
	f.createdPending = append(f.createdPending, u)
	return u, nil
}

func (f *fakeProvUsers) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeProvUsers) TouchLastSeen(_ context.Context, id string, _ time.Time) error {
	f.lastSeen = append(f.lastSeen, id)
	return nil
}

type fakeProvIdentities struct {
	byIssuerSubject map[string]model.Identity // key: issuer + "|" + subject
	countByUser     map[string]int

	relinked  []string // userID
	inserted  []string // userID
	touched   []string // identity ID
	insertErr error
}

func newFakeProvIdentities() *fakeProvIdentities {
	return &fakeProvIdentities{byIssuerSubject: map[string]model.Identity{}, countByUser: map[string]int{}}
}

func (f *fakeProvIdentities) GetByIssuerSubject(_ context.Context, issuer, subject string) (model.Identity, error) {
	if id, ok := f.byIssuerSubject[issuer+"|"+subject]; ok {
		return id, nil
	}
	return model.Identity{}, repo.ErrNotFound
}

func (f *fakeProvIdentities) CountByUser(_ context.Context, userID string) (int, error) {
	return f.countByUser[userID], nil
}

func (f *fakeProvIdentities) RelinkProvider(_ context.Context, userID, _, _, _, _ string) (model.Identity, error) {
	f.relinked = append(f.relinked, userID)
	return model.Identity{ID: "i-relink", UserID: userID}, nil
}

func (f *fakeProvIdentities) Insert(_ context.Context, userID, _, _, _, _ string) (model.Identity, error) {
	if f.insertErr != nil {
		return model.Identity{}, f.insertErr
	}
	f.inserted = append(f.inserted, userID)
	return model.Identity{ID: "i-new", UserID: userID}, nil
}

func (f *fakeProvIdentities) TouchLastLogin(_ context.Context, id string) error {
	f.touched = append(f.touched, id)
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func googleIdent() ExternalIdentity {
	return ExternalIdentity{
		Provider: "google",
		Issuer:   "https://accounts.google.com",
		Subject:  "sub-1",
		Email:    "reader@example.com",
		Name:     "Reader",
	}
}

func provisionWith(t *testing.T, users *fakeProvUsers, idents *fakeProvIdentities, details repo.OIDCAutoProvisionDetails, ident ExternalIdentity) ProvisionResult {
	t.Helper()
	p := NewProvisioner(&fakeProvSettings{details: details}, users, idents)
	res, err := p.Provision(context.Background(), ident)
	if err != nil {
		t.Fatalf("Provision returned unexpected error: %v", err)
	}
	return res
}

// ---------------------------------------------------------------------------
// 1) Direct identity hit
// ---------------------------------------------------------------------------

func TestProvisionDirectHitActive(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	idents := newFakeProvIdentities()
	idents.byIssuerSubject["https://accounts.google.com|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionResolved)
	}
	if res.User.ID != "u1" {
		t.Fatalf("user = %q, want u1", res.User.ID)
	}
	if len(idents.touched) != 1 || idents.touched[0] != "i1" {
		t.Errorf("TouchLastLogin calls = %v, want [i1]", idents.touched)
	}
	if len(users.lastSeen) != 1 || users.lastSeen[0] != "u1" {
		t.Errorf("TouchLastSeen calls = %v, want [u1]", users.lastSeen)
	}
}

func TestProvisionDirectHitPending(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusPending})
	idents := newFakeProvIdentities()
	idents.byIssuerSubject["https://accounts.google.com|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionPendingApproval {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionPendingApproval)
	}
	if res.User.ID != "u1" {
		t.Fatalf("pending outcome must carry the user, got %q", res.User.ID)
	}
}

func TestProvisionDirectHitDenied(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusDenied})
	idents := newFakeProvIdentities()
	idents.byIssuerSubject["https://accounts.google.com|sub-1"] = model.Identity{ID: "i1", UserID: "u1"}

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionDenied {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionDenied)
	}
}

// ---------------------------------------------------------------------------
// 2) Email auto-link
// ---------------------------------------------------------------------------

func TestProvisionAutoLinkActiveUser(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	idents := newFakeProvIdentities()

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionResolved)
	}
	if len(idents.relinked) != 1 || idents.relinked[0] != "u1" {
		t.Errorf("RelinkProvider calls = %v, want [u1]", idents.relinked)
	}
}

func TestProvisionAutoLinkNormalizesEmailCase(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	idents := newFakeProvIdentities()

	ident := googleIdent()
	ident.Email = "  Reader@Example.COM "
	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), ident)

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q (email should be lowercased+trimmed before match)", res.Status, ProvisionResolved)
	}
}

// The gate that closes the cross-provider bypass: a pending or denied
// user must never auto-link into a session, from either auth surface.
func TestProvisionAutoLinkPendingUserGated(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusPending})
	idents := newFakeProvIdentities()

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionPendingApproval {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionPendingApproval)
	}
	if len(idents.relinked) != 0 {
		t.Errorf("pending user must not be relinked, got %v", idents.relinked)
	}
}

func TestProvisionAutoLinkDeniedUserGated(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusDenied})
	idents := newFakeProvIdentities()

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionDenied {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionDenied)
	}
	if len(idents.relinked) != 0 {
		t.Errorf("denied user must not be relinked, got %v", idents.relinked)
	}
}

func TestProvisionAutoLinkFirstIdentityGatedByFlag(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	idents := newFakeProvIdentities() // CountByUser(u1) == 0: local-password crossover

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.AllowLocalAccountLinking = false
	res := provisionWith(t, users, idents, details, googleIdent())

	if res.Status != ProvisionNotAllowed {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionNotAllowed)
	}
	if len(idents.relinked) != 0 {
		t.Errorf("gated user must not be relinked, got %v", idents.relinked)
	}
}

func TestProvisionAutoLinkSecondProviderBypassesFlag(t *testing.T) {
	users := newFakeProvUsers()
	users.add(model.User{ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive})
	idents := newFakeProvIdentities()
	idents.countByUser["u1"] = 1 // already has one identity

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.AllowLocalAccountLinking = false
	res := provisionWith(t, users, idents, details, googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q (users with an identity attach further providers without the flag)", res.Status, ProvisionResolved)
	}
}

// ---------------------------------------------------------------------------
// 3) Auto-provision
// ---------------------------------------------------------------------------

func TestProvisionCreatesUserWhenEnabled(t *testing.T) {
	users := newFakeProvUsers()
	users.count = 3 // not first user
	idents := newFakeProvIdentities()

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.EnableAutoProvisioning = true
	res := provisionWith(t, users, idents, details, googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionResolved)
	}
	if len(users.created) != 1 || users.created[0].Role != model.RoleUser {
		t.Fatalf("created = %+v, want one active user with role user", users.created)
	}
	if len(idents.inserted) != 1 {
		t.Errorf("identity Insert calls = %v, want one", idents.inserted)
	}
}

func TestProvisionDefaultRoleAdmin(t *testing.T) {
	users := newFakeProvUsers()
	users.count = 3
	idents := newFakeProvIdentities()

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.EnableAutoProvisioning = true
	details.DefaultRole = "admin"
	res := provisionWith(t, users, idents, details, googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionResolved)
	}
	if len(users.created) != 1 || users.created[0].Role != model.RoleAdmin {
		t.Fatalf("created = %+v, want role admin", users.created)
	}
}

func TestProvisionRefusedWhenDisabled(t *testing.T) {
	users := newFakeProvUsers()
	users.count = 3
	idents := newFakeProvIdentities()

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionNotAllowed {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionNotAllowed)
	}
	if len(users.created)+len(users.createdPending) != 0 {
		t.Errorf("no user should be created, got %v / %v", users.created, users.createdPending)
	}
}

func TestProvisionFirstUserBecomesAdminEvenWhenDisabled(t *testing.T) {
	users := newFakeProvUsers() // empty table
	idents := newFakeProvIdentities()

	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionResolved)
	}
	if len(users.created) != 1 || users.created[0].Role != model.RoleAdmin {
		t.Fatalf("created = %+v, want bootstrap admin", users.created)
	}
}

func TestProvisionPendingWhenApprovalRequired(t *testing.T) {
	users := newFakeProvUsers()
	users.count = 3
	idents := newFakeProvIdentities()

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.EnableAutoProvisioning = true
	details.RequireAdminApproval = true
	res := provisionWith(t, users, idents, details, googleIdent())

	if res.Status != ProvisionPendingApproval {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionPendingApproval)
	}
	if len(users.createdPending) != 1 {
		t.Fatalf("createdPending = %+v, want one", users.createdPending)
	}
	if res.User.ID != users.createdPending[0].ID {
		t.Errorf("pending outcome must carry the created user")
	}
}

func TestProvisionFirstUserBypassesApproval(t *testing.T) {
	users := newFakeProvUsers() // empty table
	idents := newFakeProvIdentities()

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.EnableAutoProvisioning = true
	details.RequireAdminApproval = true
	res := provisionWith(t, users, idents, details, googleIdent())

	if res.Status != ProvisionResolved {
		t.Fatalf("status = %q, want %q (admin-less instance with approval-required must not be unrecoverable)", res.Status, ProvisionResolved)
	}
	if len(users.created) != 1 || users.created[0].Role != model.RoleAdmin {
		t.Fatalf("created = %+v, want bootstrap admin via CreateOIDC", users.created)
	}
}

func TestProvisionEmailRequired(t *testing.T) {
	users := newFakeProvUsers()
	users.count = 3
	idents := newFakeProvIdentities()

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.EnableAutoProvisioning = true
	ident := googleIdent()
	ident.Email = ""
	res := provisionWith(t, users, idents, details, ident)

	if res.Status != ProvisionEmailRequired {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionEmailRequired)
	}
}

func TestProvisionCleansUpUserWhenIdentityInsertFails(t *testing.T) {
	users := newFakeProvUsers()
	users.count = 3
	idents := newFakeProvIdentities()
	idents.insertErr = errors.New("boom")

	details := repo.DefaultOIDCAutoProvisionDetails()
	details.EnableAutoProvisioning = true
	p := NewProvisioner(&fakeProvSettings{details: details}, users, idents)
	_, err := p.Provision(context.Background(), googleIdent())

	if err == nil {
		t.Fatal("want infrastructure error, got nil")
	}
	if len(users.deleted) != 1 || users.deleted[0] != "u-new" {
		t.Fatalf("orphan user must be deleted, deleted = %v", users.deleted)
	}
}

// ---------------------------------------------------------------------------
// 4) Input hygiene
// ---------------------------------------------------------------------------

func TestProvisionEmptySubjectNotAllowed(t *testing.T) {
	users := newFakeProvUsers()
	idents := newFakeProvIdentities()

	ident := googleIdent()
	ident.Subject = "   "
	res := provisionWith(t, users, idents, repo.DefaultOIDCAutoProvisionDetails(), ident)

	if res.Status != ProvisionNotAllowed {
		t.Fatalf("status = %q, want %q", res.Status, ProvisionNotAllowed)
	}
}
