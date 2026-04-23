package pattern

import "testing"

// The reference test suite mirrors the "Placeholders Reference" copy
// we ship in the admin UI so the two can't drift. Every pattern
// example rendered under the help section is exercised here against
// the same two sample inputs the UI describes: full metadata and
// partial (series-less) metadata.
//
// When you change a pattern in the docs panel, update the matching
// want[] here; the test guards against shipping a docs example that
// does not resolve the way the copy claims.

// fullSample matches the "Full Metadata" sample shown in the admin UI.
func fullSample() Input {
	return Input{
		Title:           "The Name of the Wind",
		Subtitle:        "Special Edition",
		Authors:         []string{"Patrick Rothfuss"},
		Series:          "The Kingkiller Chronicle",
		SeriesIndex:     1,
		Year:            2007,
		Language:        "en",
		Publisher:       "DAW",
		ISBN:            "9780756404079",
		Extension:       "epub",
		CurrentFilename: "the_name_of_the_wind.epub",
	}
}

// partialSample matches the "Partial Metadata" sample shown in the
// admin UI — no series, no series index, no subtitle.
func partialSample() Input {
	return Input{
		Title:           "Project Hail Mary",
		Authors:         []string{"Andy Weir"},
		Year:            2021,
		Extension:       "epub",
		CurrentFilename: "project_hail_mary_original.epub",
	}
}

// singleAuthorSort exercises {authors:sort} on a proper "First Last"
// name so the expected "Last, First" is unambiguous.
func singleAuthorSort() Input {
	in := partialSample()
	in.Title = "Project Hail Mary"
	in.Authors = []string{"Andy Weir"}
	return in
}

func TestReferenceBasicPatterns(t *testing.T) {
	cases := []struct {
		label   string
		pattern string
		in      Input
		want    string
	}{
		{
			label:   "basic pattern",
			pattern: "{authors} - {title}",
			in:      fullSample(),
			want:    "Patrick Rothfuss - The Name of the Wind.epub",
		},
		{
			label:   "series in folder",
			pattern: "{authors}/{series}/{seriesIndex} - {title}",
			in:      fullSample(),
			want:    "Patrick Rothfuss/The Kingkiller Chronicle/01 - The Name of the Wind.epub",
		},
		{
			label:   "title + subtitle",
			pattern: "{title}: {subtitle}",
			in:      fullSample(),
			want:    "The Name of the Wind Special Edition.epub",
		},
		{
			label:   "absolute path",
			pattern: "/{authors}/{title}",
			in:      fullSample(),
			want:    "Patrick Rothfuss/The Name of the Wind.epub",
		},
		{
			label:   "folder only",
			pattern: "{title}/",
			in:      fullSample(),
			want:    "The Name of the Wind/the_name_of_the_wind.epub",
		},
		{
			label:   "year prefix",
			pattern: "({year}) {title}",
			in:      fullSample(),
			want:    "(2007) The Name of the Wind.epub",
		},
	}

	runReferenceCases(t, cases)
}

func TestReferenceConditionalBlocks(t *testing.T) {
	cases := []struct {
		label   string
		pattern string
		in      Input
		want    string
	}{
		{
			label:   "optional block — with series index",
			pattern: "<{seriesIndex}. >{title}",
			in:      fullSample(),
			want:    "01. The Name of the Wind.epub",
		},
		{
			label:   "optional block — without series index",
			pattern: "<{seriesIndex}. >{title}",
			in:      partialSample(),
			want:    "Project Hail Mary.epub",
		},
		{
			label:   "subtitle conditional — with subtitle",
			pattern: "{title}<: {subtitle}>",
			in:      fullSample(),
			want:    "The Name of the Wind Special Edition.epub",
		},
		{
			label:   "subtitle conditional — without subtitle",
			pattern: "{title}<: {subtitle}>",
			in:      partialSample(),
			want:    "Project Hail Mary.epub",
		},
		{
			label:   "multiple optionals — full metadata",
			pattern: "{authors}/<{series}/><{seriesIndex}. >{title}< ({year})>",
			in:      fullSample(),
			want:    "Patrick Rothfuss/The Kingkiller Chronicle/01. The Name of the Wind (2007).epub",
		},
		{
			label:   "multiple optionals — partial metadata",
			pattern: "{authors}/<{series}/><{seriesIndex}. >{title}< ({year})>",
			in:      partialSample(),
			want:    "Andy Weir/Project Hail Mary (2021).epub",
		},
		{
			label:   "else clause — series present",
			pattern: "<{series}|Standalone>/{title}",
			in:      fullSample(),
			want:    "The Kingkiller Chronicle/The Name of the Wind.epub",
		},
		{
			label:   "else clause — fallback",
			pattern: "<{series}|Standalone>/{title}",
			in:      partialSample(),
			want:    "Standalone/Project Hail Mary.epub",
		},
		{
			label:   "else fallback — primary path",
			pattern: "<{series}/{seriesIndex} - {title}|{title}>",
			in:      fullSample(),
			want:    "The Kingkiller Chronicle/01 - The Name of the Wind.epub",
		},
		{
			label:   "else fallback — fallback path",
			pattern: "<{series}/{seriesIndex} - {title}|{title}>",
			in:      partialSample(),
			want:    "Project Hail Mary.epub",
		},
		{
			label:   "else + modifier — primary",
			pattern: "<{series}|{authors:sort}>/{title}",
			in:      fullSample(),
			want:    "The Kingkiller Chronicle/The Name of the Wind.epub",
		},
		{
			label:   "else + modifier — fallback (authors:sort)",
			pattern: "<{series}|{authors:sort}>/{title}",
			in:      singleAuthorSort(),
			want:    "Weir, Andy/Project Hail Mary.epub",
		},
	}

	runReferenceCases(t, cases)
}

func TestReferenceValueModifiers(t *testing.T) {
	cases := []struct {
		label   string
		pattern string
		in      Input
		want    string
	}{
		{
			label:   "authors:sort",
			pattern: "{authors:sort}/{title}",
			in:      fullSample(),
			want:    "Rothfuss, Patrick/The Name of the Wind.epub",
		},
		{
			label:   "authors:initial + authors:sort",
			pattern: "{authors:initial}/{authors:sort}/{title}",
			in:      fullSample(),
			want:    "R/Rothfuss, Patrick/The Name of the Wind.epub",
		},
		{
			label:   "authors:first",
			pattern: "{authors:first}/{title}",
			in:      fullSample(),
			want:    "Patrick Rothfuss/The Name of the Wind.epub",
		},
		{
			label:   "title:upper",
			pattern: "{title:upper}",
			in:      fullSample(),
			want:    "THE NAME OF THE WIND.epub",
		},
		{
			label:   "title:lower",
			pattern: "{title:lower}",
			in:      fullSample(),
			want:    "the name of the wind.epub",
		},
		{
			label:   "title:initial folder",
			pattern: "{title:initial}/{authors}/{title}",
			in:      fullSample(),
			want:    "T/Patrick Rothfuss/The Name of the Wind.epub",
		},
		{
			label:   "combined modifiers",
			pattern: "{authors:sort} - {title:lower}",
			in:      fullSample(),
			want:    "Rothfuss, Patrick - the name of the wind.epub",
		},
	}

	runReferenceCases(t, cases)
}

func runReferenceCases(t *testing.T, cases []struct {
	label   string
	pattern string
	in      Input
	want    string
}) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			got := Resolve(tc.pattern, tc.in)
			if got != tc.want {
				t.Errorf("Resolve(%q)\n  got:  %q\n  want: %q", tc.pattern, got, tc.want)
			}
		})
	}
}
