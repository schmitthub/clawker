package docker

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/docker/go-units"
)

// MemBytes is a type for human readable memory bytes (like 128M, 2g, etc)
type MemBytes int64

// String returns the string format of the human readable memory bytes
func (m *MemBytes) String() string {
	// NOTE: In spf13/pflag/flag.go, "0" is considered as "zero value" while "0 B" is not.
	// We return "0" in case value is 0 here so that the default value is hidden.
	// (Sometimes "default 0 B" is actually misleading)
	if m.Value() != 0 {
		return units.BytesSize(float64(m.Value()))
	}
	return "0"
}

// Set sets the value of the MemBytes by passing a string
func (m *MemBytes) Set(value string) error {
	val, err := units.RAMInBytes(value)
	*m = MemBytes(val)
	return err
}

// Type returns the type
func (*MemBytes) Type() string {
	return "bytes"
}

// Value returns the value in int64
func (m *MemBytes) Value() int64 {
	return int64(*m)
}

// UnmarshalJSON is the customized unmarshaler for MemBytes
func (m *MemBytes) UnmarshalJSON(s []byte) error {
	if len(s) <= 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("invalid size: %q", s)
	}
	val, err := units.RAMInBytes(string(s[1 : len(s)-1]))
	*m = MemBytes(val)
	return err
}

// MemSwapBytes is a type for human readable memory bytes (like 128M, 2g, etc).
// It differs from MemBytes in that -1 is valid and the default.
type MemSwapBytes int64

// Set sets the value of the MemSwapBytes by passing a string
func (m *MemSwapBytes) Set(value string) error {
	if value == "-1" {
		*m = MemSwapBytes(-1)
		return nil
	}
	val, err := units.RAMInBytes(value)
	*m = MemSwapBytes(val)
	return err
}

// Type returns the type
func (*MemSwapBytes) Type() string {
	return "bytes"
}

// Value returns the value in int64
func (m *MemSwapBytes) Value() int64 {
	return int64(*m)
}

func (m *MemSwapBytes) String() string {
	b := MemBytes(*m)
	return b.String()
}

// UnmarshalJSON is the customized unmarshaler for MemSwapBytes
func (m *MemSwapBytes) UnmarshalJSON(s []byte) error {
	b := MemBytes(*m)
	return b.UnmarshalJSON(s)
}

// NanoCPUs is a type for fixed point fractional number.
type NanoCPUs int64

// String returns the string format of the number
func (c *NanoCPUs) String() string {
	if *c == 0 {
		return ""
	}
	return big.NewRat(c.Value(), 1e9).FloatString(3)
}

// Set sets the value of the NanoCPU by passing a string
func (c *NanoCPUs) Set(value string) error {
	cpus, err := ParseCPUs(value)
	*c = NanoCPUs(cpus)
	return err
}

// Type returns the type
func (*NanoCPUs) Type() string {
	return "decimal"
}

// Value returns the value in int64
func (c *NanoCPUs) Value() int64 {
	return int64(*c)
}

// ParseCPUs takes a string ratio and returns an integer value of nano cpus
func ParseCPUs(value string) (int64, error) {
	cpu, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, fmt.Errorf("failed to parse %v as a rational number", value)
	}
	nano := cpu.Mul(cpu, big.NewRat(1e9, 1))
	if !nano.IsInt() {
		return 0, errors.New("value is too precise")
	}
	return nano.Num().Int64(), nil
}
