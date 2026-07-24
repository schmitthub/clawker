package dnsbpf

import (
	"testing"

	"github.com/coredns/caddy"
	"github.com/stretchr/testify/require"
)

// TestSetup_ParseErrors covers the directive-parsing failure branches. All of
// them return before the shared BPF map is opened, so they run without a
// kernel and without poisoning the package-level [sync.Once].
func TestSetup_ParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"missing arg", "dnsbpf", "argument count"},
		{"non-numeric", "dnsbpf abc", "invalid route identity"},
		{"zero identity", "dnsbpf 0", "invalid route identity"},
		{"extra arg", "dnsbpf 261 262", "argument count"},
		{"duplicate directive", "dnsbpf 261\ndnsbpf 262", "once per Server Block"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := setup(caddy.NewTestController("dns", tc.input))
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
