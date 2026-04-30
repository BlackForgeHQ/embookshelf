package sidecar

import (
	"encoding/json"
	"fmt"
	"time"
)

// WriteMode tells the reader whether the sidecar holds the full
// edited metadata ("full" — file write was skipped or failed) or
// only the fields the file format couldn't carry ("spillover").
type WriteMode string

const (
	ModeSpillover WriteMode = "spillover"
	ModeFull      WriteMode = "full"
)

// envelope is the on-disk shape. Sidecar fields live under "fields";
// the surrounding keys describe how/when/by-whom the sidecar was
// written so a reader can be tolerant of newer writers.
type envelope struct {
	Version   int       `json:"version"`
	Format    string    `json:"format,omitempty"`
	Mode      WriteMode `json:"mode,omitempty"`
	Fields    Sidecar   `json:"fields"`
	WrittenAt time.Time `json:"written_at,omitempty"`
	Writer    string    `json:"writer,omitempty"`
}

const envelopeVersion = 1

// writerVersion is stamped into every encoded sidecar so the operator
// can debug "which embookshelf wrote this." Kept private; bumped
// alongside the project tag.
var writerVersion = "embookshelf"

// EncodeJSON serializes a Sidecar into the v1 envelope. format is the
// book's format tag (e.g. "EPUB"); mode is "spillover" or "full".
func EncodeJSON(s Sidecar, mode WriteMode, format string) ([]byte, error) {
	env := envelope{
		Version:   envelopeVersion,
		Format:    format,
		Mode:      mode,
		Fields:    s,
		WrittenAt: time.Now().UTC(),
		Writer:    writerVersion,
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sidecar: encode json: %w", err)
	}
	return out, nil
}

// DecodeJSON parses a v1 envelope into a Sidecar. Unknown top-level
// keys are ignored; an unset mode is treated as "spillover"; a higher
// version is logged-by-caller and parsed best-effort with the v1
// shape.
func DecodeJSON(data []byte) (Sidecar, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Sidecar{}, fmt.Errorf("sidecar: decode json: %w", err)
	}
	return env.Fields, nil
}
