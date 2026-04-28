package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

type DeviceRepo struct {
	db *db.DB
}

func NewDeviceRepo(d *db.DB) *DeviceRepo {
	return &DeviceRepo{db: d}
}

// ErrDeviceNameTaken is returned by Create/Rename when the user already has
// a device with the same display name (case-insensitive).
var ErrDeviceNameTaken = errors.New("a device with that name already exists")

const deviceCols = `id, user_id, kind, name, secret, config, last_sent_at, last_error, created_at, updated_at`

func (r *DeviceRepo) ListForUser(ctx context.Context, userID string) ([]model.Device, error) {
	const qPG = `
		SELECT ` + deviceCols + `
		FROM user_devices
		WHERE user_id = $1
		ORDER BY created_at ASC
	`
	const qSQLite = `
		SELECT ` + deviceCols + `
		FROM user_devices
		WHERE user_id = ?
		ORDER BY created_at ASC
	`
	rows, err := r.db.SQL.QueryContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Device
	for rows.Next() {
		d, err := r.scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *DeviceRepo) GetForUser(ctx context.Context, userID, id string) (model.Device, error) {
	const qPG = `
		SELECT ` + deviceCols + `
		FROM user_devices WHERE user_id = $1 AND id = $2
	`
	const qSQLite = `
		SELECT ` + deviceCols + `
		FROM user_devices WHERE user_id = ? AND id = ?
	`
	row := r.db.SQL.QueryRowContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID, id)
	return r.scanDevice(row)
}

func (r *DeviceRepo) Create(ctx context.Context, d model.Device) (model.Device, error) {
	cfg, err := json.Marshal(d.Config)
	if err != nil {
		return model.Device{}, err
	}
	id := db.NewID()
	const qPG = `
		INSERT INTO user_devices (id, user_id, kind, name, secret, config)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + deviceCols
	const qSQLite = `
		INSERT INTO user_devices (id, user_id, kind, name, secret, config)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING ` + deviceCols
	row := r.db.SQL.QueryRowContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		id, d.UserID, string(d.Kind), strings.TrimSpace(d.Name), d.Secret, cfg)
	created, err := r.scanDevice(row)
	if err != nil {
		if ok, name := dberr.IsUniqueViolation(err); ok && name == "idx_user_devices_user_name" {
			return model.Device{}, ErrDeviceNameTaken
		}
		return model.Device{}, err
	}
	return created, nil
}

func (r *DeviceRepo) Delete(ctx context.Context, userID, id string) error {
	const qPG = `DELETE FROM user_devices WHERE user_id = $1 AND id = $2`
	const qSQLite = `DELETE FROM user_devices WHERE user_id = ? AND id = ?`
	res, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkSendResult records the outcome of a push attempt.
func (r *DeviceRepo) MarkSendResult(ctx context.Context, userID, id string, sendErr error) error {
	if sendErr == nil {
		const qPG = `
			UPDATE user_devices
			SET last_sent_at = now(), last_error = '', updated_at = now()
			WHERE user_id = $1 AND id = $2
		`
		const qSQLite = `
			UPDATE user_devices
			SET last_sent_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			    last_error = '',
			    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
			WHERE user_id = ? AND id = ?
		`
		_, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), userID, id)
		return err
	}
	msg := sendErr.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	const qPG = `
		UPDATE user_devices
		SET last_error = $3, updated_at = now()
		WHERE user_id = $1 AND id = $2
	`
	const qSQLite = `
		UPDATE user_devices
		SET last_error = ?,
		    updated_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		WHERE user_id = ? AND id = ?
	`
	if r.db.Dialect == db.DialectSQLite {
		_, err := r.db.SQL.ExecContext(ctx, qSQLite, msg, userID, id)
		return err
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, userID, id, msg)
	return err
}

func (r *DeviceRepo) scanDevice(s scanner) (model.Device, error) {
	var (
		d           model.Device
		kind        string
		rawCfg      []byte
		lastSentAny any
		createdAny  any
		updatedAny  any
	)
	err := s.Scan(
		&d.ID, &d.UserID, &kind, &d.Name, &d.Secret, &rawCfg,
		&lastSentAny, &d.LastError, &createdAny, &updatedAny,
	)
	if err != nil {
		if dberr.IsNotFound(err) {
			return d, ErrNotFound
		}
		return d, err
	}
	d.Kind = model.DeviceKind(kind)
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &d.Config); err != nil {
			return d, err
		}
	}
	if d.Config == nil {
		d.Config = map[string]any{}
	}
	if err := db.ScanNullTime(r.db.Dialect, lastSentAny, &d.LastSentAt); err != nil {
		return d, fmt.Errorf("scan last_sent_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, createdAny, &d.CreatedAt); err != nil {
		return d, fmt.Errorf("scan created_at: %w", err)
	}
	if err := db.ScanTime(r.db.Dialect, updatedAny, &d.UpdatedAt); err != nil {
		return d, fmt.Errorf("scan updated_at: %w", err)
	}
	return d, nil
}
