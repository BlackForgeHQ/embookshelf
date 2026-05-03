package task

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/service"
)

// ScanImportArgs is the payload for a per-LeafBook import (ADR-0004 §1).
// One job materializes one folder-as-Book grouping into the canonical
// {books + files + cover + sidecar} state without going through the
// bookdrop staging pipeline.
//
// Files carries the LeafBook's supported files exactly as Classify
// emitted them; Mixed propagates Classify's warning flag for admin
// surfacing later.
type ScanImportArgs struct {
	LibraryID string           `json:"library_id"`
	Folder    string           `json:"folder"`
	Files     []ScanImportFile `json:"files"`
	Mixed     bool             `json:"mixed,omitempty"`
}

// ScanImportFile is a JSON-friendly slice of scan.WalkEntry. Carries
// only the fields Insert needs; ETag is dropped since the LocalFS
// path doesn't populate it and S3 backends will re-Head before use.
type ScanImportFile struct {
	Location string    `json:"location"`
	Size     int64     `json:"size"`
	Mtime    time.Time `json:"mtime"`
}

func (ScanImportArgs) Kind() string { return "scan.import" }

// ScanImport runs the per-LeafBook import. Treats
// service.ErrAlreadyImported as success so re-enqueueing a job that
// already ran is a noop, not a failure. Other errors surface to
// River's retry policy.
//
// Mixed LeafBooks (per scan.Classify §4.1: dirs holding both direct
// supported files AND nested subdir-LeafBooks) log a warning so
// admins can find ambiguous folder shapes; the import still proceeds
// against the depth-1 sweep that Classify chose.
func ScanImport(ctx context.Context, args ScanImportArgs, deps service.ScanImportLeafBookDeps) error {
	if args.Mixed {
		slog.Warn("scan import: mixed leaf book (depth-1 sweep)",
			"library_id", args.LibraryID, "folder", args.Folder, "files", len(args.Files))
	}
	lb := scan.LeafBook{
		Folder: args.Folder,
		Mixed:  args.Mixed,
		Files:  make([]scan.WalkEntry, len(args.Files)),
	}
	for i, f := range args.Files {
		lb.Files[i] = scan.WalkEntry{
			Location: f.Location,
			Size:     f.Size,
			Mtime:    f.Mtime,
		}
	}
	_, err := service.ScanImport(ctx, deps, args.LibraryID, lb)
	if err != nil {
		if errors.Is(err, service.ErrAlreadyImported) {
			slog.Debug("scan import: already imported (noop)",
				"library_id", args.LibraryID, "folder", args.Folder)
			return nil
		}
		return err
	}
	return nil
}

// ScanImportWorker is the River adapter for ScanImport.
type ScanImportWorker struct {
	river.WorkerDefaults[ScanImportArgs]
	Deps service.ScanImportLeafBookDeps
}

func (w *ScanImportWorker) Work(ctx context.Context, job *river.Job[ScanImportArgs]) error {
	return ScanImport(ctx, job.Args, w.Deps)
}
