package config

import (
	"fmt"
	"strconv"
	"strings"
)

// maxPort is the largest valid TCP/UDP port number.
const maxPort = 0xffff

// ParsePortSpec interprets the dynamic EgressRule.Port field, converting the
// user-facing string into validated integer bounds.
//
//   - ""          → set=false, err=nil      (caller applies the protocol default)
//   - "443"       → lo==hi==443, set=true
//   - "9000-9100" → lo=9000, hi=9100, set=true (inclusive)
//
// It is the single conversion+validation boundary between the string the user
// writes (in clawker.yaml or `clawker firewall add --port`) and the integers
// the firewall generator/eBPF layer consume. Every malformed value is rejected
// here with a descriptive error so an invalid spec can be surfaced and dropped
// rather than silently collapsing to a protocol default (which would widen
// egress beyond what the user intended):
//
//   - non-numeric           → "not a number"
//   - port < 1 or > 65535   → "out of range"
//   - reversed range (lo>hi)→ "must be ordered low-high"
//   - malformed range shape → "expected lo-hi"
func ParsePortSpec(s string) (lo, hi int, set bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false, nil
	}

	// A range is exactly one dash-separated lo-hi pair; anything else is malformed.
	if strings.Contains(s, "-") {
		left, right, _ := strings.Cut(s, "-")
		lo, err = parsePortNumber(strings.TrimSpace(left))
		if err != nil {
			return 0, 0, false, fmt.Errorf("invalid port range %q: low port %v", s, err)
		}
		hi, err = parsePortNumber(strings.TrimSpace(right))
		if err != nil {
			return 0, 0, false, fmt.Errorf("invalid port range %q: high port %v", s, err)
		}
		if lo > hi {
			return 0, 0, false, fmt.Errorf("invalid port range %q: must be ordered low-high (got %d-%d)", s, lo, hi)
		}
		return lo, hi, true, nil
	}

	p, err := parsePortNumber(s)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid port %q: %v", s, err)
	}
	return p, p, true, nil
}

// parsePortNumber parses a single bounds-checked port number.
func parsePortNumber(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("expected lo-hi")
	}
	p, convErr := strconv.Atoi(s)
	if convErr != nil {
		return 0, fmt.Errorf("not a number")
	}
	if p < 1 || p > maxPort {
		return 0, fmt.Errorf("out of range (must be 1-%d)", maxPort)
	}
	return p, nil
}

// ValidatePortSpec returns a descriptive error when the rule's Port field is
// malformed, and nil when it is empty (protocol default) or a valid single
// port / range.
func (r EgressRule) ValidatePortSpec() error {
	_, _, _, err := ParsePortSpec(r.Port)
	return err
}

// PortSpan returns the inclusive [lo,hi] port span the rule's Port field
// denotes. ok is false when Port is empty OR invalid (use ValidatePortSpec to
// distinguish). A single port yields lo==hi; a range yields lo<=hi.
func (r EgressRule) PortSpan() (lo, hi int, ok bool) {
	lo, hi, set, err := ParsePortSpec(r.Port)
	if err != nil || !set {
		return 0, 0, false
	}
	return lo, hi, true
}

// SinglePort returns (port,true) only when Port names exactly one port. An
// empty, ranged, or invalid Port returns (0,false).
func (r EgressRule) SinglePort() (int, bool) {
	lo, hi, ok := r.PortSpan()
	if !ok || lo != hi {
		return 0, false
	}
	return lo, true
}
