package ebpf

import (
	"context"
	"sync"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

const (
	// dnsGCInterval is how often the GC sweeps expired entries out of the
	// pinned dns_cache. CoreDNS (dnsbpf) writes one entry per resolved A
	// record and nothing else reclaims them, so without this sweep the map
	// grows unbounded over the CP lifetime and entries for since-removed
	// zones linger as orphaned hashes (surfacing via netlogger as
	// event=netlogger_reverse_dns_unattributed). IP-literal seeds are
	// protected by GarbageCollectDNS (m.seededIPs), so a live IP rule's
	// route is never reclaimed out from under it.
	dnsGCInterval = 60 * time.Second
	// dnsGCDegradedThreshold is how many consecutive failed sweeps escalate the
	// per-sweep dns_gc_panic/dns_gc_error into a distinct dns_gc_degraded line. A
	// failed sweep is one that panicked OR that GarbageCollectDNS reported an
	// error for (a wedged iterator, or expired entries that could not be
	// deleted). A single bad pass is noise; a sweep that fails every interval
	// means the map is no longer being reclaimed at all (the unbounded-growth
	// failure this goroutine exists to prevent), which an operator must be able
	// to tell apart from one transient hiccup in the greppable log surface.
	dnsGCDegradedThreshold = 5
)

// DNSGCOpts tunes the DNS garbage collector. A zero value is valid: each unset
// field falls back to the package default (dnsGCInterval / dnsGCDegradedThreshold),
// so the orchestrator can construct with DNSGCOpts{} for production behavior and
// tests can drive a fast interval / small threshold.
type DNSGCOpts struct {
	// Interval is how often a sweep runs. <= 0 uses dnsGCInterval.
	Interval time.Duration
	// DegradedThreshold is the consecutive-failure count that emits
	// dns_gc_degraded. <= 0 uses dnsGCDegradedThreshold.
	DegradedThreshold int
}

// DNSGarbageCollector periodically reclaims expired entries the CoreDNS dnsbpf
// plugin wrote into the pinned dns_cache so the map does not grow unbounded and
// stale orphaned hashes do not accumulate. It recovers per the CP no-panic
// discipline: the recover is per-sweep, so a single panicking sweep is logged
// and the loop keeps governing the map rather than surrendering it (which would
// strand the map unsupervised until CP restart).
type DNSGarbageCollector struct {
	gc        func() (int, error)
	log       *logger.Logger
	interval  time.Duration
	threshold int
}

// NewDNSGarbageCollector builds a collector that sweeps mgr's dns_cache via
// GarbageCollectDNS. opts may be the zero value for default tuning.
func NewDNSGarbageCollector(mgr *Manager, log *logger.Logger, opts DNSGCOpts) *DNSGarbageCollector {
	interval := opts.Interval
	if interval <= 0 {
		interval = dnsGCInterval
	}
	threshold := opts.DegradedThreshold
	if threshold <= 0 {
		threshold = dnsGCDegradedThreshold
	}
	return &DNSGarbageCollector{
		gc:        mgr.GarbageCollectDNS,
		log:       log,
		interval:  interval,
		threshold: threshold,
	}
}

// Start launches the sweep loop on a goroutine governed by ctx and returns a
// stop func. The loop stops on ctx.Done() (SIGTERM/drain-to-zero). The returned
// stop cancels the loop and waits for any in-flight sweep to finish before the
// BPF map fd it iterates/deletes is torn down; it is sync.Once-guarded so the
// drain callback (before FlushAll) and a deferred call (LIFO before
// Manager.Close shuts the fd) can both invoke it.
func (d *DNSGarbageCollector) Start(ctx context.Context) (stop func()) {
	loopCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(d.interval)
		defer ticker.Stop()
		escalator := dnsGCEscalator{threshold: d.threshold}
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				if escalator.record(dnsGCSweep(d.gc, d.log)) {
					d.log.Error().Int("consecutive_failures", escalator.failures).
						Str("event", "dns_gc_degraded").
						Msg("dns_cache gc has failed every sweep — map no longer being reclaimed, may grow unbounded")
				}
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			wg.Wait()
		})
	}
}

// dnsGCEscalator tracks the streak of consecutive failed dns_cache GC sweeps
// and reports the single tick on which that streak first reaches threshold, so
// the caller emits the "wedged GC" line exactly once per crossing rather than
// every interval. Extracted from the GC goroutine so the off-by-one-prone
// "escalate once, reset on success" logic is unit-testable without driving the
// whole loop.
type dnsGCEscalator struct {
	threshold int
	failures  int
}

// record folds one sweep outcome into the streak and returns true only on the
// tick where the consecutive-failure count first equals the threshold. A
// successful sweep (ok=true) resets the streak; the strict == means later ticks
// in the same failing streak do not re-fire.
func (e *dnsGCEscalator) record(ok bool) (escalate bool) {
	if ok {
		e.failures = 0
		return false
	}
	e.failures++
	return e.failures == e.threshold
}

// dnsGCSweep runs one dns_cache GC pass and reports whether it succeeded, per
// the CP no-panic discipline. The recover is the load-bearing part: a panicking
// sweep is logged (event=dns_gc_panic) and counted as a failure (ok=false)
// without tearing down the caller's ticker loop, so the loop keeps governing the
// map rather than surrendering it until CP restart. It also returns false when
// GarbageCollectDNS reports it could not reclaim (wedged iterator / failed
// deletes). A clean sweep that simply had nothing to reclaim (n==0, err==nil) is
// success (ok=true) — only a panic or a non-nil error counts as a failed sweep.
// Extracted from the GC goroutine so the panic→false and error→false mapping is
// unit-testable without driving the whole loop. gc is normally
// Manager.GarbageCollectDNS.
func dnsGCSweep(gc func() (int, error), log *logger.Logger) (ok bool) {
	ok = true
	defer func() {
		if r := recover(); r != nil {
			ok = false
			log.Error().Interface("panic", r).
				Str("event", "dns_gc_panic").
				Msg("dns_cache gc sweep panicked — skipping this pass, loop continues")
		}
	}()
	n, err := gc()
	if err != nil {
		log.Warn().Err(err).
			Str("event", "dns_gc_error").
			Msg("dns_cache gc sweep could not reclaim — map may not be shrinking, loop continues")
		return false
	}
	if n > 0 {
		log.Debug().Int("cleared", n).
			Str("event", "dns_gc_swept").
			Msg("dns_cache gc removed expired entries")
	}
	return ok
}
