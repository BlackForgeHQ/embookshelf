package provider

import (
	"strings"

	"github.com/agnivade/levenshtein"
)

// scoreMatch is a 0-100 confidence heuristic. It's intentionally naive —
// the goal is sorting matches in the UI, not building a search engine.
//
//	100  exact title AND any-author match
//	 85  exact title, no author match
//	 65  title contains the query (or vice-versa)
//	 50  fuzzy title match (Levenshtein ratio > 0.80)
//	 40  weak token overlap
//	 20  fallback
//
// A fuzzy-match tier sits between the "contains" and "token overlap"
// rungs so providers with slightly different spacing/punctuation
// (Hardcover vs. Open Library, author attributions with/without
// trailing commas) still score meaningfully above the floor.
func scoreMatch(q Query, title string, authors []string) int {
	qt := strings.ToLower(strings.TrimSpace(q.Title))
	qa := strings.ToLower(strings.TrimSpace(q.Author))
	mt := strings.ToLower(strings.TrimSpace(title))

	// Always give ISBN hits a top score — those are unambiguous.
	if strings.TrimSpace(q.ISBN) != "" {
		return 100
	}

	titleMatch := 0
	switch {
	case qt == "" || mt == "":
		titleMatch = 0
	case qt == mt:
		titleMatch = 3
	case strings.Contains(mt, qt) || strings.Contains(qt, mt):
		titleMatch = 2
	case fuzzyRatio(qt, mt) >= 0.80:
		titleMatch = 2 // tier close to "contains"; the 0.80 floor filters noise
	default:
		// Token overlap fallback.
		qTokens := wordSet(qt)
		mTokens := wordSet(mt)
		shared := 0
		for t := range qTokens {
			if _, ok := mTokens[t]; ok {
				shared++
			}
		}
		if shared > 0 {
			titleMatch = 1
		}
	}

	authorMatch := 0
	if qa != "" {
		for _, a := range authors {
			al := strings.ToLower(strings.TrimSpace(a))
			if al == "" {
				continue
			}
			if al == qa || strings.Contains(al, qa) || strings.Contains(qa, al) {
				authorMatch = 1
				break
			}
		}
	}

	// Map (titleMatch, authorMatch) → score.
	switch {
	case titleMatch == 3 && authorMatch == 1:
		return 100
	case titleMatch == 3:
		return 85
	case titleMatch == 2 && authorMatch == 1:
		return 75
	case titleMatch == 2:
		return 65
	case titleMatch == 1 && authorMatch == 1:
		return 55
	case titleMatch == 1:
		return 40
	}
	return 20
}

// fuzzyRatio returns 1 - (edit distance / max length) in [0, 1].
// 1.0 = identical, 0.0 = completely different. Cheap enough to run on
// every candidate title since the strings are short (< 200 chars).
func fuzzyRatio(a, b string) float64 {
	if a == "" && b == "" {
		return 1
	}
	if a == "" || b == "" {
		return 0
	}
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	d := levenshtein.ComputeDistance(a, b)
	return 1 - float64(d)/float64(max)
}

func wordSet(s string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == ',' || r == '.' || r == ':' || r == ';' || r == '-'
	}) {
		if len(w) > 2 { // skip very short tokens — "the", "of", "a"
			out[w] = struct{}{}
		}
	}
	return out
}
