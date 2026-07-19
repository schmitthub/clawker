package consts

import (
	"testing"
)

// TestResolveHostID exercises every branch of the resolver. Each case
// pins both the returned int and the structured Reason that callers
// (the CP daemon's startup gate) key off when surfacing degraded mode
// via their own logger.
//
// All cases drive the env through t.Setenv so the prior value (if any)
// is restored at test end — no os.Unsetenv, which would leak across
// tests in this package if CLAWKER_TEST_PROBE_UID were ever set in
// the parent env. The resolver can't distinguish unset from set-to-""
// (os.Getenv returns "" for both), so the "empty" case is the
// canonical exercise of the unset/empty branch.
func TestResolveHostID(t *testing.T) {
	cases := []struct {
		name         string
		envVal       string
		wantValue    uint32
		wantFallback bool
		wantReason   string
	}{
		{name: "happy_positive", envVal: "1234", wantValue: 1234, wantFallback: false, wantReason: ""},
		{name: "empty", envVal: "", wantValue: 1001, wantFallback: true, wantReason: "unset"},
		// Zero is rejected so a sudo'd CLI cannot propagate root into
		// userStage; root inside the agent defeats the drop-priv
		// contract of the entire init pipeline.
		{name: "zero", envVal: "0", wantValue: 1001, wantFallback: true, wantReason: "non_positive"},
		// ParseUint rejects negatives at the parser, so the "negative"
		// fallback Reason is "malformed" (ErrSyntax), not "non_positive".
		{name: "negative", envVal: "-1", wantValue: 1001, wantFallback: true, wantReason: "malformed"},
		{name: "malformed", envVal: "notanumber", wantValue: 1001, wantFallback: true, wantReason: "malformed"},
		// 2^32 sits 1 past uid_t's ceiling and would silently wrap to 0
		// on a downstream uint32 cast. ParseUint(_, 10, 32) makes it
		// "malformed" with ErrRange so userStage gets the fallback UID
		// instead of UID 0 (root).
		{
			name:         "out_of_uint32_range",
			envVal:       "4294967296",
			wantValue:    1001,
			wantFallback: true,
			wantReason:   "malformed",
		},
	}
	const probeEnv = "CLAWKER_TEST_PROBE_UID"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(probeEnv, tc.envVal)
			gotV, gotRes := resolveHostID(probeEnv, 1001)
			if gotV != tc.wantValue {
				t.Fatalf("value = %d, want %d", gotV, tc.wantValue)
			}
			if gotRes.Value != tc.wantValue {
				t.Fatalf("res.Value = %d, want %d", gotRes.Value, tc.wantValue)
			}
			if gotRes.Fallback != tc.wantFallback {
				t.Fatalf("res.Fallback = %v, want %v", gotRes.Fallback, tc.wantFallback)
			}
			if gotRes.Reason != tc.wantReason {
				t.Fatalf("res.Reason = %q, want %q", gotRes.Reason, tc.wantReason)
			}
			if tc.wantReason == "malformed" && gotRes.Err == nil {
				t.Fatalf("res.Err = nil, want non-nil for malformed input")
			}
			if gotRes.Raw != tc.envVal {
				t.Fatalf("res.Raw = %q, want %q", gotRes.Raw, tc.envVal)
			}
		})
	}
}
