// Package netlogger drains the BPF events_ringbuf, enriches each
// record with userspace attribution ({container_id, agent, project,
// domain}), and hands the result to a Sink. It is the userspace half
// of the per-decision-point egress event emitter: the BPF programs
// in controlplane/firewall/ebpf/bpf/ submit a fixed-size
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

	"github.com/schmitthub/clawker/controlplane/dockerevents"
	ebpf "github.com/schmitthub/clawker/controlplane/firewall/ebpf"
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/config"
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

	// EnrolledTopic is the typed topic the Service subscribes to for
	// LabelCache hydration. The firewall handler publishes
	// EBPFContainerEnrolled on it after a successful FirewallEnable.
	// Required — without it the cache never populates and every
	// emitted record carries empty attribution.
	EnrolledTopic *pubsub.Topic[ebpf.EBPFContainerEnrolled]

	// EvictTopic is the typed Docker-event topic the Service
	// subscribes to for LabelCache eviction. The handler filters for
	// container/{die,destroy} in its subscriber closure and drops the
	// matching entry by container_id. Required — without it stale
	// labels survive cgroup-id reuse.
	EvictTopic *pubsub.Topic[dockerevents.DockerEvent]

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
	// reverse-DNS refresher and the processor's outer ctx. The derived
	// ctx itself flows through closures rather than being held on the
	// struct.
	//
	// The enroll/evict subscriptions are owned by the injected topics:
	// Topic.Subscribe spins up the per-subscriber drain goroutine and
	// bounded buffer, and the orchestrator's Topic.Close tears every
	// subscriber down. The Service holds no per-subscription unsubscribe
	// handle and starts no subscriber goroutine of its own.
	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	cancel    context.CancelFunc
}

// New validates deps and constructs the Service. Returns
// (nil, error) on missing required deps — clawkercp.go logs
// `event=netlogger_unavailable` and degrades.
func New(deps Deps) (*Service, error) {
	switch {
	case deps.Mgr == nil:
		return nil, errors.New("netlogger: Deps.Mgr required")
	case deps.EnrolledTopic == nil:
		return nil, errors.New("netlogger: Deps.EnrolledTopic required")
	case deps.EvictTopic == nil:
		return nil, errors.New("netlogger: Deps.EvictTopic required")
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

// Start subscribes to the enroll + evict topics and launches the reader +
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
		s.subscribeBus()

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
//  1. Close the ringbuf.Reader — reader.drain returns on
//     ringbuf.ErrClosed and closes the queue, which lets the
//     processor drain remaining buffered records before exiting.
//  2. Cancel the inner ctx so the reverse-DNS refresher exits on its
//     next tick.
//  3. Wait on the goroutines with a bounded timeout. Beyond the
//     timeout we proceed so netlogger never blocks CP drain-to-zero;
//     the OS reaps any straggler on exit.
//
// Enroll/evict subscription teardown is NOT done here — the injected
// topics own it. The orchestrator's Topic.Close drops every subscriber
// at drain-to-zero. Stop confines itself to what the Service launched.
//
// Returns the joined set of errors encountered during the four
// steps so the caller's drain orchestrator can include them in its
// own aggregated drain-error. Idempotent: subsequent calls return
// nil immediately.
func (s *Service) Stop(ctx context.Context) error {
	var errs []error
	s.stopOnce.Do(func() {
		// Subscription teardown is owned by the injected topics: the
		// orchestrator's Topic.Close drops every subscriber. The
		// Service only tears down what it owns — the ringbuf reader and
		// the goroutines it launched.
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

// subscribeBus wires the two topic subscriptions netlogger depends on.
// Each subscription's delivery runs under the Topic's built-in
// per-delivery recover, so a malformed event cannot strand the pinned
// BPF programs with no consumer. The Topic owns the per-subscriber
// goroutine + bounded buffer; the orchestrator's Topic.Close tears them
// down at drain-to-zero, so the Service holds no unsubscribe handle.
func (s *Service) subscribeBus() {
	// Enrollment: the firewall handler publishes EBPFContainerEnrolled
	// after a successful FirewallEnable. FirewallInit's re-enrollment
	// sweep at CP boot ALSO calls FirewallEnable, so this single
	// subscription covers both runtime and startup hydration without a
	// separate backfill path.
	//
	// Topic.Subscribe owns the per-subscriber drain goroutine, its
	// bounded buffer (drop-oldest on overflow), and a per-delivery
	// recover — a panic in handleEnroll is contained to that one event
	// and cannot strand eBPF. The orchestrator's Topic.Close tears the
	// subscriber down at drain-to-zero.
	s.deps.EnrolledTopic.Subscribe(func(evt pubsub.Event[ebpf.EBPFContainerEnrolled]) {
		s.handleEnroll(evt.Payload)
	})

	// Eviction: dockerevents.DockerEvent flows on the evict topic. The
	// die/destroy predicate is folded into the subscriber closure (the
	// pipe carries no filtering). Stop / kill / oom are also exit
	// transitions but a container can be `docker start`-ed back after
	// stop — only destroy is irrecoverable. die fires on every exit
	// (incl. before destroy on auto-remove containers), so handling both
	// is the safest superset.
	s.deps.EvictTopic.Subscribe(func(evt pubsub.Event[dockerevents.DockerEvent]) {
		ev := evt.Payload
		if ev.Type != mobyevents.ContainerEventType {
			return
		}
		switch ev.Action {
		case mobyevents.ActionDie, mobyevents.ActionDestroy:
			s.cache.EvictByContainerID(ev.Actor.ID)
		}
	})
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
