package netlogger

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// flakyExporter returns a scripted sequence of errors from Export,
// followed by nil for any extra calls. Tracks the Export call count
// so the test can assert the breaker actually stopped delegating.
type flakyExporter struct {
	errs  []error
	calls atomic.Int32
}

func (f *flakyExporter) Export(_ context.Context, _ []sdklog.Record) error {
	n := int(f.calls.Add(1)) - 1
	if n < len(f.errs) {
		return f.errs[n]
	}
	return nil
}

func (f *flakyExporter) ForceFlush(context.Context) error { return nil }
func (f *flakyExporter) Shutdown(context.Context) error   { return nil }

// TestCircuit_TripsAfterThreshold pins the core contract: N
// consecutive Export errors trip the breaker permanently, and
// subsequent Export calls return nil without delegating.
func TestCircuit_TripsAfterThreshold(t *testing.T) {
	inner := &flakyExporter{errs: []error{
		errors.New("collector down 1"),
		errors.New("collector down 2"),
		errors.New("collector down 3"),
	}}
	c := NewCircuitExporter(inner, CircuitOptions{FailureThreshold: 3})

	ctx := context.Background()
	// Calls 1 and 2 propagate the inner error pre-trip; call 3 is
	// the trip transition itself — the breaker swallows that err so
	// the SDK's BatchProcessor records a successful export and
	// releases the in-flight batch instead of retrying past the
	// permanent trip.
	for i := 0; i < 2; i++ {
		if err := c.Export(ctx, nil); err == nil {
			t.Fatalf("call %d: want error pre-trip", i+1)
		}
	}
	if err := c.Export(ctx, nil); err != nil {
		t.Fatalf("call 3 (trip transition): want nil err, got %v", err)
	}

	// After threshold the breaker drops on the floor — Export must
	// return nil AND not delegate (call count stays at 3).
	for i := 0; i < 5; i++ {
		if err := c.Export(ctx, nil); err != nil {
			t.Fatalf("post-trip Export err = %v; want nil", err)
		}
	}
	if got := inner.calls.Load(); got != 3 {
		t.Fatalf("inner.calls = %d; want 3 (no calls after trip)", got)
	}
}

// TestCircuit_ResetsCounterOnSuccess guards against an off-by-one in
// the consecutive counter: a successful Export between failures must
// reset the count, so transient hiccups don't accumulate forever.
func TestCircuit_ResetsCounterOnSuccess(t *testing.T) {
	inner := &flakyExporter{errs: []error{
		errors.New("fail 1"),
		errors.New("fail 2"),
		nil, // success — resets counter
		errors.New("fail 3"),
		errors.New("fail 4"),
	}}
	c := NewCircuitExporter(inner, CircuitOptions{FailureThreshold: 3})

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = c.Export(ctx, nil) // record but don't trip — only 2 consecutive at any point
	}
	// One more failure to confirm we haven't tripped: should delegate
	// (5+1 = 6 inner calls), not drop on the floor.
	_ = c.Export(ctx, nil)
	if got := inner.calls.Load(); got != 6 {
		t.Fatalf("inner.calls = %d; want 6 (breaker must stay closed)", got)
	}
}
