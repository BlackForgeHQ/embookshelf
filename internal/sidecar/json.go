package sidecar

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sync"
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

var (
	writerVersionOnce sync.Once
	writerVersionStr  string
)

// writerString returns the writer identifier stamped into every
// envelope: "embookshelf/<version>" when the binary is built from a
// tagged release, "embookshelf" alone when version metadata is
// unavailable (e.g. `go run` or `go test`).
func writerString() string {
	writerVersionOnce.Do(func() {
		writerVersionStr = "embookshelf"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			writerVersionStr = "embookshelf/" + info.Main.Version
		}
	})
	return writerVersionStr
}

// EncodeJSON serializes a Sidecar into the v1 envelope. format is the
// book's format tag (e.g. "EPUB"); mode is "spillover" or "full".
func EncodeJSON(s Sidecar, mode WriteMode, format string) ([]byte, error) {
	env := envelope{
		Version:   envelopeVersion,
		Format:    format,
		Mode:      mode,
		Fields:    s,
		WrittenAt: time.Now().UTC(),
		Writer:    writerString(),
	}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sidecar: encode json: %w", err)
	}
	return out, nil
}

// DecodeJSON parses a v1 envelope into a Sidecar. This is a strict
// decoder — malformed JSON returns an error, unknown top-level keys
// in an otherwise-valid envelope are ignored (stdlib default).
//
// Spec §4.2's read-time tolerance rules — "missing mode → spillover",
// "malformed → empty sidecar", "version > 1 → warn + best-effort" —
// are NOT applied here. Those policies live in reader.Read so the
// caller can log warnings, fall back to neighbor sidecars, etc.
// Treat this function as a low-level envelope primitive.
func DecodeJSON(data []byte) (Sidecar, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Sidecar{}, fmt.Errorf("sidecar: decode json: %w", err)
	}
	return env.Fields, nil
}
