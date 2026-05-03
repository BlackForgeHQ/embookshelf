# Go Libraries for Book Metadata Enrichment: A Production-Ready Guide

> **Reference doc.** Landscape survey informing embookshelf's adapter choices in `internal/provider/`. Cited by ADR-0008. Not authoritative for what's actually wired up — see `docs/CONTEXT.md` (Metadata enrichment section) and the ADRs for the live architecture.

This report surveys Go (Golang) libraries and SDKs for building a personal library catalog that enriches records from multiple book-metadata sources. It covers ISBN utilities, Open Library, Google Books, WorldCat/OCLC, Wikidata SPARQL, MARC/SRU, Amazon PA-API 5.0, Hardcover.app GraphQL, ISBNdb, cover-image tooling, supporting infrastructure (rate limits, retries, caches, queues), ebook-file metadata extraction, barcode scanning, and reference architectures in Go. For each library I note the GitHub URL, a rough popularity signal, license where available, coverage, and gaps, and I flag where Go has **no** mature library and you should fall back to `net/http` + `encoding/json`/`encoding/xml`.

> Caveat on star counts and "last commit": GitHub star totals change constantly and the research tool surfaces cached or historical snapshots. Treat numbers below as order-of-magnitude signals as of the 2025–2026 timeframe, not as audited figures. Where the repositories themselves state a status (e.g., "maintenance mode"), I quote that directly.

---

## 1. ISBN validation, parsing, and conversion

| Library | URL | Notes |
|---|---|---|
| **moraes/isbn** | [github.com/moraes/isbn](https://github.com/moraes/isbn/blob/master/isbn.go) | The canonical, tiny, BSD-licensed library for ISBN-10/13 validation, check-digit calculation, and 10↔13 conversion. Single file, ~4 stars, very stable — it's essentially "done" code rather than actively maintained, which is fine for a protocol that itself hasn't changed. [Source](https://github.com/moraes/isbn/blob/master/isbn.go) |
| **abx123/go-isbn** (often called "abibby/isbn" colloquially) | [github.com/abx123/go-isbn](https://github.com/abx123/go-isbn) | Higher-level: validates ISBNs AND fans out to Google Books / Open Library / Goodreads / ISBNDB in parallel goroutines (3 s timeout per provider), returning the first hit. Closest thing Go has to Python's `isbnlib`. [Source](https://github.com/abx123/go-isbn) |
| **skowalak/isbn** | [pkg.go.dev/github.com/skowalak/isbn](https://pkg.go.dev/github.com/skowalak/isbn) | MIT-licensed structural validator supporting SBN, ISBN-10, ISBN-13; explicitly notes it does **not** validate that an ISBN falls in a registered range. [Source](https://pkg.go.dev/github.com/skowalak/isbn) |
| **thechriswalker/isbn** | [github.com/thechriswalker/isbn](https://github.com/thechriswalker/isbn) | Parses/validates ISBNs including the `urn:isbn:` form. [Source](https://github.com/thechriswalker/isbn) |
| **OldPanda/go-isbn** | [github.com/Panda-Home/go-isbn](https://github.com/Panda-Home/go-isbn) | Validation and ISBN-10 ↔ ISBN-13 conversion. |
| **asaskevich/govalidator** | — | If you already use govalidator you get `IsISBN10`/`IsISBN13`/`IsISBN` for free. [Source](https://www.socketloop.com/tutorials/golang-how-to-validate-isbn) |
| **Jackevansevo/golang-isbnlib** | [github.com/Jackevansevo/golang-isbnlib](https://github.com/Jackevansevo/golang-isbnlib/blob/master/openlibrary.go) | An in-progress port of Python `isbnlib` patterns; small, not widely used. |

**No `abibby/isbn` package could be located in public indexes** — the commonly-cited import path appears to be a misattribution of `abx123/go-isbn`. There is no Go equivalent of Python's `isbnlib` with range-table validation; for production use pair `moraes/isbn` (structure) with the official ISBN International range file parsed yourself.

**Recommendation:** Use `moraes/isbn` for validation and 10↔13, and build your own multi-provider enricher rather than `abx123/go-isbn` — the latter is convenient but opinionated and its Goodreads provider will not work (Goodreads API is discontinued).

---

## 2. Open Library (openlibrary.org)

There is no first-party Go client (Internet Archive publishes a Python client at [internetarchive/openlibrary-client](https://github.com/internetarchive/openlibrary-client), not Go). Community options:

| Library | URL | Coverage |
|---|---|---|
| **Open-pi/gol** | [github.com/Open-pi/gol](https://github.com/Open-pi/gol) | Works API, Editions API, Covers API. Uses `gabs` for JSON because Open Library's payloads are loosely typed. [Source](https://pkg.go.dev/github.com/Open-pi/gol?readme=expanded) |
| **alazyreader/go-openlibrary** | [pkg.go.dev/git.yetaga.in/alazyreader/go-openlibrary/client](https://pkg.go.dev/git.yetaga.in/alazyreader/go-openlibrary/client) | Focused wrapper for `/api/books` with `GetByISBN`, `GetByLCCN`, `GetByOCLC`, `GetByOLID`, `GetByRawKey`. Strongly typed structs for Identifiers, Covers, Excerpts. [Source](https://pkg.go.dev/git.yetaga.in/alazyreader/go-openlibrary/client) |
| **~timharek/openlibrary-go** | [sr.ht/~timharek/openlibrary-go](https://sr.ht/~timharek/openlibrary-go/) | Search endpoint; modest coverage. [Source](https://sr.ht/~timharek/openlibrary-go/) |
| **go.growl.space/read/openlibrary** | [pkg.go.dev](https://pkg.go.dev/go.growl.space/read/openlibrary) | Minimal Search wrapper. |

**Gaps:** None of these cover all of Search API + Books API + Works API + Editions API + Covers API with fully-typed models. Because Open Library JSON is famously deeply nested, inconsistent (sometimes `{"key": "..."}`, sometimes a bare string), and frequently adds new fields, most production users roll their own thin client against `net/http` + `encoding/json` and use `json.RawMessage` / `any` for fields that are genuinely polymorphic.

**Recommendation:** Use `alazyreader/go-openlibrary` for ISBN/OLID lookup, and hand-roll the Search and Works endpoints.

---

## 3. Google Books API

| Library | URL | Notes |
|---|---|---|
| **google.golang.org/api/books/v1** | [pkg.go.dev](https://pkg.go.dev/google.golang.org/api/books/v1) | The official, auto-generated Google client. Fully-typed structs for every field; integrates with `option.WithAPIKey` / `oauth2`. Google explicitly marks the entire `google-api-go-client` repo as **"considered complete and in maintenance mode"** — critical bugs and security fixes only. [Source](https://pkg.go.dev/google.golang.org/api/books/v1) [Source](https://github.com/googleapis/google-api-go-client) |
| **eguevara/go-books** | [github.com/eguevara/go-books](https://github.com/eguevara/go-books) | Tiny third-party wrapper; demonstrates OAuth2 handshake. Mostly a learning artifact. |

For pure ISBN lookup (`q=isbn:9780...`) a 20-line `http.Get` against `https://www.googleapis.com/books/v1/volumes` is often simpler than pulling in the ~300 k-SLOC generated client. The official client really shines if you need `mylibrary` (bookshelves), which requires OAuth2. [Source](https://developers.google.com/books/docs/v1/using)

**Recommendation:** Use `google.golang.org/api/books/v1` when you need OAuth2/bookshelves; use a hand-rolled `net/http` client for anonymous volume searches.

---

## 4. WorldCat / OCLC

**There is no mature Go client for the WorldCat Search API v2 or the WorldCat Metadata API.** OCLC explicitly states that any OAuth 2 client library can be used to obtain tokens, because they conform to the standard — they provide no Go SDK. [Source](https://www.oclc.org/developer/support/faq.en.html)

Practical Go path:

- **Auth:** use `golang.org/x/oauth2/clientcredentials` for Client Credentials flow (server-to-server, the most common pattern for metadata enrichment) or the generic `oauth2` package for Authorization Code / PKCE against `https://oauth.oclc.org/token`. [Source](https://www.oclc.org/developer/api/keys/oauth/explicit-authorization-code.en.html)
- **Transport:** wrap with `oauth2.NewClient(ctx, tokenSource)` which produces an `*http.Client` that injects bearer tokens automatically.
- **Payloads:** WorldCat Search API v2 returns JSON (unlike v1, whose retirement date was 31 December 2024 [Source](https://www.oclc.org/developer/api/oclc-apis/worldcat-search-api.en.html)). You write your own typed structs.

Note v2 uses OAuth2 and REST-style JSON, making it far easier from Go than the v1 WSKey HMAC signing scheme.

---

## 5. Wikidata SPARQL

| Library | URL | Notes |
|---|---|---|
| **knakk/sparql** | [github.com/knakk/sparql](https://github.com/knakk/sparql) | The de-facto Go SPARQL client. Provides `sparql.Repo` with functional options (`DigestAuth`, `Timeout`), parses `application/sparql-results+json` into typed `rdf.Term` solutions, and ships a query-bank facility for templated queries. Works directly against `https://query.wikidata.org/sparql`. [Source](https://github.com/knakk/sparql) |
| **ross-spencer/spargo** | [github.com/ross-spencer/spargo](https://github.com/ross-spencer/spargo) | Apache 2.0. Simple client; includes a CLI that can run `.sparql` files, with working Wikidata examples. [Source](https://github.com/ross-spencer/spargo) |
| **garsue/sparql** | [pkg.go.dev/github.com/garsue/sparql](https://pkg.go.dev/github.com/garsue/sparql) | Provides a `database/sql`-flavored driver with `$1, $2` placeholders. |
| **Navid2zp/go-wikidata** | [github.com/Navid2zp/go-wikidata](https://github.com/Navid2zp/go-wikidata) | Wraps the Wikibase **action** API (`wbgetentities`, `wbsearchentities`, `wbgetclaims`) — not SPARQL. MIT-licensed. Useful if you need labels/descriptions/claims by QID without SPARQL overhead. [Source](https://pkg.go.dev/github.com/Navid2zp/go-wikidata) |
| **jd3main/gowd** | [github.com/jd3main/gowd](https://github.com/jd3main/gowd) | Combines Wikibase API + WDQS. [Source](https://github.com/jd3main/gowd) |

`shurcooL/sanitized_anchor_name` is not a SPARQL library — it generates HTML anchor IDs from headings; that item appears to have been miscategorized in the task brief.

**Recommendation:** `knakk/sparql` for Wikidata SPARQL, optionally supplemented with `Navid2zp/go-wikidata` for direct entity lookups.

---

## 6. MARC / MARCXML / MODS parsing (for LoC, DNB, BnF SRU)

| Library | URL | Notes |
|---|---|---|
| **miku/marc21** | [github.com/miku/marc21](https://github.com/miku/marc21) | The most widely used pure-Go MARC21 library. Reads binary `.mrc` and writes MARCXML. Idiomatic API (`marc21.ReadRecord(r io.Reader)`), used in real-world ETL pipelines. [Source](https://github.com/miku/marc21) |
| **MITLibraries/fml** | [github.com/MITLibraries/fml](https://github.com/MITLibraries/fml) | MIT Libraries' "Filter MARC Library." Clean iterator API (`NewMarcIterator` / `Next`/`Value`), inspired by Traject, plus a `Filter("245ac")` mini-language that returns subfield values by tag+indicator. Great for quickly extracting, e.g., title+authors from 100/245/650. [Source](https://pkg.go.dev/github.com/mitlibraries/fml) |
| **jasonzou/gomarc21** | [github.com/jasonzou/gomarc21](https://github.com/jasonzou/gomarc21) | Pure Go reader for MARC21 and MARCXML with `recordAsJson`/`recordAsXml`. [Source](https://pkg.go.dev/github.com/jasonzou/gomarc21) |
| **aaronland/go-marc** | [github.com/aaronland/go-marc](https://github.com/aaronland/go-marc) | Specialized to MARC 034 (geo) — explicitly recommends `miku/marc21` for general work. [Source](https://github.com/aaronland/go-marc) |

**MITLibraries/go-marc** as named does not exist as a current repo; the MIT Libraries package is `MITLibraries/fml`.

**SRU clients:** There is **no actively-maintained Go SRU or Z39.50 client.** Index Data's `yaz4j` is Java-only [Source](https://github.com/indexdata/yaz4j), and there are no Go bindings to libyaz. The standard approach in Go is:

1. Build the SRU URL yourself (it's just HTTP + CQL in the query string — LoC hosts it at `http://lx2.loc.gov:210/LCDB` and via SRU at the endpoints listed at [loc.gov/standards/sru](https://www.loc.gov/standards/sru/resources/listOfServers.html)).
2. `http.Get` with an `Accept: text/xml` header.
3. Decode the response with `encoding/xml` into a `searchRetrieveResponse` struct containing `records.record.recordData`.
4. Feed the embedded MARCXML to `miku/marc21` or `MITLibraries/fml`.

**MODS:** No dedicated Go library. Define XML structs and parse with `encoding/xml`.

---

## 7. Amazon Product Advertising API (PA-API 5.0)

| Library | URL | Notes |
|---|---|---|
| **goark/pa-api** (formerly `spiegel-im-spiegel/pa-api`) | [github.com/goark/pa-api](https://github.com/spiegel-im-spiegel/pa-api) | The actively-maintained Go client. Handles PA-API 5.0 AWS SigV4 signing, marketplace/region selection (`WithMarketplace(paapi5.LocaleJapan)` etc.), typed query builders for `GetItems`, `GetVariations`, `SearchItems`, `GetBrowseNodes`, and typed response decoding via `entity.DecodeResponse`. Requires Go 1.16+. [Source](https://github.com/spiegel-im-spiegel/pa-api) |
| **utekar/gopaapi5** | [utekar.com/amazon-product-advertising-api-5-go-client-library-gopaapi](https://utekar.com/amazon-product-advertising-api-5-go-client-library-gopaapi/) | Alternative PA-API 5.0 client with `api.Resource` enums and context-aware methods. [Source](https://utekar.com/amazon-product-advertising-api-5-go-client-library-gopaapi/) |
| **mattbit/amazonpa** | [github.com/mattbit/amazonpa](https://github.com/mattbit/amazonpa) | Older — targets the legacy (pre-5.0) PA-API. Do not use for new work. |
| **ngs/go-amazon-product-advertising-api** | [github.com/ngs/go-amazon-product-advertising-api](https://github.com/ngs/go-amazon-product-advertising-api) | Also legacy XML-based API. |

**Creators API:** No public Go client; you'd have to sign requests yourself. The Creators API uses the same SigV4 scheme — you can reuse the signing code in `goark/pa-api/client_test.go`, which demonstrates correct canonical-string and signature generation. [Source](https://github.com/goark/pa-api/blob/master/client_test.go)

**Recommendation:** `goark/pa-api` for PA-API 5.0. For Creators, write your own client atop `aws-sdk-go-v2`'s `signer/v4`.

---

## 8. Hardcover.app GraphQL

Hardcover's API is a single GraphQL endpoint (`https://api.hardcover.app/v1/graphql`) with Bearer-token auth, a 60 rpm limit, 30 s query timeout, and yearly-expiring tokens. [Source](https://docs.hardcover.app/api/getting-started/)

Go GraphQL client options:

| Client | URL | Verdict for Hardcover |
|---|---|---|
| **Khan/genqlient** | [github.com/Khan/genqlient](https://github.com/Khan/genqlient) | **Best fit.** Code-generates strongly-typed Go structs and functions from `.graphql` queries + the Hardcover schema, catches schema/field mismatches at compile time, used in production at Khan Academy executing "hundreds of millions" of queries per day. [Source](https://blog.khanacademy.org/genqlient-a-truly-type-safe-go-graphql-client/) |
| **hasura/go-graphql-client** | [github.com/hasura/go-graphql-client](https://github.com/hasura/go-graphql-client) | Reflection-based; struct-defines queries at call site. Good for ad-hoc queries; also supports WebSocket subscriptions (not needed for Hardcover). [Source](https://github.com/hasura/go-graphql-client) |
| **shurcooL/graphql** | github.com/shurcooL/graphql | Minimal parent of hasura's fork; functional but less actively developed. |
| **machinebox/graphql** | github.com/machinebox/graphql | Untyped string-based queries; the weakest choice for a schema-stable API like Hardcover. |

**Recommendation:** Generate types once with `genqlient` against Hardcover's schema. Add a custom HTTP transport that injects `Authorization: Bearer <TOKEN>` and enforces 60 rpm with `golang.org/x/time/rate`.

---

## 9. Inventaire, BookBrainz, Internet Archive, LibraryThing

**No dedicated Go clients of any maturity exist for any of these.** They all expose REST APIs that you consume with `net/http` + `encoding/json`:

- **Inventaire.io** — REST/JSON, well-documented at inventaire.io.
- **BookBrainz** — Web Service returns JSON.
- **Internet Archive** — Closest Go wrappers are [zacwood9/internetarchive](https://github.com/zacwood9/internetarchive) (search only, last commit ~2018), [nektro/go-internetarchive](https://github.com/nektro/go-internetarchive) (CLI for `download`/`metadata`), and [internetarchive/isodos](https://github.com/internetarchive/isodos) (URL-archiving, not book metadata). For book metadata the hand-rolled approach against `https://archive.org/metadata/{identifier}` is standard. [Source](https://help.archive.org/help/api-information/)
- **LibraryThing** — Their APIs are sparsely documented and partially deprecated; no Go client.

---

## 10. ISBNdb

There is no dedicated Go SDK for ISBNdb. `abx123/go-isbn` can use it as one provider (requires `ISBNDB_APIKEY` env var) [Source](https://github.com/abx123/go-isbn), but otherwise you call [api2.isbndb.com](https://isbndb.com/isbndb-api-documentation-v2) directly with `net/http`, passing `Authorization: YOUR_REST_KEY`. Rate limits are 1 rps (default), 3 rps (Premium), 5 rps (Pro). [Source](https://isbndb.com/blog/book-api/)

---

## 11. Cover-image handling

| Library | URL | Notes |
|---|---|---|
| **disintegration/imaging** | [github.com/disintegration/imaging](https://github.com/disintegration/imaging) | Pure-Go (no cgo). Resize (Lanczos/Linear/etc.), crop, blur, rotate, format encode/decode. Simplest to deploy. [Source](https://github.com/disintegration/imaging) |
| **h2non/bimg** | [github.com/h2non/bimg](https://github.com/h2non/bimg) | cgo-binding to **libvips**; ~4–8× faster than ImageMagick/Go native on JPEG. Supports JPEG/PNG/WebP natively, optionally TIFF/PDF/GIF/SVG/AVIF. Best when processing thousands of covers. Requires libvips 8.3+ on the host. [Source](https://github.com/h2non/bimg) |
| **chai2010/webp** | github.com/chai2010/webp | Pure-Go WebP encoder/decoder; complements `imaging`. |
| **davidbyttow/govips** | — | Another libvips wrapper, newer API than bimg. |

**Best practice for covers:**

1. Fetch with a separate rate-limited `http.Client`.
2. Check `Content-Type` and size; reject non-image payloads.
3. Hash the bytes (SHA-256) as the filename to dedupe.
4. Resize to two or three sizes (`S` 180 px wide, `M` 360 px, `L` 720 px).
5. Store original bytes plus WebP-encoded derivatives; serve with long Cache-Control.

---

## 12. Supporting infrastructure

### Rate limiting

- **`golang.org/x/time/rate`** — token-bucket, supports bursts; `Wait(ctx)`, `Allow()`, `Reserve()`. Stdlib-quality; per-source `*rate.Limiter` is the standard pattern. [Source](https://pkg.go.dev/golang.org/x/time/rate)
- **`go.uber.org/ratelimit`** — strict leaky-bucket, evenly spaced requests; simpler API than `x/time/rate`, enforces unwavering regularity (no bursts). [Source](https://github.com/uber-go/ratelimit)
- **`bsm/redislock`** — distributed locks, not a rate limiter per se; use when multiple pods must share an Open Library/Wikidata budget.

### Circuit breakers

- **`sony/gobreaker`** — MIT-licensed, clean state machine (Closed / HalfOpen / Open), `Execute(fn)` wrapper; v2 adds generics (`CircuitBreaker[T]`) and bucketed rolling windows. The default trip condition is 5 consecutive failures. [Source](https://github.com/sony/gobreaker)
- **`afex/hystrix-go`** — Netflix Hystrix port; more features (semaphore/thread pools), but less maintained.

### Retry / backoff

- **`cenkalti/backoff/v4` or `/v5`** — exponential backoff with jitter, `Retry(operation, backoff)`, `RetryNotify`. [Source](https://github.com/cenkalti/backoff)
- **`avast/retry-go`** — functional-options API.
- **`hashicorp/go-retryablehttp`** — a drop-in `*http.Client` that retries on 5xx (except 501) and network errors with exponential backoff; has a `RateLimitLinearJitterBackoff` that respects `Retry-After` on 429/503. Used in Terraform, Vault, Consul. [Source](https://github.com/hashicorp/go-retryablehttp)

### Fuzzy string matching (for title/author deduplication)

- **`lithammer/fuzzysearch`** — `Match`, `RankMatch` (Levenshtein-based), `MatchFold`, `MatchNormalized` (Unicode-normalized), `Find` returning matches from a slice. [Source](https://github.com/lithammer/fuzzysearch)
- **`schollz/closestmatch`** — n-gram "bag-of-words" matching; the author's own benchmarks show it is ~20× faster than Levenshtein and *more accurate for long strings like book titles*, while Levenshtein slightly wins on single-word dictionaries. Supports save/load of pre-computed bags. [Source](https://github.com/schollz/closestmatch)
- **`agnivade/levenshtein`** — classic Levenshtein distance, very fast.
- **`xrash/smetrics`** — Jaro, Jaro-Winkler, Soundex.
- **`sahilm/fuzzy`** — VSCode/Sublime-style filename-oriented fuzzy search. [Source](https://github.com/sahilm/fuzzy)

(`derektata/lorem` is not a fuzzy matcher — it's a Lorem-ipsum generator; it was miscategorized in the brief.)

### Unicode normalization / transliteration

- **`golang.org/x/text/unicode/norm`** — stdlib-quality NFC/NFD/NFKC/NFKD normalization. Essential for stable comparison of titles across providers.
- **`mozillazg/go-unidecode`** — ASCII transliteration of any Unicode (e.g., `北京kožušček` → `Bei Jing kozuscek`). Useful for deduplication keys, not for display. [Source](https://github.com/mozillazg/go-unidecode)
- `rainycape/unidecode` — older fork of the same approach.

### HTTP clients

- **`hashicorp/go-retryablehttp`** (above) — retries + backoff.
- **`imroc/req`** — a request DSL with tracing, retries, and auth helpers.

### Caching

- **`dgraph-io/ristretto`** (v2 supports generics) — fixed-size, contention-free, TinyLFU admission + SampledLFU eviction; best in class for hit-ratio and throughput. Note the author's documented caveat: `Set` calls can be dropped at the buffer or admission stages, and `IgnoreInternalCost` behavior changed in v0.1.0 in a way that broke some users relying on entry-count sizing. [Source](https://github.com/dgraph-io/ristretto) [Source](https://maypok86.github.io/otter/blog/cache-evolution/)
- **`patrickmn/go-cache`** — simple in-memory with TTL; good for \<100 k entries.
- **`hashicorp/golang-lru`** — LRU and 2Q.
- **`redis/go-redis`** (formerly `go-redis/redis`) — for shared caches across replicas.

### Database access

Use whichever style fits your temperament (all viable; benchmarks vary by workload):

- **`sqlc`** — type-safe code generated from `.sql` files; on par with `database/sql` for speed, dominates developer experience when you prefer raw SQL. [Source](https://blog.jetbrains.com/go/2023/04/27/comparing-db-packages/)
- **`ent`** — schema-as-Go-code, excellent for complex graphs; slightly slower than sqlc but uses less RAM.
- **`gorm`** — feature-rich Active Record; ~3–5× slower than stdlib in microbenchmarks but fastest for 1–10-row reads due to caching. [Source](https://dev.to/techschoolguru/generate-crud-golang-code-from-sql-and-compare-db-sql-gorm-sqlx-sqlc-560j)
- **`jmoiron/sqlx`** — thin extension over `database/sql` with `StructScan`.
- **`jackc/pgx`** — the PostgreSQL driver of choice; skip `database/sql` entirely for native perf.

### Background jobs

| Library | URL | Backing | When to pick it |
|---|---|---|---|
| **`hibiken/asynq`** | [github.com/hibiken/asynq](https://github.com/hibiken/asynq) | Redis | Mature; retries, priorities, scheduled tasks, web UI (`asynqmon`), OpenTelemetry. Most popular choice. [Source](https://github.com/hibiken/asynq) |
| **`riverqueue/river`** | [riverqueue.com](https://riverqueue.com/) / [github.com/riverqueue/river](https://github.com/riverqueue/river) | PostgreSQL | Transactional enqueue — your job is committed atomically with your DB row. No Redis required. Best if you're already on Postgres. [Source](https://riverqueue.com/) |
| **`gocraft/work`** | github.com/gocraft/work | Redis | Older Sidekiq-like. Still works but less active. |
| **`RichardKnop/machinery`** | — | RabbitMQ/SQS/Redis | Broker-agnostic Celery-like. |

For a personal catalog, **River** is the most ergonomic choice because your enrichment job and your book row share a single Postgres transaction.

---

## 13. EPUB / PDF / MOBI metadata extraction

### EPUB

| Library | URL | Purpose |
|---|---|---|
| **`taylorskalyo/goreader/epub`** | [github.com/taylorskalyo/goreader](https://github.com/taylorskalyo/goreader) | Reads container.xml → rootfile → OPF; exposes `Rootfiles[0].Title`, spine, manifest. [Source](https://pkg.go.dev/github.com/taylorskalyo/goreader/epub) |
| **`meskio/epubgo`** | [github.com/meskio/epubgo](https://github.com/meskio/epubgo/blob/master/epub.go) | Pure-Go EPUB reader, LGPL. |
| **`kapmahc/epub`** | [github.com/kapmahc/epub](https://github.com/kapmahc/epub) | Parser. |
| **`timsims/pamphlet`** | [pkg.go.dev/github.com/timsims/pamphlet](https://pkg.go.dev/github.com/timsims/pamphlet) | MIT; title/author/TOC/manifest items. Good typed API. |
| **`bmaupin/go-epub`** | [pkg.go.dev/github.com/bmaupin/go-epub](https://pkg.go.dev/github.com/bmaupin/go-epub) | EPUB **generator** (not parser). |
| **`wmentor/epub`** | [pkg.go.dev/github.com/wmentor/epub](https://pkg.go.dev/github.com/wmentor/epub) | Reader with `ToTxt`. |

`barsanuphe/epubgo` and `n7olkachev/go-epub-reader` don't appear as maintained, importable packages; the first is a fork from ~2015.

### PDF

- **`pdfcpu/pdfcpu`** — Apache 2.0, pure Go. CLI has `extract -mode meta book.pdf` and the library exposes `ExtractMetadata(ctx)` returning all embedded metadata dicts. Also extracts images, fonts, XMP. [Source](https://github.com/pdfcpu/pdfcpu) [Source](https://pdfcpu.io/extract/extract_metadata.html)
- **`unidoc/unipdf`** — commercial (requires license key for production); the most feature-complete, including table extraction to CSV. [Source](https://github.com/unidoc/unipdf)
- **`ledongthuc/pdf`** — small MIT-licensed read-only parser.

### MOBI / AZW

- **`leotaku/mobi`** — MIT; KF8-format MOBI/AZW3 **writer** (not a full parser). Builds books from Chapters. [Source](https://github.com/leotaku/mobi)
- **`766b/mobi`** — Reader/Writer for MOBI, with EXTH metadata records (title, author, cover). [Source](https://github.com/766b/mobi)

For robust MOBI/AZW **reading with metadata** Go's ecosystem is thin — the best-maintained tooling is outside Go (Calibre's `mobi-tools`, ToofDerling/MobiMetadata .NET). For ISBN extraction from a MOBI you're realistically either (a) using `766b/mobi`'s EXTH reader, or (b) shelling out to `calibre` / `ebook-meta` for format breadth.

---

## 14. Barcode scanning (for ISBN barcodes)

- **`makiuchi-d/gozxing`** — pure-Go port of Google's ZXing. Decodes and encodes EAN-13 (ISBN), UPC-A, Code128, QR, Data Matrix, PDF417, Aztec. ~650 stars on GitHub at last check; active. The common pattern is `image.Decode` → `gozxing.NewBinaryBitmapFromImage` → `oned.NewMultiFormatOneDReader().Decode(bmp, hints)`. [Source](https://github.com/makiuchi-d/gozxing) Recent versions made `BinaryBitmap` thread-safe, enabling preprocessed-image caches with large speedups (~12×) on repeated scans. [Source](https://github.com/makiuchi-d/gozxing/blob/master/README.md)
- **`liyue201/goqr`** — QR-only decoder.
- **`dsoprea/go-exif`** — EXIF metadata from camera-captured images; useful if you want to pair a scanned cover photo with its barcode result.

---

## 15. Reference architectures (Go-native book/library software)

Go-native self-hosted book catalogs are **rare** — the big names are JVM/Kotlin (Komga, Kavita's .NET backend, Calibre-Web's Python). What exists in Go:

- **`dir2opds`** — lightweight OPDS server that serves a directory of ebook files, no database, written in Go; ideal as a reference for OPDS feed generation. [Source](https://elimbi.com/posts/digital-library-with-zlibrary-syncthing-opds/) [Source](https://wiki.mobileread.com/wiki/OPDS)
- **`taylorskalyo/goreader`** — terminal EPUB reader; not a server, but a complete, clean EPUB-parsing codebase.
- **FLIBGO** — Go OPDS server for FB2 archives. [Source](https://github.com/topics/opds-catalog)
- Miscellaneous "Golang-written OPDS server with browser/interactive features" projects appear in the [github.com/topics/opds-catalog](https://github.com/topics/opds-catalog) tag but are all small.

**None** of Komga, Kavita, Audiobookshelf, Calibre-Web, BookWyrm, or BookLore are written in Go. [Source](https://github.com/gotson/komga) [Source](https://github.com/topics/opds) You are effectively pioneering the Go-native catalog space, which is why you want to design the enrichment pipeline carefully.

---

## Sources with NO mature Go library (use raw `net/http` + `encoding/json`/`xml`)

Consolidated list:

- **WorldCat Search API v2** — OAuth2 via `golang.org/x/oauth2`, hand-rolled REST.
- **Open Library** (except basic Books API) — hand-roll Search, Works, Editions; Covers is a static URL pattern.
- **Inventaire, BookBrainz, LibraryThing** — no Go clients.
- **Internet Archive metadata** — existing wrappers are search-only or stale; hand-roll against `/metadata/{id}`.
- **SRU endpoints (LoC, DNB, BnF)** — no Go SRU client; build the URL + CQL, decode MARCXML with `miku/marc21`.
- **Z39.50** — no pure-Go client; if you must, CGO-bind to `libyaz`.
- **ISBNdb** — hand-roll REST with `Authorization` header.
- **Hardcover.app** — no Go client, but `genqlient` against the introspected schema is the cleanest path.
- **MODS, Dublin Core, ONIX** — schema-driven XML; just write structs.
- **Amazon Creators API** — reuse SigV4 logic from `goark/pa-api`.

---

## Recommended production-quality Go stack

```
┌─────────────────────────────────────────────────────────────────┐
│  HTTP layer                                                     │
│    net/http  →  retryablehttp  →  gobreaker  →  rate.Limiter    │
│                                                   (per-source)  │
│  + oauth2.NewClient for WorldCat / Google / Hardcover           │
├─────────────────────────────────────────────────────────────────┤
│  Source adapters (one file per provider)                        │
│   openlibrary, googlebooks, worldcat, wikidata,                 │
│   hardcover (genqlient), isbndb, amazon_paapi (goark),          │
│   loc_sru, dnb_sru, bnf_sru  (miku/marc21 + encoding/xml)       │
│  Each returns a canonical internal Book struct.                 │
├─────────────────────────────────────────────────────────────────┤
│  Merge & priority layer                                         │
│   - Preferred order per field (title: OL→Google→WorldCat)       │
│   - Title/author matching: lithammer/fuzzysearch + go-unidecode │
│                              + golang.org/x/text/unicode/norm   │
├─────────────────────────────────────────────────────────────────┤
│  Caching                                                        │
│   Ristretto (hot in-process) + Redis (shared TTLs)              │
├─────────────────────────────────────────────────────────────────┤
│  Jobs                                                           │
│   River (Postgres-transactional) OR Asynq (Redis)               │
├─────────────────────────────────────────────────────────────────┤
│  Persistence                                                    │
│   PostgreSQL + sqlc (preferred) or ent                          │
├─────────────────────────────────────────────────────────────────┤
│  Observability                                                  │
│   log/slog with context-propagated request IDs                  │
│   veqryn/slog-context for OpenTelemetry TraceID bridging        │
└─────────────────────────────────────────────────────────────────┘
```

### Sample code patterns

**Per-source HTTP client with rate limit + retry + breaker:**

```go
func newSourceClient(name string, rps float64, burst int) *http.Client {
    retry := retryablehttp.NewClient()
    retry.RetryMax = 4
    retry.RetryWaitMin = 500 * time.Millisecond
    retry.Backoff = retryablehttp.RateLimitLinearJitterBackoff // honors Retry-After

    std := retry.StandardClient()

    cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{
        Name:    name,
        Timeout: 30 * time.Second,
        ReadyToTrip: func(c gobreaker.Counts) bool {
            return c.Requests >= 10 && float64(c.TotalFailures)/float64(c.Requests) >= 0.6
        },
    })

    limiter := rate.NewLimiter(rate.Limit(rps), burst)

    return &http.Client{
        Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
            if err := limiter.Wait(r.Context()); err != nil {
                return nil, err
            }
            return cb.Execute(func() (*http.Response, error) { return std.Do(r) })
        }),
    }
}

type roundTripperFunc func(*http.Request) (*http.Response, error)
func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
```

**Open Library lookup:**

```go
type olBook struct {
    Title      string `json:"title"`
    NumberOfPages int `json:"number_of_pages"`
    Authors    []struct{ Name string `json:"name"` } `json:"authors"`
    Publishers []struct{ Name string `json:"name"` } `json:"publishers"`
    Cover      struct{ Small, Medium, Large string } `json:"cover"`
    Identifiers struct {
        ISBN10, ISBN13, OCLC, LCCN []string
    } `json:"identifiers"`
}

func (c *Client) FetchOpenLibraryISBN(ctx context.Context, isbn string) (*olBook, error) {
    u := fmt.Sprintf("https://openlibrary.org/api/books?bibkeys=ISBN:%s&jscmd=data&format=json", isbn)
    req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
    resp, err := c.http.Do(req)
    if err != nil {
        return nil, fmt.Errorf("openlibrary fetch: %w", err)
    }
    defer resp.Body.Close()
    var m map[string]olBook
    if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
        return nil, fmt.Errorf("openlibrary decode: %w", err)
    }
    b, ok := m["ISBN:"+isbn]
    if !ok { return nil, nil }
    return &b, nil
}
```

**Google Books anonymous search:**

```go
func (c *Client) FetchGoogleBooks(ctx context.Context, isbn string) ([]Volume, error) {
    u := fmt.Sprintf("https://www.googleapis.com/books/v1/volumes?q=isbn:%s&key=%s",
        url.QueryEscape(isbn), c.googleKey)
    req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
    resp, err := c.http.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    var out struct{ Items []Volume `json:"items"` }
    return out.Items, json.NewDecoder(resp.Body).Decode(&out)
}
```

**Wikidata SPARQL with knakk/sparql:**

```go
import "github.com/knakk/sparql"

repo, _ := sparql.NewRepo("https://query.wikidata.org/sparql",
    sparql.Timeout(10*time.Second))

q := `SELECT ?book ?title ?authorLabel WHERE {
    ?book wdt:P212 "%s" .   # P212 = ISBN-13
    OPTIONAL { ?book wdt:P1476 ?title . }
    OPTIONAL { ?book wdt:P50 ?author . }
    SERVICE wikibase:label { bd:serviceParam wikibase:language "en" . }
} LIMIT 5`

res, err := repo.Query(fmt.Sprintf(q, isbn))
// res.Solutions() yields []map[string]rdf.Term
```

**SRU endpoint (Library of Congress, returning MARCXML):**

```go
import (
    "encoding/xml"
    "github.com/miku/marc21"
)

type sruResponse struct {
    XMLName xml.Name `xml:"searchRetrieveResponse"`
    Records struct {
        Record []struct {
            RecordData struct {
                InnerXML []byte `xml:",innerxml"`
            } `xml:"recordData"`
        } `xml:"record"`
    } `xml:"records"`
}

func (c *Client) FetchLoC(ctx context.Context, isbn string) (*marc21.Record, error) {
    // LoC SRU, using CQL: `bath.isbn = "..."`
    u := "http://lx2.loc.gov:210/LCDB?version=1.1&operation=searchRetrieve" +
         "&recordSchema=marcxml&maximumRecords=1&query=" +
         url.QueryEscape(fmt.Sprintf(`bath.isbn="%s"`, isbn))
    req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
    resp, err := c.http.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()

    var sr sruResponse
    if err := xml.NewDecoder(resp.Body).Decode(&sr); err != nil {
        return nil, fmt.Errorf("SRU decode: %w", err)
    }
    if len(sr.Records.Record) == 0 { return nil, nil }

    // Parse embedded MARCXML
    rec, err := marc21.ReadRecord(bytes.NewReader(sr.Records.Record[0].RecordData.InnerXML))
    return rec, err
}
```

### Go-specific gotchas

1. **XML namespaces in SRU/MARCXML.** `encoding/xml` handles default namespaces poorly. Either strip namespaces before decoding (string replace `xmlns="..."` or a custom `xml.TokenReader`) or declare your struct fields with explicit `xml:"http://www.loc.gov/MARC21/slim record"` namespace tags. The `innerxml` trick above is the cleanest.
2. **Open Library's polymorphic fields.** Fields like `works[].type` are sometimes a string, sometimes an object `{"key": "..."}`. Use `json.RawMessage` and custom `UnmarshalJSON` methods for those fields.
3. **`omitempty` and zero values.** A `PageCount int` with `omitempty` will not send 0 — fine for Google Books where 0 means "unknown", problematic for count fields where 0 is meaningful. Use pointer fields (`*int`) to distinguish absent from zero.
4. **Deeply-nested responses.** Don't model the entire Open Library response; use `json.RawMessage` for subtrees you rarely need, and decode on demand.
5. **Unicode and non-Latin scripts.** Titles from DNB (German), BnF (French diacritics), NDL (Japanese) must be NFC-normalized before comparison. Transliterate to ASCII *only* for matching keys — never store the transliteration as the canonical title.
6. **Context propagation.** Every source call must accept a `context.Context` and pass it into `http.NewRequestWithContext`. Add a request ID early:
   ```go
   logger := slog.Default().With("request_id", reqID, "isbn", isbn)
   ctx := logging.NewContext(ctx, logger)
   ```
   `veqryn/slog-context` can hoist attrs from `ctx` into every record automatically. [Source](https://github.com/veqryn/slog-context)
7. **Error wrapping.** Always `fmt.Errorf("openlibrary: %w", err)` so `errors.Is(err, context.Canceled)` works across layers.
8. **`http.Client.Timeout` vs. per-request.** Set a long client-level timeout (60 s) but use `context.WithTimeout(ctx, 5*time.Second)` per call so cancellation actually propagates into the transport.
9. **GZIP response bodies.** `net/http` auto-decompresses only when `Accept-Encoding` is *not* set manually. Don't hand-set it for JSON APIs.
10. **Ristretto `Set` drops.** Treat Ristretto as best-effort; don't rely on immediate read-your-writes without calling `cache.Wait()`. [Source](https://maypok86.github.io/otter/blog/cache-evolution/)

---

## Consolidated comparison table

| Source | Best Go library | Alternative | Maintenance | Verdict |
|---|---|---|---|---|
| ISBN validation/conversion | moraes/isbn | skowalak/isbn, govalidator | "Done" since ~2015 | Use it |
| Open Library | alazyreader/go-openlibrary (Books) | Open-pi/gol (Works/Editions) | Low activity | Supplement with net/http for Search |
| Google Books | google.golang.org/api/books/v1 | hand-rolled | Maintenance mode (Google) | Official is fine; roll your own for simple cases |
| WorldCat v2 | — | `golang.org/x/oauth2` + net/http | — | Hand-roll |
| Wikidata SPARQL | knakk/sparql | ross-spencer/spargo | Stable | knakk/sparql |
| Wikibase action API | Navid2zp/go-wikidata | jd3main/gowd | Low | Either works |
| MARC21 binary | miku/marc21 | MITLibraries/fml, jasonzou/gomarc21 | Stable | miku + fml for Filter DSL |
| SRU | — | net/http + miku/marc21 | — | Hand-roll |
| Z39.50 | — | cgo to libyaz | — | Avoid unless required |
| PA-API 5.0 | goark/pa-api | gopaapi5 | Active | goark/pa-api |
| Hardcover GraphQL | Khan/genqlient (codegen) | hasura/go-graphql-client | Both active | genqlient |
| ISBNdb | — | net/http | — | Hand-roll |
| Internet Archive | — | net/http | stale Go wrappers | Hand-roll |
| Inventaire/BookBrainz/LibraryThing | — | net/http | — | Hand-roll |
| Cover images (pure Go) | disintegration/imaging | chai2010/webp | Stable | Use both |
| Cover images (high perf) | h2non/bimg (libvips) | davidbyttow/govips | Active | bimg if cgo acceptable |
| Rate limit | golang.org/x/time/rate | uber-go/ratelimit | Stdlib | x/time/rate |
| Circuit breaker | sony/gobreaker | afex/hystrix-go | Active v2 | gobreaker |
| Retry | cenkalti/backoff/v5 | avast/retry-go | Active | either |
| Retryable HTTP | hashicorp/go-retryablehttp | — | Hashicorp-maintained | Use it |
| Fuzzy match | lithammer/fuzzysearch | schollz/closestmatch | Both active | Combine: fuzzysearch for short strings, closestmatch for titles |
| Transliteration | mozillazg/go-unidecode | golang.org/x/text/unicode/norm | Stable | Both |
| In-memory cache | dgraph-io/ristretto v2 | patrickmn/go-cache, hashicorp/golang-lru | Ristretto has quirks (see source) | Ristretto for hot path |
| Distributed cache | redis/go-redis | — | Active | Standard |
| ORM/DB | sqlc | ent, gorm, jmoiron/sqlx | All active | sqlc preferred for explicit SQL |
| Job queue | riverqueue/river (Postgres) | hibiken/asynq (Redis) | Both active | River if you're on Postgres |
| EPUB parsing | taylorskalyo/goreader/epub | meskio/epubgo, timsims/pamphlet | Mixed | taylorskalyo |
| EPUB writing | bmaupin/go-epub | — | Stable | — |
| PDF metadata | pdfcpu/pdfcpu | unidoc/unipdf (commercial) | Active | pdfcpu |
| MOBI/AZW | 766b/mobi (rw), leotaku/mobi (w) | — | Niche | 766b for metadata read |
| Barcode | makiuchi-d/gozxing | liyue201/goqr (QR only) | Active | gozxing |
| Structured logging | stdlib `log/slog` | zerolog, zap | Stdlib since 1.21 | slog |

---

## Bottom line

For a personal Go-first library catalog in 2026, a production-quality starter stack is:

```
go.mod excerpt
------------------------------------------------------------------
github.com/moraes/isbn                            // ISBN utils
github.com/knakk/sparql                           // Wikidata
github.com/miku/marc21                            // MARC + SRU
github.com/MITLibraries/fml                       // MARC filter DSL
google.golang.org/api/books/v1                    // Google Books
github.com/goark/pa-api                           // Amazon PA-API 5.0
github.com/Khan/genqlient                         // Hardcover
golang.org/x/oauth2                               // WorldCat, Hardcover
golang.org/x/time/rate                            // rate limit
golang.org/x/text/unicode/norm                    // Unicode NFC
github.com/hashicorp/go-retryablehttp             // HTTP retries
github.com/sony/gobreaker/v2                      // circuit breaker
github.com/cenkalti/backoff/v5                    // backoff for SPARQL/SRU
github.com/lithammer/fuzzysearch                  // fuzzy match
github.com/schollz/closestmatch                   // title matching
github.com/mozillazg/go-unidecode                 // transliteration
github.com/dgraph-io/ristretto/v2                 // in-memory cache
github.com/redis/go-redis/v9                      // shared cache
github.com/riverqueue/river                       // job queue
github.com/disintegration/imaging                 // cover resize (no cgo)
github.com/makiuchi-d/gozxing                     // barcode scanning
github.com/pdfcpu/pdfcpu                          // PDF metadata
github.com/taylorskalyo/goreader                  // EPUB parse
github.com/766b/mobi                              // MOBI metadata
github.com/veqryn/slog-context                    // slog + ctx
log/slog                                          // logging (stdlib)
```

The **biggest gaps in Go's ecosystem** are WorldCat, SRU, Inventaire, BookBrainz, LibraryThing, ISBNdb, and Hardcover — all require hand-rolled HTTP code. Everything else has an adequate library, though you'll frequently find that a thin custom wrapper is simpler than adopting an older abandoned one. Because the underlying protocols (MARC21, SRU, OPDS, ISBN) are all extremely stable, "not updated for three years" does **not** mean "broken"; it usually means "finished."

A well-designed enrichment pipeline in Go looks like: a `Source` interface with one method `Enrich(ctx, isbn) (*Book, error)`, one implementation per provider, all sharing a decorated `*http.Client` with per-source rate limits and circuit breakers, merged by a priority-per-field layer, fronted by Ristretto + Redis caches, and driven by River jobs that fan out concurrently via `errgroup.Group.Go`. With that backbone, adding a new metadata source (say, the German National Library DNB SRU endpoint) is roughly 100 lines of Go: struct definitions, one `Fetch` method, one `Canonicalize` method, and a registration line in your source registry.