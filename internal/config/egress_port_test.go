package config

import "testing"

func TestParsePortSpec(t *testing.T) {
	cases := []struct {
		in      string
		wantLo  int
		wantHi  int
		wantSet bool
		wantErr bool
	}{
		{in: "", wantSet: false},    // protocol default
		{in: "   ", wantSet: false}, // whitespace trims to empty
		{in: "443", wantLo: 443, wantHi: 443, wantSet: true},
		{in: "1", wantLo: 1, wantHi: 1, wantSet: true},
		{in: "65535", wantLo: 65535, wantHi: 65535, wantSet: true},
		{in: "9000-9100", wantLo: 9000, wantHi: 9100, wantSet: true},
		{in: "9000-9000", wantLo: 9000, wantHi: 9000, wantSet: true}, // degenerate range == single
		{in: " 9000 - 9100 ", wantLo: 9000, wantHi: 9100, wantSet: true},
		// invalid
		{in: "0", wantErr: true},         // below range
		{in: "65536", wantErr: true},     // above range
		{in: "-1", wantErr: true},        // parsed as range with empty low
		{in: "abc", wantErr: true},       // not a number
		{in: "9100-9000", wantErr: true}, // reversed
		{in: "9000-", wantErr: true},     // missing high
		{in: "-9000", wantErr: true},     // missing low
		{in: "9000-abc", wantErr: true},  // non-numeric high
		{in: "1-70000", wantErr: true},   // high out of range
		{in: "0-9000", wantErr: true},    // low out of range
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			lo, hi, set, err := ParsePortSpec(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParsePortSpec(%q): err=%v, wantErr=%v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if lo != tc.wantLo || hi != tc.wantHi || set != tc.wantSet {
				t.Fatalf("ParsePortSpec(%q) = (%d,%d,%v), want (%d,%d,%v)",
					tc.in, lo, hi, set, tc.wantLo, tc.wantHi, tc.wantSet)
			}
		})
	}
}

func TestEgressRulePortHelpers(t *testing.T) {
	// ParsePortSpec itself is covered exhaustively by TestParsePortSpec. These
	// assertions pin only the wrapper-specific logic the methods add on top of it:
	// SinglePort's single-vs-range gate (lo != hi) and PortSpan's collapse of
	// (err != nil || !set) to ok=false.
	if _, ok := (EgressRule{Port: "443"}).SinglePort(); !ok {
		t.Fatal("SinglePort(single) must be ok")
	}
	if _, ok := (EgressRule{Port: "9000-9100"}).SinglePort(); ok {
		t.Fatal("SinglePort(range) must NOT be ok (lo != hi)")
	}
	if _, _, ok := (EgressRule{Port: "9100-9000"}).PortSpan(); ok {
		t.Fatal("PortSpan(reversed) must collapse the error to ok=false")
	}
}
