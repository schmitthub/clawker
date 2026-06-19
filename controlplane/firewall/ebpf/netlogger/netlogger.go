// Package netlogger drains the BPF events_ringbuf, enriches each
// record with userspace attribution ({container_id, agent, project,
// domain}), and hands the result to a Sink. It is the userspace half
// of the per-decision-point egress event emitter: the BPF programs
// in internal/controlplane/firewall/ebpf/bpf/ submit a fixed-size
// egress_event record at every decision point (connect4/6,
// sendmsg4/6, sock_create); this package shapes those records into
// the OTLP log stream the monitoring backend consumes.
//
// Pipeline shape:
//
//	events_ringbuf (kernel) → reader goroutine → bounded queue →
//	processor goroutine → Sink
//
// The reader's send to the queue is non-blocking with a drop-newest
// fallback — a stalled processor MUST NOT back-pressure the ringbuf
// because that converts userspace queue drops (counted via
// QueueDropped) into kernel-fault drops (counted via events_drops in
// BPF) which are more expensive and harder to attribute.
package netlogger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cilium/ebpf/ringbuf"
	mobyevents "github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/dockerevents"
	ebpf "github.com/schmitthub/clawker/internal/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// defaultQueueBuffer sizes the in-process queue between the reader
// and processor goroutines. 8192 is far larger than the BPF
// ratelimit-bounded ~640 records/sec per cgroup × the modest agent
// count clawker hosts, so steady-state operation stays buffer-
// resident. A burst that overflows this window drops at the
// userspace queue, which is bounded back-pressure; kernel-side drops
// (events_drops) are reserved for catastrophic stalls.
const defaultQueueBuffer = 8192

// defaultStopTimeout is how long Service.Stop waits for the reader
// + processor + reverse-DNS refresher to exit cleanly before it
// returns. Sized below the AgentWatcher drain-callback budget so
// netlogger can never be the long pole during CP teardown.
const defaultStopTimeout = 5 * time.Second

// defaultReverseDNSInterval is the periodic refresh cadence for the
// reverse-DNS observed-hash set. 5s is well under the dns_cache TTL
// horizon and is cheap (one map iterate per tick over a bounded
// pinned map).
const defaultReverseDNSInterval = 5 * time.Second

// inspectTimeout caps the docker ContainerInspect call fired per
// EBPFContainerEnrolled event. Inspect is local-socket and normally
// sub-millisecond; the cap exists so a wedged docker daemon cannot
// pin the subscriber goroutine.
const inspectTimeout = 5 * time.Second

// Deps bundles the collaborators a Service needs. Required fields
// are validated by New; nil-tolerant fields degrade specific
// behaviors but never panic.
type Deps struct {
	// Mgr provides read-only handles to the pinned BPF maps the
	// pipeline drains (events_ringbuf) and observes (dns_cache).
	// Required.
	Mgr *ebpf.Manager

	// Bus is the overseer bus the Service subscribes to for
	// LabelCache hydration (EBPFContainerEnrolled) and eviction
	// (dockerevents.DockerEvent with container/die,destroy).
	// Required — without a bus the cache never populates and
	// every emitted record carries empty attribution.
	Bus *overseer.Overseer

	// Docker is the daemon client used to fetch container labels
	// when an EBPFContainerEnrolled event lands. One inspect per
	// enroll; no userspace response cache (the daemon's own
	// in-memory state makes per-enroll inspects cheap). Required.
	Docker ContainerInspecter

	// Cfg supplies the dev.clawker.{agent,project} label keys
	// (Config interface — never hardcode label strings). Required.
	Cfg config.Config

	// Domains supplies the live set of domains dnsbpf may resolve
	// under the current firewall configuration. ReverseDNSMap hashes
	// each entry on every refresh tick to rebuild the hash→domain
	// table the otelSink reads when stamping `dst_host` on each
	// emitted security record. Production wiring: a closure over
	// firewall.Handler.ReverseDNSDomains (CoreDNS zones + IP-literal
	// dns_cache seeds). Nil is supported —
	// every emitted record then carries dst_host="" (degraded
	// attribution; firewall enforcement unaffected).
	Domains DomainSource

	// OtelLoggerProvider drives the production sink. nil routes
	// every event into nopSink — the test-only default that drops
	// records on the floor. Production wiring in cmd/clawkercp
	// supplies a provider built via controlplane.NewOtelLoggerProvider
	// against the trusted-infra OTLP receiver. The Service does NOT
	// Shutdown the provider on Stop; lifetime is the caller's.
	OtelLoggerProvider *sdklog.LoggerProvider

	// Log captures degraded-path structured lines
	// (event=netlogger_*_unavailable, parse errors, dropped record
	// summaries). Never used for the network event records
	// themselves — those flow through the internal sink. nil
	// defaults to a Nop logger.
	Log *logger.Logger

	// QueueBuffer overrides defaultQueueBuffer. 0 means default.
	QueueBuffer int

	// ReverseDNSInterval overrides defaultReverseDNSInterval. 0
	// means default.
	ReverseDNSInterval time.Duration

	// StopTimeout overrides defaultStopTimeout. 0 means default.
	StopTimeout time.Duration
}

// ContainerInspecter is the moby-API surface netlogger needs to fetch
// container labels at enroll time. *mobyclient.Client satisfies it;
// tests inject a fake. Daemon packages (this one included) are
// permitted to import moby directly under the docker-client.md
// exception.
type ContainerInspecter interface {
	ContainerInspect(ctx context.Context, id string, opts mobyclient.ContainerInspectOptions) (mobyclient.ContainerInspectResult, error)
}

// Service is the long-lived netlogger handle. Constructed via New,
// driven by Start, drained by Stop.
type Service struct {
	deps    Deps
	cache   *LabelCache
	revDNS  *ReverseDNSMap
	metrics *Metrics

	// sink is the live event sink chosen by New. otelSink when
	// deps.OtelLoggerProvider is non-nil; nopSink otherwise.
	sink Sink

	// rb is the live ringbuf.Reader. nil until Start.
	rb *ringbuf.Reader

	queue   chan []byte
	reader  *reader
	process *processor

	// Lifecycle: started once, stopped once. cancel terminates the
	// reverse-DNS refresher, the processor's outer ctx, and both
	// subscriber goroutines. The derived ctx itself flows through
	// closures rather than being held on the struct.
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	cancel    context.CancelFunc

	// unsubs holds the two overseer subscription cancel funcs so
	// Stop can drop them in order.
	unsubs []func()
}

// New validates deps and constructs the Service. Returns
// (nil, error) on missing required deps — clawkercp.go logs
// `event=netlogger_unavailable` and degrades.
func New(deps Deps) (*Service, error) {
	switch {
	case deps.Mgr == nil:
		return nil, errors.New("netlogger: Deps.Mgr required")
	case deps.Bus == nil:
		return nil, errors.New("netlogger: Deps.Bus required")
	case deps.Docker == nil:
		return nil, errors.New("netlogger: Deps.Docker required")
	case deps.Cfg == nil:
		return nil, errors.New("netlogger: Deps.Cfg required")
	}
	if deps.Log == nil {
		deps.Log = logger.Nop()
	}
	if deps.QueueBuffer == 0 {
		deps.QueueBuffer = defaultQueueBuffer
	}
	if deps.ReverseDNSInterval == 0 {
		deps.ReverseDNSInterval = defaultReverseDNSInterval
	}
	if deps.StopTimeout == 0 {
		deps.StopTimeout = defaultStopTimeout
	}

	// nopSink is the test/degraded default. CP main wires a real
	// provider; nil here means "drop records on the floor" and is
	// the same shape the netlogger_unavailable degraded path
	// produces — every fields-required check above takes priority.
	var sink Sink = nopSink{}
	if s := newOtelSink(deps.OtelLoggerProvider); s != nil {
		sink = s
	}

	cache := NewLabelCache(deps.Log)
	revDNS := NewReverseDNSMap(deps.Mgr.DNSCache(), deps.Domains, deps.Log)
	metrics := NewMetrics()

	return &Service{
		deps:    deps,
		cache:   cache,
		revDNS:  revDNS,
		metrics: metrics,
		sink:    sink,
		queue:   make(chan []byte, deps.QueueBuffer),
	}, nil
}

// Metrics returns the Service's Metrics for registration with a
// prometheus.Registerer.
func (s *Service) Metrics() *Metrics { return s.metrics }

// LabelCache returns the LabelCache for direct test inspection.
// Production callers do not touch it; the cache is hydrated by the
// bus subscription wired in Start.
func (s *Service) LabelCache() *LabelCache { return s.cache }

// Start subscribes to the overseer bus and launches the reader +
// processor + reverse-DNS goroutines. Idempotent on success; the
// second call returns nil without doing extra work. The subscription
// wiring uses the FirewallInit re-enrollment sweep to hydrate the
// cache at boot — there is no explicit backfill path.
//
// On ringbuf attach failure, Start returns an error and the Service
// is unusable; clawkercp.go logs degraded and reuses a nopSink for the
// rest of the CP lifetime (no background reconnect).
func (s *Service) Start(ctx context.Context) error {
	var startErr error
	s.startOnce.Do(func() {
		rb, err := ringbuf.NewReader(s.deps.Mgr.EventsRingbuf())
		if err != nil {
			startErr = fmt.Errorf("netlogger: open ringbuf reader: %w", err)
			return
		}
		s.rb = rb

		innerCtx, cancel := context.WithCancel(ctx)
		s.cancel = cancel

		s.reader = &reader{
			src:     s.rb,
			queue:   s.queue,
			metrics: s.metrics,
			log:     s.deps.Log,
		}
		s.process = &processor{
			queue:   s.queue,
			cache:   s.cache,
			revDNS:  s.revDNS,
			sink:    s.sink,
			metrics: s.metrics,
			log:     s.deps.Log,
		}

		// Subscribe BEFORE launching goroutines so any
		// EBPFContainerEnrolled event delivered while the goroutines
		// spin up still lands in the LabelCache.
		s.subscribeBus(innerCtx)

		s.wg.Add(3)
		go func() {
			defer s.wg.Done()
			s.reader.drain()
			// Reader exited → close the queue so the processor's
			// range can drain remaining buffered records then
			// return. Safe to call once; reader is single-goroutine.
			close(s.queue)
		}()
		go func() {
			defer s.wg.Done()
			s.process.run(innerCtx)
		}()
		go func() {
			defer s.wg.Done()
			s.revDNS.Run(innerCtx, s.deps.ReverseDNSInterval)
		}()
	})
	return startErr
}

// Stop drains the pipeline in the order documented in CLAUDE.md:
//  1. Unsubscribe from the overseer bus so no new events feed the
//     LabelCache mid-teardown.
//  2. Close the ringbuf.Reader — reader.drain returns on
//     ringbuf.ErrClosed and closes the queue, which lets the
//     processor drain remaining buffered records before exiting.
//  3. Cancel the inner ctx so the reverse-DNS refresher and the
//     subscriber goroutines (which select on ctx.Done as a
//     belt-and-braces against a bus-closed unsubscribe race) exit.
//  4. Wait on the goroutines with a bounded timeout. Beyond the
//     timeout we proceed so netlogger never blocks CP drain-to-zero;
//     the OS reaps any straggler on exit.
//
// Returns the joined set of errors encountered during the four
// steps so the caller's drain orchestrator can include them in its
// own aggregated drain-error. Idempotent: subsequent calls return
// nil immediately.
func (s *Service) Stop(ctx context.Context) error {
	var errs []error
	s.stopOnce.Do(func() {
		for _, unsub := range s.unsubs {
			unsub()
		}
		if s.rb != nil {
			if err := s.rb.Close(); err != nil {
				s.deps.Log.Warn().
					Err(err).
					Str("event", "netlogger_ringbuf_close_error").
					Msg("ringbuf reader close failed during Stop")
				errs = append(errs, fmt.Errorf("netlogger: ringbuf close: %w", err))
			}
		}
		if s.cancel != nil {
			s.cancel()
		}

		done := make(chan struct{})
		go func() {
			s.wg.Wait()
			close(done)
		}()
		timeout := s.deps.StopTimeout
		select {
		case <-done:
		case <-time.After(timeout):
			s.deps.Log.Warn().
				Dur("timeout", timeout).
				Str("event", "netlogger_stop_timeout").
				Msg("netlogger goroutines did not exit within Stop deadline")
			errs = append(errs, fmt.Errorf("netlogger: goroutines did not exit within %s", timeout))
		case <-ctx.Done():
			s.deps.Log.Warn().
				Err(ctx.Err()).
				Str("event", "netlogger_stop_ctx_cancelled").
				Msg("Stop ctx cancelled before goroutines exited")
			errs = append(errs, fmt.Errorf("netlogger: stop ctx: %w", ctx.Err()))
		}
	})
	return errors.Join(errs...)
}

// subscribeBus wires the two overseer subscriptions netlogger
// depends on. Each subscriber goroutine recovers; a malformed
// event must not strand the BPF programs pinned with no consumer.
// ctx is the inner lifecycle context cancelled by Stop.
func (s *Service) subscribeBus(ctx context.Context) {
	// Enrollment: the firewall handler publishes
	// EBPFContainerEnrolled after a successful FirewallEnable.
	// FirewallInit's re-enrollment sweep at CP boot ALSO calls
	// FirewallEnable, so this single subscription covers both
	// runtime and startup hydration without a separate backfill
	// path.
	enrollSub, ok := overseer.Subscribe[ebpf.EBPFContainerEnrolled](s.deps.Bus, "netlogger.enroll")
	if ok {
		s.unsubs = append(s.unsubs, enrollSub.Unsubscribe)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.deps.Log.Error().
						Interface("panic", r).
						Str("event", "netlogger_enroll_subscriber_panic").
						Msg("netlogger enroll subscriber panicked — LabelCache will not be hydrated")
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, open := <-enrollSub.C:
					if !open {
						return
					}
					s.handleEnroll(ev)
				}
			}
		}()
	} else {
		s.deps.Log.Warn().
			Str("event", "netlogger_enroll_subscribe_failed").
			Msg("overseer Subscribe[EBPFContainerEnrolled] returned false — bus may be closed")
	}

	// Eviction: dockerevents.DockerEvent already flows on the bus
	// today; we filter for container/{die,destroy} and drop the
	// matching LabelCache entry by container_id. Stop / kill / oom
	// are also exit transitions but a container can be `docker
	// start`-ed back after stop — only destroy is irrecoverable.
	// die fires on every exit (incl. before destroy on auto-remove
	// containers), so subscribing to both is the safest superset.
	dieFilter := func(ev dockerevents.DockerEvent) bool {
		if ev.Type != mobyevents.ContainerEventType {
			return false
		}
		switch ev.Action {
		case mobyevents.ActionDie, mobyevents.ActionDestroy:
			return true
		}
		return false
	}
	evictSub, ok := overseer.SubscribeFiltered(s.deps.Bus, "netlogger.evict", dieFilter)
	if ok {
		s.unsubs = append(s.unsubs, evictSub.Unsubscribe)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					s.deps.Log.Error().
						Interface("panic", r).
						Str("event", "netlogger_evict_subscriber_panic").
						Msg("netlogger evict subscriber panicked — stale cache entries may persist")
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case ev, open := <-evictSub.C:
					if !open {
						return
					}
					s.cache.EvictByContainerID(ev.Actor.ID)
				}
			}
		}()
	} else {
		s.deps.Log.Warn().
			Str("event", "netlogger_evict_subscribe_failed").
			Msg("overseer SubscribeFiltered[DockerEvent] returned false — bus may be closed")
	}
}

// handleEnroll resolves a freshly-enrolled container's labels via
// docker ContainerInspect and writes the binding into the
// LabelCache. Inspect failures emit a structured warn line and
// leave the cache without the entry; the next egress event for
// that cgroup_id will land with empty attribution, which the OTel
// sink emits verbatim per the strict directive.
func (s *Service) handleEnroll(ev ebpf.EBPFContainerEnrolled) {
	ctx, cancel := context.WithTimeout(context.Background(), inspectTimeout)
	defer cancel()
	info, err := s.deps.Docker.ContainerInspect(ctx, ev.ContainerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		s.deps.Log.Warn().
			Err(err).
			Str("container_id", ev.ContainerID).
			Uint64("cgroup_id", ev.CgroupID).
			Str("event", "netlogger_inspect_failed").
			Msg("docker ContainerInspect failed during enroll; netlogger record attribution will be empty for this cgroup_id")
		return
	}
	var agent, project string
	if c := info.Container; c.Config != nil {
		agent = c.Config.Labels[s.deps.Cfg.LabelAgent()]
		project = c.Config.Labels[s.deps.Cfg.LabelProject()]
	}
	s.cache.AddOrUpdate(ev.CgroupID, ev.ContainerID, agent, project)
}
