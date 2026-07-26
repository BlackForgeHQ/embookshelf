// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeDevices struct {
	device     model.Device
	getErr     error
	sendResult error
	marked     bool
}

func (f *fakeDevices) ListForUser(context.Context, string) ([]model.Device, error) {
	return []model.Device{f.device}, nil
}

func (f *fakeDevices) GetForUser(context.Context, string, string) (model.Device, error) {
	return f.device, f.getErr
}

func (f *fakeDevices) Create(_ context.Context, d model.Device) (model.Device, error) {
	return d, nil
}

func (f *fakeDevices) Delete(context.Context, string, string) error { return nil }

func (f *fakeDevices) MarkSendResult(_ context.Context, _, _ string, sendErr error) error {
	f.marked = true
	f.sendResult = sendErr
	return nil
}

type fakeBooks struct {
	book model.Book
}

func (f *fakeBooks) GetByID(context.Context, string, string) (model.Book, error) {
	return f.book, nil
}

type fakeLibStore struct {
	handle *LibraryHandle
}

func (f *fakeLibStore) For(context.Context, string) (*LibraryHandle, error) {
	return f.handle, nil
}

type recordingDriver struct {
	kind    model.DeviceKind
	content string
	meta    BookMeta
	sendErr error
	calls   int
}

func (d *recordingDriver) Kind() model.DeviceKind { return d.kind }

func (d *recordingDriver) Pair(context.Context, map[string]any) (model.Device, error) {
	return model.Device{}, nil
}

func (d *recordingDriver) Send(_ context.Context, _ model.Device, content io.Reader, meta BookMeta) error {
	d.calls++
	b, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	d.content = string(b)
	d.meta = meta
	return d.sendErr
}

// ---------------------------------------------------------------------------
// Send
// ---------------------------------------------------------------------------

// The defect this candidate exists for: a book in a backend-backed
// library has no on-disk path, so the old os.Open(book.Path) path
// failed with "this book has no on-disk file to send".
func TestSendDeliversBookFromBackendBackedLibrary(t *testing.T) {
	t.Parallel()

	backendID := "backend-1"
	store := &fakeStorage{objects: map[string][]byte{"Author/Title/b.epub": []byte("s3-bytes")}}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: &backendID},
		Storage: store,
		files: &fakeFiles{byBook: map[string][]model.File{
			"b1": {{Location: "Author/Title/b.epub", Format: "epub"}},
		}},
	}
	devices := &fakeDevices{device: model.Device{ID: "d1", Kind: "remarkable"}}
	books := &fakeBooks{book: model.Book{
		ID: "b1", LibraryID: "lib1", Title: "Deep Work", Author: "Newport", Format: "epub",
		// No Path — bytes live in the backend.
	}}
	driver := &recordingDriver{kind: "remarkable"}

	svc := NewDeviceService(devices, books, &fakeLibStore{handle: handle}, driver)
	if err := svc.Send(context.Background(), "u1", "d1", "b1"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if driver.content != "s3-bytes" {
		t.Errorf("driver received %q, want s3-bytes", driver.content)
	}
	if driver.meta.Size != int64(len("s3-bytes")) {
		t.Errorf("meta.Size = %d, want %d", driver.meta.Size, len("s3-bytes"))
	}
	if driver.meta.Title != "Deep Work" || driver.meta.Author != "Newport" {
		t.Errorf("meta = %+v, want title/author carried through", driver.meta)
	}
}

func TestSendRecordsSuccessOnTheDeviceRow(t *testing.T) {
	t.Parallel()

	svc, devices, driver := newSendFixture(t, nil)
	if err := svc.Send(context.Background(), "u1", "d1", "b1"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !devices.marked {
		t.Fatal("send outcome was not recorded on the device row")
	}
	if devices.sendResult != nil {
		t.Errorf("recorded error = %v, want nil", devices.sendResult)
	}
	if driver.calls != 1 {
		t.Errorf("driver called %d times, want 1", driver.calls)
	}
}

// A failing driver must still leave a record — the UI surfaces
// last-error from the device row.
func TestSendRecordsDriverFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("device offline")
	svc, devices, _ := newSendFixture(t, boom)

	err := svc.Send(context.Background(), "u1", "d1", "b1")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want device offline", err)
	}
	if !devices.marked || !errors.Is(devices.sendResult, boom) {
		t.Errorf("failure not recorded: marked=%v result=%v", devices.marked, devices.sendResult)
	}
}

func TestSendRefusesUnknownDeviceKind(t *testing.T) {
	t.Parallel()

	devices := &fakeDevices{device: model.Device{ID: "d1", Kind: "kindle-scribe"}}
	books := &fakeBooks{book: model.Book{ID: "b1", LibraryID: "lib1"}}
	svc := NewDeviceService(devices, books, &fakeLibStore{}, &recordingDriver{kind: "remarkable"})

	err := svc.Send(context.Background(), "u1", "d1", "b1")
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Fatalf("err = %v, want ErrUnsupportedKind", err)
	}
}

func newSendFixture(t *testing.T, driverErr error) (*DeviceService, *fakeDevices, *recordingDriver) {
	t.Helper()
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1"},
		Storage: &fakeStorage{objects: map[string][]byte{"k.epub": []byte("bytes")}},
		files:   &fakeFiles{byBook: map[string][]model.File{"b1": {{Location: "k.epub", Format: "epub"}}}},
	}
	devices := &fakeDevices{device: model.Device{ID: "d1", Kind: "remarkable"}}
	books := &fakeBooks{book: model.Book{ID: "b1", LibraryID: "lib1", Format: "epub"}}
	driver := &recordingDriver{kind: "remarkable", sendErr: driverErr}
	return NewDeviceService(devices, books, &fakeLibStore{handle: handle}, driver), devices, driver
}
