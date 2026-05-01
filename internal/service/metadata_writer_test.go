package service_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// fakeBookWriter records UpdateMetadata calls for the test.
type fakeBookWriter struct {
	called []model.Book
	err    error
}

func (f *fakeBookWriter) UpdateMetadata(ctx context.Context, b model.Book) error {
	f.called = append(f.called, b)
	return f.err
}

func TestMetadataWriter_Write_AutoEnrichment_DBOnly(t *testing.T) {
	books := &fakeBookWriter{}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{Books: books})
	book := model.Book{ID: "b1", Title: "Auto-applied"}
	if err := mw.Write(context.Background(), book, service.TriggerAutoEnrichment); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(books.called) != 1 {
		t.Fatalf("UpdateMetadata called %d times; want 1", len(books.called))
	}
	if books.called[0].Title != "Auto-applied" {
		t.Errorf("Title=%q", books.called[0].Title)
	}
}

func TestMetadataWriter_Write_DBFails_ErrorReturned(t *testing.T) {
	books := &fakeBookWriter{err: context.Canceled}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{Books: books})
	err := mw.Write(context.Background(), model.Book{ID: "b1"}, service.TriggerManualEdit)
	if err == nil {
		t.Fatal("Write: want error, got nil")
	}
}
