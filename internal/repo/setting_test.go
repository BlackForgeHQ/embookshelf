// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestSettingsRegistryCoversEveryDeclaredRow is the guard the seed
// registry exists for.
//
// Every settings row that needs a default on first boot used to be
// seeded by a hand-written call in App.Start, and nothing forced that
// call to be written. The failure was silent: an unseeded row reads back
// as its declared default, so the feature works and only the admin UI is
// short a panel — nobody finds out until someone opens Settings.
//
// So the test reads the package's own source: every package-level
// Setting[T] declared anywhere in internal/repo must appear in
// settingsRegistry. Declaring a new domain and forgetting to register it
// fails here, which is the whole point — the same shape as the job
// registry's parity test (#237).
func TestSettingsRegistryCoversEveryDeclaredRow(t *testing.T) {
	t.Parallel()

	declared := declaredSettingVars(t)
	registered := registeredSettingVars(t)

	for _, name := range declared {
		if !registered[name] {
			t.Errorf("%s is a Setting[T] declared in package repo but absent from "+
				"settingsRegistry — its row will never be seeded, and the admin "+
				"settings panel renders nothing for it on a fresh install", name)
		}
	}

	rows := settingsRegistry(nil)
	if len(rows) != len(declared) {
		t.Errorf("settingsRegistry has %d rows, %d Setting[T] values are declared "+
			"— a row is registered twice, or registered without being declared",
			len(rows), len(declared))
	}

	seen := map[string]string{}
	for _, row := range rows {
		if row.key == "" {
			t.Error("a registered row has an empty key")
		}
		if row.seed == nil {
			t.Errorf("row %q is registered without a seed step", row.key)
		}
		if _, dup := seen[row.key]; dup {
			t.Errorf("key %q is registered twice", row.key)
		}
		seen[row.key] = row.key
	}
}

// SeedAll is what boot calls, once, in place of the five hand-written
// calls it used to make. Every registered row must exist afterwards —
// including the rows whose Validate refuses an incomplete config, since
// those refuse only once Enabled is true and the seeded row is disabled.
// A row that fails to seed here is a settings panel with nothing in it.
func TestSeedAllWritesEveryRegisteredRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewAppSettingsRepo(repotest.New(t), nil)

	if err := r.SeedAll(ctx); err != nil {
		t.Fatalf("SeedAll: %v", err)
	}
	for _, row := range settingsRegistry(r) {
		if _, err := r.GetRaw(ctx, row.key); err != nil {
			t.Errorf("row %q missing after SeedAll: %v", row.key, err)
		}
	}
}

// Seeding runs on every boot, so it has to be a no-op on the second one.
// Overwriting an admin's edit with the default would silently unconfigure
// an instance on restart.
func TestSeedAllIsIdempotentAcrossRestarts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewAppSettingsRepo(repotest.New(t), nil)

	if err := r.SeedAll(ctx); err != nil {
		t.Fatalf("first SeedAll: %v", err)
	}
	cfg := DefaultEmailConfig()
	cfg.SMTP.Host = "edited.example.com"
	if err := r.SetEmail(ctx, cfg); err != nil {
		t.Fatalf("SetEmail: %v", err)
	}
	if err := r.SetBool(ctx, SettingOIDCForceOnlyMode, true); err != nil {
		t.Fatalf("SetBool: %v", err)
	}

	if err := r.SeedAll(ctx); err != nil {
		t.Fatalf("second SeedAll: %v", err)
	}

	got, err := r.GetEmail(ctx)
	if err != nil {
		t.Fatalf("GetEmail: %v", err)
	}
	if got.SMTP.Host != "edited.example.com" {
		t.Errorf("seed overwrote an edited row: %+v", got)
	}
	force, err := r.GetBool(ctx, SettingOIDCForceOnlyMode)
	if err != nil {
		t.Fatalf("GetBool: %v", err)
	}
	if !force {
		t.Error("seed reset force-only mode on the second boot")
	}
}

// One row that cannot be seeded must not cost the others theirs — they
// are independent rows, and boot treats the whole thing as non-fatal.
func TestSeedAllSeedsEveryRowEvenWhenOneFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := NewAppSettingsRepo(repotest.New(t), nil)

	boom := errors.New("boom")
	err := seedRows(ctx, []settingSeeder{
		{key: "FIRST", seed: func(context.Context) error { return boom }},
		{key: SettingEmail, seed: seedRow(r, emailSetting).seed},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("joined error = %v, want the row failure", err)
	}
	if _, err := r.GetRaw(ctx, SettingEmail); err != nil {
		t.Errorf("EMAIL not seeded after an earlier row failed: %v", err)
	}
}

// declaredSettingVars parses every non-test file in this package and
// returns the names of the package-level variables holding a Setting[T]
// — whether written as a composite literal or built by a helper that
// returns one (presetSetting).
func declaredSettingVars(t *testing.T) []string {
	t.Helper()

	files := parsePackage(t)

	// Helpers that hand back a Setting[T]: a var assigned from one of
	// these is a declaration too, even though its source never names the
	// type.
	builders := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Results == nil {
				continue
			}
			if len(fn.Type.Results.List) == 1 && isSettingType(fn.Type.Results.List[0].Type) {
				builders[fn.Name.Name] = true
			}
		}
	}

	var names []string
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range vs.Names {
					if isSettingDecl(vs, i, builders) {
						names = append(names, name.Name)
					}
				}
			}
		}
	}

	if len(names) == 0 {
		t.Fatal("parsed no Setting[T] declarations from package repo — the shape " +
			"this test keys on has moved, so it was about to report success on nothing")
	}
	return names
}

// isSettingDecl reports whether the i-th name of a var spec is a
// Setting[T]: either the spec is typed as one, or its value is a
// Setting[T] literal or a call to a builder that returns one.
func isSettingDecl(vs *ast.ValueSpec, i int, builders map[string]bool) bool {
	if vs.Type != nil {
		return isSettingType(vs.Type)
	}
	if i >= len(vs.Values) {
		return false
	}
	switch v := vs.Values[i].(type) {
	case *ast.CompositeLit:
		return isSettingType(v.Type)
	case *ast.CallExpr:
		id, ok := v.Fun.(*ast.Ident)
		return ok && builders[id.Name]
	}
	return false
}

// isSettingType reports whether an expression names Setting[...].
func isSettingType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.IndexExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && id.Name == "Setting"
	case *ast.IndexListExpr:
		id, ok := t.X.(*ast.Ident)
		return ok && id.Name == "Setting"
	}
	return false
}

// registeredSettingVars returns the variable names settingsRegistry
// passes to seedRow. Read from source rather than from the returned
// slice because the slice carries keys, and it is the declaration a new
// domain forgets — the key it forgets to register is by definition not
// in there to compare against.
func registeredSettingVars(t *testing.T) map[string]bool {
	t.Helper()

	var body *ast.BlockStmt
	for _, f := range parsePackage(t) {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name.Name == "settingsRegistry" {
				body = fn.Body
			}
		}
	}
	if body == nil {
		t.Fatal("no settingsRegistry function in package repo")
	}

	names := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "seedRow" {
			return true
		}
		for _, arg := range call.Args {
			if id, ok := arg.(*ast.Ident); ok {
				names[id.Name] = true
			}
		}
		return true
	})
	if len(names) == 0 {
		t.Fatal("settingsRegistry names no settings — this test was about to " +
			"report success on nothing")
	}
	return names
}

// parsePackage parses this package's non-test sources.
func parsePackage(t *testing.T) []*ast.File {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("parsed no source files from the package directory")
	}
	return files
}
