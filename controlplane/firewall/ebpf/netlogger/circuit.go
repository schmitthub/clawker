package netlogger

import (
	"context"
	"sync"
	"sync/atomic"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/schmitthub/clawker/internal/logger"
)

// CircuitOptions configures a circuitExporter. The breaker is
// intentionally permanent: once tripped, all subsequent Export calls
// drop on the floor for the rest of the process lifetime. No probe
// loop, no half-open state.
//
// The rationale: telemetry availability is binary per-CP-lifetime —
// either the collector was up at boot and stayed up, or netlogger is
// dropping. A reconnect loop against a missing collector costs more
// than it earns; operators recover by restarting CP once the
// monitoring stack returns.
type CircuitOptions struct {
	// FailureThreshold is the number of consecutive Export failures
	// that trip the breaker. Default 3.
	FailureThreshold int

	// Log receives the one-shot event=netlogger_collector_lost line
	// emitted on trip. Required for production wiring; nil falls back
	// to logger.Nop for tests that don't assert the log surface.
	Log *logger.Logger
}

// NewCircuitExporter wraps inner with a fail-fast circuit breaker. The
// returned exporter conforms to sdklog.Exporter and is safe for
// concurrent use by the BatchProcessor's export goroutine (single
// caller in practice, but the atomic + mutex guard against future SDK
// changes that parallelize Export).
func NewCircuitExporter(inner sdklog.Exporter, opts CircuitOptions) sdklog.Exporter {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 3
	}
	if opts.Log == nil {
		opts.Log = logger.Nop()
	}
	return &circuitExporter{
		inner:     inner,
		threshold: opts.FailureThreshold,
		log:       opts.Log,
	}
}

// circuitExporter counts consecutive Export failures and permanently
// drops records once the threshold is reached. Once tripped, Export
// returns nil so the BatchProcessor records a successful export and
// the in-flight batch is released — the alternative (returning the
// error indefinitely) would refill the queue many times over and
// starve newer records on the natural drop-oldest path.
type circuitExporter struct {
	inner     sdklog.Exporter
	threshold int
	log       *logger.Logger

	mu              sync.Mutex
	consecutiveFail int
	tripped         atomic.Bool
}

// Export delegates to the inner exporter when the breaker is closed.
// On failure the consecutive-failure counter advances; reaching
// threshold trips the breaker and emits the one-shot log line. After
// trip, Export drops records on the floor.
func (c *circuitExporter) Export(ctx context.Context, recs []sdklog.Record) error {
	if c.tripped.Load() {
		return nil
	}
	err := c.inner.Export(ctx, recs)

	c.mu.Lock()
	defer c.mu.Unlock()
	// Recheck under lock — another goroutine may have tripped between
	// the atomic.Load above and now.
	if c.tripped.Load() {
		return nil
	}
	if err == nil {
		c.consecutiveFail = 0
		return nil
	}
	c.consecutiveFail++
	if c.consecutiveFail >= c.threshold {
		c.tripped.Store(true)
		c.log.Error().
			Err(err).
			Int("failures", c.consecutiveFail).
			Int("threshold", c.threshold).
			Str("event", "netlogger_collector_lost").
			Str("component", "netlogger.circuit").
			Msg("netlogger OTLP exporter tripped after consecutive failures; subsequent records will be dropped until CP restart")
		// Swallow the err on the trip transition so the SDK's
		// BatchProcessor sees a successful export and releases the
		// in-flight batch. Returning err here would surface one
		// more retry/log cycle before the breaker fully takes
		// over — inconsistent with the post-trip drop-on-floor
		// contract documented in CLAUDE.md.
		return nil
	}
	return err
}

// ForceFlush delegates while the breaker is closed; after trip it is a
// no-op because there is nothing in flight worth flushing through a
// known-broken exporter.
func (c *circuitExporter) ForceFlush(ctx context.Context) error {
	if c.tripped.Load() {
		return nil
	}
	return c.inner.ForceFlush(ctx)
}

// Shutdown always delegates so the underlying transport closes its
// gRPC connection regardless of breaker state.
func (c *circuitExporter) Shutdown(ctx context.Context) error {
	return c.inner.Shutdown(ctx)
}
