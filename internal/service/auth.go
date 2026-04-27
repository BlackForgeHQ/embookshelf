package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sse"
)

// SessionTTL is how long a freshly-issued session remains valid.
const SessionTTL = 7 * 24 * time.Hour

// ErrInvalidCredentials is the sentinel returned by Login when email or
// password do not match. It is intentionally generic to avoid leaking which
// half was wrong.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrSignupClosed       = errors.New("signup is disabled — an administrator must create your account")
	ErrEmailTaken         = errors.New("that email is already registered")
	ErrCannotTargetSelf   = errors.New("cannot change your own approval status")
)

// userStatusRepo is the slice of UserRepo that ApproveUser and DenyUser
// touch. Defining it as a tiny interface lets the service test substitute
// an in-memory fake without spinning up a database — the rest of
// AuthService keeps using the concrete *repo.UserRepo via embedding.
type userStatusRepo interface {
	GetByID(ctx context.Context, id string) (model.User, error)
	UpdateStatus(ctx context.Context, id string, status model.UserStatus) error
	CountByRole(ctx context.Context, role model.Role) (int, error)
}

type AuthService struct {
	users    *repo.UserRepo
	sessions *repo.SessionRepo
	hub      *sse.Hub
}

func NewAuthService(users *repo.UserRepo, sessions *repo.SessionRepo, hub *sse.Hub) *AuthService {
	return &AuthService{users: users, sessions: sessions, hub: hub}
}

// Login verifies credentials and issues a new session.
func (s *AuthService) Login(ctx context.Context, email, password, userAgent string) (model.Session, model.User, error) {
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return model.Session{}, model.User{}, ErrInvalidCredentials
		}
		return model.Session{}, model.User{}, err
	}
	if err := auth.VerifyPassword(u.PasswordHash, password); err != nil {
		return model.Session{}, model.User{}, ErrInvalidCredentials
	}

	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	// Fire-and-forget last-seen update.
	_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())
	return sess, u, nil
}

// Logout destroys the given session.
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.sessions.Delete(ctx, sessionID)
}

// Verify checks credentials and returns the user without creating a session.
// Used by the OPDS Basic Auth middleware, which is stateless per-request.
func (s *AuthService) Verify(ctx context.Context, email, password string) (model.User, error) {
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return model.User{}, ErrInvalidCredentials
		}
		return model.User{}, err
	}
	if err := auth.VerifyPassword(u.PasswordHash, password); err != nil {
		return model.User{}, ErrInvalidCredentials
	}
	_ = s.users.TouchLastSeen(ctx, u.ID, time.Now())
	return u, nil
}

// UserBySession validates the session and returns the associated user. It
// also slides the session forward (last_used_at = now()).
func (s *AuthService) UserBySession(ctx context.Context, sessionID string) (model.User, error) {
	_, u, err := s.sessions.GetActive(ctx, sessionID)
	return u, err
}

// SignupEnabled reports whether /signup should accept submissions: true when
// the users table is empty (first-run bootstrap), false afterwards.
func (s *AuthService) SignupEnabled(ctx context.Context) (bool, error) {
	n, err := s.users.Count(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// Signup creates the first-run bootstrap admin. It returns ErrSignupClosed if
// users already exist.
func (s *AuthService) Signup(ctx context.Context, email, name, password, userAgent string) (model.Session, model.User, error) {
	open, err := s.SignupEnabled(ctx)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	if !open {
		return model.Session{}, model.User{}, ErrSignupClosed
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return model.Session{}, model.User{}, errors.New("email is required")
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return model.Session{}, model.User{}, err
	}
	u, err := s.users.Create(ctx, email, name, hash, model.RoleAdmin)
	if err != nil {
		// Pretty-print the well-known unique violation.
		if strings.Contains(err.Error(), "users_email_key") {
			return model.Session{}, model.User{}, ErrEmailTaken
		}
		return model.Session{}, model.User{}, err
	}
	sess, err := s.sessions.Create(ctx, u.ID, userAgent, SessionTTL)
	if err != nil {
		return model.Session{}, u, err
	}
	return sess, u, nil
}

// PurgeExpiredSessions is called at boot to clean up.
func (s *AuthService) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	return s.sessions.PurgeExpired(ctx)
}

// ChangePassword verifies the current password and replaces the hash.
// Returns ErrInvalidCredentials when `current` doesn't match so callers can
// surface a generic message without leaking which half was wrong.
func (s *AuthService) ChangePassword(ctx context.Context, userID, current, next string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if err := auth.VerifyPassword(u.PasswordHash, current); err != nil {
		return ErrInvalidCredentials
	}
	hash, err := auth.HashPassword(next)
	if err != nil {
		return err
	}
	return s.users.UpdatePassword(ctx, userID, hash)
}

// UpdateDisplayName lets a user edit their own name. Pass an empty string
// to clear it (Display() falls back to email).
func (s *AuthService) UpdateDisplayName(ctx context.Context, userID, name string) error {
	return s.users.UpdateName(ctx, userID, name)
}

// ErrLastAdmin guards against removing the only administrator. Both the
// "demote last admin" and "delete last admin" paths surface this.
var ErrLastAdmin = errors.New("cannot remove the last administrator")

// ListUsers returns every user. Admin-only.
func (s *AuthService) ListUsers(ctx context.Context) ([]model.User, error) {
	return s.users.List(ctx)
}

// CreateUser is the admin-driven account creation flow. It does NOT issue a
// session — the new user logs in separately.
func (s *AuthService) CreateUser(ctx context.Context, email, name, password string, role model.Role) (model.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return model.User{}, errors.New("email is required")
	}
	if role != model.RoleAdmin && role != model.RoleUser {
		return model.User{}, errors.New("invalid role")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return model.User{}, err
	}
	u, err := s.users.Create(ctx, email, name, hash, role)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_key") {
			return model.User{}, ErrEmailTaken
		}
		return model.User{}, err
	}
	return u, nil
}

// SetUserRole flips a user's role. Refuses to demote the last remaining admin.
func (s *AuthService) SetUserRole(ctx context.Context, userID string, role model.Role) error {
	if role != model.RoleAdmin && role != model.RoleUser {
		return errors.New("invalid role")
	}
	if role == model.RoleUser {
		u, err := s.users.GetByID(ctx, userID)
		if err != nil {
			return err
		}
		if u.Role == model.RoleAdmin {
			n, err := s.users.CountByRole(ctx, model.RoleAdmin)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
	}
	return s.users.UpdateRole(ctx, userID, role)
}

// DeleteUser removes a user and all their sessions. Refuses to delete the
// last remaining admin.
func (s *AuthService) DeleteUser(ctx context.Context, userID string) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.Role == model.RoleAdmin {
		n, err := s.users.CountByRole(ctx, model.RoleAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	return s.users.Delete(ctx, userID)
}

// approveUser/denyUser are pure helpers operating against any repo that
// satisfies userStatusRepo. They contain the guard logic and are unit
// tested in auth_test.go without a database. The exported method wrappers
// add SSE broadcast on success.

func approveUser(ctx context.Context, r userStatusRepo, callerID, targetID string) error {
	u, err := r.GetByID(ctx, targetID)
	if err != nil {
		return err
	}
	if u.Status == model.UserStatusActive {
		return nil // idempotent
	}
	return r.UpdateStatus(ctx, targetID, model.UserStatusActive)
}

func denyUser(ctx context.Context, r userStatusRepo, callerID, targetID string) error {
	if callerID == targetID {
		return ErrCannotTargetSelf
	}
	u, err := r.GetByID(ctx, targetID)
	if err != nil {
		return err
	}
	if u.Status == model.UserStatusDenied {
		return nil // idempotent
	}
	if u.Role == model.RoleAdmin && u.Status == model.UserStatusActive {
		n, err := r.CountByRole(ctx, model.RoleAdmin)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	return r.UpdateStatus(ctx, targetID, model.UserStatusDenied)
}

// ApproveUser flips a pending or denied user back to active. Idempotent
// for already-active users. Broadcasts a users.updated SSE event
// so open admin tabs refresh.
func (s *AuthService) ApproveUser(ctx context.Context, callerID, targetID string) (model.User, error) {
	if err := approveUser(ctx, s.users, callerID, targetID); err != nil {
		return model.User{}, err
	}
	u, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return model.User{}, err
	}
	s.broadcastUsersUpdate()
	return u, nil
}

// DenyUser flips a pending user to denied. Idempotent. Refuses to deny
// the caller's own row or the last remaining admin.
func (s *AuthService) DenyUser(ctx context.Context, callerID, targetID string) (model.User, error) {
	if err := denyUser(ctx, s.users, callerID, targetID); err != nil {
		return model.User{}, err
	}
	u, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return model.User{}, err
	}
	s.broadcastUsersUpdate()
	return u, nil
}

func (s *AuthService) broadcastUsersUpdate() {
	if s.hub == nil {
		return
	}
	s.hub.Broadcast(sse.Event{Name: "users.updated", Data: "{}"})
}
