# Managed Local Library Folders Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per ADR 0002. For `kind=local` libraries, drop operator-supplied path. Service derives `${cfg.DataPath}/libraries/{slug}/`, creates directory at create-time, points the new `libraries` row at it.

**Architecture:** Modify `LibraryService.Create` signature to drop the `path` arg. `LibraryServiceDeps` gains `DataPath string`. For `kind=local`, the service: slugifies the name → derives `path = filepath.Join(DataPath, "libraries", slug)` → `os.MkdirAll(path, 0o755)` → inserts the library row. mkdir failure surfaces as the error returned to the handler. UI drops the path input, shows the derived path as a read-only preview. Existing libraries (legacy paths) untouched — no migration.

**Tech Stack:** Go 1.25 stdlib + existing project packages. React/TS for UI.

**Companion docs:**
- `docs/adr/0002-managed-local-library-folders.md` — decision rationale.
- `CONTEXT.md` — Library glossary entry updated.
- `docs/spec/library-creation.spec.md` — needs spec update for the new local-kind flow.

**Out of scope:**
- Migration of existing libraries to managed layout. (Documented in ADR as manual.)
- Symlink resolution for operators who want external mounts (use OS-level symlink/bind-mount).
- Editing an existing library's path. (Already not exposed.)

---

## File Structure

| Path | Change |
|---|---|
| `internal/service/library.go` | Modify. `LibraryServiceDeps` gains `DataPath string`. `Create(ctx, name, kind)` — drop `path string` arg. Local branch derives path + `os.MkdirAll`. |
| `internal/service/library_test.go` | Modify. Update `Create` callsites to drop `path` arg. Add test asserting managed local path under `t.TempDir()`. |
| `internal/handler/settings.go` | Modify. `createLibraryReq.Path` removed (or ignored). "path is required" check dropped. Pass-through to `lib.Create(ctx, name, kind)`. |
| `internal/handler/settings_test.go` (if exists) | Modify. POST body shape change. |
| `cmd/embookshelf/main.go` | Modify. Pass `cfg.DataPath` into `LibraryServiceDeps`. |
| `docs/spec/library-creation.spec.md` | Modify. Document managed-local layout. |
| `ui/src/components/settings/LibrariesPanel.tsx` (and `ui/src/api/settings.ts` if `createLibrary` ships `path`) | Modify. Drop path input from form. Show derived `${DATA_PATH}/libraries/{slug}/` as read-only preview computed from name. |

---

## Phase 1 — Service layer

### Task 1: `LibraryService.Create(ctx, name, kind)` derives + mkdirs managed path

**Files:**
- Modify: `internal/service/library.go`
- Modify: `internal/service/library_test.go`

- [ ] **Step 1: Find current `Create` signature**

Run: `grep -n "func.*LibraryService.*Create\b\|LibraryServiceDeps" internal/service/library.go`. Expected to find `Create(ctx context.Context, name string, kind LibraryKind, path string) (model.Library, error)` and `LibraryServiceDeps{ Backends, SharedS3, Resolver, Dialect }`.

- [ ] **Step 2: Edit `LibraryServiceDeps`** in `internal/service/library.go`:

Add field:

```go
type LibraryServiceDeps struct {
	Backends *repo.StorageBackendRepo
	SharedS3 config.SharedS3Config
	Resolver storage.Resolver
	Dialect  config.Dialect
	// DataPath is the root under which managed local-library folders
	// live. Per ADR 0002, kind=local libraries derive their filesystem
	// path as `${DataPath}/libraries/{slug}/`. Required for local
	// library creation; empty DataPath returns ErrDataPathNotConfigured.
	DataPath string
}
```

- [ ] **Step 3: Add new sentinel error** at top of file (near `ErrS3NotConfigured`):

```go
// ErrDataPathNotConfigured is returned when the caller requests
// kind=local but cfg.DataPath is empty in deps.
var ErrDataPathNotConfigured = errors.New("local libraries require DATA_PATH to be set")
```

- [ ] **Step 4: Replace `Create` signature + body**

Drop the `path` parameter. Replace the `case "", LibraryKindLocal:` branch:

```go
// Create inserts a new library. Kind selects the storage flavour:
//
//   - local (or ""): managed. Path is derived as
//     ${cfg.DataPath}/libraries/{slug}/; the directory is created
//     idempotently via os.MkdirAll. Operator does not supply a path.
//   - s3: prefix=libraries/{slug}/ inside the shared bucket.
func (s *LibraryService) Create(ctx context.Context, name string, kind LibraryKind) (model.Library, error) {
	name = strings.TrimSpace(name)
	slug := slugify(name)

	switch kind {
	case "", LibraryKindLocal:
		if s.deps.DataPath == "" {
			return model.Library{}, ErrDataPathNotConfigured
		}
		path := filepath.Join(s.deps.DataPath, "libraries", slug)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return model.Library{}, fmt.Errorf("create library directory: %w", err)
		}
		return s.repo.CreateLibrary(ctx, name, slug, path, nil)

	case LibraryKindS3:
		// existing s3 branch unchanged
		// ...
	}
	// ...
}
```

Add imports: `os`, `path/filepath`. Keep all existing s3 logic intact.

- [ ] **Step 5: Update existing tests** in `internal/service/library_test.go`

Run: `grep -n "lib.Create\|libSvc.Create\|Create(ctx" internal/service/library_test.go`. Each callsite passes 4 args (`ctx, name, kind, path`); drop the `path` arg.

For tests that exercise the local-kind happy path, add a `DataPath` to the deps:

```go
deps := service.LibraryServiceDeps{
	Backends: backendRepo,
	Dialect:  config.Dialect("sqlite"),
	DataPath: t.TempDir(),
}
```

After `Create` succeeds, assert `lib.Path` starts with the temp DataPath + "/libraries/" + slug, and that the directory exists on disk:

```go
want := filepath.Join(deps.DataPath, "libraries", "my-fiction")
if lib.Path != want {
	t.Errorf("Path=%q want %q", lib.Path, want)
}
if _, err := os.Stat(lib.Path); err != nil {
	t.Errorf("library dir not created: %v", err)
}
```

Also add a test asserting `Create` returns `ErrDataPathNotConfigured` when `deps.DataPath` is empty (local kind only).

- [ ] **Step 6: Build + test**

Run: `go test ./internal/service/...`. All pass.

- [ ] **Step 7: Commit**

```
git add internal/service/library.go internal/service/library_test.go
git commit -m "feat(service): managed local library folders under DATA_PATH"
```

---

## Phase 2 — Handler

### Task 2: Drop `path` from `SettingsLibraryCreate`

**Files:**
- Modify: `internal/handler/settings.go`
- Modify: any `internal/handler/settings_test.go` covering library create.

- [ ] **Step 1: Find request struct**

Run: `grep -n "createLibraryReq\b" internal/handler/settings.go`. Locate struct definition (likely near top of file).

- [ ] **Step 2: Edit `createLibraryReq`**

Drop `Path` field. Result:

```go
type createLibraryReq struct {
	Name string `json:"name" binding:"required"`
	Kind string `json:"kind"`
	Scan bool   `json:"scan"`
}
```

If a `Scan` field exists in the existing struct, keep it. Otherwise just trim Path.

- [ ] **Step 3: Edit `SettingsLibraryCreate` handler**

Replace the body section that touched `path`:

```go
func (h *Handler) SettingsLibraryCreate(c *gin.Context) {
	var body createLibraryReq
	if !bindJSON(c, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	kind := service.LibraryKind(strings.TrimSpace(body.Kind))
	if name == "" {
		writeError(c, http.StatusBadRequest, "name is required")
		return
	}

	lib, err := h.lib.Create(c.Request.Context(), name, kind)
	if err != nil {
		switch {
		case errors.Is(err, repo.ErrLibraryNameTaken):
			writeError(c, http.StatusConflict, "a library with that name already exists")
		case errors.Is(err, repo.ErrLibraryPathTaken):
			writeError(c, http.StatusConflict, "that filesystem path is already bound to another library")
		case errors.Is(err, service.ErrS3NotConfigured):
			writeError(c, http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrDataPathNotConfigured):
			writeError(c, http.StatusBadRequest, err.Error())
		default:
			writeServerError(c, "settings library create", err)
		}
		return
	}

	// existing scan + JSON response logic unchanged
	// ...
}
```

Drop the `path is required` validation. Drop the `path :=` line at handler entry.

- [ ] **Step 4: Update handler tests** if any exercise this endpoint with a `path` field in body. Drop the field from request fixtures.

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./internal/handler/...`. All pass.

- [ ] **Step 6: Commit**

```
git add internal/handler/settings.go
git commit -m "refactor(handler): drop path field from POST /api/libraries"
```

(If handler tests touched, add them to the same commit.)

---

## Phase 3 — Wiring

### Task 3: Pass `cfg.DataPath` into `LibraryServiceDeps`

**Files:**
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Find `NewLibraryService` callsite**

Run: `grep -n "NewLibraryService\|LibraryServiceDeps" cmd/embookshelf/main.go`.

- [ ] **Step 2: Edit deps literal**

```go
libSvc := service.NewLibraryService(libRepo, bookRepo, service.LibraryServiceDeps{
	Backends: backendRepo,
	SharedS3: cfg.SharedS3,
	Resolver: storageResolver,
	Dialect:  config.Dialect(string(dbh.Dialect)),
	DataPath: cfg.DataPath,
})
```

- [ ] **Step 3: Build + test**

Run: `go build ./cmd/embookshelf/ && make test`. All pass.

- [ ] **Step 4: Commit**

```
git add cmd/embookshelf/main.go
git commit -m "feat(main): wire DataPath into LibraryServiceDeps"
```

---

## Phase 4 — UI

### Task 4: Drop path input from library-create form; show derived preview

**Files:**
- Modify: `ui/src/components/settings/LibrariesPanel.tsx`
- Modify: `ui/src/api/settings.ts` (drop `path` from `CreateLibraryInput` for kind=local)

- [ ] **Step 1: Find the create-library form**

Run: `grep -n "createLibrary\|path:" ui/src/components/settings/LibrariesPanel.tsx ui/src/api/settings.ts`. Locate the kind=local form path input + `createLibrary({name, kind, path})` shape.

- [ ] **Step 2: Edit `ui/src/api/settings.ts` `CreateLibraryInput` type**

```ts
export type CreateLibraryInput = {
  name: string;
  kind: LibraryKind;  // 'local' | 's3'
  scan?: boolean;
};
```

Drop the `path?: string` field. Drop the `path` from the body the API client posts.

- [ ] **Step 3: Edit `LibrariesPanel.tsx`**

Find the `<input>` for the path field (visible when `kind === 'local'`). Replace with a read-only preview:

```tsx
{kind === 'local' && (
  <p className="t-small italic text-(--color-ink-3)">
    Will be created at{' '}
    <span className="mono">{`${dataPath}/libraries/${slugify(name)}/`}</span>
  </p>
)}
```

`dataPath` comes from `GET /api/config` (already exposed for `s3Available`; extend if not present). If extending the config endpoint is too much, hard-code the prefix `(data path)/libraries/{slug}/` as illustrative. The s3 case already shows a similar `libraries/{slug}/` preview — mirror that style.

`slugify` lives in TS — small helper that mirrors Go's `slugify` (lowercase, replace non-alphanumeric with `-`, trim leading/trailing dashes). Add to `ui/src/lib/utils.ts` if not present:

```ts
export function slugify(s: string): string {
  const lower = s.trim().toLowerCase();
  let dash = true;
  let out = '';
  for (const ch of lower) {
    if ((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
      out += ch;
      dash = false;
    } else if (!dash) {
      out += '-';
      dash = true;
    }
  }
  return out.replace(/^-+|-+$/g, '');
}
```

- [ ] **Step 4: Build + typecheck**

Run: `cd ui && bun run typecheck && bun run lint`. Clean.

- [ ] **Step 5: Commit**

```
git add ui/src/components/settings/LibrariesPanel.tsx ui/src/api/settings.ts ui/src/lib/utils.ts
git commit -m "feat(ui): library create form drops path input; shows derived preview"
```

---

## Phase 5 — Spec update

### Task 5: Update `docs/spec/library-creation.spec.md`

**Files:**
- Modify: `docs/spec/library-creation.spec.md`

- [ ] **Step 1: Read current spec**

Run: `grep -n "path\|kind=local\|managed" docs/spec/library-creation.spec.md`.

- [ ] **Step 2: Edit the kind=local section**

Replace any "operator types path" language with the managed-folder description. Add reference to ADR 0002. Specifically:

- Section describing local libraries: state that path is derived as `${DATA_PATH}/libraries/{slug}/` and the directory is created at create-time.
- Drop any "path is required" or "path validation" rules for local kind.
- Note: existing libraries pre-ADR-0002 keep their explicit paths.

- [ ] **Step 3: Commit**

```
git add docs/spec/library-creation.spec.md
git commit -m "docs(spec): update library-creation for managed-local-folder convention"
```

---

## Phase 6 — Verification

### Task 6: Lint + test + smoke

- [ ] `make test` — every Go package green.
- [ ] `make go-lint` — 0 issues.
- [ ] `go vet ./...` — silent.
- [ ] `cd ui && bun run typecheck && bun run lint && bun run build` — clean.
- [ ] Manual smoke (optional): `make up`, create a local library named "Test"; verify `${DATA_PATH}/libraries/test/` exists on disk; drop an EPUB into it; trigger scan; book lands in DB.

If any blocked, report. No commit.

---

## Self-Review

**Spec coverage:**
- ADR 0002 decisions (managed path derived, mkdir at create, slug as folder, no operator path input) — Tasks 1+2+3.
- UI removal of path input — Task 4.
- Spec doc — Task 5.
- Verification — Task 6.

**Placeholder scan:** no `TBD`, no `add appropriate handling`. Every step has actual code.

**Type consistency:**
- `LibraryServiceDeps.DataPath` — Task 1 defines, Task 3 wires.
- `ErrDataPathNotConfigured` — Task 1 defines, Task 2 maps to HTTP 400.
- `Create(ctx, name, kind)` 3-arg signature — Task 1 changes, Task 2 callsite updated.
- UI `CreateLibraryInput` — Task 4 drops `path`.

**Ordering:**
- Task 1 must land before Task 2 (handler depends on service signature).
- Task 3 (main.go DataPath wiring) can land same commit batch as Task 1 if compiler insists; otherwise after Task 1.
- Task 4 (UI) is independent of Tasks 1-3 once the API drops `path`. Either after or in parallel.

---

## Execution Handoff

User said: write plan + execute. Proceed via subagent-driven dev, batching where safe.
