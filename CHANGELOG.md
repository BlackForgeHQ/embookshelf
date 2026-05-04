# Changelog

## [0.4.11](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.10...v0.4.11) (2026-05-04)


### Features

* **cover:** add endpoint and service method to remove book covers ([17aba7a](https://github.com/BlackForgeHQ/embookshelf/commit/17aba7a70597090600ad0ace4299d5cc9b06ed5e))
* **pdf-discovery:** XMP, hex strings, ISBN, client-rendered cover ([#96](https://github.com/BlackForgeHQ/embookshelf/issues/96)) ([38c9f38](https://github.com/BlackForgeHQ/embookshelf/commit/38c9f3868c4f775d3380217c76e9ee03000f5a5c))
* **pdf:** implement client-side cover rendering and enhance PDF metadata extraction ([af6005c](https://github.com/BlackForgeHQ/embookshelf/commit/af6005cfd5c6e339fdd25d47ad632522d4e7470c))


### Bug Fixes

* **bookdrop:** sanitize file names during upload to prevent issues with special characters ([bb69417](https://github.com/BlackForgeHQ/embookshelf/commit/bb69417b388a9fbe292be96d53ffcf862396b922))

## [0.4.10](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.9...v0.4.10) (2026-05-03)


### Features

* **bookdrop:** implement admin-only housekeeping operations for BookDrop ([c685fe1](https://github.com/BlackForgeHQ/embookshelf/commit/c685fe1bcabc72938250c9819a85b882229d0101))
* **cover:** add coverVersion for cache-busting on cover URLs ([6342a1f](https://github.com/BlackForgeHQ/embookshelf/commit/6342a1fa868dce5ae9a33a4427fc7a3f306d73c5))


### Documentation

* reorganize sections in PRD for clarity and consistency ([ab2ff73](https://github.com/BlackForgeHQ/embookshelf/commit/ab2ff73fada94ac6d8544334556b5d26819825cc))
* update architecture and context documentation; remove obsolete testing plan ([1610824](https://github.com/BlackForgeHQ/embookshelf/commit/1610824b832726a01169e367ef6b54f09523705f))
* update project overview and architecture details ([8474f5c](https://github.com/BlackForgeHQ/embookshelf/commit/8474f5ca001e5f8a68b5f1575cbd848e169da859))

## [0.4.9](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.8...v0.4.9) (2026-05-03)


### Features

* **auth:** add comprehensive tests for basic authentication and user context handling ([3c35b9a](https://github.com/BlackForgeHQ/embookshelf/commit/3c35b9a274b20c8bb43651dc92160c2ed0c4ddac))

## [0.4.8](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.7...v0.4.8) (2026-05-03)


### Features

* **enrichment:** implement SHA-256 hashing for book cover storage ([ab8e25b](https://github.com/BlackForgeHQ/embookshelf/commit/ab8e25b9b2a140ea469d442414691b6e119de769))
* **orphan-management:** implement pending orphans for S3 folder renames ([#86](https://github.com/BlackForgeHQ/embookshelf/issues/86)) ([3ef55a0](https://github.com/BlackForgeHQ/embookshelf/commit/3ef55a09224445d586f498c99865b4ed6f67e6a2))

## [0.4.7](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.6...v0.4.7) (2026-05-02)


### Features

* **files:** enhance book file delivery options with streaming support ([e439198](https://github.com/BlackForgeHQ/embookshelf/commit/e439198a1c8d507455f3e861f9806ab0117aefb7))

## [0.4.6](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.5...v0.4.6) (2026-05-02)


### Features

* **storage:** reconcile shared-S3 backends from env at boot ([45b82ec](https://github.com/BlackForgeHQ/embookshelf/commit/45b82ecad9d9e26df6e8298cc3b3f209fbccc553))


### Bug Fixes

* **s3:** auto-prepend https:// to scheme-less endpoint ([9734ddb](https://github.com/BlackForgeHQ/embookshelf/commit/9734ddb6e1a4911d98314e126ec59784dfeb06f2))
* **s3:** downgrade missing bucket versioning to warning ([1b5de8b](https://github.com/BlackForgeHQ/embookshelf/commit/1b5de8b03e5427ee9fb20cfd1dd2c54db7684070))

## [0.4.5](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.4...v0.4.5) (2026-05-02)


### Features

* **docker:** add S3 configuration options to production compose file ([4dde55e](https://github.com/BlackForgeHQ/embookshelf/commit/4dde55ee5c20c6be916c5131c081d3cf1b30e827))


### Bug Fixes

* **bookdrop:** improve error handling for missing placer in library approval ([e3f168f](https://github.com/BlackForgeHQ/embookshelf/commit/e3f168f4e6d9ef0eeaef8b2e87910f4ef5ab277b))
* **book:** update user_id parameter in ListMissingCoverHash query ([3edafad](https://github.com/BlackForgeHQ/embookshelf/commit/3edafad7db29f5f243f14f629ce0b4543a7242a8))

## [0.4.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.3...v0.4.4) (2026-05-02)


### Features

* **account:** UI for multi-provider OIDC linking (PR-2) ([187d162](https://github.com/BlackForgeHQ/embookshelf/commit/187d162a6e0b5e4043c84ea7812a52cea284eada))
* **account:** user_identities table, multi-provider OIDC linking (PR-1, backend) ([#78](https://github.com/BlackForgeHQ/embookshelf/issues/78)) ([eb3d722](https://github.com/BlackForgeHQ/embookshelf/commit/eb3d722f96b712ee8099c3ac84f65f91932572d5))

## [0.4.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.2...v0.4.3) (2026-05-02)


### Features

* **release:** add production build target to Makefile ([1ac1105](https://github.com/BlackForgeHQ/embookshelf/commit/1ac1105c8276ff5edd37b33d4a4fe9b9fcf838ad))

## [0.4.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.1...v0.4.2) (2026-05-02)


### Features

* **account:** restructure user settings into dedicated account route ([73feebf](https://github.com/BlackForgeHQ/embookshelf/commit/73feebf10c3025272116c6805ce2c95456c72554))

## [0.4.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.4.0...v0.4.1) (2026-05-02)


### Refactors

* **queue:** simplify ScanImport dependencies by removing unnecessary struct ([ccb885e](https://github.com/BlackForgeHQ/embookshelf/commit/ccb885e))

## [0.4.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.3.1...v0.4.0) (2026-05-02)


### ⚠ BREAKING CHANGES

* libraries.org_mode column removed; new approves and edits write the folder-per-book layout. Existing flat-layout libraries keep working but converge into the new layout lazily on user edits.

### Features

* book-per-folder library layout (ADR-0003 + ADR-0004) ([19ab3fb](https://github.com/BlackForgeHQ/embookshelf/commit/19ab3fba97444aa904b0a1bbfcc417ef71391cfd))
* **metadata:** implement MetadataWriter and effects decision logic ([bb48ea5](https://github.com/BlackForgeHQ/embookshelf/commit/bb48ea59a99cbed510d2590cee981de9b1e7ea8e))

## [0.3.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.3.0...v0.3.1) (2026-05-02)


### Features

* **fileproc:** buildIncrementalUpdate appends new /Info + xref + trailer ([a524fe0](https://github.com/BlackForgeHQ/embookshelf/commit/a524fe0d47af7f2e353251ea42b466d79a17bf95))
* **fileproc:** buildInfoBody renders /Info dict; never writes /CreationDate ([bd5b8ea](https://github.com/BlackForgeHQ/embookshelf/commit/bd5b8ea5cb1f7bc91167287b37bf6e9812d5e18f))
* **fileproc:** Embedder interface + DispatchEmbedder (EPUB only stub) ([db6a6a5](https://github.com/BlackForgeHQ/embookshelf/commit/db6a6a554bf909db9951d4ce644f018943e5fef9))
* **fileproc:** emit Calibre-compatible series meta alongside OPF 3 belongs-to-collection ([ad53cfe](https://github.com/BlackForgeHQ/embookshelf/commit/ad53cfee1a002d5e61b163116d02ceada5ca45ed))
* **fileproc:** encodePDFString emits UTF-16BE hex w/ BOM ([6f710a4](https://github.com/BlackForgeHQ/embookshelf/commit/6f710a459343b2017d8ee4a995cf87374576d2c8))
* **fileproc:** EPUBEmbedder.Embed wires mutate + rezip end-to-end ([7533204](https://github.com/BlackForgeHQ/embookshelf/commit/75332042f0ce21fe6c05602d1d20ee4b99930a15))
* **fileproc:** findInfoRef extracts existing /Info reference ([faa99a4](https://github.com/BlackForgeHQ/embookshelf/commit/faa99a4900af672e19133d54cfeacb4c09b03e6b))
* **fileproc:** findStartxref locates trailer offset ([9b34254](https://github.com/BlackForgeHQ/embookshelf/commit/9b34254364b949b80c58d129dbe8d9fd7827810c))
* **fileproc:** mutateOPF writes scalar metadata fields ([f8a989a](https://github.com/BlackForgeHQ/embookshelf/commit/f8a989adbd9537dfe33035d437d7fe3005b653ad))
* **fileproc:** nextObjectNumber returns next free object slot ([985edf1](https://github.com/BlackForgeHQ/embookshelf/commit/985edf148f132ccbafe80020e6bea48a1d8e6523))
* **fileproc:** PDFEmbedder.Embed via incremental update; register in DispatchEmbedder ([efb1590](https://github.com/BlackForgeHQ/embookshelf/commit/efb1590d11c982f40e46e57fc9487582a1826abb))
* **fileproc:** rezipEPUB copies entries verbatim, swaps OPF + cover ([e5daebc](https://github.com/BlackForgeHQ/embookshelf/commit/e5daebc9a6ef4f332ecec99f7dccfe6db687b6d5))
* **fileproc:** Tags/Genres dual write (embookshelf:* + dc:subject) ([1e8bc64](https://github.com/BlackForgeHQ/embookshelf/commit/1e8bc646ac952703cd1196e37f7ce2f66ba70c75))
* **main:** wire BookRepo + MergeLocked into LibraryScanDeps ([432a29f](https://github.com/BlackForgeHQ/embookshelf/commit/432a29f4a72d8b2baa01a84898ba7e033ca2b9bc))
* **main:** wire DataPath into LibraryServiceDeps ([339d352](https://github.com/BlackForgeHQ/embookshelf/commit/339d352fb6ecd74b7d9606437e9c5a6b01fe1298))
* **main:** wire FileRepo into MetadataWriter for hash-stamping ([1658d8e](https://github.com/BlackForgeHQ/embookshelf/commit/1658d8e06758da7adb374ff83dbed0e14e5bc869))
* **main:** wire MetadataWriter; inject into LibraryService + EnrichmentService ([c1c4984](https://github.com/BlackForgeHQ/embookshelf/commit/c1c4984c65a2cf904a5372752b9a88894f380a51))
* **metadata:** implement EditableMetadata struct and related methods ([bbd859d](https://github.com/BlackForgeHQ/embookshelf/commit/bbd859da63eb72d5fd460b8b24024f4a60fc16f9))
* **model:** EditableMetadata + Book.Editable/ApplyEditable helpers ([61dd2fa](https://github.com/BlackForgeHQ/embookshelf/commit/61dd2fac4b3766cc15ee5876f0e245d365d0ec4e))
* **repo:** introduce BookRepo for book management ([028619a](https://github.com/BlackForgeHQ/embookshelf/commit/028619a1385fda17f7c7652bf04939ce50b261e5))
* **service+handler:** ApplyMatch threads Trigger; AutoEnrich passes auto_enrichment ([0d0e691](https://github.com/BlackForgeHQ/embookshelf/commit/0d0e691225b811715b40777839cf943e71093d7e))
* **service:** implement LibraryStore for library management ([adb6f47](https://github.com/BlackForgeHQ/embookshelf/commit/adb6f47442ef11ba578cde0a8272d3a9b399fe8f))
* **service:** introduce Placer interface for bookdrop file placement ([39ebbd5](https://github.com/BlackForgeHQ/embookshelf/commit/39ebbd5efc177d9eeca8ac6a7cc407c8b70c6c41))
* **service:** LibraryHandle.SidecarKey + CanWriteInFile helpers ([311b4be](https://github.com/BlackForgeHQ/embookshelf/commit/311b4bebe79fe940f2bdc15d754f72e1565f62ee))
* **service:** LibraryService.UpdateBookMetadata routes via MetadataWriter when wired ([40e518b](https://github.com/BlackForgeHQ/embookshelf/commit/40e518b160668d1dbdc6ee5b2afcf3ec2e9ac0fa))
* **service:** managed local library folders under DATA_PATH ([c40d6e2](https://github.com/BlackForgeHQ/embookshelf/commit/c40d6e259cd7300e483fe0609e5a56c3c8f268f0))
* **service:** MergeLocked applies per-field lock-aware merge ([249ac05](https://github.com/BlackForgeHQ/embookshelf/commit/249ac0540aefb304b2f2c245c21f65b402062e90))
* **service:** MetadataWriter file embed step + spillover-mode resolution ([0f97ea2](https://github.com/BlackForgeHQ/embookshelf/commit/0f97ea2bbbc0c701f94b4bcb1049882833d2f76a))
* **service:** MetadataWriter hash-stamps files.content_hash after file write ([691f19b](https://github.com/BlackForgeHQ/embookshelf/commit/691f19bcade3c5909f612846052dfa3676d5d638))
* **service:** MetadataWriter sidecar step (manual_edit + apply_enrichment) ([3b33a3a](https://github.com/BlackForgeHQ/embookshelf/commit/3b33a3aaf8eb1215d23d9052b21fa80b665e0afe))
* **service:** MetadataWriter w/ Trigger enum; DB-only step wired ([d4a377a](https://github.com/BlackForgeHQ/embookshelf/commit/d4a377a72be4a81cf9106eb919aa97b447ae4bc2))
* **sidecar:** add JSON envelope encoder + WriteMode constants ([f18cdfd](https://github.com/BlackForgeHQ/embookshelf/commit/f18cdfd9cff75e3d1dee6cacc4225b349dd1bab7))
* **sidecar:** add KeyFor helper for paired sidecar filename ([3f237dc](https://github.com/BlackForgeHQ/embookshelf/commit/3f237dce24449c300e3ea494485110e35174d0a3))
* **sidecar:** hard-cutover TOML support; drop pelletier/go-toml dep ([6c90ca9](https://github.com/BlackForgeHQ/embookshelf/commit/6c90ca9342ba29060fd6eafbb32c7ef97f9c450a))
* **sidecar:** implement JSON sidecar cutover and embedder integration ([414ecb8](https://github.com/BlackForgeHQ/embookshelf/commit/414ecb8db5c8bd5a05d6a4fe4cf5444cb6889438))
* **sidecar:** Read takes bookKey, derives paired JSON sidecar ([1ed85f9](https://github.com/BlackForgeHQ/embookshelf/commit/1ed85f9d0d0474a0a2be2b52ee713df597d9bc0c))
* **sidecar:** switch Sidecar struct tags from toml to json ([7e4934a](https://github.com/BlackForgeHQ/embookshelf/commit/7e4934aa75fe212e02002ca9915bdbf153fc77b4))
* **sidecar:** Writer.Write emits JSON envelope (mode + format args) ([7b937ef](https://github.com/BlackForgeHQ/embookshelf/commit/7b937efdd8f942def47d25bc8598bc262a2221dd))
* **task:** library scan re-extracts + lock-aware merges on file change ([8d33989](https://github.com/BlackForgeHQ/embookshelf/commit/8d3398935c9703d4f0276f7276089f90afadb2cd))
* **ui:** implement BookDrop redesign and enhance settings panels ([47294d7](https://github.com/BlackForgeHQ/embookshelf/commit/47294d72ed00d87c0929c0904b713beb854535cb))
* **ui:** library create form drops path input; shows derived preview ([cb36f7c](https://github.com/BlackForgeHQ/embookshelf/commit/cb36f7cf33f2ea9c44bbb8caa78e0340c522964e))


### Bug Fixes

* **sidecar:** log malformed sidecar payloads instead of silently dropping ([f6913d8](https://github.com/BlackForgeHQ/embookshelf/commit/f6913d84eac254179ff937636b797211ad82f51b))
* **sidecar:** wire writerVersion via debug.BuildInfo; document DecodeJSON contract ([64d5014](https://github.com/BlackForgeHQ/embookshelf/commit/64d5014130bcecdd2e27c13dff7afe4b75e98c45))


### Documentation

* document managed local-library folders (ADR 0002) ([724b0e0](https://github.com/BlackForgeHQ/embookshelf/commit/724b0e0e0aaa5d1590de60447a5b04d5d5f025dd))
* **sidecar:** tighten readAndParse + bookdrop comments; rename test keys to .json ([aa983d2](https://github.com/BlackForgeHQ/embookshelf/commit/aa983d20234be5817d2f35dd05c8ca7d9c4d9afa))
* **spec:** update library-creation for managed-local-folder convention ([f7b222a](https://github.com/BlackForgeHQ/embookshelf/commit/f7b222a3c13ade94980dd00ed590a24704a246c9))

## [0.3.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.8...v0.3.0) (2026-04-30)


### ⚠ BREAKING CHANGES

* drop file-naming-patterns feature ([#72](https://github.com/BlackForgeHQ/embookshelf/issues/72))

### Features

* drop file-naming-patterns feature ([#72](https://github.com/BlackForgeHQ/embookshelf/issues/72)) ([d7936bf](https://github.com/BlackForgeHQ/embookshelf/commit/d7936bffd28f23deea14e1e4281df6a08981c519))

## [0.2.8](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.7...v0.2.8) (2026-04-30)


### Features

* S3 libraries via shared bucket ([#70](https://github.com/BlackForgeHQ/embookshelf/issues/70)) ([5331411](https://github.com/BlackForgeHQ/embookshelf/commit/53314117836cb81b50295e12986c2d83da443d7a))
* **service:** Approve uploads to S3 for s3-backed libraries ([#71](https://github.com/BlackForgeHQ/embookshelf/issues/71)) ([148086c](https://github.com/BlackForgeHQ/embookshelf/commit/148086c0edfabea2cc44e4a99ef6a92a3b6f48cf))


### Bug Fixes

* **storageloader:** resolve relative local backend root via filepath.Abs ([#66](https://github.com/BlackForgeHQ/embookshelf/issues/66)) ([243bc8e](https://github.com/BlackForgeHQ/embookshelf/commit/243bc8e540dc9fb6053ce047b8f2063bdb891c82))
* **storageloader:** root LocalFS at "/" regardless of config.root ([#68](https://github.com/BlackForgeHQ/embookshelf/issues/68)) ([abc26b1](https://github.com/BlackForgeHQ/embookshelf/commit/abc26b19d03e50eeae372ccc741894167c5e0c53))

## [0.2.7](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.6...v0.2.7) (2026-04-30)


### Features

* **coverstore:** hash-keyed cover layout + backfill (Plan E of 8) ([#59](https://github.com/BlackForgeHQ/embookshelf/issues/59)) ([ebb873d](https://github.com/BlackForgeHQ/embookshelf/commit/ebb873dbe5a75c256f5fae6e4d1d22ddfdd9988b))
* **handler:** presigned URL redirect for S3-backed books (Plan G of 8) ([#62](https://github.com/BlackForgeHQ/embookshelf/issues/62)) ([f18dd49](https://github.com/BlackForgeHQ/embookshelf/commit/f18dd49b6ba33193c6d96040ac4474845de0fbbe))
* S3 events + tier tagging (Plan H of 8) ([#63](https://github.com/BlackForgeHQ/embookshelf/issues/63)) ([71dbcca](https://github.com/BlackForgeHQ/embookshelf/commit/71dbccab4588a50401bec6c7c764974f8270cf08))
* **storage:** S3 backend + per-library resolver (Plan F of 8) ([#61](https://github.com/BlackForgeHQ/embookshelf/issues/61)) ([1ee6924](https://github.com/BlackForgeHQ/embookshelf/commit/1ee6924d36dbcd0764298b1bebcd6cda6396a93b))

## [0.2.6](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.5...v0.2.6) (2026-04-29)


### Features

* **sidecar:** metadata.opf + .embookshelf.toml read pipeline (Plan D of 8) ([#57](https://github.com/BlackForgeHQ/embookshelf/issues/57)) ([d6828b3](https://github.com/BlackForgeHQ/embookshelf/commit/d6828b31d0026f65f1d040195b787331e91c5fac))

## [0.2.5](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.4...v0.2.5) (2026-04-29)


### Features

* **scan:** two-phase scan + reconciliation (Plan C of 8) ([#55](https://github.com/BlackForgeHQ/embookshelf/issues/55)) ([364e8d1](https://github.com/BlackForgeHQ/embookshelf/commit/364e8d1156a16e7fa80094587ba5e2aa482f4c1a))

## [0.2.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.3...v0.2.4) (2026-04-29)


### Features

* **db:** storage_v2 schema + content-hash identity (Plan B of 8) ([#54](https://github.com/BlackForgeHQ/embookshelf/issues/54)) ([384270d](https://github.com/BlackForgeHQ/embookshelf/commit/384270debf3ed3f6b1797e68e131b8767e4e87ea))


### Documentation

* **plan:** storage v2 schema + content-hash identity (Plan B of 8) ([#51](https://github.com/BlackForgeHQ/embookshelf/issues/51)) ([5a82d4c](https://github.com/BlackForgeHQ/embookshelf/commit/5a82d4cab35ccb3b0281805e8746f0b4eba1068f))

## [0.2.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.2...v0.2.3) (2026-04-29)


### Features

* **storage:** backend-agnostic Storage interface (Plan A of 8) ([#50](https://github.com/BlackForgeHQ/embookshelf/issues/50)) ([32354ae](https://github.com/BlackForgeHQ/embookshelf/commit/32354ae11ee8451b5bc086b6acea5b81bf6d438f))

## [0.2.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.1...v0.2.2) (2026-04-29)


### Features

* add feature specifications for CI/CD, file naming patterns, library creation, metadata providers, OIDC settings, and S3 storage ([f27f336](https://github.com/BlackForgeHQ/embookshelf/commit/f27f336607157d67264edecd9fbbe98465c45c3f))
* comic (CBZ) and audiobook (MP3/M4B) readers ([#47](https://github.com/BlackForgeHQ/embookshelf/issues/47)) ([6ca0087](https://github.com/BlackForgeHQ/embookshelf/commit/6ca0087c619b1062487a685b80f7a474544ef5e1))

## [0.2.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.2.0...v0.2.1) (2026-04-29)


### Features

* **db:** enhance ScanStringSlice for PostgreSQL text-array literals ([5c9515f](https://github.com/BlackForgeHQ/embookshelf/commit/5c9515f0884dc8076380db82c0b0297b5a028432))

## [0.2.0](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.4...v0.2.0) (2026-04-29)


### ⚠ BREAKING CHANGES

* bare-default Postgres connections are no longer attempted. Existing deployments that already set DATABASE_URL explicitly are unaffected. Operators relying on the implicit postgres://localhost:5432/embookshelf default must now set DATABASE_URL explicitly. See README quickstart and architecture.md for the new defaults.

### Features

* SQLite backend — driver, schema, repos, FTS5 (Plan 2A of 4) ([#40](https://github.com/BlackForgeHQ/embookshelf/issues/40)) ([7256c16](https://github.com/BlackForgeHQ/embookshelf/commit/7256c167649b2c74908e9e89d59727b5d1410bc4))
* SQLite CI lanes + e2e + final docs (Plan 4 of 4) ([#43](https://github.com/BlackForgeHQ/embookshelf/issues/43)) ([0d0bbc8](https://github.com/BlackForgeHQ/embookshelf/commit/0d0bbc830f9a7f7bd3bbd4c90f8a9b2026ae49c2))
* SQLite is the default + test matrix harness (Plan 2B of 4) ([#41](https://github.com/BlackForgeHQ/embookshelf/issues/41)) ([4009de1](https://github.com/BlackForgeHQ/embookshelf/commit/4009de109d7364dba0747dd2d9a71ab8c3f2b2af))
* SQLite queue worker (Plan 3 of 4) ([#42](https://github.com/BlackForgeHQ/embookshelf/issues/42)) ([3f7d74d](https://github.com/BlackForgeHQ/embookshelf/commit/3f7d74d1a79cf9d8e782aabcedfd37059041e36e))
* **ui:** add sidebar toggle button to TopBar and enhance BookDrop layout ([4c3b1e9](https://github.com/BlackForgeHQ/embookshelf/commit/4c3b1e9bb3d78ba8a0c616967634efeab55c26ff))
* **ui:** enhance StarRating component with interactivity and rating mutation ([2235c3d](https://github.com/BlackForgeHQ/embookshelf/commit/2235c3d2d225615f15c2f2278d2263d9372d2b8f))
* **ui:** rethink edit metadata as two dedicated pages ([#44](https://github.com/BlackForgeHQ/embookshelf/issues/44)) ([f305c42](https://github.com/BlackForgeHQ/embookshelf/commit/f305c42a12bd86b9372a79a366a65de34b6ad3da))


### Bug Fixes

* **ci:** scan correct image tag for SBOM generation ([#38](https://github.com/BlackForgeHQ/embookshelf/issues/38)) ([205939f](https://github.com/BlackForgeHQ/embookshelf/commit/205939f4be5e627e9c8fd965fd4f148bc78b859c))

## [0.1.4](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.3...v0.1.4) (2026-04-27)


### Features

* OIDC admin-approval flow ([#37](https://github.com/BlackForgeHQ/embookshelf/issues/37)) ([1f662d2](https://github.com/BlackForgeHQ/embookshelf/commit/1f662d2bd0d1483139f4bcb786d7e665a9aad18c))
* update logo and manifest for embookshelf ([12eefe3](https://github.com/BlackForgeHQ/embookshelf/commit/12eefe3e47599516bcae7f8a641cad3a2726cb96))

## [0.1.3](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.2...v0.1.3) (2026-04-27)


### Features

* command-powered search palette and library combobox ([#34](https://github.com/BlackForgeHQ/embookshelf/issues/34)) ([65db034](https://github.com/BlackForgeHQ/embookshelf/commit/65db0347b36a4f3de6eefdc23ac24557696e947f))

## [0.1.2](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.1...v0.1.2) (2026-04-24)


### Features

* **docs:** add comprehensive guide for Go libraries in book metadata enrichment ([df38e20](https://github.com/BlackForgeHQ/embookshelf/commit/df38e20a36d35805d8a4e43fde757ea603842200))
* **provider:** add per-provider rate limit config to catalog ([ce0d84d](https://github.com/BlackForgeHQ/embookshelf/commit/ce0d84db4bf95e5a980a94de01b1ccbc7d7cf1f9))
* **provider:** add resilient HTTP transport with rate limiting, circuit breaking, and retries ([b57c805](https://github.com/BlackForgeHQ/embookshelf/commit/b57c8057326cd6603734aca786e754836b7b76b0))
* **provider:** add Unicode NFC normalization to match scoring ([4f952b9](https://github.com/BlackForgeHQ/embookshelf/commit/4f952b9a3acdd2146914394a29e5fcc4695e70bd))
* **provider:** resilient enrichment pipeline with per-provider rate limits, circuit breakers, retries, and Unicode scoring ([03a31cb](https://github.com/BlackForgeHQ/embookshelf/commit/03a31cbf443f4f1cf1867810371db4b1672c71e9))
* **provider:** wire per-provider resilient clients through Build() ([f40db75](https://github.com/BlackForgeHQ/embookshelf/commit/f40db75a1ffd00422d57cdb1e7ae7f0306cc643f))


### Bug Fixes

* resolve lint issues (errcheck, gofmt) ([9eae62e](https://github.com/BlackForgeHQ/embookshelf/commit/9eae62e4d21805dd5e8594ad20e07c73d8b267d6))

## [0.1.1](https://github.com/BlackForgeHQ/embookshelf/compare/v0.1.0...v0.1.1) (2026-04-24)


### Bug Fixes

* **ci:** commit internal/staticfs/dist/.gitkeep so embed target exists ([89dca50](https://github.com/BlackForgeHQ/embookshelf/commit/89dca5019e64ccbe8430e80ada2f7a7769846faf))
* **ui:** reconcile lockfile + tsconfig + eslint after dep bumps ([0efa9e9](https://github.com/BlackForgeHQ/embookshelf/commit/0efa9e910cfe9b5aedd312da5235dc0501e39499))

## 0.1.0 (2026-04-24)

Initial public release. Future entries on this file are managed by
[release-please](https://github.com/googleapis/release-please) based on
conventional-commit messages landed on `main`.

### Highlights at 0.1.0

* Self-hosted multi-user digital library — Go backend + React (TanStack
  Start) SPA + Postgres, shipped as a single binary with the SPA embedded
  via `//go:embed`.
* EPUB + PDF readers, full-text search, per-user shelves and annotations.
* BookDrop import queue with polling watcher, metadata enrichment across
  four providers (Google Books, Open Library, Amazon, DuckDuckGo),
  configurable file-naming patterns.
* OIDC / SSO (Google, GitHub, generic) with PKCE and admin-controlled
  provider configuration.
* OPDS 1.2 catalog for e-readers, reMarkable device sync.
* OpenTelemetry export (traces, metrics, logs) via OTLP.
* CI/CD pipeline on GitHub Actions: PR gate, path-gated Playwright e2e,
  multi-arch GHCR image publish with SBOM + SLSA provenance on tag push,
  CodeQL + Trivy + dependency-review security scans, Dependabot, and
  release-please-driven versioning.
