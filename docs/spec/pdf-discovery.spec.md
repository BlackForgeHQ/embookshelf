# PDF Discovery, Title & Author Handling — Spec (Go)

## 1. Layout

```
booklore/
├── cmd/booklore/main.go
└── internal/
    ├── monitoring/        # fs watcher
    ├── bookdrop/          # alt drop folder
    ├── ingest/            # event processor + dispatch
    ├── fileprocessor/     # per-format processors (pdf, epub, ...)
    ├── metadata/
    │   └── pdf/           # PDFBox-equivalent extractor
    ├── store/             # repos + entities (gorm or sqlc)
    └── config/
```

Deps:
- `github.com/fsnotify/fsnotify` — fs events.
- `github.com/pdfcpu/pdfcpu` or `github.com/ledongthuc/pdf` or `github.com/unidoc/unipdf/v3` — PDF parse + render.
- `github.com/beevik/etree` — XMP XML.
- `gorm.io/gorm` or `database/sql` + `sqlc`.

## 2. Discovery

Two entry points feed shared event channel.

### 2.1 Library watcher (primary)

- Pkg: `internal/monitoring`
- File: `library_watch.go`
- Wraps `fsnotify.Watcher`. Walks library roots with `filepath.WalkDir`, calls `watcher.Add()` per dir.

```go
type WatchService struct {
    w        *fsnotify.Watcher
    out      chan<- FileEvent
    roots    []string
    log      *slog.Logger
    mu       sync.Mutex
    watched  map[string]struct{}
}

func (s *WatchService) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case ev := <-s.w.Events:
            if !isSupported(ev.Name) { continue }
            switch {
            case ev.Op&fsnotify.Create != 0:
                if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
                    s.addRecursive(ev.Name)
                    continue
                }
                s.out <- FileEvent{Op: OpCreate, Path: ev.Name}
            case ev.Op&fsnotify.Remove != 0:
                s.out <- FileEvent{Op: OpDelete, Path: ev.Name}
            }
        case err := <-s.w.Errors:
            s.log.Warn("watch error", "err", err)
        }
    }
}
```

`isSupported` — extension whitelist (`.pdf`, `.epub`, ...).

### 2.2 Bookdrop watcher (alt)

- Pkg: `internal/bookdrop`
- File: `monitor.go`
- Watches `cfg.BookdropFolder`.
- Backfill on startup: `filepath.WalkDir` → emit synthetic create events for existing files.
- Same `FileEvent` channel.

### 2.3 Event processor

- Pkg: `internal/ingest`
- File: `processor.go`

Constants:

```go
const (
    debounceFile        = 500 * time.Millisecond
    debounceFolder      = 5 * time.Second
    stabilityInterval   = 3 * time.Second
    stabilityMaxWait    = 120 * time.Second
)
```

Pipeline:

```go
type Processor struct {
    in       <-chan FileEvent
    handler  TransactionalHandler
    debounce map[string]*time.Timer
    mu       sync.Mutex
}

func (p *Processor) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done(): return
        case ev := <-p.in:
            p.schedule(ev)
        }
    }
}
```

Per-path debounce timer resets on every event. After fire:
1. `fileHasContent(path)` — `os.Stat`, skip if size 0.
2. `awaitStable(path)` — poll size every `stabilityInterval` until 2 consecutive equal sizes or `stabilityMaxWait` elapsed.
3. Resolve org mode for library: `BookPerFile` | `BookPerFolder` | `AutoDetect`.
4. `handler.HandleNewBookFile(ctx, libraryID, path)`.

`AutoDetect`: walk folder, count audio files, treat as folder-book only if ≥ 2.

## 3. PDF dispatch

- Pkg: `internal/fileprocessor`
- Interface:

```go
type BookFileProcessor interface {
    Supports() []BookFileType
    ProcessNew(ctx context.Context, lf LibraryFile) error
}
```

- File: `pdf_processor.go`

```go
type PdfProcessor struct {
    creator   *BookCreatorService
    extractor *pdf.MetadataExtractor
    cover     *pdf.CoverGenerator
    log       *slog.Logger
}

func (p *PdfProcessor) Supports() []BookFileType { return []BookFileType{BookFilePDF} }

func (p *PdfProcessor) ProcessNew(ctx context.Context, lf LibraryFile) error {
    book, err := p.creator.CreateShell(ctx, lf)
    if err != nil { return err }

    if err := p.cover.Generate(ctx, lf.Path, book.ID); err != nil {
        p.log.Warn("cover gen failed, fallback folder image", "err", err)
        _ = p.cover.FolderFallback(ctx, lf, book.ID)
    }

    return p.extractAndSet(ctx, lf.Path, book)
}
```

Cover render — first page, 150 DPI, RGB. Wrap pdf lib calls in `defer recover()` to catch panics from corrupt PDFs (Go equivalent of `OutOfMemoryError` / `NegativeArraySizeException` handling). On fail: log, return nil error so pipeline proceeds.

## 4. Title resolution

- Pkg: `internal/metadata/pdf`
- File: `extractor.go`

Fallback chain (first non-blank wins):

1. PDF Document Info dictionary → `Title`.
2. XMP `dc:title/rdf:Alt/rdf:li`. Overrides DocInfo if present.
3. Filename basename (`strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))`) — when DocInfo + XMP both blank.
4. Bookdrop late fallback: `bookdrop.AttachInitial()` sets filename when extractor returned `nil`.

Helper:

```go
func firstNonBlank(vals ...string) string {
    for _, v := range vals {
        if strings.TrimSpace(v) != "" { return v }
    }
    return ""
}
```

Title-adjacent XMP fields:
- `booklore:subtitle` (camelCase) + PascalCase legacy `booklore:Subtitle`.
- `calibre:series`, `calibreSI:series_index`.
- `booklore:seriesName`, `booklore:seriesNumber`, `booklore:seriesTotal`.

## 5. Author resolution

Chain:

1. PDF Doc Info `Author`. Split on regex `[,&]`:

```go
var authorSep = regexp.MustCompile(`[,&]`)

func parseAuthors(raw string) []string {
    out := []string{}
    for _, s := range authorSep.Split(raw, -1) {
        s = strings.TrimSpace(s)
        if s != "" { out = append(out, s) }
    }
    return out
}
```

2. XMP `dc:creator/rdf:Seq/rdf:li` (preserves order). Overrides DocInfo if non-empty.
3. **No further fallback.** Empty slice valid. No `"Unknown Author"` sentinel.

Persist:

```go
func (s *BookCreatorService) AddAuthors(ctx context.Context, bookID int64, names []string) error {
    for _, n := range names {
        if len(n) > 255 { n = n[:255] }
        a, err := s.authors.GetOrCreate(ctx, n)
        if err != nil { return err }
        if err := s.authors.Link(ctx, bookID, a.ID); err != nil { return err }
    }
    return nil
}
```

## 6. XMP namespaces

XML parse: `etree.NewDocument().ReadFromBytes(xmpBytes)`. Namespace-aware paths.

| Namespace | Fields |
|-----------|--------|
| `dc:*` (Dublin Core) | title, creator, subject (categories), description, publisher, language |
| `calibre:*`, `calibreSI:*` | series name, series index |
| `booklore:*` (camelCase + PascalCase legacy) | subtitle, IDs, ratings, moods, tags |
| `xmp:Identifier/rdf:Bag/rdf:li` | scheme map: `isbn`, `amazon`, `goodreads`, `google`, `hardcover`, `comicvine`, `ranobedb`, `lubimyczytac` |

IDs read: `isbn13`, `isbn10`, `googleId`, `goodreadsId`, `hardcoverId`, `hardcoverBookId`, `asin`, `comicvineId`, `lubimyczytacId`, `ranobedbId`.

Moods / tags — accept both RDF Bag and legacy `;`-separated string.

ISBN clean:

```go
var nonIsbn = regexp.MustCompile(`[^0-9Xx]`)

func cleanISBN(s string) string { return nonIsbn.ReplaceAllString(s, "") }
```

Validate length 10 or 13 post-clean.

Custom DocInfo keys: `Language` → language; `EBX_PUBLISHER` → publisher.

## 7. Filename pattern extractor (Bookdrop only)

- Pkg: `internal/bookdrop`
- File: `pattern.go`
- Placeholders: `{Title}`, `{Authors}`, `{SeriesName}`, `{Published:yyyy-MM-dd}`, ...
- Compile placeholder template → `*regexp.Regexp`.
- Per-match timeout 5 s — run in goroutine, select on `time.After(5*time.Second)`. Drop match on timeout.
- Batch 500 files.
- Merge with PDF-extracted result via `metadata.Merge(base, override)`.

## 8. Truncation

In `PdfProcessor.extractAndSet` before persist:

```go
func truncate(s string, n int) string {
    if len(s) > n { return s[:n] }
    return s
}
```

Limits:

| Field | Limit |
|-------|-------|
| title | 1000 |
| subtitle | 1000 |
| seriesName | 1000 |
| description | 5000 |
| author name | 255 |
| publisher / language | trim only |

Blank guard: only assign when `strings.TrimSpace(v) != ""`. Blank never overwrites existing.

## 9. Persistence

GORM models (or sqlc structs):

```go
type Book struct {
    ID            int64     `gorm:"primaryKey"`
    LibraryID     int64
    LibraryPathID int64
    Metadata      BookMetadata `gorm:"foreignKey:BookID;constraint:OnDelete:CASCADE"`
    Files         []BookFile   `gorm:"foreignKey:BookID;constraint:OnDelete:CASCADE"`
    AddedOn       time.Time
    ScannedOn     time.Time
    DeletedAt     gorm.DeletedAt
}

type BookMetadata struct {
    BookID         int64    `gorm:"primaryKey"`
    Title          string   `gorm:"size:1000;not null"`
    Subtitle       sql.NullString `gorm:"size:1000"`
    SeriesName     sql.NullString `gorm:"size:1000"`
    SeriesNumber   sql.NullFloat64
    SeriesTotal    sql.NullInt32
    Publisher      sql.NullString
    Language       sql.NullString
    PublishedDate  sql.NullTime
    Description    sql.NullString `gorm:"type:text"`
    PageCount      sql.NullInt32
    ISBN10, ISBN13 sql.NullString
    ASIN, GoogleID, GoodreadsID, HardcoverID, ComicvineID, RanobedbID, LubimyczytacID sql.NullString
    AmazonRating, GoodreadsRating, HardcoverRating, LubimyczytacRating, RanobedbRating, Rating sql.NullFloat64
    Authors    []Author   `gorm:"many2many:book_metadata_author"`
    Categories []Category `gorm:"many2many:book_metadata_category"`
    Moods      []Mood     `gorm:"many2many:book_metadata_mood"`
    Tags       []Tag      `gorm:"many2many:book_metadata_tag"`
}

type Author struct {
    ID   int64  `gorm:"primaryKey"`
    Name string `gorm:"size:255;uniqueIndex"`
}
```

Save flow per PDF:
1. `CreateShell` — single tx insert: `Book` + empty `BookMetadata` + `BookFile`.
2. Mutate metadata in-memory in `extractAndSet`.
3. `AddAuthors` — get-or-create per name, link via join.
4. Final tx: `db.Save(&book)` cascades metadata + saves connections. Optional sidecar metadata file write.

Wrap end-to-end in `db.Transaction(func(tx *gorm.DB) error { ... })`.

## 10. Configuration

Viper or stdlib `flag` + env. `cfg.go`:

```go
type Config struct {
    BookdropFolder string `env:"APP_BOOKDROP_FOLDER"`
    DiskType       string `env:"APP_DISK_TYPE" envDefault:"LOCAL"` // LOCAL|NETWORK
    PathConfig     string `env:"APP_PATH_CONFIG"`
    DBDsn          string `env:"DB_DSN"`
}
```

`DiskType`:
- `LOCAL` — `os.Rename` for atomic move.
- `NETWORK` — copy + fsync + delete (rename across mounts unreliable).

No PDF-specific tunables. Limits hard-coded as `const`.

## 11. Edge cases & errors

| Scenario | Behavior |
|----------|----------|
| Panic in PDF lib (corrupt file) | `defer recover()` in cover + extractor; log; return nil err |
| OOM-equivalent (huge alloc) | bound page render to first page; PDF lib hard limits; runtime.GC() after fail |
| PDF parse fail | log; metadata stays whatever DocInfo gave |
| XMP parse fail | log warn; continue with DocInfo only |
| File not found | return zero `Metadata{}` |
| Zero-byte file | skipped pre-processing |
| Partial write | stability poll up to 120 s |
| Bookdrop dir missing/unwritable | log; disable that watcher; app continues |
| `ctx` cancel | abort current op, return `ctx.Err()` upstream |

## 12. Concurrency

- One goroutine per watcher (`Run(ctx)`).
- Single processor goroutine consumes channel; `sync.Map` of debounce timers per path.
- Worker pool (`errgroup` + `semaphore.Weighted`) for actual file processing — cap at `runtime.NumCPU()` to bound PDF render memory.
- `BookCreatorService` — stateless, methods take `ctx`.
- All DB ops scoped to per-task transactions.

## 13. End-to-end flow

```
filesystem
   │  fsnotify event
   ▼
WatchService / BookdropMonitor
   │  FileEvent on chan
   ▼
ingest.Processor
   │  debounce 500ms / folder 5s, stability ≤ 120s
   ▼
TransactionalHandler.HandleNewBookFile()
   │
   ▼
PdfProcessor.ProcessNew()
   ├─ BookCreatorService.CreateShell()  → DB shell row
   ├─ cover.Generate()                  → page 1, 150 DPI
   └─ extractAndSet()
        │
        ▼
   pdf.MetadataExtractor.Extract()
   ├─ DocumentInformation   (title, author, subject, keywords, dates, pages)
   ├─ XMP Dublin Core       (overrides DocInfo)
   ├─ XMP Calibre           (series)
   ├─ XMP Booklore custom   (subtitle, IDs, ratings, moods, tags)
   └─ XMP Identifier map    (scheme IDs)
        │
        ▼
   Title:  XMP dc:title → DocInfo.Title → filename
   Author: XMP dc:creator → DocInfo.Author (split [,&]) → []
        │
        ▼
   truncate → AddAuthors → tx.Commit
```

## 14. Testing

- `internal/metadata/pdf/extractor_test.go` — golden-file PDFs in `testdata/`. One per fallback branch (DocInfo only, XMP only, neither, corrupt).
- `internal/ingest/processor_test.go` — fake `fsnotify` events via channel; assert debounce + stability.
- `internal/fileprocessor/pdf_processor_test.go` — integration with sqlite in-memory.
- Race detector: `go test -race ./...`.
- Fuzz `parseAuthors`, `cleanISBN`.

## 15. Key takeaways

- DocumentInformation = primary; XMP DC overrides; filename = last-resort title only.
- Author chain stops at XMP. Empty slice valid.
- XMP backward-compat: legacy PascalCase keys still read.
- Panics from PDF lib contained via `defer recover()`. Pipeline never crashes.
- Goroutines + channels replace virtual-thread watcher. fsnotify cheap on large trees.
- Truncation enforced in processor, not relied on at DB layer.
- All long-running ops accept `context.Context` for cancellation.
