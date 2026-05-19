package consts

import (
	"os"
	"testing"
)

// TestResolveHostUID exercises every guard in the resolver. Each case
// targets a distinct branch:
//   - env_set_numeric: happy path (positive integer)
//   - env_unset: missing env, must return fallback (CLI process state)
//   - env_set_zero: sudo'd CLI propagates UID 0; must reject (defeats
//     drop-priv contract if userStage runs as root)
//   - env_negative: must reject
//   - env_malformed: strconv.Atoi error must reject
func TestResolveHostUID(t *testing.T) {
	cases := []struct {
		name     string
		envVal   string
		envSet   bool
		fallback int
		want     int
	}{
		{name: "env_set_numeric", envVal: "1234", envSet: true, fallback: 1001, want: 1234},
		{name: "env_unset", envSet: false, fallback: 1001, want: 1001},
		{name: "env_set_zero", envVal: "0", envSet: true, fallback: 1001, want: 1001},
		{name: "env_negative", envVal: "-1", envSet: true, fallback: 1001, want: 1001},
		{name: "env_malformed", envVal: "notanumber", envSet: true, fallback: 1001, want: 1001},
	}
	const probeEnv = "CLAWKER_TEST_PROBE_UID"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv(probeEnv, tc.envVal)
			} else {
				if err := os.Unsetenv(probeEnv); err != nil {
					t.Fatalf("unset env: %v", err)
				}
			}
			if got := resolveHostUID(probeEnv, tc.fallback); got != tc.want {
				t.Fatalf("resolveHostUID(%q, %d) = %d, want %d", tc.envVal, tc.fallback, got, tc.want)
			}
		})
	}
}
