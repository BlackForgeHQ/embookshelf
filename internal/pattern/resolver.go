// Package pattern implements the file-naming-pattern grammar described in
// spec/file-naming-patterns.spec.md. Patterns are templates like
//
//	{authors}/<{series}/><{seriesIndex}. >/{title}
//
// and expand against a book's metadata into a deterministic, filesystem-safe
// relative path. The resolver is stateless and the parser is a small
// hand-written recursive descent over the pattern string — no regex on the
// hot path, so resolution is O(n) in the pattern length.
package pattern

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Input is the minimum metadata the resolver needs. Mirrors the placeholder
// list in §4.1 of the spec, trimmed to what embookshelf actually stores.
type Input struct {
	Title           string
	Subtitle        string
	Authors         []string
	Year            int
	Series          string
	SeriesIndex     float64
	Language        string
	Publisher       string
	ISBN            string
	CurrentFilename string
	Extension       string
	// FolderBased indicates the item is a directory (CBZ/CBR etc.) so no
	// automatic extension should be appended. Defaults to false.
	FolderBased bool
}

const (
	// authorBudget is the UTF-8 byte cap before truncating the joined
	// author list and appending " et al." (spec §6.3).
	authorBudget = 180
	// componentBudget caps each resolved path component (spec §6.3).
	componentBudget = 245
	// filenameBudget caps the final filename+extension (spec §6.3).
	filenameBudget = 245
	// elideMarker is what we append when truncating an over-budget author list.
	// No trailing dot — per-component sanitization would strip it anyway.
	elideMarker = " et al"
)

// whitespaceRun is used once per resolved value to collapse runs of spaces
// (spec §6.2). Compiled at package init to avoid per-call regex compilation.
var whitespaceRun = regexp.MustCompile(`\s+`)

// invalidFSChars matches filesystem-unsafe characters to strip from resolved
// placeholder values. ASCII 0–31 controls + the classic Windows set. Forward
// slashes in pattern *structure* are preserved (they're added by the parser,
// not by placeholder substitution).
var invalidFSChars = regexp.MustCompile(`[\x00-\x1f\\/:*?"<>|]`)

// Resolve expands pattern against in and returns a filesystem-safe relative
// path. Blank/invalid patterns or a degenerate resolution fall back to
// in.CurrentFilename so callers never lose the upload.
func Resolve(pattern string, in Input) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return in.CurrentFilename
	}

	nodes, err := parse(pattern)
	if err != nil {
		return in.CurrentFilename
	}

	// Trailing slash means "pattern describes a directory; auto-name the
	// file" (spec §10). We append {currentFilename} so the output ends
	// with a usable filename.
	if strings.HasSuffix(pattern, "/") {
		nodes = append(nodes, node{kind: nodePlaceholder, name: "currentFilename"})
	}

	raw := render(nodes, in)
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "/")
	if raw == "" {
		return in.CurrentFilename
	}

	// Per-component sanitization first — so a placeholder value that
	// contained a path separator (user-supplied metadata!) doesn't get
	// interpreted as a directory break. Sanitizer already scrubbed the raw
	// placeholder values at resolve time; this pass just walks the
	// pattern-structured slashes and applies length caps.
	parts := strings.Split(raw, "/")
	out := parts[:0]
	for _, p := range parts {
		p = sanitizeComponent(p)
		if p == "" {
			continue
		}
		out = append(out, truncateRunes(p, componentBudget))
	}
	if len(out) == 0 {
		return in.CurrentFilename
	}

	// Pattern-level extension: if nothing in the pattern referenced the
	// original name/extension, append the extension to the last component.
	// Skip if the last component already ends with ".ext" (e.g. {title}
	// fell back to CurrentFilename which already carries the extension).
	hasExtRef := mentionsPlaceholder(nodes, "extension") || mentionsPlaceholder(nodes, "currentFilename")
	ext := normalizeExt(in.Extension)
	last := out[len(out)-1]
	if !in.FolderBased && !hasExtRef && ext != "" && !strings.EqualFold(filepath.Ext(last), "."+ext) {
		last = strings.TrimRight(last, ".") + "." + ext
		out[len(out)-1] = last
	}

	// Final-filename budget: truncate the last segment's *name* while
	// preserving its extension (spec §6.3 filename row).
	if byteLen(out[len(out)-1]) > filenameBudget {
		out[len(out)-1] = truncateKeepingExt(out[len(out)-1], filenameBudget)
	}

	joined := strings.Join(out, "/")
	// If everything stripped down to just an extension (.epub), that's a
	// degenerate resolution — fall back (spec §10 "resolved path is only an
	// extension").
	if strings.HasPrefix(joined, ".") && !strings.ContainsAny(joined, "/ ") {
		return in.CurrentFilename
	}
	return joined
}

// Preview runs Resolve against a sample Input and returns the result; used by
// the Settings UI so admins can see the shape of a pattern before saving.
func Preview(pattern string, sample Input) string { return Resolve(pattern, sample) }

// ------------------------------------------------------------
// Parser
// ------------------------------------------------------------

type nodeKind int

const (
	nodeText nodeKind = iota
	nodePlaceholder
	nodeBlock // <...|...>
)

type node struct {
	kind nodeKind
	// text: literal string
	text string
	// placeholder: name + optional modifier
	name     string
	modifier string
	// block: parsed children for primary half, and optional fallback half
	primary  []node
	fallback []node
	hasElse  bool
}

// parse consumes the pattern top-to-bottom. Recursive descent keeps nesting
// predictable — a `<` opens a block, an unescaped `>` closes it, a `|`
// splits primary from fallback (only the first `|` is the separator).
func parse(s string) ([]node, error) {
	p := &parser{src: s}
	nodes, err := p.parseSeq(false)
	if err != nil {
		return nil, err
	}
	if p.pos != len(s) {
		return nil, fmt.Errorf("unexpected %q at pos %d", s[p.pos], p.pos)
	}
	return nodes, nil
}

type parser struct {
	src string
	pos int
}

// parseSeq reads a sequence of nodes. When inBlock is true, we stop at the
// enclosing `>` or `|`; otherwise we run to end of input.
func (p *parser) parseSeq(inBlock bool) ([]node, error) {
	var out []node
	var buf strings.Builder
	flushText := func() {
		if buf.Len() > 0 {
			out = append(out, node{kind: nodeText, text: buf.String()})
			buf.Reset()
		}
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '{':
			flushText()
			ph, err := p.parsePlaceholder()
			if err != nil {
				return nil, err
			}
			out = append(out, ph)
		case c == '<':
			flushText()
			blk, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			out = append(out, blk)
		case inBlock && (c == '>' || c == '|'):
			flushText()
			return out, nil
		default:
			buf.WriteByte(c)
			p.pos++
		}
	}
	if inBlock {
		return nil, fmt.Errorf("unterminated block")
	}
	flushText()
	return out, nil
}

func (p *parser) parsePlaceholder() (node, error) {
	p.pos++ // consume '{'
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] != '}' {
		p.pos++
	}
	if p.pos >= len(p.src) {
		return node{}, fmt.Errorf("unterminated placeholder")
	}
	body := p.src[start:p.pos]
	p.pos++ // consume '}'
	name, mod, _ := strings.Cut(body, ":")
	name = strings.TrimSpace(name)
	mod = strings.TrimSpace(mod)
	if name == "" {
		return node{}, fmt.Errorf("empty placeholder name")
	}
	return node{kind: nodePlaceholder, name: name, modifier: mod}, nil
}

func (p *parser) parseBlock() (node, error) {
	p.pos++ // consume '<'
	primary, err := p.parseSeq(true)
	if err != nil {
		return node{}, err
	}
	blk := node{kind: nodeBlock, primary: primary}
	if p.pos < len(p.src) && p.src[p.pos] == '|' {
		p.pos++ // consume '|'
		fb, err := p.parseSeq(true)
		if err != nil {
			return node{}, err
		}
		blk.fallback = fb
		blk.hasElse = true
	}
	if p.pos >= len(p.src) || p.src[p.pos] != '>' {
		return node{}, fmt.Errorf("expected '>'")
	}
	p.pos++ // consume '>'
	return blk, nil
}

// ------------------------------------------------------------
// Renderer
// ------------------------------------------------------------

// render produces a string from parsed nodes. Blocks where any placeholder is
// empty emit either their fallback (if `|` present) or nothing — matching the
// spec's §4.2 / §4.3 rules.
func render(nodes []node, in Input) string {
	var buf strings.Builder
	for _, n := range nodes {
		switch n.kind {
		case nodeText:
			buf.WriteString(n.text)
		case nodePlaceholder:
			v, _ := resolvePlaceholder(n.name, n.modifier, in)
			buf.WriteString(v)
		case nodeBlock:
			if allPlaceholdersPresent(n.primary, in) {
				buf.WriteString(render(n.primary, in))
			} else if n.hasElse {
				buf.WriteString(render(n.fallback, in))
			}
		}
	}
	return buf.String()
}

// allPlaceholdersPresent walks a block's nodes and returns true iff every
// placeholder resolves to a non-empty value. Nested blocks count as present
// when they themselves render to non-empty; unknown placeholder names count
// as present (spec §10 "unknown placeholder left verbatim").
func allPlaceholdersPresent(nodes []node, in Input) bool {
	for _, n := range nodes {
		switch n.kind {
		case nodePlaceholder:
			v, known := resolvePlaceholder(n.name, n.modifier, in)
			if known && strings.TrimSpace(v) == "" {
				return false
			}
		case nodeBlock:
			// A nested block is "present" if it renders to non-empty when
			// evaluated with the current input.
			if strings.TrimSpace(render([]node{n}, in)) == "" {
				return false
			}
		}
	}
	return true
}

func mentionsPlaceholder(nodes []node, name string) bool {
	for _, n := range nodes {
		switch n.kind {
		case nodePlaceholder:
			if n.name == name {
				return true
			}
		case nodeBlock:
			if mentionsPlaceholder(n.primary, name) || mentionsPlaceholder(n.fallback, name) {
				return true
			}
		}
	}
	return false
}

// ------------------------------------------------------------
// Placeholders + modifiers
// ------------------------------------------------------------

// resolvePlaceholder returns (value, known). known=false means the name is
// not a recognized placeholder — callers leave it verbatim in output.
//
// The resolved value is sanitized here (not later) so any `/` in a
// user-supplied title becomes a space instead of a directory break.
// {currentFilename} is an exception — it can legitimately contain a `.` or
// already-sanitized path separator and we want to pass it through unchanged
// so fallbacks like "blank title → currentFilename" don't get mangled.
func resolvePlaceholder(name, modifier string, in Input) (string, bool) {
	raw, known := rawPlaceholder(name, in)
	if !known {
		return "{" + name + optionalMod(modifier) + "}", false
	}
	out := applyModifier(name, modifier, raw, in)
	if name != "currentFilename" {
		out = sanitizePlaceholderValue(out)
	}
	return out, true
}

// sanitizePlaceholderValue scrubs a single resolved placeholder. Invalid
// filesystem characters become nothing, runs of whitespace collapse to one,
// leading/trailing whitespace is trimmed. We deliberately do NOT trim
// trailing dots here — the per-component sanitizer does that once after all
// substitutions, which avoids dropping dots that appear mid-pattern
// (e.g. "{seriesIndex}. {title}" where the literal ". " is from the
// pattern itself).
func sanitizePlaceholderValue(s string) string {
	s = invalidFSChars.ReplaceAllString(s, "")
	s = whitespaceRun.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func rawPlaceholder(name string, in Input) (string, bool) {
	switch name {
	case "title":
		if strings.TrimSpace(in.Title) == "" {
			return in.CurrentFilename, true
		}
		return in.Title, true
	case "subtitle":
		return in.Subtitle, true
	case "authors":
		return joinAuthors(in.Authors), true
	case "year":
		if in.Year <= 0 {
			return "", true
		}
		return strconv.Itoa(in.Year), true
	case "series":
		return in.Series, true
	case "seriesIndex":
		return formatSeriesIndex(in.SeriesIndex), true
	case "language":
		return in.Language, true
	case "publisher":
		return in.Publisher, true
	case "isbn":
		return in.ISBN, true
	case "currentFilename":
		return in.CurrentFilename, true
	case "extension":
		return normalizeExt(in.Extension), true
	}
	return "", false
}

func applyModifier(name, mod, raw string, in Input) string {
	switch mod {
	case "":
		return raw
	case "first":
		// Comma-separated first element. For authors we use the slice
		// directly so "A, B" and ["A", "B"] behave the same.
		if name == "authors" && len(in.Authors) > 0 {
			return strings.TrimSpace(in.Authors[0])
		}
		if i := strings.Index(raw, ","); i >= 0 {
			return strings.TrimSpace(raw[:i])
		}
		return raw
	case "sort":
		// "First Last" → "Last, First". For an author list we only sort
		// the first author; chained authors keep the canonical "A, B" form.
		head := raw
		if name == "authors" && len(in.Authors) > 0 {
			head = strings.TrimSpace(in.Authors[0])
		}
		return lastNameFirst(head)
	case "initial":
		head := raw
		if name == "authors" && len(in.Authors) > 0 {
			head = strings.TrimSpace(in.Authors[0])
		}
		return initial(head)
	case "upper":
		return strings.ToUpper(raw)
	case "lower":
		return strings.ToLower(raw)
	}
	return raw
}

func optionalMod(mod string) string {
	if mod == "" {
		return ""
	}
	return ":" + mod
}

// joinAuthors joins the author slice with ", " and truncates over-budget
// lists to the last rune that fits plus " et al." (spec §6.3).
func joinAuthors(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	joined := strings.Join(trimAll(authors), ", ")
	if byteLen(joined) <= authorBudget {
		return joined
	}
	// Accumulate authors until the next one would push us over budget,
	// then append the marker.
	var out []string
	var used int
	for _, a := range trimAll(authors) {
		add := byteLen(a)
		if len(out) > 0 {
			add += 2 // ", "
		}
		if used+add+byteLen(elideMarker) > authorBudget {
			break
		}
		out = append(out, a)
		used += add
	}
	if len(out) == 0 {
		out = []string{trimAll(authors)[0]}
	}
	return strings.Join(out, ", ") + elideMarker
}

// formatSeriesIndex returns whole numbers zero-padded to 2 digits (01) and
// decimals in the shortest lossless form (02.5). Zero means "no index".
func formatSeriesIndex(v float64) string {
	if v <= 0 {
		return ""
	}
	trunc := float64(int64(v))
	if v == trunc {
		return fmt.Sprintf("%02d", int64(v))
	}
	// "-1" precision emits the shortest representation; zero-pad the whole
	// part to keep lexicographic sort stable.
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if i := strings.Index(s, "."); i >= 0 && i == 1 {
		return "0" + s
	}
	return s
}

// lastNameFirst returns "Last, First [Middle…]". If the input has no spaces
// it's returned unchanged (e.g. "Plato").
func lastNameFirst(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	i := strings.LastIndex(name, " ")
	if i <= 0 {
		return name
	}
	last := strings.TrimSpace(name[i+1:])
	rest := strings.TrimSpace(name[:i])
	if last == "" || rest == "" {
		return name
	}
	return last + ", " + rest
}

// initial returns the first rune of the last name, uppercased.
func initial(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	i := strings.LastIndex(name, " ")
	last := name
	if i >= 0 {
		last = strings.TrimSpace(name[i+1:])
	}
	r, size := utf8.DecodeRuneInString(last)
	if size == 0 {
		return ""
	}
	return strings.ToUpper(string(r))
}

// ------------------------------------------------------------
// Sanitization
// ------------------------------------------------------------

func sanitizeComponent(s string) string {
	s = invalidFSChars.ReplaceAllString(s, "")
	s = whitespaceRun.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	// Trim trailing dots (Windows-hostile) but keep them in folder-based
	// items where they're meaningful. The caller decides whether to pass
	// FolderBased=true, which skips extension append; this rule is per
	// path component so it always trims.
	s = strings.TrimRight(s, ".")
	return s
}

// truncateRunes returns s trimmed to at most budget UTF-8 bytes, with the
// trim happening on a rune boundary so the result is always valid UTF-8.
// Trailing whitespace is stripped, but trailing dots are preserved — the
// caller decides whether it's safe to strip them (filename extensions keep
// their dots; directory names lose them).
func truncateRunes(s string, budget int) string {
	if byteLen(s) <= budget {
		return s
	}
	var used int
	for i, r := range s {
		size := utf8.RuneLen(r)
		if used+size > budget {
			return strings.TrimRight(s[:i], " ")
		}
		used += size
	}
	return s
}

// truncateKeepingExt shrinks the name portion of "name.ext" so the total fits
// budget, preserving the extension verbatim (spec §6.3 filename row).
func truncateKeepingExt(s string, budget int) string {
	ext := filepath.Ext(s)
	if ext == "" || byteLen(ext) >= budget {
		return truncateRunes(s, budget)
	}
	name := strings.TrimSuffix(s, ext)
	nameBudget := budget - byteLen(ext)
	return truncateRunes(name, nameBudget) + ext
}

func byteLen(s string) int { return len(s) }

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func normalizeExt(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	return ext
}
