// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sse"
)

// PublicSlugPrefix is the URL/API namespace that disambiguates a public
// shelf from a per-user shelf with the same slug. See ADR-0017.
const PublicSlugPrefix = "public:"

// ErrShelfPublishForbidden is returned when a non-admin tries to flip a
// shelf's is_public flag. Admins can only publish their own shelves;
// the repo layer enforces ownership separately via ErrNotFound.
var ErrShelfPublishForbidden = errors.New("only admins can share shelves")

type ShelfService struct {
	repo *repo.ShelfRepo
	hub  *sse.Hub
}

func NewShelfService(r *repo.ShelfRepo, hub *sse.Hub) *ShelfService {
	return &ShelfService{repo: r, hub: hub}
}

// SplitPublicSlug returns (bareSlug, isPublic). When the input begins
// with "public:" the prefix is stripped and isPublic is true. The
// returned bare slug is what the repo's slug column holds.
func SplitPublicSlug(slug string) (string, bool) {
	if strings.HasPrefix(slug, PublicSlugPrefix) {
		return strings.TrimPrefix(slug, PublicSlugPrefix), true
	}
	return slug, false
}

// List returns every shelf visible to the user — own shelves plus all
// public shelves. Smart-shelf book counts are filled in the same way
// as before (repo emits 0; service evaluates the rule).
func (s *ShelfService) List(ctx context.Context, userID string) ([]model.Shelf, error) {
	shelves, err := s.repo.ListVisibleToUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range shelves {
		if !shelves[i].IsSmart || shelves[i].Rule == nil {
			continue
		}
		// Smart shelves are never public (CHECK constraint), so the
		// rule evaluates against the viewer (who is also the owner).
		n, err := s.repo.CountForSmartShelf(ctx, userID, shelves[i].Rule)
		if err != nil {
			// A broken rule shouldn't 500 the whole sidebar — leave
			// BookCount at 0 and continue.
			continue
		}
		shelves[i].BookCount = n
	}
	return shelves, nil
}

// Books returns the books on a shelf. A `public:<slug>` argument routes
// to the public lookup (no user_id filter); a bare slug stays scoped to
// the user's own shelves.
func (s *ShelfService) Books(ctx context.Context, userID, slug, sort string) ([]model.Book, error) {
	if bare, isPublic := SplitPublicSlug(slug); isPublic {
		// Confirm the public shelf actually exists (and isn't a stale
		// link to an un-published row) so callers get a 404 instead of
		// an empty list.
		if _, err := s.repo.GetPublicBySlug(ctx, bare); err != nil {
			return nil, err
		}
		return s.repo.BooksInPublicShelf(ctx, userID, bare, sort)
	}
	return s.repo.BooksInShelfForUser(ctx, userID, slug, sort)
}

// GetMeta resolves a shelf slug (public-prefixed or bare) to its row
// for the public-shelf detail surfaces. Used where the shelf-books
// endpoint needs to surface the shelf's display name / owner alongside
// the books list.
func (s *ShelfService) GetMeta(ctx context.Context, userID, slug string) (model.Shelf, error) {
	if bare, isPublic := SplitPublicSlug(slug); isPublic {
		return s.repo.GetPublicBySlug(ctx, bare)
	}
	return s.repo.GetBySlugForUser(ctx, userID, slug)
}

// Create accepts an optional rule; nil creates a regular shelf and a
// non-nil rule creates a smart shelf (validated up-front).
func (s *ShelfService) Create(ctx context.Context, userID, name, accent, icon string, rule *model.ShelfRule) (model.Shelf, error) {
	if rule != nil {
		if err := rule.Validate(); err != nil {
			return model.Shelf{}, err
		}
	}
	return s.repo.Create(ctx, userID, name, accent, icon, rule)
}

// Update edits a shelf's name, accent, and/or rule. Nil pointers are
// untouched. ruleChanged disambiguates "don't touch the rule" from
// "replace with nil" — the latter is rejected at the repo layer for
// smart shelves. Public shelves get a broadcast invalidation so every
// connected viewer's sidebar/cache stays fresh.
func (s *ShelfService) Update(ctx context.Context, userID, slug string, name, accent, icon *string, rule *model.ShelfRule, ruleChanged bool) (model.Shelf, error) {
	if ruleChanged && rule != nil {
		if err := rule.Validate(); err != nil {
			return model.Shelf{}, err
		}
	}
	updated, err := s.repo.Update(ctx, userID, slug, name, accent, icon, rule, ruleChanged)
	if err != nil {
		return updated, err
	}
	if updated.IsPublic {
		s.broadcastPublicUpdated(updated.Slug)
	}
	return updated, nil
}

// Delete removes a shelf. If the shelf was public, viewers learn about
// it via a removed-broadcast so their sidebars and any open shelf view
// transition cleanly without a 404 mid-page.
func (s *ShelfService) Delete(ctx context.Context, userID, slug string) error {
	cur, err := s.repo.GetBySlugForUser(ctx, userID, slug)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, userID, slug); err != nil {
		return err
	}
	if cur.IsPublic {
		s.broadcastPublicRemoved(cur.Slug)
	}
	return nil
}

// AddBook puts a book on the user's shelf. If the target is the user's
// public shelf, broadcast the membership change so other viewers
// invalidate their cached books-on-shelf query.
func (s *ShelfService) AddBook(ctx context.Context, userID, slug, bookID string) error {
	if err := s.repo.AddBook(ctx, userID, slug, bookID); err != nil {
		return err
	}
	if sh, err := s.repo.GetBySlugForUser(ctx, userID, slug); err == nil && sh.IsPublic {
		s.broadcastPublicUpdated(sh.Slug)
	}
	return nil
}

// RemoveBook takes a book off the user's shelf, with the same broadcast
// rule as AddBook for public shelves.
func (s *ShelfService) RemoveBook(ctx context.Context, userID, slug, bookID string) error {
	if err := s.repo.RemoveBook(ctx, userID, slug, bookID); err != nil {
		return err
	}
	if sh, err := s.repo.GetBySlugForUser(ctx, userID, slug); err == nil && sh.IsPublic {
		s.broadcastPublicUpdated(sh.Slug)
	}
	return nil
}

func (s *ShelfService) SlugsForBook(ctx context.Context, userID, bookID string) ([]string, error) {
	return s.repo.ShelfSlugsForBook(ctx, userID, bookID)
}

// CountUnshelved returns the size of the user's "Unshelved" virtual view —
// books not on any of their regular non-system shelves. Surfaced in the
// /shelves response so the sidebar can show a live count alongside
// All Books without a second roundtrip.
func (s *ShelfService) CountUnshelved(ctx context.Context, userID string) (int, error) {
	return s.repo.CountUnshelvedForUser(ctx, userID)
}

// SetPublic flips a shelf's is_public flag. Caller must already be an
// admin (gated upstream); ownership is enforced at the repo layer via
// the (user_id, slug) lookup. Returns the updated shelf and emits the
// matching broadcast so connected viewers update sidebars in lockstep.
func (s *ShelfService) SetPublic(ctx context.Context, userID, slug string, public bool) (model.Shelf, error) {
	updated, err := s.repo.SetPublic(ctx, userID, slug, public)
	if err != nil {
		return updated, err
	}
	if public {
		s.broadcastPublicUpdated(updated.Slug)
	} else {
		s.broadcastPublicRemoved(updated.Slug)
	}
	return updated, nil
}

// UnpublishAllForOwner is the cascade hook fired when an admin loses
// the admin role — they can no longer keep shelves shared. Each
// affected shelf gets a removed-broadcast.
func (s *ShelfService) UnpublishAllForOwner(ctx context.Context, userID string) error {
	slugs, err := s.repo.UnpublishAllForOwner(ctx, userID)
	if err != nil {
		return err
	}
	for _, slug := range slugs {
		s.broadcastPublicRemoved(slug)
	}
	return nil
}

// broadcastPublicUpdated emits a fan-out event for every connected SSE
// subscriber. The hub is broadcast-by-default (no per-user filtering),
// so this is one publish per mutation regardless of viewer count.
func (s *ShelfService) broadcastPublicUpdated(slug string) {
	if s.hub == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"slug": PublicSlugPrefix + slug})
	s.hub.Broadcast(sse.Event{Name: "shelf.public.updated", Data: string(payload)})
}

func (s *ShelfService) broadcastPublicRemoved(slug string) {
	if s.hub == nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"slug": PublicSlugPrefix + slug})
	s.hub.Broadcast(sse.Event{Name: "shelf.public.removed", Data: string(payload)})
}
