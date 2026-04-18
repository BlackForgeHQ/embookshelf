package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
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
)

type AuthService struct {
	users    *repo.UserRepo
	sessions *repo.SessionRepo
}

func NewAuthService(users *repo.UserRepo, sessions *repo.SessionRepo) *AuthService {
	return &AuthService{users: users, sessions: sessions}
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
