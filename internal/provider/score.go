package provider

import "strings"

// scoreMatch is a crude 0-100 confidence heuristic. It's intentionally naive —
// the goal is sorting matches in the UI, not building a search engine.
//
//   100  exact title AND any-author match
//    85  exact title, no author match
//    65  title contains the query (or vice-versa)
//    40  weak overlap
//    20  fallback
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
