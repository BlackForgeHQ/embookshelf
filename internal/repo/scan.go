// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"encoding/json"
	"fmt"

	"github.com/blackforge/embookshelf/internal/model"
)

// The adapters below exist so a projection's scan destination is always
// the model field itself. Anything that used to happen after the Scan —
// a nullable column landing in a plain string, a JSONB document being
// unmarshaled — happens inside a sql.Scanner instead, which is what lets
// the column list and the destination list be one declaration.
//
// Each adapter names its wrapped field `Dst`, which projection_test.go
// relies on to check that no two columns aim at the same field.

// nullText scans a nullable text or uuid column into a plain string
// field, mapping SQL NULL to "".
type nullText struct{ Dst *string }

func (n nullText) Scan(src any) error {
	if n.Dst == nil {
		return fmt.Errorf("scan text: nil dst")
	}
	switch v := src.(type) {
	case nil:
		*n.Dst = ""
	case string:
		*n.Dst = v
	case []byte:
		*n.Dst = string(v)
	default:
		return fmt.Errorf("scan text: unexpected type %T", src)
	}
	return nil
}

// chaptersJSON decodes the books.chapters JSONB document. SQL NULL, an
// empty payload, an empty array and malformed JSON all leave the field
// nil — the reader treats nil as "no chapter data" and there is nothing
// to tell the four cases apart downstream.
type chaptersJSON struct{ Dst *[]model.Chapter }

func (c chaptersJSON) Scan(src any) error {
	if c.Dst == nil {
		return fmt.Errorf("scan chapters: nil dst")
	}
	raw, err := jsonBytes("chapters", src)
	if err != nil || len(raw) == 0 {
		return err
	}
	var ch []model.Chapter
	if err := json.Unmarshal(raw, &ch); err == nil && len(ch) > 0 {
		*c.Dst = ch
	}
	return nil
}

// shelfRuleJSON decodes the shelves.rule JSONB document. A regular shelf
// stores NULL there, which leaves the pointer nil.
type shelfRuleJSON struct{ Dst **model.ShelfRule }

func (r shelfRuleJSON) Scan(src any) error {
	if r.Dst == nil {
		return fmt.Errorf("scan rule: nil dst")
	}
	raw, err := jsonBytes("rule", src)
	if err != nil || len(raw) == 0 {
		return err
	}
	var rule model.ShelfRule
	if err := json.Unmarshal(raw, &rule); err != nil {
		return fmt.Errorf("decode rule: %w", err)
	}
	*r.Dst = &rule
	return nil
}

// jsonBytes normalises a JSONB column value. The driver hands one over
// as []byte, but a string is equally valid JSON.
func jsonBytes(what string, src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, fmt.Errorf("scan %s: unexpected type %T", what, src)
	}
}
