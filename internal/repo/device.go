// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"encoding/json"
	"errors"
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
	const q = `
		SELECT ` + deviceCols + `
		FROM user_devices
		WHERE user_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, r.scanDevice)
}

func (r *DeviceRepo) GetForUser(ctx context.Context, userID, id string) (model.Device, error) {
	const q = `
		SELECT ` + deviceCols + `
		FROM user_devices WHERE user_id = $1 AND id = $2
	`
	row := r.db.SQL.QueryRowContext(ctx, q, userID, id)
	return r.scanDevice(row)
}

func (r *DeviceRepo) Create(ctx context.Context, d model.Device) (model.Device, error) {
	cfg, err := json.Marshal(d.Config)
	if err != nil {
		return model.Device{}, err
	}
	id := db.NewID()
	const q = `
		INSERT INTO user_devices (id, user_id, kind, name, secret, config)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + deviceCols
	row := r.db.SQL.QueryRowContext(ctx, q,
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
	const q = `DELETE FROM user_devices WHERE user_id = $1 AND id = $2`
	return execOne(ctx, r.db.SQL, q, userID, id)
}

// MarkSendResult records the outcome of a push attempt.
func (r *DeviceRepo) MarkSendResult(ctx context.Context, userID, id string, sendErr error) error {
	if sendErr == nil {
		const qPG = `
			UPDATE user_devices
			SET last_sent_at = now(), last_error = '', updated_at = now()
			WHERE user_id = $1 AND id = $2
		`
		_, err := r.db.SQL.ExecContext(ctx, qPG, userID, id)
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
	_, err := r.db.SQL.ExecContext(ctx, qPG, userID, id, msg)
	return err
}

func (r *DeviceRepo) scanDevice(s scanner) (model.Device, error) {
	var (
		d      model.Device
		kind   string
		rawCfg []byte
	)
	err := s.Scan(
		&d.ID, &d.UserID, &kind, &d.Name, &d.Secret, &rawCfg,
		&d.LastSentAt, &d.LastError, &d.CreatedAt, &d.UpdatedAt,
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
	return d, nil
}
