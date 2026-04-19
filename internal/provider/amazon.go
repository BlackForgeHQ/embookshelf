package provider

import (
	"context"
	"fmt"
	"net/http"
)

// Amazon is a cover-only fallback provider. We can't query Amazon's
// metadata catalog without a paid Product Advertising API key, but
// their public images CDN exposes covers keyed by ISBN-10:
//
//	https://images-na.ssl-images-amazon.com/images/P/{ISBN10}.01.LZZZZZZZ.jpg
//
// The endpoint always returns 200, even for unknown ISBNs (you get a
// 1x1 transparent pixel), so the SPA's match card still renders even
// when there's nothing useful to display. The user picks; we don't
// pretend the cover exists.
type Amazon struct {
	Client *http.Client
}

func NewAmazon() *Amazon {
	return &Amazon{Client: defaultHTTPClient}
}

func (*Amazon) Name() Source { return SourceAmazon }

// Search returns at most one Match — a cover-only fallback when the
// query carries an ISBN we can convert to ISBN-10. Other providers
// own the metadata side; Amazon is here strictly for "the cover the
// other two don't have".
func (a *Amazon) Search(_ context.Context, q Query) ([]Match, error) {
	isbn10 := toISBN10(q.ISBN)
	if isbn10 == "" {
		return nil, nil
	}
	cover := fmt.Sprintf(
		"https://images-na.ssl-images-amazon.com/images/P/%s.01.LZZZZZZZ.jpg",
		isbn10,
	)
	return []Match{{
		Source:   SourceAmazon,
		SourceID: isbn10,
		ISBN:     q.ISBN,
		CoverURL: cover,
		// Low confidence on purpose — we have no title/author
		// corroboration, so this should sort below proper metadata
		// hits from Google Books / Open Library.
		Confidence: 30,
	}}, nil
}
