package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// fakeBookCreator captures Create/SetCoverHash calls.
type fakeBookCreator struct {
	created    []model.Book
	coverHash  map[string][]byte
	createErr  error
	hashErrMap map[string]error
}

func (f *fakeBookCreator) Create(ctx context.Context, b model.Book) (model.Book, error) {
	if f.createErr != nil {
		return model.Book{}, f.createErr
	}
	b.ID = "b-" + b.Title
	f.created = append(f.created, b)
	return b, nil
}

func (f *fakeBookCreator) SetCoverHash(ctx context.Context, bookID string, hash []byte) error {
	if f.coverHash == nil {
		f.coverHash = map[string][]byte{}
	}
	f.coverHash[bookID] = hash
	if f.hashErrMap != nil {
		return f.hashErrMap[bookID]
	}
	return nil
}

func (f *fakeBookCreator) UpdateAudio(ctx context.Context, id string, durationSeconds *int, narrator string, chapters []model.Chapter) error {
	return nil
}

func (f *fakeBookCreator) SetFolderPath(ctx context.Context, bookID, folderPath, path string) error {
	return nil
}

// fakeScanFileRepo gates inserts and exists lookups.
type fakeScanFileRepo struct {
	exists    map[string]bool // key: libraryID + "|" + location
	inserted  []model.File
	insertErr error
}

func (f *fakeScanFileRepo) ExistsByLocation(ctx context.Context, libraryID, location string) (bool, error) {
	if f.exists == nil {
		return false, nil
	}
	return f.exists[libraryID+"|"+location], nil
}

func (f *fakeScanFileRepo) GetByContentHash(ctx context.Context, hash []byte) ([]model.File, error) {
	return nil, nil
}

func (f *fakeScanFileRepo) Insert(ctx context.Context, m model.File) (model.File, error) {
	if f.insertErr != nil {
		return model.File{}, f.insertErr
	}
	m.ID = "f-" + m.Location
	f.inserted = append(f.inserted, m)
	return m, nil
}

// fakeCoverPromoter captures SaveBookHashed calls.
type fakeCoverPromoter struct {
	saves   []coverSave
	saveErr error
}

type coverSave struct {
	Hash []byte
	MIME string
	Data []byte
}

func (f *fakeCoverPromoter) SaveBookHashed(hash []byte, mime string, data []byte) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, coverSave{Hash: append([]byte{}, hash...), MIME: mime, Data: append([]byte{}, data...)})
	return nil
}

func TestScanImport_NoLibStore(t *testing.T) {
	_, err := service.ScanImport(context.Background(), service.ScanImportLeafBookDeps{}, "lib1", scan.LeafBook{})
	if err == nil {
		t.Fatal("expected error when LibStore is nil")
	}
}

func TestScanImport_EmptyLeafBookErrors(t *testing.T) {
	deps := service.ScanImportLeafBookDeps{
		LibStore: &fakeLibStore{handle: &service.LibraryHandle{}},
		Books:    &fakeBookCreator{},
		Files:    &fakeScanFileRepo{},
	}
	_, err := service.ScanImport(context.Background(), deps, "lib1", scan.LeafBook{})
	if err == nil {
		t.Fatal("expected error on empty LeafBook")
	}
}

func TestScanImport_AlreadyImportedReturnsSentinel(t *testing.T) {
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", Path: "/lib"},
		Storage: fs,
	}
	files := &fakeScanFileRepo{
		exists: map[string]bool{
			"lib1|Tolkien/Hobbit/hobbit.epub": true,
		},
	}
	deps := service.ScanImportLeafBookDeps{
		LibStore: &fakeLibStore{handle: handle},
		Books:    &fakeBookCreator{},
		Files:    files,
	}
	lb := scan.LeafBook{
		Folder: "Tolkien/Hobbit",
		Files: []scan.WalkEntry{
			{Location: "Tolkien/Hobbit/hobbit.epub"},
		},
	}
	_, err = service.ScanImport(context.Background(), deps, "lib1", lb)
	if !errors.Is(err, service.ErrAlreadyImported) {
		t.Fatalf("err=%v want ErrAlreadyImported", err)
	}
}

func TestScanImport_NoStorageOnHandle(t *testing.T) {
	deps := service.ScanImportLeafBookDeps{
		LibStore: &fakeLibStore{handle: &service.LibraryHandle{}},
		Books:    &fakeBookCreator{},
		Files:    &fakeScanFileRepo{},
	}
	lb := scan.LeafBook{
		Folder: "Tolkien/Hobbit",
		Files: []scan.WalkEntry{
			{Location: "Tolkien/Hobbit/hobbit.epub"},
		},
	}
	_, err := service.ScanImport(context.Background(), deps, "lib1", lb)
	if err == nil {
		t.Fatal("expected error when handle has no storage")
	}
}
