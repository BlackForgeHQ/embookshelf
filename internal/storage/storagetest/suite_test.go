package storagetest_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
	"github.com/blackforge/embookshelf/internal/storage/storagetest"
)

func TestLocalFS_Contract(t *testing.T) {
	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
		fs, err := local.New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return fs, func() {}
	})
}
