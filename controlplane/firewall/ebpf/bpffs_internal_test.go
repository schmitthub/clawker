package ebpf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseKernelRelease covers the release strings real distributions
// produce. The gate decides whether privileged syscalls are attempted at
// all, so a parse failure here would either block a capable kernel or, far
// worse, let an incapable one through.
func TestParseKernelRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		release       string
		wantMajor     int
		wantMinor     int
		wantSupported bool
	}{
		{
			name:          "ubuntu hwe with flavour suffix",
			release:       "6.11.0-28-generic",
			wantMajor:     6,
			wantMinor:     11,
			wantSupported: true,
		},
		{
			name:          "exactly the minimum",
			release:       "6.9.0",
			wantMajor:     6,
			wantMinor:     9,
			wantSupported: true,
		},
		{
			name:          "6.8 carries uid/gid but had the token feature reverted",
			release:       "6.8.0-51-generic",
			wantMajor:     6,
			wantMinor:     8,
			wantSupported: false,
		},
		{
			name:          "release candidate, no patch component",
			release:       "6.9-rc1",
			wantMajor:     6,
			wantMinor:     9,
			wantSupported: true,
		},
		{
			name:          "a major bump clears the floor regardless of minor",
			release:       "7.0.0-28-generic",
			wantMajor:     7,
			wantMinor:     0,
			wantSupported: true,
		},
		{
			name:          "older major fails even with a high minor",
			release:       "5.15.0-100-generic",
			wantMajor:     5,
			wantMinor:     15,
			wantSupported: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			version, err := parseKernelRelease(tt.release)
			require.NoError(t, err)
			assert.Equal(t, tt.wantMajor, version.major)
			assert.Equal(t, tt.wantMinor, version.minor)
			assert.Equal(t, tt.wantSupported, version.atLeast(MinKernelMajor, MinKernelMinor),
				"support verdict for %s", tt.release)
		})
	}
}

// TestParseKernelRelease_Malformed pins that unparseable input is an error
// rather than a zero version. A zero version would silently read as "older
// than the floor" and turn a parsing bug into a permanent, unexplained
// refusal to start on a perfectly capable host.
func TestParseKernelRelease_Malformed(t *testing.T) {
	t.Parallel()

	for _, release := range []string{"", "6", "notakernel", "x.y.z", "6.x"} {
		t.Run(release, func(t *testing.T) {
			t.Parallel()

			_, err := parseKernelRelease(release)
			require.Error(t, err, "release %q must not parse", release)
		})
	}
}
