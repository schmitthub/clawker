package adminclient

import (
	"testing"
	"time"
)

// TestClockSkew verifies the offset math used to align the CLI's minted
// assertion to the CP's clock. The local reference is the request-window
// midpoint so symmetric round-trip latency cancels out.
func TestClockSkew(t *testing.T) {
	// Fixed local window: t0 .. t1, midpoint = t0 + 1ms.
	t0 := time.Unix(1_000_000, 0)
	t1 := t0.Add(2 * time.Millisecond)
	mid := t0.Add(1 * time.Millisecond)

	for _, tc := range []struct {
		name string
		off  time.Duration // CP clock relative to local midpoint
	}{
		{"cp ahead 90s", 90 * time.Second},
		{"cp behind 90s", -90 * time.Second},
		{"cp aligned", 0},
		{"cp behind 5m (post-sleep drift)", -5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cpUnixNanos := mid.Add(tc.off).UnixNano()
			got := clockSkew(t0, t1, cpUnixNanos)
			// skew is what we add to the local clock to get CP time, so it
			// equals the CP-vs-midpoint offset exactly.
			if got != tc.off {
				t.Fatalf("clockSkew = %s, want %s", got, tc.off)
			}
		})
	}
}

// TestClockSkew_DiscountsLatency confirms the midpoint reference makes the
// skew independent of round-trip duration: the same true offset yields the
// same skew whether the call took 1ms or 1s.
func TestClockSkew_DiscountsLatency(t *testing.T) {
	base := time.Unix(2_000_000, 0)
	const trueOffset = 42 * time.Second

	skewFor := func(rtt time.Duration) time.Duration {
		t0 := base
		t1 := base.Add(rtt)
		mid := t0.Add(rtt / 2)
		return clockSkew(t0, t1, mid.Add(trueOffset).UnixNano())
	}

	fast := skewFor(1 * time.Millisecond)
	slow := skewFor(1 * time.Second)
	if fast != trueOffset || slow != trueOffset {
		t.Fatalf("skew should be latency-independent: fast=%s slow=%s want=%s", fast, slow, trueOffset)
	}
}
