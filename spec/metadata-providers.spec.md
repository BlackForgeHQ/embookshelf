# Metadata Providers — Feature Specification

> Fetch, compare, and apply book metadata (title, authors, description, covers, ratings, ISBN, etc.) from multiple external services. BookLore supports nine providers, pluggable via a strategy pattern, with per-provider configuration, per-field locking, and streaming search results.

- **Status:** Shipped
- **Scope:** `booklore-api` (Go) + `booklore-ui` (Angular)
- **Permission required:**
  - Settings: `ADMIN` or `MANAGE_METADATA_CONFIG`
  - Metadata fetch/apply: `canEditMetadata` or `isAdmin`
  - Bulk edit / lock: `canBulkEditMetadata` / `canBulkLockUnlockMetadata` or `isAdmin`
- **Settings location:** Settings → Global Preferences → Metadata Providers

---

## 1. Purpose

Users rarely want to type a book's description or rating by hand. The Metadata Providers feature lets BookLore query one or more external catalogs for a given book and present candidate metadata the user can review, compare, and apply. Each provider has its own fetch strategy (HTML scraping, REST, GraphQL) and its own configuration needs (cookie, API key, region, language). Admins decide which providers are on, in what order they run, which fields they can overwrite, and which individual fields are locked.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Admin | Enable/disable individual providers | I only pay for / scrape the ones I trust |
| 2 | Admin | Enter an API key or cookie per provider | Authenticated providers (Hardcover, Comic Vine, Amazon) actually work |
| 3 | Admin | Pick an Amazon/Audible region and a Google Books language | Results match my catalog's locale |
| 4 | Admin | Define a provider priority chain per metadata field | When two providers disagree, the right one wins |
| 5 | User | Click "Fetch metadata" on a book and see streamed results from every enabled provider | I can compare and pick the best match without waiting for the slowest provider |
| 6 | User | Lock individual fields (title, description, …) on a book | Automatic refreshes don't clobber my hand-edited values |
| 7 | User | Apply one provider's metadata, then merge categories from another | I build up a complete record from several sources |
| 8 | Admin | Choose which provider-specific fields (ASIN, Goodreads rating, Hardcover moods, …) get persisted | The DB stays tidy; unwanted fields never land |
| 9 | Admin | Have new books auto-fetched on bookdrop using the default provider chain | Bulk imports come in already enriched |

---

## 3. Supported Providers

Named-string type `MetadataProvider` ([metadata_provider.go](booklore-api/internal/metadata/enum/metadata_provider.go)):

```go
type MetadataProvider string

const (
    ProviderAmazon       MetadataProvider = "Amazon"
    ProviderGoodReads    MetadataProvider = "GoodReads"
    ProviderGoogle       MetadataProvider = "Google"
    ProviderHardcover    MetadataProvider = "Hardcover"
    ProviderComicvine    MetadataProvider = "Comicvine"
    ProviderDouban       MetadataProvider = "Douban"
    ProviderLubimyczytac MetadataProvider = "Lubimyczytac"
    ProviderRanobedb     MetadataProvider = "Ranobedb"
    ProviderAudible      MetadataProvider = "Audible"
)
```

| Provider | Type | Config | Extra identifiers stored | Notes |
|----------|------|--------|--------------------------|-------|
| **Amazon** | HTML (`goquery`) | `enabled`, `cookie`, `domain` (19 regions) | `asin`, `amazonRating`, `amazonReviewCount` | Cookie recommended to bypass rate limits; detailed fetch by ASIN; reviews supported |
| **GoodReads** | HTML (`goquery`) | `enabled` | `goodreadsId`, `goodreadsRating`, `goodreadsReviewCount` | Uses existing `goodreadsId` if set, else searches |
| **Google** (Books) | REST JSON | `enabled`, `language`, `apiKey` (optional) | `googleId` | Rate limit 1.5 s; up to 20 results |
| **Hardcover** | GraphQL (`genqlient`) | `enabled`, `apiKey` (required) | `hardcoverId`, `hardcoverBookId`, `hardcoverRating`, `hardcoverReviewCount`, moods | Fuzzy author matching (threshold 0.5) |
| **Comicvine** | REST JSON | `enabled`, `apiKey` (required) | `comicvineId`, credits | Volume cache (10 min); rate limit 2 s |
| **Douban** | HTML (`goquery`) | `enabled` | `doubanId`, `doubanRating`, `doubanReviewCount` | Chinese catalogue; data parsed from `window.__DATA__` |
| **Lubimyczytac** | HTML (`goquery`) | `enabled` | `lubimyczytacId`, `lubimyczytacRating` | Polish catalogue; 10 s connection timeout, 3 retries |
| **Ranobedb** | REST JSON | `enabled` | `ranobedbId`, `ranobedbRating` | Light novels; `x/time/rate` limiter 2 req/s |
| **Audible** | HTML (`goquery`) | `enabled`, `domain` (10 regions) | `audibleId`, `audibleRating`, `audibleReviewCount` | Narrators + duration extracted |

All parsers implement the [`BookParser`](booklore-api/internal/metadata/parser/book_parser.go) interface. Amazon / Audible / Goodreads / Comicvine additionally satisfy [`DetailedMetadataProvider`](booklore-api/internal/metadata/parser/detailed_metadata_provider.go).

```go
type BookParser interface {
    Provider() MetadataProvider
    FetchMetadata(ctx context.Context, book *model.Book, req FetchMetadataRequest) ([]BookMetadata, error)
    FetchTopMetadata(ctx context.Context, book *model.Book, req FetchMetadataRequest) ([]BookMetadata, error)
}

type DetailedMetadataProvider interface {
    BookParser
    FetchDetailedMetadata(ctx context.Context, providerItemID string) (*BookMetadata, error)
}
```

---

## 4. Form Fields (Settings UI)

Reference: [metadata-provider-settings.component.ts](booklore-ui/src/app/features/settings/global-preferences/metadata-provider-settings/metadata-provider-settings.component.ts), [metadata-provider-settings.component.html](booklore-ui/src/app/features/settings/global-preferences/metadata-provider-settings/metadata-provider-settings.component.html).

### 4.1 Configurable providers (UI section)

| Provider | Fields |
|----------|--------|
| Amazon | `enabled` toggle, `domain` dropdown (19 TLDs), `cookie` textarea, instructional help text |
| Google Books | `enabled` toggle, `language` dropdown (10 languages), `apiKey` input |
| Hardcover | `enabled` toggle (disabled until token present), `apiKey` input |
| Comic Vine | `enabled` toggle, `apiKey` input |
| Audible | `enabled` toggle, `domain` dropdown (10 TLDs) |

### 4.2 Ready-to-use providers

Simple enable toggles only: Goodreads, Douban, Lubimyczytac, Ranobedb.

### 4.3 Per-field persistence selector

Separate component: [metadata-provider-field-selector.component.ts](booklore-ui/src/app/features/metadata/component/metadata-provider-field-selector/metadata-provider-field-selector.component.ts).

Checkboxes grouped by provider toggle individual extra fields on/off (e.g. `asin`, `amazonRating`, `goodreadsRating`, `hardcoverBookId`, `audibleReviewCount`, `lubimyczytacId`). Disabled fields are dropped from the update payload on apply.

### 4.4 Priority chain

The global "quick match" panel and per-library options expose, for each metadata field, up to four providers (`p1`, `p2`, `p3`, `p4`). The same structure is used by the ISBN lookup endpoint to decide call order.

### 4.5 i18n namespace

`settingsMeta.providers.*` for the main panel; `settingsMeta.fieldSelector.*` for the per-field selector; `settingsMeta.providers.saveSuccess` / `saveError` for toasts.

---

## 5. API Surface

### 5.1 Metadata fetch & apply

Handler: [metadata_handler.go](booklore-api/internal/metadata/handler.go).

```
POST /api/v1/books/{bookId}/metadata/prospective
  Body:     FetchMetadataRequest { providers[], isbn?, title?, author?, asin? }
  Response: text/event-stream  -- one `data: <BookMetadata JSON>\n\n` frame per result
  Auth:     canEditMetadata or isAdmin

PUT  /api/v1/books/{bookId}/metadata
  Body:     MetadataUpdateWrapper
  Query:    mergeCategories, replaceMode=REPLACE_ALL|...
  Response: BookMetadata
  Auth:     canEditMetadata or isAdmin

POST /api/v1/books/metadata/isbn-lookup
  Body:     IsbnLookupRequest { isbn }
  Response: BookMetadata (404 if nothing found)
  Auth:     canManageLibrary or isAdmin

GET  /api/v1/books/metadata/detail/{provider}/{providerItemId}
  Response: BookMetadata
  Auth:     canEditMetadata or isAdmin

PUT  /api/v1/books/bulk-edit-metadata
  Body:     BulkMetadataUpdateRequest
  Auth:     canBulkEditMetadata or isAdmin

PUT  /api/v1/books/metadata/toggle-all-lock
  Body:     ToggleAllLockRequest
  Response: []BookMetadata
  Auth:     canBulkLockUnlockMetadata or isAdmin

PUT  /api/v1/books/metadata/toggle-field-locks
  Body:     ToggleFieldLocksRequest { bookIds[], fieldActions: map[string]"LOCK"|"UNLOCK" }
  Auth:     canEditMetadata or isAdmin

POST /api/v1/books/metadata/recalculate-match-scores        -- admin only
POST /api/v1/books/metadata/manage/consolidate              -- canBulkEditMetadata or isAdmin
POST /api/v1/books/metadata/manage/delete                   -- canBulkEditMetadata or isAdmin
```

Streaming is implemented with Server-Sent Events over `http.Flusher`: each parser runs in its own goroutine and pushes results into a buffered `chan BookMetadata`; the handler loops on the channel and writes a `data:` frame per message. The request `ctx` is propagated into every parser, so a client disconnect (or the client's own cancel) cancels in-flight HTTP calls via `net/http`'s context integration.

### 5.2 Settings

Handler: [app_setting_handler.go](booklore-api/internal/appsettings/handler.go).

```
GET /api/v1/settings                    -- full AppSettings (includes provider configs for authorized viewers)
PUT /api/v1/settings                    -- [{ name: "METADATA_PROVIDER_SETTINGS", value: {...} }]
                                           (admin or MANAGE_METADATA_CONFIG)
```

Relevant `AppSettingKey` string constants ([app_setting_key.go](booklore-api/internal/appsettings/enum/app_setting_key.go)):

| Key | Purpose |
|-----|---------|
| `METADATA_PROVIDER_SETTINGS` | Provider enable flags + per-provider config |
| `METADATA_PROVIDER_SPECIFIC_FIELDS` | Which provider-specific columns are persisted |
| `METADATA_MATCH_WEIGHTS` | Per-field weight for match scoring |
| `METADATA_PERSISTENCE_SETTINGS_V2` | EPUB/CBX/PDF write, sidecar, auto-move behavior |
| `METADATA_PUBLIC_REVIEWS_SETTINGS` | Review fetch sources and limits |
| `QUICK_BOOK_MATCH` | Default field → provider-chain map |
| `LIBRARY_METADATA_REFRESH_OPTIONS` | Per-library override of the chain |
| `METADATA_DOWNLOAD_ON_BOOKDROP` | Auto-fetch on bookdrop ingest |

### 5.3 DTOs

**`MetadataProviderSettings`** ([provider_settings.go](booklore-api/internal/metadata/dto/provider_settings.go)):

```go
type MetadataProviderSettings struct {
    Amazon       Amazon       `json:"amazon"`
    Google       Google       `json:"google"`
    GoodReads    Goodreads    `json:"goodReads"`
    Hardcover    Hardcover    `json:"hardcover"`
    Comicvine    Comicvine    `json:"comicvine"`
    Douban       Douban       `json:"douban"`
    Lubimyczytac Lubimyczytac `json:"lubimyczytac"`
    Ranobedb     Ranobedb     `json:"ranobedb"`
    Audible      Audible      `json:"audible"`
}

type Amazon    struct{ Enabled bool; Cookie, Domain string }
type Google    struct{ Enabled bool; Language, APIKey string }
type Hardcover struct{ Enabled bool; APIKey string }
type Audible   struct{ Enabled bool; Domain string }
// Goodreads, Comicvine (APIKey), Douban, Lubimyczytac, Ranobedb follow the same shape
```

**`FetchMetadataRequest`** — `Providers []MetadataProvider` plus optional `ISBN`, `Title`, `Author`, `ASIN` overrides. Absent fields fall back to the book's current metadata.

**`IsbnLookupRequest`** — `ISBN string`.

**`MetadataUpdateWrapper`** — the selected candidate `BookMetadata` + merge flags.

**`ToggleFieldLocksRequest`** — `{ BookIDs []int64, FieldActions map[string]LockAction }` where `LockAction` is the string type `"LOCK"` / `"UNLOCK"`.

Validation uses `github.com/go-playground/validator/v10`; JSON (de)serialization uses `encoding/json`.

---

## 6. Backend Logic

### 6.1 Parser registry

[metadata_service.go](booklore-api/internal/metadata/service.go) holds `parsers map[MetadataProvider]BookParser` populated at startup. Each parser package exposes a `New<Provider>Parser(deps) *Parser` constructor; the composition root registers them:

```go
func NewMetadataService(deps Deps) *MetadataService {
    s := &MetadataService{parsers: map[MetadataProvider]BookParser{}}
    s.register(amazon.New(deps))
    s.register(goodreads.New(deps))
    s.register(google.New(deps))
    s.register(hardcover.New(deps))
    s.register(comicvine.New(deps))
    s.register(douban.New(deps))
    s.register(lubimyczytac.New(deps))
    s.register(ranobedb.New(deps))
    s.register(audible.New(deps))
    return s
}

func (s *MetadataService) register(p BookParser) { s.parsers[p.Provider()] = p }
```

`getParser(provider)` returns `ErrMetadataSourceNotImplemented` (wrapped with `%w`) when no bean matches.

### 6.2 Streaming fetch

`GetProspectiveMetadataForBookID(ctx, bookID, req)` ([service.go:71](booklore-api/internal/metadata/service.go:71)):

1. Load `BookModel` with `db.WithContext(ctx).First(&book, bookID)`.
2. Create a buffered `chan BookMetadata` (capacity = len(req.Providers) * 8).
3. Use `golang.org/x/sync/errgroup` to fan out one goroutine per provider:
   ```go
   g, gctx := errgroup.WithContext(ctx)
   for _, prov := range req.Providers {
       prov := prov
       g.Go(func() error {
           parser, err := s.getParser(prov)
           if err != nil { return nil } // skip unknown providers
           results, err := parser.FetchMetadata(gctx, &book, req)
           if err != nil { s.log.Warn(...); return nil } // skip, do not cancel siblings
           for _, r := range results { select {
               case out <- r:
               case <-gctx.Done(): return gctx.Err()
           } }
           return nil
       })
   }
   go func() { _ = g.Wait(); close(out) }()
   return out
   ```
4. Handler ranges over the channel, writes each element as an SSE frame, and calls `flusher.Flush()` after each write. Client disconnect cancels `ctx`, which propagates through every outbound HTTP call.

### 6.3 ISBN lookup chain

`LookupByISBN(ctx, req)` ([service.go:92](booklore-api/internal/metadata/service.go:92)):

- Reads `QUICK_BOOK_MATCH.fieldOptions.title.{p1,p2,p3,p4}`, de-duplicates in order.
- Falls back to `[]MetadataProvider{ProviderGoogle}` when the chain is empty.
- Iterates providers; the first non-empty, non-error result wins and is returned.

### 6.4 Detailed fetch

`GetDetailedProviderMetadata(ctx, provider, providerItemID)` ([service.go:138](booklore-api/internal/metadata/service.go:138)) uses a type assertion to promote the parser:

```go
detailed, ok := parser.(DetailedMetadataProvider)
if !ok { return nil, ErrDetailedFetchNotSupported }
return detailed.FetchDetailedMetadata(ctx, providerItemID)
```

Used for Amazon ASIN, Audible ASIN, Goodreads book ID, and Comic Vine issue ID.

### 6.5 Apply & lock semantics

- Candidate metadata is **not** written automatically. The UI applies it via `PUT /api/v1/books/{bookId}/metadata`.
- Each field on `BookMetadataModel` has a sibling `<field>_locked bool` column. `BookMetadataUpdater` skips any field whose lock flag is true.
- `METADATA_PROVIDER_SPECIFIC_FIELDS` gates provider-scoped columns: a disabled field is dropped before persist even if the provider returned it.
- `mergeCategories` query param unions new categories with existing ones instead of replacing; `replaceMode=REPLACE_ALL` overwrites.
- Apply is wrapped in `db.Transaction(func(tx *gorm.DB) error { ... })` so partial failures roll back.

### 6.6 Match scoring

`MetadataMatchService` computes a score per candidate using per-field weights from `METADATA_MATCH_WEIGHTS`. The UI sorts the streamed results by score. `POST /api/v1/books/metadata/recalculate-match-scores` lets admins rescore the whole corpus after changing weights; the rescore runs in a background goroutine tied to the app lifecycle (`ctx` honors shutdown).

### 6.7 Automatic fetch (bookdrop)

When `METADATA_DOWNLOAD_ON_BOOKDROP` is true, the bookdrop ingest path runs the provider chain from `QUICK_BOOK_MATCH` and applies the highest-scoring result automatically (respecting locks and per-field filters). The ingest goroutine passes through the bookdrop's `ctx`, so shutdown cancels outbound HTTP cleanly.

### 6.8 Provider behavior details

| Provider | Search order | Rate limit | Caching | Failure modes |
|----------|--------------|------------|---------|---------------|
| Amazon | ISBN → title+author → title; detailed by ASIN | 500–1500 ms random jitter (`rand.Intn`) between detail fetches | — | Returns `ErrAmazonAntiScraping` on 503/500. UI surfaces the "refresh cookie" message. Filters out box sets and "summary & study guide" entries. |
| GoodReads | Existing `goodreadsId` → search; detailed by book ID | — | — | Fuzzy title/author match using `github.com/lithammer/fuzzysearch/fuzzy`. |
| Google | ISBN → title+author → title (max 20) | 1500 ms min interval via `atomic.Int64 lastRequestNs` + `time.Sleep` gap | — | API key optional; missing key halves quota. |
| Hardcover | ISBN → title+author → title | — | — | Levenshtein distance (`github.com/agnivade/levenshtein`) + fuzzy score, threshold 0.5; per-edition results. |
| Comicvine | Volume search → issue lookup | 2000 ms min interval; `X-RateLimit-Reset` respected | `sync.Map` of volumeID → {cachedAt, data}, TTL 600 s, evicted lazily | Honors reset header; skips fetch until reset. |
| Douban | Keyword search on `/search?keywords=` | — | — | Pulls JSON from `window.__DATA__`; multiple CJK-aware date formats. |
| Lubimyczytac | `/szukaj/ksiazki` → detail page | 10 s dial timeout via `http.Transport` | — | Max 3 retries on `net.Error` / 5xx with exponential backoff. |
| Ranobedb | Title query | `rate.NewLimiter(rate.Every(500*time.Millisecond), 1)` (2 req/s) | — | Image URLs resolved against `https://images.ranobedb.org/`. |
| Audible | ISBN → title+author → title; detailed by ASIN | 1500 ms min interval | — | Extracts narrators, duration, series from subtitle; locale-specific date parsing via `time.Parse`. |

All HTTP clients share a single `*http.Client` built with a pooled `http.Transport`, a 30 s overall timeout, and TLS set to the Go default. Each parser holds its own `*http.Client` only if it needs different timeouts (e.g. Lubimyczytac).

---

## 7. Data Model

### 7.1 `app_settings`

Standard key/value table (`id BIGINT`, `name TEXT UNIQUE`, `val JSONB`). Provider settings live under `name = 'metadata_provider_settings'` as a JSON object; reads are cached in-process (`sync.RWMutex` around a `map[string]any`) and invalidated on write.

### 7.2 `book_metadata` provider columns

Additive migrations build up a per-provider column set:

| Migration | Adds |
|-----------|------|
| `0006_add_amazon_goodreads_rating_columns.up.sql` | `amazon_rating`, `amazon_review_count`, `goodreads_rating`, `goodreads_review_count` + lock flags |
| `0027_add_provider_book_ids_to_book_metadata.up.sql` | `goodreads_id`, `hardcover_id`, `google_id` + lock flags |
| `0080_add_lubimyczytac_provider.up.sql` | `lubimyczytac_id`, `lubimyczytac_rating` + lock flags |
| `0090_add_ranobedb_provider.up.sql` | `ranobedb_id`, `ranobedb_rating` + lock flags |

Other provider IDs (`asin`, `comicvine_id`, `douban_id`, `douban_rating`, `douban_review_count`, `audible_id`, `audible_rating`, `audible_review_count`, `hardcover_book_id`, `hardcover_rating`, `hardcover_review_count`) and their `<col>_locked` siblings arrive in later migrations.

### 7.3 `metadata_fetch_proposal`

Background metadata tasks ([metadata_task_handler.go](booklore-api/internal/metadata/task/handler.go)) store candidate metadata in `metadata_fetch_proposal` for the user to review before applying. Out of scope for this spec's API tables but worth calling out so readers don't confuse proposals with the live `book_metadata` row.

### 7.4 GORM models

- [BookMetadataModel](booklore-api/internal/metadata/model/book_metadata_model.go) — large struct with paired `<Field> string` and `<Field>Locked bool` columns, plus JSONB columns for categories, moods, and credits that use `datatypes.JSON`.
- [AppSettingModel](booklore-api/internal/appsettings/model/app_setting_model.go) — `Name string; Val datatypes.JSON`.
- [MetadataFetchProposalModel](booklore-api/internal/metadata/task/model/proposal_model.go).

---

## 8. Frontend

### 8.1 Provider settings page

[metadata-provider-settings.component.ts](booklore-ui/src/app/features/settings/global-preferences/metadata-provider-settings/metadata-provider-settings.component.ts) reads `appSettings$` and renders:

- A **Configurable Providers** block (Amazon, Google, Hardcover, Comic Vine, Audible) with context-specific inputs.
- A **Ready-to-Use** grid of boolean toggles (Goodreads, Douban, Lubimyczytac, Ranobedb).
- A single **Save** button that serializes the full `MetadataProviderSettings` into a single `PUT /api/v1/settings` payload and toasts the result (`saveSuccess` / `saveError`).

Input behaviors worth noting:

- Clearing the Hardcover token auto-toggles `hardcover.enabled` to false (the provider can't work without it).
- The Amazon cookie field sits next to copy-to-clipboard instructions and a link to the extraction helper.
- Domain/language dropdowns are bound to fixed lists in the component (`amazonDomains`, `googleLanguages`, `audibleDomains`).

### 8.2 Field selector

[metadata-provider-field-selector.component.ts](booklore-ui/src/app/features/metadata/component/metadata-provider-field-selector/metadata-provider-field-selector.component.ts) renders a grouped checkbox list and saves each toggle immediately (no explicit Save button) so admins can prune the set of provider-scoped columns.

### 8.3 Search & apply flow

The per-book metadata fetch dialog (invoked from book details or bulk edit) posts to `/books/{id}/metadata/prospective` and renders a streaming list via the browser's `EventSource` API. Each row shows provider name, match score, cover, title, authors, and a diff preview. Selecting a row and clicking **Apply** hits `PUT /books/{id}/metadata`. **Apply & merge categories** flips the `mergeCategories` query flag.

### 8.4 Services & models

- [app-settings.service.ts](booklore-ui/src/app/shared/service/app-settings.service.ts) — exposes `appSettings$` and `saveSettings()`.
- [app-settings.model.ts](booklore-ui/src/app/shared/model/app-settings.model.ts) — `MetadataProviderSettings`, `MetadataProviderSpecificFields`.
- Metadata fetch / proposal services live under [booklore-ui/src/app/features/metadata](booklore-ui/src/app/features/metadata).

---

## 9. Security & Secrets

- Cookies and API keys are stored **unencrypted** in `app_settings.val` (plain JSON). The only mitigations today are:
  - `METADATA_PROVIDER_SETTINGS` is marked "not public" in `AppSettingKey`, so `/public-settings` strips it.
  - The full settings endpoint requires `ADMIN` / `MANAGE_METADATA_CONFIG`.
- Scraped providers have no abuse-prevention layer beyond each parser's own rate limiter. A misbehaving BookLore instance can earn a temporary block from the target site.
- Amazon's `ErrAmazonAntiScraping` is user-visible — the UI surfaces the "refresh your cookie" message rather than swallowing the failure. Sentinel errors are defined once per package and checked with `errors.Is`.
- The Hardcover and Comic Vine tokens go out in `Authorization` / `api_key` query parameters via the shared `*http.Client`; HTTPS is assumed.
- Context cancellation is load-bearing — client disconnect cancels every outbound request, which prevents a zombie goroutine from hammering a third party after the user walked away.

Open work: application-level encryption of `app_settings.val` when it contains secrets (e.g. `crypto/aes-gcm` with a KEK from env); per-field encryption keys; short-lived credential rotation.

---

## 10. Edge Cases

| Case | Outcome |
|------|---------|
| Provider disabled in settings but listed in `FetchMetadataRequest.Providers` | Parser still resolves (the enable flag is a UI guard, not a hard stop); documented as "opt-out via UI, not enforced server-side." |
| Parser returns an error during streaming fetch | Error is logged via `slog.Warn`; provider is skipped; remaining providers still stream. |
| Amazon returns 503/500 | `ErrAmazonAntiScraping` — UI shows the "update your cookie or pick another source" message. |
| ISBN lookup with no configured chain | Falls back to `[]MetadataProvider{ProviderGoogle}`. |
| Candidate has a field that's locked on the book | That field is dropped during apply (lock flag consulted in the updater). |
| Candidate has a provider-specific field disabled in `METADATA_PROVIDER_SPECIFIC_FIELDS` | Field is dropped during apply. |
| Two providers disagree on the same field | No automatic merge — the user picks one, or applies one, then uses `mergeCategories` / manual edit. |
| `goodreadsId` already on the book | Goodreads skips search and fetches the detail page directly. |
| Comic Vine rate-limit reset in the future | Fetch is short-circuited until the reset time; parser returns an empty slice. |
| Amazon cookie blank on a domain that requires it | Parser still tries; most queries succeed but reviews and detailed fields may be missing. |
| Hardcover token removed | UI disables the toggle automatically; server-side calls fail with 401 which the UI surfaces. |
| Parser returns zero results | Empty stream — UI shows "no match from {provider}". |
| `replaceMode=REPLACE_ALL` on apply | All non-locked fields overwritten, including to null if the candidate is missing them. |
| Bookdrop auto-fetch when provider chain is empty | Falls back to `[]MetadataProvider{ProviderGoogle}`; if that also returns nothing, the book lands with no metadata. |
| Client disconnect mid-stream | `ctx` cancels; parsers return early on the next context check; SSE handler exits without writing an error. |
| Shutdown during an in-flight apply | `db.Transaction` commits or rolls back before the handler returns; the request's `ctx` is a child of the app root context. |

---

## 11. Validation & Authorization Summary

| Layer | Rule |
|-------|------|
| UI | Hardcover toggle blocked until token present. Free-form inputs are persisted as-is; there is no upfront key-format validation. |
| Handler middleware | `RequireMetadataEdit`, `RequireBulkMetadataEdit`, `RequireBulkLockUnlockMetadata`, `RequireLibraryManage`, `RequireAdmin` — checked before the service method runs. |
| Settings | Writing `METADATA_*` keys requires `ADMIN` or `MANAGE_METADATA_CONFIG`. |
| Service | Unknown `MetadataProvider` → wrapped `ErrMetadataSourceNotImplemented`. |
| DB | One row per setting in `app_settings` (unique `name`). |

---

## 12. Configuration Examples

Enable Google + Hardcover + Amazon (Germany, with a cookie):

```json
{
  "amazon":       { "enabled": true,  "cookie": "session-id=...", "domain": "de" },
  "google":       { "enabled": true,  "language": "en", "apiKey": "AIza..." },
  "goodReads":    { "enabled": true },
  "hardcover":    { "enabled": true,  "apiKey": "hc_..." },
  "comicvine":    { "enabled": false, "apiKey": "" },
  "douban":       { "enabled": false },
  "lubimyczytac": { "enabled": false },
  "ranobedb":     { "enabled": false },
  "audible":      { "enabled": true,  "domain": "co.uk" }
}
```

Persist only Amazon ASIN + Goodreads ratings:

```json
{
  "asin": true,
  "amazonRating": true,
  "amazonReviewCount": true,
  "goodreadsId": true,
  "goodreadsRating": true,
  "goodreadsReviewCount": true,
  "hardcoverId": false,
  "hardcoverBookId": false,
  "hardcoverRating": false,
  "hardcoverReviewCount": false,
  "audibleId": false,
  "audibleRating": false,
  "audibleReviewCount": false,
  "comicvineId": false,
  "googleId": false,
  "lubimyczytacId": false,
  "lubimyczytacRating": false,
  "ranobedbId": false,
  "ranobedbRating": false
}
```

Default provider chain for title (used by ISBN lookup and bookdrop):

```json
{
  "fieldOptions": {
    "title": { "p1": "Amazon", "p2": "GoodReads", "p3": "Google", "p4": "Hardcover" }
  }
}
```

---

## 13. Open / Future Work

1. **Encrypt secrets at rest** — `app_settings.val` holds cookies and API keys in cleartext. `crypto/aes-gcm` with an env-sourced KEK is the shortest path.
2. **Server-side enforcement of `enabled` flags** during `prospective` fetches, so the UI toggle is authoritative everywhere.
3. **Automatic field-level merge** across providers (today users merge manually).
4. **Provider health surface** — last success/failure timestamp per provider, exposed in settings so rotten cookies and expired tokens are visible without doing a fetch.
5. **Global rate-limit coordinator** — swap each parser's per-process limiter for a Redis-backed token bucket so horizontally scaled replicas don't collectively tank a provider.
6. **Deprecation signals for scraping parsers** — when Amazon/GoodReads/Douban change their DOM, a dedicated sentinel error + UI banner beats silent "no results".
7. **Explicit per-user provider overrides** (some deployments want different cookies per end user, not a shared session).
8. **Proposal UI parity** — surface `metadata_fetch_proposal` entries so asynchronous background fetches can be reviewed in batch.
9. **Circuit breaker** — wrap each parser's HTTP client with `github.com/sony/gobreaker` so a degraded provider stops blocking the stream entirely.

---

## 14. Key References

- Enum: [metadata_provider.go](booklore-api/internal/metadata/enum/metadata_provider.go)
- Parser interfaces: [book_parser.go](booklore-api/internal/metadata/parser/book_parser.go), [detailed_metadata_provider.go](booklore-api/internal/metadata/parser/detailed_metadata_provider.go)
- Parsers: [amazon/parser.go](booklore-api/internal/metadata/parser/amazon/parser.go), [goodreads/parser.go](booklore-api/internal/metadata/parser/goodreads/parser.go), [google/parser.go](booklore-api/internal/metadata/parser/google/parser.go), [hardcover/parser.go](booklore-api/internal/metadata/parser/hardcover/parser.go), [comicvine/parser.go](booklore-api/internal/metadata/parser/comicvine/parser.go), [douban/parser.go](booklore-api/internal/metadata/parser/douban/parser.go), [lubimyczytac/parser.go](booklore-api/internal/metadata/parser/lubimyczytac/parser.go), [ranobedb/parser.go](booklore-api/internal/metadata/parser/ranobedb/parser.go), [audible/parser.go](booklore-api/internal/metadata/parser/audible/parser.go)
- Service: [service.go](booklore-api/internal/metadata/service.go), [updater.go](booklore-api/internal/metadata/updater.go), [match_service.go](booklore-api/internal/metadata/match_service.go)
- Handlers: [handler.go](booklore-api/internal/metadata/handler.go), [task/handler.go](booklore-api/internal/metadata/task/handler.go), [appsettings/handler.go](booklore-api/internal/appsettings/handler.go)
- DTOs: [provider_settings.go](booklore-api/internal/metadata/dto/provider_settings.go), [refresh_options.go](booklore-api/internal/metadata/dto/refresh_options.go)
- Models: [app_setting_model.go](booklore-api/internal/appsettings/model/app_setting_model.go), [book_metadata_model.go](booklore-api/internal/metadata/model/book_metadata_model.go)
- Settings key constants: [app_setting_key.go](booklore-api/internal/appsettings/enum/app_setting_key.go)
- Third-party libraries: `github.com/PuerkitoBio/goquery` (HTML), `github.com/Khan/genqlient` (GraphQL), `golang.org/x/sync/errgroup`, `golang.org/x/time/rate`, `github.com/agnivade/levenshtein`, `github.com/lithammer/fuzzysearch`, `gorm.io/gorm` + `gorm.io/datatypes`, `github.com/go-playground/validator/v10`, `golang-migrate/migrate`.
- UI settings: [metadata-provider-settings.component.ts](booklore-ui/src/app/features/settings/global-preferences/metadata-provider-settings/metadata-provider-settings.component.ts), [metadata-provider-settings.component.html](booklore-ui/src/app/features/settings/global-preferences/metadata-provider-settings/metadata-provider-settings.component.html)
- UI field selector: [metadata-provider-field-selector.component.ts](booklore-ui/src/app/features/metadata/component/metadata-provider-field-selector/metadata-provider-field-selector.component.ts)
- UI service & model: [app-settings.service.ts](booklore-ui/src/app/shared/service/app-settings.service.ts), [app-settings.model.ts](booklore-ui/src/app/shared/model/app-settings.model.ts)
