package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blackforge/embookshelf/internal/model"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

const userCols = `id, email, password_hash, name, role, created_at, updated_at, last_seen_at`

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT `+userCols+`
		FROM users
		WHERE lower(email) = lower($1)
	`, strings.TrimSpace(email))
	return scanUser(row)
}

func (r *UserRepo) GetByID(ctx context.Context, id string) (model.User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func (r *UserRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (r *UserRepo) Create(ctx context.Context, email, name, hash string, role model.Role) (model.User, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES (lower($1), $2, $3, $4)
		RETURNING `+userCols+`
	`, strings.TrimSpace(email), strings.TrimSpace(name), hash, string(role))
	return scanUser(row)
}

func (r *UserRepo) TouchLastSeen(ctx context.Context, id string, at time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET last_seen_at = $2 WHERE id = $1`, id, at)
	return err
}

func scanUser(s scanner) (model.User, error) {
	var (
		u    model.User
		role string
	)
	err := s.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &role, &u.CreatedAt, &u.UpdatedAt, &u.LastSeenAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.Role = model.Role(role)
	return u, nil
}
