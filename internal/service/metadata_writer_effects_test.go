// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// TestDecideEffects pins ADR-0001 §3 (trigger × backend) matrix as a
// pure function. No I/O; no fakes. The single source of truth for
// "what fires when."
func TestDecideEffects(t *testing.T) {
	root := t.TempDir()
	localFS, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	backendID := "backend-s3"
	localHandle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
		Storage: localFS,
	}
	s3Handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib2", BackendID: &backendID},
		// The adapter is what makes it an object store, not the backend
		// id — a migrated local library has one of those too (#202).
		Storage: objectStore{localFS},
	}
	degradedHandle := &service.LibraryHandle{
		Library: model.Library{ID: "lib3", BackendID: nil},
		Storage: storage.Storage(nil),
	}

	cases := []struct {
		name          string
		trigger       service.Trigger
		handle        *service.LibraryHandle
		folderChanged bool
		want          service.Effects
	}{
		{
			name:    "auto-enrichment with no handle is DB only",
			trigger: service.TriggerAutoEnrichment,
			handle:  nil,
			want:    service.Effects{DB: true},
		},
		{
			name:    "auto-enrichment ignores backend kind",
			trigger: service.TriggerAutoEnrichment,
			handle:  localHandle,
			want:    service.Effects{DB: true},
		},
		{
			name:          "auto-enrichment never renames even with folder change",
			trigger:       service.TriggerAutoEnrichment,
			handle:        localHandle,
			folderChanged: true,
			want:          service.Effects{DB: true},
		},
		{
			name:    "manual edit with no handle degrades to DB only",
			trigger: service.TriggerManualEdit,
			handle:  nil,
			want:    service.Effects{DB: true},
		},
		{
			name:    "manual edit with degraded handle (no storage) is DB only",
			trigger: service.TriggerManualEdit,
			handle:  degradedHandle,
			want:    service.Effects{DB: true},
		},
		{
			name:    "manual edit on S3-backed library writes DB + sidecar (no in-file)",
			trigger: service.TriggerManualEdit,
			handle:  s3Handle,
			want:    service.Effects{DB: true, Sidecar: true, InFile: false},
		},
		{
			name:          "manual edit on S3 with folder change adds rename (ADR-0005)",
			trigger:       service.TriggerManualEdit,
			handle:        s3Handle,
			folderChanged: true,
			want:          service.Effects{DB: true, Sidecar: true, InFile: false, FolderRename: true},
		},
		{
			name:          "apply-enrichment on S3 with folder change adds rename (ADR-0005)",
			trigger:       service.TriggerApplyEnrichment,
			handle:        s3Handle,
			folderChanged: true,
			want:          service.Effects{DB: true, Sidecar: true, InFile: false, FolderRename: true},
		},
		{
			name:    "manual edit on local library writes DB + sidecar + in-file",
			trigger: service.TriggerManualEdit,
			handle:  localHandle,
			want:    service.Effects{DB: true, Sidecar: true, InFile: true},
		},
		{
			name:          "manual edit on local with folder change adds rename",
			trigger:       service.TriggerManualEdit,
			handle:        localHandle,
			folderChanged: true,
			want:          service.Effects{DB: true, Sidecar: true, InFile: true, FolderRename: true},
		},
		{
			name:    "apply-enrichment matches manual edit on local",
			trigger: service.TriggerApplyEnrichment,
			handle:  localHandle,
			want:    service.Effects{DB: true, Sidecar: true, InFile: true},
		},
		{
			name:          "apply-enrichment with folder change adds rename",
			trigger:       service.TriggerApplyEnrichment,
			handle:        localHandle,
			folderChanged: true,
			want:          service.Effects{DB: true, Sidecar: true, InFile: true, FolderRename: true},
		},
		{
			name:    "apply-enrichment matches manual edit on S3",
			trigger: service.TriggerApplyEnrichment,
			handle:  s3Handle,
			want:    service.Effects{DB: true, Sidecar: true, InFile: false},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := service.DecideEffects(c.trigger, c.handle, c.folderChanged)
			if got != c.want {
				t.Errorf("DecideEffects = %+v; want %+v", got, c.want)
			}
		})
	}
}

// objectStore wraps any Storage and advertises the one capability that
// makes the write pipeline treat a library as remote.
type objectStore struct{ storage.Storage }

func (objectStore) Capabilities() storage.Capability { return storage.CapObjectStore }
