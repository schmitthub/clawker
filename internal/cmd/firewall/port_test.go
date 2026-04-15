package firewall

import "testing"

func TestValidatePortFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		port    int
		wantErr bool
	}{
		{"zero means protocol default", 0, false},
		{"valid low", 1, false},
		{"valid https", 443, false},
		{"valid high", 65535, false},
		{"negative one wraps to huge uint32", -1, true},
		{"int32 min", -2147483648, true},
		{"above max", 65536, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validatePortFlag(tc.port)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validatePortFlag(%d): err=%v, wantErr=%v", tc.port, err, tc.wantErr)
			}
		})
	}
}
