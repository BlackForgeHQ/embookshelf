// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Delivery modes for BookSource.Kind and the EMBOOKSHELF_PRESIGN_FALLBACK
// env var. The env var name predates the inversion; the string values
// here are the canonical delivery-mode identifiers.
const (
	BookDeliveryPresign = "presign"
	BookDeliveryStream  = "stream"
	BookDeliveryLocal   = "local"
)

// LibraryStore turns a libraryID into a ready-to-use view of that
// Library: the row, the Storage it lives in, the Placer that knows how
// to write into it, and the delivery decision for serving its bytes to
// clients.
//
// Deep seam: callers asking "give me access to library X" — Approve,
// the file-serve handler, audio re-extract, and (eventually) library
// scan + files backfill — all route through For. The Library + its
// Backend (CONTEXT.md: Library is "pinned to one Backend via
// libraries.backend_id") travel together because they always do.
//
// Stateless. Each For() does a fresh LibraryRepo lookup + Resolver
// call. Library mutations are admin-rare; the per-call cost is one
// indexed PK lookup. Cache only on profiling signal.
type LibraryStore interface {
	For(ctx context.Context, libraryID string) (*LibraryHandle, error)
}

// LibraryHandle bundles the Library row with the seams it pins to
// (Storage, Placer) plus the delivery glue. Methods deliver the
// answers callers actually want — relativize, open, deliver — so they
// do not reach back into Resolver / Storage themselves.
//
// Storage may be nil when the Resolver could not resolve the library's
// backend (transient backend error; library still useful for metadata
// reads). Placer may be nil for libraries that have no path AND no
// backend. Callers that require either field check for nil.
type LibraryHandle struct {
	Library model.Library
	Storage storage.Storage
	Placer  Placer

	// PlacerErr holds the reason Placer is nil. Callers that require a
	// Placer (Approve) surface this so admins see the real cause —
	// missing backend, deleted backend_id, library with no path —
	// instead of an opaque "no placer" message.
	PlacerErr error

	files           bookFileLister
	orphans         PendingOrphansEnqueuer
	presignTTL      time.Duration
	presignFallback string
}

// bookFileLister is the slice of FileRepo a handle actually needs to
// find a book's bytes. Narrow so the delivery logic is testable
// without a database.
type bookFileLister interface {
	ListByBook(ctx context.Context, bookID string) ([]model.File, error)
}

// IsObjectStore reports whether this library's bytes live in a remote
// object store rather than on the local filesystem. Names the question
// once: the in-file metadata embed (ADR-0001), the folder-rename
// strategy (ADR-0005) and byte deletion (whether the orphan queue is
// involved at all, also ADR-0005) branch on it. Those are policies — what
// this library's kind means for a decision — and each is stated where its
// ADR lives.
//
// The mechanical questions do not come here. Every question about *keys*
// — walking, resolving a location, writing one — goes through keyRoot,
// and every question about *serving* bytes goes through FileSource; both
// ask this once on their caller's behalf. A caller reading the capability
// to derive a key or a delivery mode is answering a question that already
// has an answer, and will answer it differently.
//
// Asked of the adapter, not of libraries.backend_id. That column was
// the previous answer and it was wrong for exactly one install shape:
// the storage-v2 backfill gave every pre-existing *local* library a
// kind=local backend row, so a non-NULL backend_id meant "a backend row
// exists" to the migrator and "is not local" to this tier. A migrated
// local library therefore took every object-store branch — the key rule
// handed a "/"-rooted local backend a library-relative key, the in-file
// embed switched itself off, and a folder rename enqueued pending
// orphans that CONTEXT says local backends do not produce (#202).
//
// A nil Storage answers false: a library whose backend could not be
// resolved is not an object store, it is unusable, and every caller
// guards Storage separately.
func (h *LibraryHandle) IsObjectStore() bool {
	if h.Storage == nil {
		return false
	}
	return h.Storage.Capabilities()&storage.CapObjectStore != 0
}

// keyRoot is the prefix this library's own keys hang off, and whether it
// has one at all.
//
// The single place the handle asks IsObjectStore to answer a question
// about *keys*. An object store owns its own per-library prefix, so a
// stored location is already the key it answers to and an empty root is
// the correct answer. The local backend is rooted at "/" for the whole
// instance (ADR-0030 §1), so a location means nothing until it is joined
// onto the library's own root.
//
// Telling those two empties apart is why there is a second return value,
// and it is not a formality: for an object store an empty root is by
// design, while for a local library it means unconfigured, and every
// caller that took the location as a key anyway did real damage — a walk
// that did reported the whole library empty and flagged every row for
// the purge sweeper (#203), and a write that did puts a book's file at
// the filesystem root. Callers name that case in their own vocabulary
// (ErrNoWalkRoot, ErrNoPlaceRoot) because what it costs differs.
func (h *LibraryHandle) keyRoot() (string, bool) {
	if h.IsObjectStore() {
		return "", true
	}
	root := h.localRoot()
	return root, root != ""
}

// localRoot is the library's on-disk root, preferring the storage-v2
// Root column and falling back to the legacy Path.
func (h *LibraryHandle) localRoot() string {
	return libraryLocalRoot(h.Library)
}

// libraryLocalRoot is the one reading of "where does this library live on
// disk". Two columns say it — storage-v2's root and the legacy path — and
// which one is populated depends on how old the row is, so the preference
// order is a fact about the schema rather than about any one caller. Empty
// means the library has no filesystem of its own: an s3 library, whose
// prefix the backend encodes and whose root column is deliberately blank
// (repo.LibraryRepo.CreateLibrary).
//
// A free function and not only a handle method because the sandbox roots
// ask it of every library in a listing, where building a handle apiece
// would resolve N backends to read two columns.
func libraryLocalRoot(lib model.Library) string {
	if lib.Root != nil && *lib.Root != "" {
		return *lib.Root
	}
	return lib.Path
}

// LibraryLister is the slice of the library catalog the sandbox roots
// need. Narrow so the roots are testable without a database, and so both
// tiers can pass what they already hold — the service its LibraryRepo,
// the HTTP layer its LibraryService.
type LibraryLister interface {
	List(ctx context.Context) ([]model.Library, error)
}

// BookFileRoots is the allow-list half of the Book file sandbox: the
// directories a book file may be read from or deleted under. Two sources,
// and only two — the BookDrop staging area, and every library that lives
// on a filesystem. A library with no local root (s3-backed) contributes
// nothing, which is how an object-store install ends up with a sandbox
// that admits only BookDrop.
//
// The counterpart to SandboxPath, and here for the same reason: serving
// and deleting have to be gated by the same allow-list or a change to one
// tier silently loosens the other. It was written twice — once here in
// the service tier, once character-for-character in the HTTP layer — and
// a third time in the comic reader before CBZ moved onto the storage
// seam. One implementation is the property the sandbox is named for.
//
// Degrades rather than fails: a listing error yields the roots we do
// have. The sandbox fails closed, so the worst case is a refused read or
// a skipped unlink, never a widened gate. A nil lister is the same case —
// an install with no library catalog wired keeps BookDrop and nothing
// else.
func BookFileRoots(ctx context.Context, bookDropPath string, libs LibraryLister) []string {
	roots := make([]string, 0, 4)
	if bookDropPath != "" {
		roots = append(roots, bookDropPath)
	}
	if libs == nil {
		return roots
	}
	list, err := libs.List(ctx)
	if err != nil {
		slog.Warn("book file sandbox: list libraries for roots", "err", err)
		return roots
	}
	for _, lib := range list {
		if root := libraryLocalRoot(lib); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

// LocalPath resolves a library-relative location to an absolute path on
// a local library. Empty for object-store-backed libraries, which have
// no filesystem to resolve against, and for a local library with no root
// configured, which has nothing to resolve against yet.
func (h *LibraryHandle) LocalPath(location string) string {
	root, ok := h.keyRoot()
	if !ok || root == "" {
		return ""
	}
	return filepath.Join(root, filepath.FromSlash(location))
}

// StorageKey turns a files.location into the key this library's Storage
// actually answers to.
//
// Exported because the job tier needs it too. While it was private,
// internal/task re-derived it and the copy was missing both branches
// below — it joined the root on unconditionally, so a legacy absolute
// location asked for /lib/root/lib/root/… and hashed nothing, on every
// boot, forever (#201).
//
// files.location is relative to the library root (CONTEXT, "Files row"),
// but a local install's LocalFS is rooted at "/" and expects absolute
// keys — it is deliberately not rooted per library, because the scan
// worker and bookdrop ingest hand it absolute paths. Nothing reconciled
// the two, so every read of a locally-placed book asked the filesystem
// for "/Author/Title/book.epub" and got nothing.
//
// The symptom was quiet in one direction and loud in the other: reading
// a book failed outright, while reading-guide generation degrades an
// unreadable book to a metadata-only guide by design, so every EPUB on a
// local library silently produced the weaker guide ADR-0024 §2 reserves
// for formats with no extractable text.
//
// Backend-backed libraries are untouched: their keys are already the
// object keys, and the backend encodes its own prefix — which is what
// LocalPath returning "" for them means here, rather than a second
// reading of the same capability bit.
func (h *LibraryHandle) StorageKey(location string) string {
	// Already absolute: a legacy row. books.path predates storage-v2, and
	// the storage-v2 backfill wrote files.location verbatim whenever the
	// library root was unknown at seed time (migrator.seedFilesFromBooks,
	// which its own tests pin). Such a string is already the key a
	// "/"-rooted LocalFS wants; joining it onto the root asks for
	// /lib/root/lib/root/… and finds nothing.
	//
	// This is what makes the shim total over both shapes, which is what
	// lets the edit-side write pipeline read books.path — mixed to this
	// day — through it (#168).
	if filepath.IsAbs(location) {
		return location
	}
	if abs := h.LocalPath(location); abs != "" {
		return abs
	}
	return location
}

// OpenBook returns the book's bytes, wherever they live. Callers that
// want content — Send-to-Kindle, device push — use this and never
// learn the delivery vocabulary; BookSource stays for the file-serve
// handler, which genuinely needs a routing answer.
//
// Deliberately never presigns: a presigned URL is an answer for the
// browser, useless to a caller that needs the bytes in-process.
//
// The returned Closer is always non-nil on success and must be closed.
func (h *LibraryHandle) OpenBook(ctx context.Context, book model.Book) (io.Reader, int64, io.Closer, error) {
	if h.Storage != nil && h.files != nil {
		if f, err := primaryFile(ctx, h.files, book); err == nil {
			key := h.StorageKey(f.Location)
			src, oerr := h.Storage.Open(ctx, key)
			if oerr != nil {
				return nil, 0, nil, fmt.Errorf("open %s: %w", key, oerr)
			}
			return io.NewSectionReader(src, 0, src.Size()), src.Size(), src, nil
		}
	}

	if book.Path == "" {
		return nil, 0, nil, errors.New("book has no stored file to open")
	}
	file, err := os.Open(book.Path)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("open book file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, nil, err
	}
	return file, info.Size(), file, nil
}

// BookDeleteGrace is how long a deleted book's bytes linger on a
// backend-backed library before the sweeper removes them. Comfortably
// longer than any presigned URL this application issues, so a download
// already in flight when the row went finishes rather than 404s.
const BookDeleteGrace = time.Hour

// DeleteBookAndBytes deletes a book and the bytes it owned, in the only
// order that works, by taking the row delete as the middle step.
//
// The ordering is forced by the schema and it is not free to choose:
// deleting the books row cascades its files rows, so the keys have to be
// snapshotted while the row that names them still exists, and the bytes
// removed only once it is gone. Both halves were well covered on their
// own and neither can enforce the sequence alone, so it lived as prose
// plus one correct caller — which is a rule, not a type. Handing the row
// delete in makes the wrong order unwritable: a caller supplies *what*
// deletes the row and this method decides *when*, so there is no
// arrangement of the two calls left for it to get wrong.
//
// Two errors because the caller does two different things with them.
// err is the row delete's, and it is fatal: it is the authoritative step,
// nothing after it has run, and re-issuing the request is a legitimate
// retry. bytesErr is the cleanup's, and it is a warning: the row is
// already gone, so failing the call would tell the user their delete did
// not happen when it did — while swallowing it would let a stranded
// half-gigabyte of narration read as a clean delete (ADR-0025 §6). A
// failed snapshot lands in bytesErr too: it costs the list, not the
// delete, so the row still goes and the bytes wait for an operator.
func (h *LibraryHandle) DeleteBookAndBytes(
	ctx context.Context,
	bookID string,
	deleteRow func(context.Context) error,
) (bytesErr error, err error) {
	locations, listErr := h.BookFileLocations(ctx, bookID)
	if err := deleteRow(ctx); err != nil {
		return nil, err
	}
	return errors.Join(listErr, h.DeleteBookBytes(ctx, bookID, locations)), nil
}

// BookFileLocations lists the storage keys belonging to a book.
//
// Split from DeleteBookBytes on purpose: they sit on opposite sides of
// the row delete, and DeleteBookAndBytes above is what holds them in that
// order. Kept exported for the readers that want the list for its own
// sake — the comic handler asks whether a book has any files at all.
// A nil error with no keys is the normal answer for a book whose files
// were never backfilled.
func (h *LibraryHandle) BookFileLocations(ctx context.Context, bookID string) ([]string, error) {
	if h.files == nil {
		return nil, nil
	}
	list, err := h.files.ListByBook(ctx, bookID)
	if err != nil {
		return nil, fmt.Errorf("list files for book %s: %w", bookID, err)
	}
	out := make([]string, 0, len(list))
	for _, f := range list {
		if f.Location != "" {
			out = append(out, f.Location)
		}
	}
	return out, nil
}

// BookFile returns one of a book's files rows by id. Used by callers
// that hold a pointer to a specific rendition rather than a format.
func (h *LibraryHandle) BookFile(ctx context.Context, bookID, fileID string) (model.File, bool) {
	if h.files == nil || fileID == "" {
		return model.File{}, false
	}
	list, err := h.files.ListByBook(ctx, bookID)
	if err != nil {
		return model.File{}, false
	}
	for _, f := range list {
		if f.ID == fileID {
			return f, true
		}
	}
	return model.File{}, false
}

// PrimaryContentHash is the hash of the book's own file — the one whose
// format matches books.format, i.e. the thing a narration is made from
// rather than the narration itself.
func (h *LibraryHandle) PrimaryContentHash(ctx context.Context, book model.Book) []byte {
	if h.files == nil {
		return nil
	}
	f, err := primaryFile(ctx, h.files, book)
	if err != nil {
		return nil
	}
	return f.ContentHash
}

// NewPrimaryHash is the "which bytes is this book, right now" lookup —
// the provenance side of every derived artifact, and the current side
// of every model.Stale comparison. One constructor so every tier shares
// the degrade policy: an unresolvable library degrades to "no hash"
// (the callers' documented empty-hash tolerance) but says so — silently
// absorbing an infrastructure failure into "reads as fresh" is the
// quiet failure ADR-0033 §5 exists to refuse (#297).
func NewPrimaryHash(store LibraryStore) func(context.Context, model.Book) []byte {
	return func(ctx context.Context, book model.Book) []byte {
		handle, err := store.For(ctx, book.LibraryID)
		if err != nil {
			slog.Warn("primary hash: resolve library", "book", book.ID, "err", err)
			return nil
		}
		return handle.PrimaryContentHash(ctx, book)
	}
}

// DeleteBookBytes removes the objects a book owned, given keys that were
// read before the row was deleted. Deleting a book goes through
// DeleteBookAndBytes, which is what guarantees that; this stays exported
// for the callers that already hold one specific key — the narration
// sweep holds the single files row a run generated.
//
// Exists because deleting a book used to unlink only books.path — the
// legacy single-path field — so a book on a backend-backed library left
// its objects behind entirely, and a book with more than one file left
// all but one. Tolerable while a book was one 2 MB EPUB; not once a
// generated narration is half a gigabyte (ADR-0025 §6).
//
// Local libraries delete inline. Backend-backed ones enqueue the keys
// for the orphan sweeper instead (ADR-0005), because the bytes may still
// be serving a presigned URL. Best-effort per key: one unreachable
// object does not strand the rest, and every failure is reported in the
// returned error. Callers treat that error as a warning — the row is
// already gone and lying about it would be worse.
func (h *LibraryHandle) DeleteBookBytes(ctx context.Context, bookID string, locations []string) error {
	if len(locations) == 0 {
		return nil
	}

	if h.IsObjectStore() {
		if h.orphans == nil {
			return nil
		}
		id := bookID
		rows := make([]repo.PendingOrphanInsert, 0, len(locations))
		eligible := time.Now().Add(BookDeleteGrace)
		for _, key := range locations {
			rows = append(rows, repo.PendingOrphanInsert{
				LibraryID:  h.Library.ID,
				Key:        key,
				EligibleAt: eligible,
				Reason:     repo.ReasonOrphanBookDelete,
				BookID:     &id,
			})
		}
		if err := h.orphans.Insert(ctx, rows); err != nil {
			return fmt.Errorf("enqueue orphans for book %s: %w", bookID, err)
		}
		return nil
	}

	if h.Storage == nil {
		return nil
	}
	var failures []error
	for _, location := range locations {
		// Through storageKey, not the raw location: a local install's
		// LocalFS is rooted at "/" and answers to absolute keys, so
		// handing it the library-relative files.location asked the
		// filesystem to delete "/Author/Title/book.epub" and quietly
		// removed nothing. The same reason OpenBook goes through it.
		key := h.StorageKey(location)
		if derr := h.Storage.Delete(ctx, key); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
			failures = append(failures, fmt.Errorf("delete %s: %w", key, derr))
		}
	}
	return errors.Join(failures...)
}

// OpenBookSource is OpenBook for callers that need random access — the
// EPUB text extractor reads a zip central directory and cannot work from
// a stream. Resolution is not duplicated: it delegates to OpenBook and
// adapts, so the "never reach around this with os.Open(book.Path)" rule
// holds here too.
//
// The returned Source must be closed.
func (h *LibraryHandle) OpenBookSource(ctx context.Context, book model.Book) (storage.Source, error) {
	r, size, closer, err := h.OpenBook(ctx, book)
	if err != nil {
		return nil, err
	}
	// Backend path: the Closer is the Source OpenBook opened.
	if src, ok := closer.(storage.Source); ok {
		return src, nil
	}
	// Local path: *os.File is both ReaderAt and Closer.
	if ra, ok := r.(io.ReaderAt); ok {
		return readerAtSource{ReaderAt: ra, size: size, closer: closer}, nil
	}
	_ = closer.Close()
	return nil, errors.New("book bytes are not randomly accessible")
}

// readerAtSource adapts a ReaderAt plus a size into a storage.Source.
type readerAtSource struct {
	io.ReaderAt
	size   int64
	closer io.Closer
}

func (s readerAtSource) Size() int64  { return s.size }
func (s readerAtSource) Close() error { return s.closer.Close() }

// SidecarKey returns the paired JSON sidecar storage key for a book
// file's storage key. Delegates to sidecar.KeyFor so the derivation
// rule lives in one place.
func (h *LibraryHandle) SidecarKey(bookLocation string) string {
	return sidecar.KeyFor(bookLocation)
}

// Relativize is gone with its only caller. The scan worker walked from
// the library root and un-absolutized each entry against it; Walk below
// does the rooting itself, from the prefix it listed under rather than
// from the root as spelled, and nothing else ever asked. ADR-0030's
// Consequences names Relativize and relativeToRoot as the two live
// producers of absolute locations, "the very rows a migration would have
// had to clean up" — this is one of the two, retired rather than
// reimplemented.

// ErrNoWalkRoot says a local library has no root configured, so there
// is nothing for Walk to start from.
//
// A named error rather than an empty result because those two are not
// the same answer anywhere it matters: an empty walk means every file
// in the library is gone, and the scan worker acts on that by
// soft-flagging every row for the purge sweeper. "Unconfigured" is a
// state to report to an admin, and it has to be impossible to mistake
// for "the library is empty".
var ErrNoWalkRoot = errors.New("library has no local root to walk")

// Walk lists everything in the library, as entries whose Location is
// library-relative — the vocabulary files.location is stored in
// (CONTEXT, "Files row") — and whose Key is what this library's Storage
// answers to for those bytes.
//
// The rooting question is answered here, once, for both kinds of
// backend. An object store owns its own per-library prefix, so the walk
// starts at the top of it and its keys are already library-relative. A
// local backend is rooted at "/" for the whole instance (ADR-0030 §1),
// so the walk starts at the library's own root and every key comes back
// as a whole filesystem path with the leading slash off — which is what
// a "/"-rooted backend reports even though it also answers to the
// absolute form (storagetest, KeyShapesNameTheSameObject).
//
// Callers used to make that choice for themselves, and the scan worker
// made it wrong in the one direction that is silent: an S3 library has
// no root by design, that read as "not configured", and library scan
// skipped every one of them while reporting success (#203).
//
// The relative form is derived by stripping the prefix the walk
// actually listed under, not by matching the library root as an admin
// happened to spell it. The backend cleans the prefix before listing,
// so a root that is spelled with a redundant separator does not match
// what comes back, and the old string-prefix rule fell through to
// emitting the absolute path — at which point every entry reads New,
// every row reads Missing, and the whole library is flagged for the
// purge sweeper. A walk that promises library-relative locations has to
// promise it for every spelling of the root.
//
// The listing is materialised: its one consumer diffs it against the
// whole files table, so streaming buys nothing. A walk that fails
// partway returns what it collected *and* the error — a caller that
// treats a half-finished listing as the truth marks the rest of the
// library missing, so it needs to see both.
//
// The iteration is here rather than behind a helper in the scan package
// because there was never a second caller to justify one: what scan
// exported was a channel pair plus an invariant that both had to be
// drained, and its only consumer drained them straight into this slice.
// Cancellation is not lost with the channels — storage iterators check
// ctx themselves, and the error comes back with the partial listing.
func (h *LibraryHandle) Walk(ctx context.Context) ([]scan.WalkEntry, error) {
	if h.Storage == nil {
		return nil, errors.New("library handle: no storage")
	}
	prefix, ok := h.keyRoot()
	if !ok {
		return nil, ErrNoWalkRoot
	}
	base := walkBase(prefix)

	it, err := h.Storage.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()

	var walked []scan.WalkEntry
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return walked, nil
		}
		if err != nil {
			return walked, err
		}
		walked = append(walked, scan.WalkEntry{
			// The location is rooted here, by the one place that knows
			// what the walk listed under; the key is what the backend
			// answered with, carried through untouched so a reader never
			// re-derives it (ADR-0030 §2).
			Location: trimWalkBase(obj.Key, base),
			Key:      obj.Key,
			Size:     obj.Size,
			Mtime:    obj.ModTime,
			ETag:     obj.ETag,
		})
	}
}

// walkBase is the prefix in the shape the backend reports keys under it:
// cleaned, slash-separated, leading slash off, because a listing key is
// relative to the backend's own root.
func walkBase(prefix string) string {
	if prefix == "" {
		return ""
	}
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(prefix)), "/")
}

// trimWalkBase turns a key the backend reported into a location relative
// to the walked prefix.
//
// It compares with the leading slash off both sides for the same reason
// the conformance suite does (storagetest.rebased.in): a "/"-rooted
// backend answers to "/a/b" but reports "a/b". A key that does not sit
// under the prefix at all comes back untouched — the backend only lists
// what is under the prefix it was given, so there is no case for this to
// paper over, and silently rewriting one would hide a backend that broke
// that contract.
func trimWalkBase(key, base string) string {
	if base == "" {
		return key
	}
	k := strings.TrimPrefix(key, "/")
	switch {
	case k == base:
		// The library root is itself a file. Degenerate, but it must
		// still name something.
		return path.Base(k)
	case strings.HasPrefix(k, base+"/"):
		return k[len(base)+1:]
	default:
		return key
	}
}

// Open opens a Source against the library's storage at the given
// library-relative location. Errors when the handle has no Storage
// (resolver failed at construction).
func (h *LibraryHandle) Open(ctx context.Context, location string) (storage.Source, error) {
	if h.Storage == nil {
		return nil, errors.New("library handle: no storage")
	}
	return h.Storage.Open(ctx, location)
}

// ErrNoPlaceRoot says a local library has no root configured, so there
// is nowhere for PlaceAt to write.
//
// Named, and checked before a byte moves, because the alternative is
// silent and destructive: the local backend is rooted at "/", so a
// placement that let an unrooted location through would write a book's
// file to the filesystem root and record a files row pointing at it.
var ErrNoPlaceRoot = errors.New("library has no local root to place into")

// PlaceAt writes a local file to a location this library already has a
// name for, and consumes the source.
//
// This is placement for a book that exists. Placer is placement for a
// book that does not, and the difference is not a mode — it is which
// question is being answered. Placer *names* the destination: it derives
// {Author}/{Title}/ from the source's metadata, walks a " (2)" suffix
// until it finds a folder nobody owns, and keeps the source's basename.
// PlaceAt is handed the name and only has to write it. Pointed at an
// existing book, every one of Placer's naming decisions is wrong: the
// collision suffix drops the file into a sibling "Title (2)" folder that
// a later Library scan reads as a second book — the exact outcome
// ADR-0025 exists to prevent — and the basename would be the temp file's,
// embookshelf-audiobook-1234567.mp3.
//
// So the collision suffix is deliberately out of scope here rather than
// switched off by a flag. Landing on the same key twice is the point:
// regeneration is destructive by design (ADR-0025 §4), and a second run
// that suffixed instead would accumulate half-gigabyte renditions.
//
// Nor does it route through the Placer seam to reuse the adapter bodies,
// which is the tempting version of this. PlacerBuilder still picks its
// adapter from libraries.backend_id — the column IsObjectStore exists
// because it answers "a backend row exists", not "is not local" (#202) —
// so a migrated local library gets a BackendPlacer over a "/"-rooted
// LocalFS, and a library-relative key would land at the filesystem root.
// Approve lives with that; a write path being built today should not
// inherit it. The mechanics are not duplicated either way: an object
// store and a local library differ only in what their keys are rooted at,
// which keyRoot already answers, and both write through the one Put.
//
// Going through the backend rather than moving the file by hand is also
// what makes the failure clean. Both adapters write all-or-nothing —
// LocalFS through a temp file in the destination directory that it
// removes on any failure, S3 in a single PutObject — so a placement that
// fails leaves no partial artifact at the book's own key and no stray
// temp beside it. The source is left alone on failure: it is the
// caller's, to retry with or to reap.
//
// Destructive on srcPath on success, like Placer.Place: after it returns
// the caller must treat srcPath as gone. Size and Mtime are the source's,
// captured before the bytes move out of reach, so the caller does not
// re-stat. On a local library that used to be exact — a rename carries
// the source's mtime across and a write does not — so the recorded mtime
// is now approximate there too, as it has always been for an upload. The
// cost is bounded and self-correcting: scan/differ compares size plus
// mtime-to-the-second and falls back to hashing, so at worst the next
// scan reads the file once and settles the row.
func (h *LibraryHandle) PlaceAt(ctx context.Context, location, srcPath, format string) (PlaceResult, error) {
	if h.Storage == nil {
		return PlaceResult{}, errors.New("library handle: no storage")
	}
	if _, ok := h.keyRoot(); !ok {
		return PlaceResult{}, ErrNoPlaceRoot
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("stat source: %w", err)
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = f.Close() }()

	opts := []storage.PutOption{}
	if mime := storageMIMEForFormat(format); mime != "" {
		opts = append(opts, storage.WithContentType(mime))
	}
	if _, err := h.Storage.Put(ctx, h.StorageKey(location), f, opts...); err != nil {
		return PlaceResult{}, fmt.Errorf("put %s: %w", location, err)
	}

	_ = f.Close()
	if err := os.Remove(srcPath); err != nil {
		slog.Warn("place: remove source after write", "path", srcPath, "err", err)
	}
	return PlaceResult{
		Location:   location,
		FolderPath: path.Dir(location),
		Size:       info.Size(),
		Mtime:      info.ModTime(),
	}, nil
}

// BookSource returns the delivery decision for serving a book through
// this library. Picks one of three modes based on storage capabilities
// and config:
//
//   - "presign" — 302 redirect to a pre-signed URL. Opt-in via
//     EMBOOKSHELF_PRESIGN_FALLBACK="presign" (the env var name
//     predates the inversion; treat it as the delivery mode). Requires
//     the bucket to be configured with CORS rules that allow the SPA
//     origin, otherwise browser XHR (epub.js, pdf.js) will fail on
//     the cross-origin redirect.
//   - "stream" — open the bytes via Storage.Get/Open and pipe them
//     through the app server. Default for any backend that has Storage
//     wired (LocalFS or S3). Avoids the CORS/redirect pitfalls of
//     presign at the cost of routing bytes through the app.
//   - "local" — legacy fallback for installs where Storage / files
//     wiring is missing; the handler streams book.Path off disk.
func (h *LibraryHandle) BookSource(ctx context.Context, book model.Book) (BookSource, error) {
	if book.Path == "" && (h.Storage == nil || h.files == nil) {
		return BookSource{}, errors.New("book has no path and storage/files are unavailable")
	}

	if h.Storage == nil || h.files == nil {
		return BookSource{Kind: BookDeliveryLocal, Path: book.Path}, nil
	}

	f, ferr := primaryFile(ctx, h.files, book)
	if ferr == nil {
		return h.FileSource(ctx, f.Location)
	}

	if book.Path != "" {
		return BookSource{Kind: BookDeliveryLocal, Path: book.Path}, nil
	}
	return BookSource{}, fmt.Errorf("book source: %w", ferr)
}

// FileSource is the delivery decision for one stored location, which is
// the whole of the decision — BookSource above is this plus the question
// of which of a book's files is the primary one.
//
// Split out because a book has more than one thing to serve. The reader
// dispatches on the rendition the user picked rather than on books.format
// (ADR-0025 §3), so the generated narration is served by location while
// the EPUB is served by primaryFile, and the narration path answered the
// delivery question a second time to get there: it read the object-store
// capability itself and hardcoded a stream. Whatever the deployment
// configured, the audio was piped through the app server — so an install
// that turned presign on redirected its EPUBs and streamed its
// half-gigabyte MP3s, which is the case presign exists for. One selector
// must not mean two delivery policies.
//
// The key rule is not restated either: StorageKey answers it for both
// kinds of backend, so an object store gets the location it already
// answers to and a local library gets it rooted (#168).
func (h *LibraryHandle) FileSource(ctx context.Context, location string) (BookSource, error) {
	if location == "" {
		return BookSource{}, errors.New("file source: no location")
	}
	if h.Storage == nil {
		// No Storage resolved: the local filesystem is all that is left,
		// and only a local library has a path to offer.
		if path := h.LocalPath(location); path != "" {
			return BookSource{Kind: BookDeliveryLocal, Path: path}, nil
		}
		return BookSource{}, errors.New("file source: library has no storage")
	}

	key := h.StorageKey(location)
	if h.presignFallback == BookDeliveryPresign && h.Storage.Capabilities()&storage.CapPresign != 0 {
		if ps, ok := h.Storage.(Presigner); ok {
			// A failed signature falls through to streaming rather than
			// failing the read: the bytes are reachable either way, and the
			// redirect is an optimisation.
			if url, err := ps.PresignGet(ctx, key, h.presignTTL); err == nil {
				return BookSource{Kind: BookDeliveryPresign, URL: url, TTL: h.presignTTL}, nil
			}
		}
	}
	return BookSource{Kind: BookDeliveryStream, Storage: h.Storage, Key: key}, nil
}

// BookSource describes how a book file should be delivered.
//
//   - Kind=="local"   → stream Path through the app server (filesystem path).
//   - Kind=="presign" → 302 redirect to URL, valid for TTL.
//   - Kind=="stream"  → open Key on Storage and pipe through the app.
type BookSource struct {
	Kind    string
	Path    string
	URL     string
	TTL     time.Duration
	Storage storage.Storage
	Key     string
}

// Presigner is the capability-gated interface CapPresign-bearing
// backends satisfy. Backends are probed by type assertion so the
// storage interface stays minimal.
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// LibraryStoreDeps groups everything LibraryStore needs to build a
// LibraryHandle. PresignTTL and PresignFallback feed BookSource; pass
// zero values to disable presign (handle will always pick local).
type LibraryStoreDeps struct {
	Libs      *repo.LibraryRepo
	Resolver  storage.Resolver
	NewPlacer PlacerBuilder
	Files     *repo.FileRepo
	// Orphans defers byte deletion on backend-backed libraries. Optional:
	// when nil, DeleteBookBytes degrades to leaving the bytes for a human
	// rather than deleting something a presigned URL may still be serving.
	Orphans         PendingOrphansEnqueuer
	PresignTTL      time.Duration
	PresignFallback string
}

// NewLibraryStore builds the default LibraryStore — stateless,
// fresh-lookup-per-call.
func NewLibraryStore(deps LibraryStoreDeps) LibraryStore {
	return &defaultLibraryStore{deps: deps}
}

type defaultLibraryStore struct {
	deps LibraryStoreDeps
}

func (s *defaultLibraryStore) For(ctx context.Context, libraryID string) (*LibraryHandle, error) {
	if s.deps.Libs == nil {
		return nil, errors.New("library store: no library repo")
	}
	lib, err := s.deps.Libs.GetByID(ctx, libraryID)
	if err != nil {
		return nil, fmt.Errorf("library store: lookup: %w", err)
	}

	var store storage.Storage
	if s.deps.Resolver != nil {
		backendID := ""
		if lib.BackendID != nil {
			backendID = *lib.BackendID
		}
		// Resolve failure is non-fatal: caller checks Storage for nil
		// before reaching for bytes. Library-only callers (e.g. just
		// reading the row) still get a usable handle.
		if st, rerr := s.deps.Resolver.Resolve(backendID); rerr == nil {
			store = st
		}
	}

	var placer Placer
	var placerErr error
	if s.deps.NewPlacer != nil {
		// Placer build failure is non-fatal at handle construction —
		// metadata-only callers still get a usable handle. The error is
		// captured so write-path callers (Approve) surface the real
		// reason instead of "no placer".
		if p, perr := s.deps.NewPlacer(lib); perr == nil {
			placer = p
		} else {
			placerErr = perr
		}
	} else {
		placerErr = errors.New("no placer builder configured")
	}

	// Assign through a nil check: a nil *repo.FileRepo stored in the
	// interface field would be a non-nil interface, and every
	// h.files == nil guard below would silently stop working.
	var files bookFileLister
	if s.deps.Files != nil {
		files = s.deps.Files
	}

	return &LibraryHandle{
		Library:         lib,
		Storage:         store,
		Placer:          placer,
		PlacerErr:       placerErr,
		files:           files,
		orphans:         s.deps.Orphans,
		presignTTL:      s.deps.PresignTTL,
		presignFallback: s.deps.PresignFallback,
	}, nil
}

// primaryFile finds the canonical files row for a book — the row whose
// format matches books.format. Falls back to the first row when no
// format match exists; returns an error when there are no rows yet
// (pre-files-backfill installs).
func primaryFile(ctx context.Context, files bookFileLister, book model.Book) (model.File, error) {
	list, err := files.ListByBook(ctx, book.ID)
	if err != nil {
		return model.File{}, err
	}
	for _, f := range list {
		if f.Format == book.Format {
			return f, nil
		}
	}
	if len(list) > 0 {
		return list[0], nil
	}
	return model.File{}, fmt.Errorf("no files row for book %s", book.ID)
}
