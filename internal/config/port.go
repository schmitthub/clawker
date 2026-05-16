package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Port is a TCP/UDP port number constrained to the valid 1–65535 range.
// UnmarshalYAML rejects out-of-range values at parse time, so callers
// reading a Port from a storage.Store snapshot can rely on the invariant
// without defensive `<= 0` guards. Stores wired with
// WithDefaultsFromStruct[T]() backfill missing fields with the schema's
// `default` tag value, never zero.
//
// Programmatic mutation via Store.Set bypasses UnmarshalYAML — callers
// writing a Port directly are responsible for staying in range.
// Validation re-engages on the next file load or Refresh.
type Port int

func (p *Port) UnmarshalYAML(node *yaml.Node) error {
	var v int
	if err := node.Decode(&v); err != nil {
		return fmt.Errorf("port: %w", err)
	}
	if v < 1 || v > 65535 {
		return fmt.Errorf("port %d out of range (must be 1-65535)", v)
	}
	*p = Port(v)
	return nil
}
