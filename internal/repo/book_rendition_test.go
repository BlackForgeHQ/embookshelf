// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// renditionShape adapts one artifact's repo to the shared lifecycle so
// the suite below runs identically over both tracking tables. markReady
// hides what genuinely differs — the artifact projection's columns.
type renditionShape struct {
	table       string
	start       func(ctx context.Context, bookID string) error
	markRunning func(ctx context.Context, bookID string) error
	markReady   func(ctx context.Context, bookID string) error
	markFailed  func(ctx context.Context, bookID, msg string) error
	get         func(ctx context.Context, bookID string) (state model.RenditionState, errMsg string, err error)
}

// renditionShapes builds both artifact adapters over one database and a
// book to track. The epub shape needs a files row to point at; it makes
// one per MarkReady.
func renditionShapes(t *testing.T, d *db.DB) (string, []renditionShape) {
	t.Helper()
	ctx := context.Background()
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	files := repo.NewFileRepo(d)

	lib, err := libs.CreateLibrary(ctx, "Renditions", "renditions", "/tmp/renditions", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	b, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Continuous Architecture", Author: "Murat Erder", Format: "PDF",
		Path: "ca.pdf",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}

	md := repo.NewBookMarkdownRenditionRepo(d)
	ep := repo.NewBookEpubRenditionRepo(d)

	// files.location is unique per library, and the parity test calls
	// MarkReady more than once — each call gets its own files row.
	epubSeq := 0

	return b.ID, []renditionShape{
		{
			table:       "book_markdown_renditions",
			start:       md.Start,
			markRunning: md.MarkRunning,
			markReady: func(ctx context.Context, bookID string) error {
				return md.MarkReady(ctx, bookID, "Author/Title/Title.md", 1234, []byte{0xde, 0xad}, "0.1.0")
			},
			markFailed: md.MarkFailed,
			get: func(ctx context.Context, bookID string) (model.RenditionState, string, error) {
				r, err := md.GetByBookID(ctx, bookID)
				return r.State, r.Error, err
			},
		},
		{
			table:       "book_epub_renditions",
			start:       ep.Start,
			markRunning: ep.MarkRunning,
			markReady: func(ctx context.Context, bookID string) error {
				epubSeq++
				f, err := files.Insert(ctx, model.File{
					LibraryID: lib.ID, BookID: bookID,
					Location: fmt.Sprintf("A/T/T-%d.epub", epubSeq), Format: "EPUB", Size: 10,
				})
				if err != nil {
					return err
				}
				return ep.MarkReady(ctx, bookID, f.ID, []byte{0xde, 0xad}, "0.1.0")
			},
			markFailed: ep.MarkFailed,
			get: func(ctx context.Context, bookID string) (model.RenditionState, string, error) {
				r, err := ep.GetByBookID(ctx, bookID)
				return r.State, r.Error, err
			},
		},
	}
}

// TestRenditionLifecycle — the happy path and the loud-failure path,
// identical over both artifact shapes: Start is pending with a clean
// error channel, MarkFailed records why verbatim (ADR-0033 §5: what
// lands in the row is exactly what the status API surfaces), and
// re-triggering goes back to pending, error cleared.
func TestRenditionLifecycle(t *testing.T) {
	d := repotest.New(t)
	bookID, shapes := renditionShapes(t, d)
	ctx := context.Background()

	for _, s := range shapes {
		t.Run(s.table, func(t *testing.T) {
			if err := s.start(ctx, bookID); err != nil {
				t.Fatalf("Start: %v", err)
			}
			state, errMsg, err := s.get(ctx, bookID)
			if err != nil {
				t.Fatalf("get after Start: %v", err)
			}
			if state != model.RenditionPending || errMsg != "" {
				t.Fatalf("after Start: state=%q error=%q", state, errMsg)
			}

			if err := s.markRunning(ctx, bookID); err != nil {
				t.Fatalf("MarkRunning: %v", err)
			}
			if state, _, _ = s.get(ctx, bookID); state != model.RenditionRunning {
				t.Fatalf("after MarkRunning: state=%q", state)
			}

			if err := s.markReady(ctx, bookID); err != nil {
				t.Fatalf("MarkReady: %v", err)
			}
			if state, _, _ = s.get(ctx, bookID); state != model.RenditionReady {
				t.Fatalf("after MarkReady: state=%q", state)
			}

			// Regenerate and fail loudly: the message is verbatim.
			if err := s.start(ctx, bookID); err != nil {
				t.Fatalf("Start over ready: %v", err)
			}
			const msg = "converter extension is not configured"
			if err := s.markFailed(ctx, bookID, msg); err != nil {
				t.Fatalf("MarkFailed: %v", err)
			}
			state, errMsg, err = s.get(ctx, bookID)
			if err != nil {
				t.Fatalf("get after MarkFailed: %v", err)
			}
			if state != model.RenditionFailed || errMsg != msg {
				t.Fatalf("after MarkFailed: state=%q error=%q", state, errMsg)
			}

			// Re-triggering clears the error and goes back to pending.
			if err := s.start(ctx, bookID); err != nil {
				t.Fatalf("Start again: %v", err)
			}
			state, errMsg, _ = s.get(ctx, bookID)
			if state != model.RenditionPending || errMsg != "" {
				t.Fatalf("after restart: state=%q error=%q", state, errMsg)
			}
		})
	}
}

// TestRenditionReadyRowIsSealed — a concluded ready row records an
// artifact a consumer may be reading, and only Start reopens it. A late
// write from a superseded job — MarkFailed above all — must land as a
// refused no-op, not overwrite the conclusion (#296; the bug class
// book_audiobook.go's #210 records).
func TestRenditionReadyRowIsSealed(t *testing.T) {
	d := repotest.New(t)
	bookID, shapes := renditionShapes(t, d)
	ctx := context.Background()

	for _, s := range shapes {
		t.Run(s.table, func(t *testing.T) {
			if err := s.start(ctx, bookID); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if err := s.markRunning(ctx, bookID); err != nil {
				t.Fatalf("MarkRunning: %v", err)
			}
			if err := s.markReady(ctx, bookID); err != nil {
				t.Fatalf("MarkReady: %v", err)
			}

			if err := s.markFailed(ctx, bookID, "late failure from a superseded job"); err != nil {
				t.Fatalf("MarkFailed on ready: %v, want refused no-op", err)
			}
			state, errMsg, err := s.get(ctx, bookID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if state != model.RenditionReady || errMsg != "" {
				t.Fatalf("after late MarkFailed: state=%q error=%q, want the ready row untouched", state, errMsg)
			}

			if err := s.markRunning(ctx, bookID); err != nil {
				t.Fatalf("MarkRunning on ready: %v, want refused no-op", err)
			}
			if state, _, _ := s.get(ctx, bookID); state != model.RenditionReady {
				t.Fatalf("after late MarkRunning: state=%q, want ready kept", state)
			}

			// Start is the one legitimate reopening: back to pending, clean
			// error channel.
			if err := s.start(ctx, bookID); err != nil {
				t.Fatalf("Start over ready: %v", err)
			}
			state, errMsg, err = s.get(ctx, bookID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if state != model.RenditionPending || errMsg != "" {
				t.Fatalf("after Start: state=%q error=%q, want pending with a clean error", state, errMsg)
			}
		})
	}
}

// TestRenditionGuardAgreesWithTheModel — the SQL guard on every state
// write is array membership over model.RenditionWrite's From, and this
// holds the rendering to the declaration for every state × write, the
// job TestTransitionGuardAgreesWithTheModel does for the audiobook
// (#233's lesson applied here).
func TestRenditionGuardAgreesWithTheModel(t *testing.T) {
	d := repotest.New(t)
	bookID, shapes := renditionShapes(t, d)
	ctx := context.Background()

	for _, s := range shapes {
		writes := map[model.RenditionState]func() error{
			model.RenditionRunning: func() error { return s.markRunning(ctx, bookID) },
			model.RenditionReady:   func() error { return s.markReady(ctx, bookID) },
			model.RenditionFailed:  func() error { return s.markFailed(ctx, bookID, "x") },
		}
		if err := s.start(ctx, bookID); err != nil {
			t.Fatalf("Start: %v", err)
		}
		for _, from := range model.AllRenditionStates() {
			for to, write := range writes {
				// Force the from-state directly — going through the writes
				// under test would assume the very guards being checked —
				// and age the row, so "did it move" is answerable even when
				// from == to: every lifecycle write stamps updated_at.
				if _, err := d.SQL.ExecContext(ctx,
					`UPDATE `+s.table+` SET state = $2, updated_at = now() - interval '1 day'
					  WHERE book_id = $1`, bookID, string(from)); err != nil {
					t.Fatalf("force state %q: %v", from, err)
				}
				if err := write(); err != nil {
					t.Fatalf("%s: write to %q from %q: %v", s.table, to, from, err)
				}
				var state string
				var fresh bool
				if err := d.SQL.QueryRowContext(ctx,
					`SELECT state, updated_at > now() - interval '1 hour' FROM `+s.table+
						` WHERE book_id = $1`, bookID).Scan(&state, &fresh); err != nil {
					t.Fatalf("read row: %v", err)
				}
				if want := model.RenditionWrite(to).Admits(from); fresh != want {
					t.Errorf("%s: write to %q from %q: SQL moved = %v, model Admits = %v — "+
						"the guard and the model's declaration disagree", s.table, to, from, fresh, want)
				}
				if fresh && model.RenditionState(state) != to {
					t.Errorf("%s: write to %q from %q landed on %q", s.table, to, from, state)
				}
			}
		}
	}
}

// TestRenditionGetMissing — ErrNotFound for a book with no row, on both
// shapes.
func TestRenditionGetMissing(t *testing.T) {
	d := repotest.New(t)
	bookID, shapes := renditionShapes(t, d)

	for _, s := range shapes {
		if _, _, err := s.get(context.Background(), bookID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s: err = %v, want ErrNotFound", s.table, err)
		}
	}
}

// TestRenditionWriteMissingRow — a state write with no row to move is
// ErrNotFound, not a silent no-op: the book was deleted mid-job and the
// worker should hear it.
func TestRenditionWriteMissingRow(t *testing.T) {
	d := repotest.New(t)
	bookID, shapes := renditionShapes(t, d)
	ctx := context.Background()

	for _, s := range shapes {
		if err := s.markRunning(ctx, bookID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s: MarkRunning without a row: %v, want ErrNotFound", s.table, err)
		}
		if err := s.markFailed(ctx, bookID, "x"); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s: MarkFailed without a row: %v, want ErrNotFound", s.table, err)
		}
	}
}

// TestRenditionDeletedWithBook — the tracking row rides the book's
// cascade on both shapes.
func TestRenditionDeletedWithBook(t *testing.T) {
	d := repotest.New(t)
	bookID, shapes := renditionShapes(t, d)
	ctx := context.Background()

	for _, s := range shapes {
		if err := s.start(ctx, bookID); err != nil {
			t.Fatalf("%s: Start: %v", s.table, err)
		}
	}
	if _, err := d.PG.Exec(ctx, "DELETE FROM books WHERE id = $1", bookID); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	for _, s := range shapes {
		if _, _, err := s.get(ctx, bookID); !errors.Is(err, repo.ErrNotFound) {
			t.Fatalf("%s: row survived its book: err = %v", s.table, err)
		}
	}
}
