# LibraryHandle stays the interface; the implementation is carved into internal seams

`LibraryHandle` was ~23 methods over four files, whose six caller
clusters (keys, files lookups, byte open, delivery, delete sequencing,
walk) never overlap — no caller outside `internal/service` uses more
than one cluster — and whose four partial-construction axes each carried
its own silently-decided degrade. #346 asked where the carve should go.

## Status

accepted (2026-08-15)

## Decision

The handle **survives as the one interface** callers resolve through
`LibraryStore.For`. The implementation is carved into three internal
seams — private to the package, each with its own file and tests:

- `libraryKeys` (`library_keys.go`) — pure key arithmetic: `root`,
  `localPath`, `storageKey`, the walk-base helpers. No I/O; the
  object-store capability crosses in as a value.
- `bookFiles` (`book_files.go`) — the files-table lookups over the
  narrow lister, with **one stated absence policy**: a nil lister
  answers "no rows" everywhere; a failed lookup keeps its error where
  the caller acts on the difference (`locations`, `primary`) and folds
  only where the answer is advisory (`byID`, `primaryHash`).
- `bookDelivery` (`book_delivery.go`) — the presign/stream/local
  decision over the keys seam and the presign config.

The exported methods delegate. This is the deep-module principle from
the design vocabulary applied literally: a deep module may be internally
composed of small, testable parts — they just aren't part of the
interface.

## Alternatives rejected

**Exposed sub-modules** (`h.Keys()`, `h.Files()`, `h.Delivery()`): every
caller learns a two-hop navigation for no behavioural gain, and ~40 call
sites across five packages churn. The interface-per-caller shrinkage is
real but purchasable later, per cluster, if a cluster ever grows a
second implementation.

**Full dissolution** (For returns per-cluster values): the clusters
interlock through the key arithmetic — delivery, delete and open all
consume it — so the handle *is* the composition point; dissolving it
moves the composition to every caller.

**Moving `PlaceAt`/`DerivedKey`/`PlaceDerived` onto Book operations**
(the issue's free-win list): rejected on contact — the "one consumer"
premise counted only production callers. The trio is pinned by external
tests and documented as vocabulary in CONTEXT.md (Derived artifact
placement); it stays exported.

## Consequences

- The nil-orphan-queue degrade on an object-store byte delete is no
  longer silent: `DeleteBookBytes` answers `ErrNoOrphanQueue` (plus a
  warn), riding the bytes-step warning path — the row delete stays
  authoritative, the leak is on the record. The unresolved-Storage arm
  of the same method got the same treatment (`errNoStorage`): a bytes
  step that touched nothing never again reports bytes cleaned.
- `SidecarKey` is gone (callers use `sidecar.KeyFor`),
  `PrimaryContentHash` is unexported behind `NewPrimaryHash`,
  `DeleteNarrationAndBytes` lives beside `DeleteBookAndBytes`, and
  `DefaultPlacerBuilder` reads the capability bit instead of building a
  throwaway handle.
- Future reviews should not re-suggest exposing or dissolving the
  handle without new evidence — a second adapter for one of the seams,
  or a caller genuinely blocked by the interface's width.
