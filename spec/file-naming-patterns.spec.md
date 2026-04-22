# File Naming Patterns — Feature Specification

> Configure automatic file organization using metadata placeholders. Patterns are applied when uploading files, moving files within your library, and after metadata updates.

- **Status:** Shipped
- **Scope:** `booklore-api` (Go) + `booklore-ui` (Angular)
- **Permission required:** `ADMIN` or `MANAGE_METADATA_CONFIG`
- **Settings location:** Settings → Metadata → File Naming Patterns

---

## 1. Purpose

File Naming Patterns allow administrators to define how book files are organized on disk. Instead of manually sorting files, the system resolves a template (e.g. `{authors}/{series}/{title}`) against each book's metadata to produce a deterministic, filesystem-safe path. This keeps library directories consistent across uploads, moves, and metadata edits.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Admin | Define one default pattern for the whole instance | New uploads are organized consistently |
| 2 | Admin | Override the default pattern per library | Different libraries (comics vs novels) can use different layouts |
| 3 | Admin | Preview a pattern before saving | I can verify the result against sample metadata |
| 4 | Admin | Include optional segments that disappear when metadata is missing | I don't get "Unknown/…" folders polluting the tree |
| 5 | Admin | Transform values (upper/lower/initial/sort/first) | I can produce author-letter folders and normalized filenames |
| 6 | Admin | Have the system reorganize files after metadata updates | My library stays tidy without manual intervention |

---

## 3. Configuration Model

### 3.1 Hierarchy

```
┌─────────────────────────────────────────┐
│  Library.FileNamingPattern (*string)    │  ← per-library override
└────────────────┬────────────────────────┘
                 │ if nil, fall through to
                 ▼
┌─────────────────────────────────────────┐
│  AppSettingKey.UploadFilePattern        │  ← system default
└────────────────┬────────────────────────┘
                 │ if nil/blank, fall through to
                 ▼
┌─────────────────────────────────────────┐
│  {currentFilename}                      │  ← hard-coded fallback
└─────────────────────────────────────────┘
```

### 3.2 System default

```
{authors}/<{series}/><{seriesIndex}. >/{title}/{title}< - {authors}>< ({year})>
```

Produces, for a book with full metadata:
```
Patrick Rothfuss/The Kingkiller Chronicle/01. The Name of the Wind/The Name of the Wind - Patrick Rothfuss (2007).epub
```

And for a standalone book missing series / year:
```
Andy Weir/Project Hail Mary/Project Hail Mary - Andy Weir.epub
```

### 3.3 Storage

| Setting | Location | Column / Key | Type |
|---------|----------|--------------|------|
| System default | `app_settings` table | key `upload_file_pattern` | `JSONB` value |
| Library override | `library` table | `file_naming_pattern` | `VARCHAR(1000)`, nullable |

Migration: `0043_add_file_naming_pattern_column_to_library.up.sql`.

---

## 4. Pattern Grammar

### 4.1 Placeholders

| Placeholder | Source | Notes |
|-------------|--------|-------|
| `{title}` | `BookMetadata.Title` | Falls back to current filename if blank |
| `{subtitle}` | `BookMetadata.Subtitle` | |
| `{authors}` | `BookMetadata.Authors` joined with `, ` | Truncated at 180 UTF-8 bytes, appends ` et al.` if needed |
| `{year}` | `BookMetadata.PublishedDate.Year()` | 4-digit integer |
| `{series}` | `BookMetadata.SeriesName` | |
| `{seriesIndex}` | `BookMetadata.SeriesNumber` | Whole numbers zero-padded to 2 digits (`01`), decimals preserved (`02.5`) |
| `{language}` | `BookMetadata.Language` | |
| `{publisher}` | `BookMetadata.Publisher` | |
| `{isbn}` | `BookMetadata.ISBN13` or `ISBN10` | ISBN-13 preferred |
| `{currentFilename}` | `BookFileModel.FileName` | Original on-disk filename |
| `{extension}` | Derived | File extension without the leading dot |

### 4.2 Optional blocks — `< … >`

A segment wrapped in angle brackets is emitted only when **all** placeholders inside it resolve to non-empty values. Literal text (e.g. separators, parentheses) disappears with the block.

```
{title}< - {authors}>          → "The Hobbit - Tolkien" or "The Hobbit"
{title}< ({year})>              → "Dune (1965)" or "Dune"
```

### 4.3 Else clause — `< primary | fallback >`

Inside an optional block, a `|` provides a fallback used when any placeholder in the primary half is missing.

```
<{series}/{seriesIndex}|Standalone>/{title}
  with series    → "Dune/01/Dune.epub"
  without series → "Standalone/Dune.epub"
```

### 4.4 Modifiers — `{placeholder:modifier}`

| Modifier | Behavior |
|----------|----------|
| `:first` | First comma-separated item (e.g. first author) |
| `:sort` | For authors, reformat as `Last, First` |
| `:initial` | For authors, first letter of last name (uppercase); otherwise first letter of value |
| `:upper` | Uppercase (`strings.ToUpper`) |
| `:lower` | Lowercase (`strings.ToLower`) |

Example — "author letter" organization:
```
{authors:initial}/{authors:sort}/{title}
  → "R/Rothfuss, Patrick/The Name of the Wind.epub"
```

### 4.5 Extension handling

- If the pattern contains neither `{extension}` nor `{currentFilename}`, the original extension is appended automatically via `filepath.Ext`.
- For folder-based items (CBZ/CBR and similar), no extension is appended.
- If the final filename exceeds 245 UTF-8 bytes, the name portion is truncated on a UTF-8 boundary (`utf8.RuneStart`) while preserving the extension.

---

## 5. Application Points

Patterns are resolved against a book in three flows. All three use the same `PathPatternResolver` engine.

### 5.1 Upload

`FileUploadService.UploadFile(ctx)`  →  `FileMoveHelper.GetFileNamingPattern(library)`  →  `PathPatternResolver.ResolvePattern(metadata, pattern, originalFileName)`  →  file written to `filepath.Join(library.RootPath, resolvedRelativePath)`.

### 5.2 Move between libraries

`FileMoveService.BulkMoveFiles(ctx)` / `MoveSingleFile(ctx)` resolves the **target** library's pattern and moves atomically (`os.Rename`, with a copy+fsync+remove fallback across filesystems) with rollback support. Library monitoring is paused (`LibraryWatchService.Pause(libraryID)`) before the moves and resumed once filesystem events have drained.

### 5.3 Post metadata update

`BookMetadataUpdater.SetBookMetadata(ctx)` calls `FileMoveService.MoveSingleFile(ctx, book)` only when:
1. `MetadataPersistenceSettings.MoveFilesToLibraryPattern` is enabled, **and**
2. The metadata update actually changed fields used by the pattern.

Errors during move are logged (`slog.Error`) but do not fail the metadata update — the outer transaction still commits.

---

## 6. Validation, Sanitization & Safety

### 6.1 Frontend validation

Pattern input regex: `/^[\w\s\-{}\[\]\/().<>.,:'"#|]*$/`. Save is disabled if the pattern contains characters outside this set.

### 6.2 Backend sanitization (per resolved value)

- Strip invalid filesystem characters: `\ / : * ? " < > |` plus ASCII 0–31 control characters.
- Collapse consecutive whitespace to a single space (via a precompiled `*regexp.Regexp`).
- Trim trailing dots on folder names (except for folder-based items where trailing dots are meaningful).

### 6.3 Length limits

All length accounting is done in UTF-8 bytes (`len(s)`), but truncation happens on rune boundaries using `utf8.DecodeLastRuneInString` so the output remains valid UTF-8.

| Scope | Limit | Behavior on overflow |
|-------|-------|----------------------|
| Author list | 180 UTF-8 bytes | Truncate, append ` et al.` |
| Title / series / publisher | 200 UTF-8 bytes | Truncate on UTF-8 boundary |
| Any path component | 245 UTF-8 bytes | Truncate on UTF-8 boundary |
| Final filename + extension | 245 UTF-8 bytes | Truncate name, preserve extension |

### 6.4 Degenerate results

If the resolved path is blank after sanitization, the resolver returns the original filename so no upload/move is lost. Callers treat a returned error as "keep the original name" as well.

---

## 7. API Surface

### 7.1 Endpoints

```
PATCH /api/v1/libraries/{libraryId}/file-naming-pattern
  Body:     { "fileNamingPattern": "<pattern or null>" }
  Response: Library DTO
  Audit:    audit.NamingPatternChanged
```

System-default pattern is read/written via the existing `AppSettings` endpoint under the key `uploadPattern`.

### 7.2 DTOs

```go
type Library struct {
    ID                int64   `json:"id"`
    Name              string  `json:"name"`
    // …
    FileNamingPattern *string `json:"fileNamingPattern,omitempty"` // nil ⇒ use system default
}

type AppSettings struct {
    UploadPattern string `json:"uploadPattern"` // system default
    // …
}

type UpdateFileNamingPatternRequest struct {
    FileNamingPattern *string `json:"fileNamingPattern"`
}
```

---

## 8. Backend Components

| Component | File | Responsibility |
|-----------|------|----------------|
| `PathPatternResolver` | `booklore-api/internal/pattern/resolver.go` | Core resolution engine: placeholder substitution, optional blocks, modifiers, sanitization, truncation |
| `FileMoveHelper` | `booklore-api/internal/file/move_helper.go` | Pattern lookup (`GetFileNamingPattern`) + path generation (`GenerateNewFilePath`) |
| `FileUploadService` | `booklore-api/internal/upload/service.go` | Applies pattern during upload |
| `FileMoveService` | `booklore-api/internal/file/move_service.go` | Bulk and single-file moves (`os.Rename` + cross-FS fallback) |
| `BookMetadataUpdater` | `booklore-api/internal/metadata/updater.go` | Triggers a move after metadata changes when the flag is on |
| `LibraryHandler` / `LibraryService` | `booklore-api/internal/library/handler.go`, `.../library/service.go` | Expose per-library pattern PATCH endpoint with audit logging |
| `FilenamePatternExtractor` | `booklore-api/internal/pattern/extractor.go` | Reverse direction — extract metadata back out of filenames using the same grammar |

The resolver is a stateless struct; placeholder lookup is a `map[string]func(BookMetadata) (string, bool)` populated once at package init. Optional blocks and else clauses are parsed with a hand-written recursive descent parser (no regex) to keep nesting predictable.

---

## 9. Frontend Components

| Component | File | Responsibility |
|-----------|------|----------------|
| `FileNamingPatternComponent` | `booklore-ui/src/app/features/settings/file-naming-pattern/file-naming-pattern.component.ts` | Settings page: default pattern input, per-library overrides, previews, save actions |
| `pattern-resolver.ts` | `booklore-ui/src/app/shared/util/pattern-resolver.ts` | Client-side mirror of `PathPatternResolver` used only for live preview |
| `settings-naming.json` | `booklore-ui/src/i18n/en/settings-naming.json` (+ other locales) | i18n strings for the page |

UI sections:
1. **Default Pattern** — single input, live preview against sample metadata, save button.
2. **Library Overrides** — expandable list of libraries with "Custom" / "Using Default" badges, per-library input, Clear, individual preview, bulk Save.
3. **Reference** — placeholder table, optional-block / else-clause explanations, modifier table.
4. **Examples** — curated patterns grouped by Basic / Conditional / Modifiers, with sample metadata panels (full and partial).

---

## 10. Edge Cases

| Case | Outcome |
|------|---------|
| Pattern is nil / blank | Use original filename |
| Placeholder value is empty outside an optional block | Emit empty string |
| Placeholder value is empty inside `< … >` without `|` | Omit the entire block |
| Placeholder value is empty inside `< primary | fallback >` | Emit fallback |
| Pattern ends with `/` | `{currentFilename}` is appended so the file has a name |
| Unknown placeholder name | Left verbatim in output (not replaced) |
| Non-ASCII characters (e.g. Chinese, Cyrillic) | Preserved; only invalid filesystem characters are stripped. Unicode classes handled via `unicode.Is*` helpers |
| Decimal `seriesIndex` (e.g. 1.5) | Formatted as `01.5` via `strconv.FormatFloat(v, 'f', -1, 64)` with zero-pad prefix |
| Very long author list | Truncated at 180 UTF-8 bytes + ` et al.` |
| Resolved path is only an extension | Fall back to original filename |
| Folder-based item (CBZ/CBR) | No automatic extension append |
| Path contains OS-specific separators on Windows | Resolver always emits `/`; callers apply `filepath.FromSlash` before touching the FS |

---

## 11. Security Considerations

- **Path traversal** — Backslashes and control characters are stripped. `..` cannot appear because `.` followed by `.` would require the literal sequence in a placeholder value, which is sanitized per component. The final resolved path is further checked with `filepath.Rel(libraryRoot, resolved)` to reject any component that escapes the library root.
- **Injection** — Only whitelisted placeholder names are substituted; metadata values are sanitized before substitution; `regexp.QuoteMeta` is used where regex is involved.
- **Source integrity** — Pattern resolution never mutates the source file contents; only destination name/path.
- **Symlink handling** — Moves use `os.Rename` on the resolved target; `os.Lstat` is used to detect symlinks before rename so the resolver never follows a link out of the library root.

---

## 12. Performance Notes

- Patterns are resolved on demand per file; there is no compiled-pattern cache. Resolution is O(n) in the pattern length.
- Sanitization regexes are package-level `*regexp.Regexp` values compiled once at `init` to avoid per-call compilation cost.
- File moves use atomic rename with rollback; library monitoring is paused to avoid event storms. Cross-filesystem moves fall back to `io.Copy` + `f.Sync()` + `os.Remove(src)`.
- Author-list truncation iterates authors until the UTF-8 byte budget is met (using `range` over the string to walk runes cheaply), then appends the " et al." marker.

---

## 13. Testing

Standard `testing` package with table-driven tests (`t.Run`) and `testify/require` for assertions.

- **`resolver_test.go`** — 100+ table-driven cases covering nil/blank patterns, optional blocks, else clauses, modifiers, ISBN priority, series-index formatting, author truncation, UTF-8 boundary truncation, invalid characters.
- **`upload_service_test.go`** — pattern application during upload.
- **`move_service_test.go`, `move_service_ordering_test.go`** — pattern application during moves and correct ordering; uses `t.TempDir()` for filesystem isolation.
- **`bookdrop_service_test.go`** — bookdrop ingestion flow.
- **`extractor_test.go`** — reverse extraction.

Fuzz test `FuzzResolvePattern` in `resolver_fuzz_test.go` exercises the parser against random input to ensure it never panics or produces path-traversal output.

---

## 14. Open / Future Work

1. Server-side pattern syntax validation on save (currently only character-class validation on the client).
2. Pre-built pattern templates ("Author letter", "Series-first", "Flat").
3. Bulk retroactive apply to existing books, with a dry-run preview.
4. Pattern conflict detection (two books resolving to the same path).
5. Richer reverse extraction to auto-fill metadata from existing filenames.
6. Optional compiled-pattern cache keyed by pattern string (with a `sync.Map`) if resolution shows up in CPU profiles.

---

## 15. Glossary

- **Placeholder** — `{name}` token replaced by a metadata value.
- **Optional block** — `< … >` segment emitted only when its placeholders resolve.
- **Else clause** — `|` inside an optional block providing a fallback segment.
- **Modifier** — `:first | :sort | :initial | :upper | :lower` applied after the placeholder name.
- **Resolution** — The act of turning a pattern + a book into a concrete relative path.
