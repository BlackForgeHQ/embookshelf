// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/blackforge/embookshelf/internal/config"
	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/recovery"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storageloader"
)

// recoverMisplacedCmd repairs installs that ran the broken placer
// dispatch (#265, shipped v0.3.1–v0.6.2):
//
//	embookshelf recover-misplaced [--apply]
//
// Dry run by default. It reports what it would move and changes nothing
// until --apply, because the paths it names live outside any library and
// a tool that writes there unprompted is not one an operator should have
// to trust.
//
// Like import-sqlite, DATABASE_URL names the database, so there is no
// second config mechanism to learn. Unlike import-sqlite it does not run
// migrations: this repairs a live install, whose schema its own server
// has already brought forward.
func recoverMisplacedCmd(args []string) int {
	fs := flag.NewFlagSet("recover-misplaced", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "move the files (default: report only)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: embookshelf recover-misplaced [--apply]

Finds book files that a shipped bug wrote to the filesystem root instead
of into their library, and moves them where the catalog already says they
are. Affects installs that predate the storage-v2 migration and upgraded
onto v0.3.1–v0.6.2 while running as root; see docs/ops/misplaced-books.md.

Reports and changes nothing without --apply. Never deletes anything.

`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}

	ctx := context.Background()
	dbh, err := db.Open(ctx, cfg)
	if errors.Is(err, db.ErrSQLiteUnsupported) {
		fmt.Fprintf(os.Stderr,
			"DATABASE_URL points at SQLite (ADR-0023). Import it into Postgres first:\n"+
				"  embookshelf import-sqlite --from <file.db>\n")
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		return 1
	}
	defer func() { _ = dbh.Close() }()

	backends := repo.NewStorageBackendRepo(dbh)
	resolver, err := storageloader.LoadStorageBackends(ctx, backends)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage backends: %v\n", err)
		return 1
	}
	libs := repo.NewLibraryRepo(dbh)
	files := repo.NewFileRepo(dbh)
	deps := recovery.Deps{
		Libraries: libs,
		Books:     repo.NewBookRepo(dbh),
		Files:     files,
		Store: service.NewLibraryStore(service.LibraryStoreDeps{
			Libs:     libs,
			Resolver: resolver,
			Files:    files,
		}),
	}

	// FSRoot is left at its default here: "/" is where the broken placer
	// actually wrote, and the field exists so tests can point elsewhere.
	rep, err := recovery.Run(ctx, deps, recovery.Options{Apply: *apply})
	if err != nil {
		fmt.Fprintf(os.Stderr, "recover-misplaced: %v\n", err)
		return 1
	}

	printRecoveryReport(rep)
	if rep.Count(recovery.KindFailed) > 0 {
		return 1
	}
	return 0
}

func printRecoveryReport(rep recovery.Report) {
	mode := "dry run — nothing will be changed"
	if rep.Applied {
		mode = "applying"
	}
	fmt.Printf("embookshelf recover-misplaced (%s)\n", mode)
	fmt.Printf("searching under %s for %d book(s) across %d affected librar(ies)",
		rep.Root, rep.BooksInspected, rep.LibrariesInspected)
	if rep.LibrariesSkipped > 0 {
		fmt.Printf("; %d librar(ies) skipped as never affected", rep.LibrariesSkipped)
	}
	fmt.Print("\n\n")

	if len(rep.Findings) == 0 {
		fmt.Printf("Nothing found. No book in this install has its file at the\n" +
			"filesystem root, so it was never affected — or it has already\n" +
			"been repaired.\n")
		return
	}

	verb := "would move"
	if rep.Applied {
		verb = "moved"
	}
	printFindingGroup(rep, recovery.KindRecovered, "MISPLACED ("+verb+")")
	printFindingGroup(rep, recovery.KindFailed, "FAILED")
	printFindingGroup(rep, recovery.KindOccupied, "STRAY — destination already occupied")
	printFindingGroup(rep, recovery.KindMismatch, "STRAY — contents do not match the catalog")
	printFindingGroup(rep, recovery.KindAmbiguous, "STRAY — claimed by more than one book")

	recovered := rep.Count(recovery.KindRecovered)
	strays := rep.Count(recovery.KindOccupied) + rep.Count(recovery.KindMismatch) +
		rep.Count(recovery.KindAmbiguous)
	if strays > 0 {
		fmt.Printf(`%d file(s) were left exactly where they are. This tool never
deletes anything under the filesystem root — check each path above and
remove it yourself once you are sure the library has a good copy.

`, strays)
	}
	if recovered > 0 && !rep.Applied {
		fmt.Printf("Re-run with --apply to move %d file(s).\n", recovered)
	}
	if recovered > 0 && rep.Applied {
		fmt.Printf("Moved %d file(s) into their libraries.\n", recovered)
	}
	if rep.Count(recovery.KindFailed) > 0 {
		fmt.Printf("%d file(s) could not be repaired — see FAILED above.\n",
			rep.Count(recovery.KindFailed))
	}
}

func printFindingGroup(rep recovery.Report, kind recovery.Kind, heading string) {
	if rep.Count(kind) == 0 {
		return
	}
	fmt.Printf("%s: %d\n", heading, rep.Count(kind))
	for _, f := range rep.Findings {
		if f.Kind != kind {
			continue
		}
		fmt.Printf("  %s — %s  [%s]\n", nonEmpty(f.Author, "Unknown Author"),
			nonEmpty(f.Title, "Untitled"), f.Library)
		fmt.Printf("    at   %s\n", f.Suspect)
		fmt.Printf("    want %s\n", f.Correct)
		if f.FileRowRecreated {
			fmt.Printf("    note: the file record the 24h purge had deleted was recreated\n")
		}
		if f.Detail != "" {
			fmt.Printf("    note: %s\n", f.Detail)
		}
	}
	fmt.Println()
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
