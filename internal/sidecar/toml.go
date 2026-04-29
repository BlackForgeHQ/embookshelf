package sidecar

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// ParseTOML deserializes TOML bytes into a Sidecar.
func ParseTOML(data []byte) (Sidecar, error) {
	var s Sidecar
	if err := toml.Unmarshal(data, &s); err != nil {
		return Sidecar{}, fmt.Errorf("sidecar: parse toml: %w", err)
	}
	return s, nil
}

// EncodeTOML serializes a Sidecar to TOML for atomic write.
func EncodeTOML(s Sidecar) ([]byte, error) {
	out, err := toml.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("sidecar: encode toml: %w", err)
	}
	return out, nil
}
