package pattern

import (
	"strings"
	"testing"
)

// TestResolve covers the grammar + sanitization paths from the spec §4/§6.
// Table-driven to keep the catalog of rules readable at a glance — each row
// is one "claim about how a pattern resolves".
func TestResolve(t *testing.T) {
	full := Input{
		Title:     "The Name of the Wind",
		Authors:   []string{"Patrick Rothfuss"},
		Year:      2007,
		Series:    "The Kingkiller Chronicle",
		SeriesIndex: 1,
		Extension: "epub",
		CurrentFilename: "the_name_of_the_wind.epub",
	}
	standalone := Input{
		Title:     "Project Hail Mary",
		Authors:   []string{"Andy Weir"},
		Extension: "epub",
		CurrentFilename: "phm.epub",
	}
	decimal := Input{
		Title:       "Half Volume",
		Authors:     []string{"X"},
		Series:      "S",
		SeriesIndex: 2.5,
		Extension:   "epub",
		CurrentFilename: "half.epub",
	}

	cases := []struct {
		name    string
		pattern string
		in      Input
		want    string
	}{
		{
			name:    "blank pattern → currentFilename",
			pattern: "",
			in:      full,
			want:    "the_name_of_the_wind.epub",
		},
		{
			name:    "plain placeholders",
			pattern: "{authors}/{title}",
			in:      full,
			want:    "Patrick Rothfuss/The Name of the Wind.epub",
		},
		{
			name:    "system default — full metadata",
			pattern: "{authors}/<{series}/><{seriesIndex}. >{title}/{title}< - {authors}>< ({year})>",
			in:      full,
			want:    "Patrick Rothfuss/The Kingkiller Chronicle/01. The Name of the Wind/The Name of the Wind - Patrick Rothfuss (2007).epub",
		},
		{
			name:    "system default — no series / year",
			pattern: "{authors}/<{series}/><{seriesIndex}. >{title}/{title}< - {authors}>< ({year})>",
			in:      standalone,
			want:    "Andy Weir/Project Hail Mary/Project Hail Mary - Andy Weir.epub",
		},
		{
			name:    "optional block without fallback drops cleanly",
			pattern: "{title}< ({year})>",
			in:      standalone,
			want:    "Project Hail Mary.epub",
		},
		{
			name:    "else clause — primary path",
			pattern: "<{series}/{seriesIndex}|Standalone>/{title}",
			in:      full,
			want:    "The Kingkiller Chronicle/01/The Name of the Wind.epub",
		},
		{
			name:    "else clause — fallback path",
			pattern: "<{series}/{seriesIndex}|Standalone>/{title}",
			in:      standalone,
			want:    "Standalone/Project Hail Mary.epub",
		},
		{
			name:    "author letter organization",
			pattern: "{authors:initial}/{authors:sort}/{title}",
			in:      full,
			want:    "R/Rothfuss, Patrick/The Name of the Wind.epub",
		},
		{
			name:    "modifier: first",
			pattern: "{authors:first}/{title}",
			in: Input{
				Authors:   []string{"Doe Jane", "Smith Bob"},
				Title:     "T",
				Extension: "epub",
				CurrentFilename: "t.epub",
			},
			want: "Doe Jane/T.epub",
		},
		{
			name:    "modifier: upper",
			pattern: "{authors:upper}/{title}",
			in:      standalone,
			want:    "ANDY WEIR/Project Hail Mary.epub",
		},
		{
			name:    "unknown placeholder preserved verbatim",
			pattern: "{title}--{not_a_thing}",
			in:      standalone,
			want:    "Project Hail Mary--{not_a_thing}.epub",
		},
		{
			name:    "decimal series index keeps half-step",
			pattern: "{series}/{seriesIndex}. {title}",
			in:      decimal,
			want:    "S/02.5. Half Volume.epub",
		},
		{
			name:    "invalid filesystem characters stripped per component",
			pattern: "{title}",
			in: Input{
				Title:     "Hello: World / Goodbye*",
				Extension: "epub",
				CurrentFilename: "hw.epub",
			},
			want: "Hello World Goodbye.epub",
		},
		{
			name:    "trailing dots trimmed from components",
			pattern: "{title}",
			in: Input{
				Title:     "Dr. Strangelove...",
				Extension: "epub",
				CurrentFilename: "ds.epub",
			},
			want: "Dr. Strangelove.epub",
		},
		{
			name:    "trailing slash appends currentFilename",
			pattern: "{authors}/",
			in:      standalone,
			want:    "Andy Weir/phm.epub",
		},
		{
			name:    "extension not appended when pattern references {extension}",
			pattern: "{title}.{extension}",
			in:      standalone,
			want:    "Project Hail Mary.epub",
		},
		{
			name:    "extension not appended when pattern references {currentFilename}",
			pattern: "{authors}/{currentFilename}",
			in:      standalone,
			want:    "Andy Weir/phm.epub",
		},
		{
			name:    "blank title falls back to currentFilename",
			pattern: "{title}",
			in: Input{
				Extension:       "epub",
				CurrentFilename: "fallback.epub",
			},
			// {title} empty → falls back to CurrentFilename, extension already
			// present in filename so no double-append.
			want: "fallback.epub",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.pattern, tc.in)
			if got != tc.want {
				t.Errorf("Resolve(%q)\n  got:  %q\n  want: %q", tc.pattern, got, tc.want)
			}
		})
	}
}

// TestAuthorTruncation: the spec caps author-list byte length at 180 and
// appends " et al." when over. A ten-copy list of "Firstname Lastname" is
// comfortably over 180 bytes.
func TestAuthorTruncation(t *testing.T) {
	authors := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		authors = append(authors, "Firstname Lastname")
	}
	in := Input{
		Title:           "T",
		Authors:         authors,
		Extension:       "epub",
		CurrentFilename: "t.epub",
	}
	got := Resolve("{authors}/{title}", in)
	// Marker is " et al" (no trailing dot) — per-component sanitization
	// strips trailing dots, so we don't include one to begin with.
	if !strings.Contains(got, " et al") {
		t.Fatalf("expected ' et al' marker in output: %q", got)
	}
	// The author component must be <= 180 bytes before the slash.
	head := strings.SplitN(got, "/", 2)[0]
	if len(head) > 180 {
		t.Fatalf("author component is %d bytes, want ≤ 180: %q", len(head), head)
	}
}

// TestFilenameBudget: a 300-byte title must come back ≤ 245 bytes after
// truncation, and the extension must survive (spec §6.3 filename row).
func TestFilenameBudget(t *testing.T) {
	long := strings.Repeat("a", 300)
	got := Resolve("{title}", Input{
		Title:           long,
		Extension:       "epub",
		CurrentFilename: "x.epub",
	})
	if len(got) > 245 {
		t.Fatalf("final filename is %d bytes, want ≤ 245: %q", len(got), got)
	}
	if !strings.HasSuffix(got, ".epub") {
		t.Fatalf("extension not preserved: %q", got)
	}
}

// TestFolderBased: CBZ-as-folder items should not get an auto-appended
// extension (spec §4.5).
func TestFolderBased(t *testing.T) {
	got := Resolve("{title}", Input{
		Title:           "Saga Vol 1",
		Extension:       "cbz",
		FolderBased:     true,
		CurrentFilename: "saga.cbz",
	})
	if strings.HasSuffix(got, ".cbz") {
		t.Fatalf("folder-based item got an extension: %q", got)
	}
}

// TestUnterminatedPattern: a broken pattern falls back to the current
// filename rather than panicking or erroring (spec §6.4 degenerate results).
func TestUnterminatedPattern(t *testing.T) {
	got := Resolve("{title", Input{
		Title:           "Unclosed",
		Extension:       "epub",
		CurrentFilename: "x.epub",
	})
	if got != "x.epub" {
		t.Fatalf("unterminated placeholder should fall back to currentFilename, got %q", got)
	}
}

// TestSanitizationCollapseWhitespace: multiple spaces in a placeholder value
// collapse to one (spec §6.2).
func TestSanitizationCollapseWhitespace(t *testing.T) {
	got := Resolve("{title}", Input{
		Title:           "Too     many       spaces",
		Extension:       "epub",
		CurrentFilename: "x.epub",
	})
	if got != "Too many spaces.epub" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}
}
