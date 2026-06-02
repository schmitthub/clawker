package firewall

import "testing"

func TestValidatePortFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		port    string
		wantErr bool
	}{
		{"empty means protocol default", "", false},
		{"valid low", "1", false},
		{"valid https", "443", false},
		{"valid high", "65535", false},
		{"valid range", "9000-9100", false},
		{"single-port range", "9000-9000", false},
		{"not a number", "abc", true},
		{"above max", "65536", true},
		{"zero invalid", "0", true},
		{"reversed range", "9100-9000", true},
		{"range out of bounds", "1-70000", true},
		{"malformed range", "9000-", true},
		{"non-numeric range bound", "9000-abc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validatePortFlag(tc.port)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePortFlag(%q): err=%v, wantErr=%v", tc.port, err, tc.wantErr)
			}
		})
	}
}
