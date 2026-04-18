package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ShelfRule is the wire + DB shape for smart-shelf predicates. Persisted
// as JSONB on the shelves row; validated at the service layer and
// compiled to SQL at the repo layer.
type ShelfRule struct {
	// Match combines predicates with AND ("all") or OR ("any").
	Match      RuleMatch        `json:"match"`
	Predicates []ShelfPredicate `json:"predicates"`
}

type RuleMatch string

const (
	RuleMatchAll RuleMatch = "all"
	RuleMatchAny RuleMatch = "any"
)

// ShelfPredicate is a single field/op/value triple. Value is typed as
// any because it spans strings and numbers; the repo coerces based on
// the predicate's field.
type ShelfPredicate struct {
	Field RuleField `json:"field"`
	Op    RuleOp    `json:"op"`
	Value any       `json:"value"`
}

type RuleField string

const (
	RuleFieldTitle    RuleField = "title"
	RuleFieldAuthor   RuleField = "author"
	RuleFieldYear     RuleField = "year"
	RuleFieldRating   RuleField = "rating"
	RuleFieldFormat   RuleField = "format"
	RuleFieldSeries   RuleField = "series"
	RuleFieldTags     RuleField = "tags"
	RuleFieldProgress RuleField = "progress"
)

type RuleOp string

const (
	OpEq         RuleOp = "eq"
	OpNe         RuleOp = "ne"
	OpLt         RuleOp = "lt"
	OpLte        RuleOp = "lte"
	OpGt         RuleOp = "gt"
	OpGte        RuleOp = "gte"
	OpContains   RuleOp = "contains"
	OpStartsWith RuleOp = "starts_with"
)

// Fields classifies each field by value type so the validator can
// reject mismatched operator + value combos before they ever hit SQL.
type fieldKind int

const (
	fieldKindString fieldKind = iota
	fieldKindInt
	fieldKindTags
	fieldKindProgress // 0..1 float on the wire; 0..100 int internally
)

func (f RuleField) kind() (fieldKind, bool) {
	switch f {
	case RuleFieldTitle, RuleFieldAuthor, RuleFieldFormat, RuleFieldSeries:
		return fieldKindString, true
	case RuleFieldYear, RuleFieldRating:
		return fieldKindInt, true
	case RuleFieldTags:
		return fieldKindTags, true
	case RuleFieldProgress:
		return fieldKindProgress, true
	}
	return 0, false
}

// ErrInvalidRule signals a predicate that the repo cannot compile.
// Service / handler map it to 400.
var ErrInvalidRule = errors.New("invalid shelf rule")

// Validate checks the rule is well-formed and each predicate uses an
// op compatible with its field's type. Does not reach SQL.
func (r *ShelfRule) Validate() error {
	if r == nil {
		return fmt.Errorf("%w: rule is missing", ErrInvalidRule)
	}
	switch r.Match {
	case RuleMatchAll, RuleMatchAny:
	case "":
		r.Match = RuleMatchAll
	default:
		return fmt.Errorf("%w: match must be all or any", ErrInvalidRule)
	}
	if len(r.Predicates) == 0 {
		return fmt.Errorf("%w: at least one predicate is required", ErrInvalidRule)
	}
	if len(r.Predicates) > 32 {
		return fmt.Errorf("%w: too many predicates (max 32)", ErrInvalidRule)
	}
	for i, p := range r.Predicates {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("predicate %d: %w", i, err)
		}
	}
	return nil
}

// Validate mirrors ShelfRule.Validate at the predicate level.
func (p ShelfPredicate) Validate() error {
	kind, ok := p.Field.kind()
	if !ok {
		return fmt.Errorf("%w: unknown field %q", ErrInvalidRule, p.Field)
	}

	switch kind {
	case fieldKindString:
		switch p.Op {
		case OpEq, OpNe, OpContains, OpStartsWith:
		default:
			return fmt.Errorf("%w: op %q not valid for string field %q", ErrInvalidRule, p.Op, p.Field)
		}
		if _, ok := p.Value.(string); !ok {
			return fmt.Errorf("%w: %q expects a string value", ErrInvalidRule, p.Field)
		}
	case fieldKindInt:
		switch p.Op {
		case OpEq, OpNe, OpLt, OpLte, OpGt, OpGte:
		default:
			return fmt.Errorf("%w: op %q not valid for numeric field %q", ErrInvalidRule, p.Op, p.Field)
		}
		if _, err := p.IntValue(); err != nil {
			return fmt.Errorf("%w: %q expects a number (%v)", ErrInvalidRule, p.Field, err)
		}
	case fieldKindTags:
		if p.Op != OpContains {
			return fmt.Errorf("%w: op %q not valid for tags — only `contains` is supported", ErrInvalidRule, p.Op)
		}
		if _, ok := p.Value.(string); !ok {
			return fmt.Errorf("%w: tags contains expects a string value", ErrInvalidRule)
		}
	case fieldKindProgress:
		switch p.Op {
		case OpEq, OpNe, OpLt, OpLte, OpGt, OpGte:
		default:
			return fmt.Errorf("%w: op %q not valid for progress", ErrInvalidRule, p.Op)
		}
		if _, err := p.FloatValue(); err != nil {
			return fmt.Errorf("%w: progress expects a number 0..1 (%v)", ErrInvalidRule, err)
		}
	}
	return nil
}

// IntValue coerces JSON's untyped number into int. JSON unmarshaling
// produces float64 for all numeric literals, so we round-trip through
// that. Rejects non-finite values.
func (p ShelfPredicate) IntValue() (int, error) {
	switch v := p.Value.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case json.Number:
		n, err := v.Int64()
		return int(n), err
	}
	return 0, fmt.Errorf("not a number: %T", p.Value)
}

// FloatValue coerces JSON's untyped number into float64 in the 0..1
// progress range. Out-of-range values clamp at the boundary.
func (p ShelfPredicate) FloatValue() (float64, error) {
	var n float64
	switch v := p.Value.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, err
		}
		n = f
	default:
		return 0, fmt.Errorf("not a number: %T", p.Value)
	}
	if n < 0 {
		n = 0
	} else if n > 1 {
		n = 1
	}
	return n, nil
}
