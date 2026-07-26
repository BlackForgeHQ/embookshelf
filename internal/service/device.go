// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackforge/embookshelf/internal/model"
)

// deviceRows is the slice of DeviceRepo this service needs.
type deviceRows interface {
	ListForUser(ctx context.Context, userID string) ([]model.Device, error)
	GetForUser(ctx context.Context, userID, id string) (model.Device, error)
	Create(ctx context.Context, d model.Device) (model.Device, error)
	Delete(ctx context.Context, userID, id string) error
	MarkSendResult(ctx context.Context, userID, id string, sendErr error) error
}

// bookLookup is the slice of BookRepo this service needs.
type bookLookup interface {
	GetByID(ctx context.Context, userID, id string) (model.Book, error)
}

// DeviceService orchestrates pairing, listing, and pushing books to the
// user's registered devices. Driver selection is by DeviceKind — add a new
// driver, register it once here, and every handler/UI flow picks it up.
//
// Book bytes come from LibraryStore, never from the filesystem
// directly, so pushing a book works the same on a local library and an
// S3-backed one.
type DeviceService struct {
	devices  deviceRows
	books    bookLookup
	libStore LibraryStore
	drivers  map[model.DeviceKind]DeviceDriver
}

func NewDeviceService(devices deviceRows, books bookLookup, libStore LibraryStore, drivers ...DeviceDriver) *DeviceService {
	m := make(map[model.DeviceKind]DeviceDriver, len(drivers))
	for _, d := range drivers {
		m[d.Kind()] = d
	}
	return &DeviceService{devices: devices, books: books, libStore: libStore, drivers: m}
}

// ErrUnsupportedKind is returned when the user asks to pair a kind we
// have no driver for.
var ErrUnsupportedKind = errors.New("unsupported device kind")

// ListForUser returns every device the user has registered, ordered by
// creation time (oldest first).
func (s *DeviceService) ListForUser(ctx context.Context, userID string) ([]model.Device, error) {
	return s.devices.ListForUser(ctx, userID)
}

// Pair runs the driver's handshake and persists the resulting row.
// `params["name"]` overrides the default display name from the driver.
func (s *DeviceService) Pair(ctx context.Context, userID string, kind model.DeviceKind, params map[string]any) (model.Device, error) {
	driver, ok := s.drivers[kind]
	if !ok {
		return model.Device{}, ErrUnsupportedKind
	}
	d, err := driver.Pair(ctx, params)
	if err != nil {
		return model.Device{}, err
	}
	d.UserID = userID
	d.Kind = kind
	return s.devices.Create(ctx, d)
}

// Delete removes a device. Idempotent-ish — returns ErrNotFound when the
// row is already gone.
func (s *DeviceService) Delete(ctx context.Context, userID, id string) error {
	return s.devices.Delete(ctx, userID, id)
}

// Send pushes a book file to one of the user's registered devices. The
// bytes come through LibraryStore, so a book in an S3-backed library
// pushes exactly like one on local disk. Records the outcome on the
// device row so the UI can surface last-success / last-error.
func (s *DeviceService) Send(ctx context.Context, userID, deviceID, bookID string) error {
	dev, err := s.devices.GetForUser(ctx, userID, deviceID)
	if err != nil {
		return err
	}
	driver, ok := s.drivers[dev.Kind]
	if !ok {
		return ErrUnsupportedKind
	}

	book, err := s.books.GetByID(ctx, userID, bookID)
	if err != nil {
		return err
	}
	if s.libStore == nil {
		return errors.New("device service: no library store")
	}
	handle, err := s.libStore.For(ctx, book.LibraryID)
	if err != nil {
		return fmt.Errorf("library handle: %w", err)
	}
	reader, size, closer, err := handle.OpenBook(ctx, book)
	if err != nil {
		return fmt.Errorf("open book: %w", err)
	}
	defer func() { _ = closer.Close() }()

	sendErr := driver.Send(ctx, dev, reader, BookMeta{
		Title:  book.Title,
		Author: book.Author,
		Format: book.Format,
		Size:   size,
	})
	// Always record the outcome so the UI reflects it even on failure.
	_ = s.devices.MarkSendResult(ctx, userID, deviceID, sendErr)
	return sendErr
}
