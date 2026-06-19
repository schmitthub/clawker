// Package dockerevents subscribes to the Docker engine's event stream
// and publishes typed lifecycle events for clawker-managed containers
// and their network attachments onto an injected
// *pubsub.Topic[DockerEvent].
//
// Scope (v1)
//
// Resources mirrored:
//   - container (Started/Stopped/Removed)
//   - container→network edges (Connected/Disconnected)
//
// Events not republished:
//   - volume create/destroy — no consumer; internal bookkeeping was
//     dropped along with the publishes (no subscriber tracks volumes).
//   - image pull/tag/untag/delete — same.
//   - network create/destroy — feeder still tracks managed networks
//     internally to filter Connect/Disconnect events, but does not
//     publish a NetworkCreated event since no subscriber consumes it.
//
// # Filtering policy
//
// Only objects carrying dev.clawker.managed=true matter. Containers
// carry their labels in the event Actor.Attributes, so the managed
// check happens directly on the event. Network actor attributes do
// NOT carry network labels (verified vs moby
// daemon/events.go::LogNetworkEventWithAttributes), so the feeder
// maintains an in-memory networkSet of managed network IDs, populated
// at reconcile time and on every observed network create event via
// NetworkInspect.
//
// # Reconnect protocol
//
// The Docker events stream is a single long-lived HTTP connection.
// Any error on the error channel kills the stream — the feeder
// rebuilds it via reconcile + reopen. Reconcile re-publishes a
// DockerEvent{Action=start} envelope for every running container;
// subscribers that project state must keep their own apply idempotent
// so the replay is harmless.
package dockerevents

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/logger"
)

// EventsClient is the slice of moby's APIClient surface the feeder
// uses. Real callers pass the client.APIClient embedded in
// docker.Client (via dockerCli.APIClient). Tests can substitute a fake.
type EventsClient interface {
	Events(ctx context.Context, options client.EventsListOptions) client.EventsResult
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	NetworkList(ctx context.Context, options client.NetworkListOptions) (client.NetworkListResult, error)
	NetworkInspect(ctx context.Context, networkID string, options client.NetworkInspectOptions) (client.NetworkInspectResult, error)
}

// Options configures a Feeder. Zero values are valid.
type Options struct {
	// ManagedLabelKey is the label name (e.g. "dev.clawker.managed")
	// the feeder uses to identify clawker-owned objects. Required.
	ManagedLabelKey string
	// ManagedLabelValue is the expected label value (typically "true").
	// Required.
	ManagedLabelValue string
	// Logger receives the feeder's audit lines. Nil → logger.Nop().
	Logger *logger.Logger
	// ReconnectMin is the floor for the exponential backoff between
	// stream attempts. Defaults to 1s.
	ReconnectMin time.Duration
	// ReconnectMax caps the backoff. Defaults to 30s.
	ReconnectMax time.Duration
}

const (
	// 1s floor keeps a flapping moby socket from hot-looping; 30s ceiling
	// recovers from a normal daemon restart in <1 minute (3 doublings).
	defaultReconnectMin = 1 * time.Second
	defaultReconnectMax = 30 * time.Second
)

// Feeder republishes clawker-managed Docker container and
// container→network lifecycle events onto a typed pub/sub topic. It
// holds no projected worldview of its own beyond the goroutine-local
// managed-object sets it needs to filter the stream.
type Feeder struct {
	cli   EventsClient
	topic *pubsub.Topic[DockerEvent]
	log   *logger.Logger
	opts  Options

	// managed object sets — only mutated by Run's single goroutine, no
	// lock needed. Populated by reconcile and patched by event dispatch.
	containers map[string]bool
	networks   map[string]bool

	// networksNeedRecheck tracks network IDs whose initial Create-time
	// NetworkInspect failed (transient daemon hiccup, race with
	// concurrent removal, etc). The next event for the same ID retries
	// inspection so a network doesn't go permanently unmanaged just
	// because the very first inspect lost a race.
	networksNeedRecheck map[string]bool
}

// New constructs a Feeder. Returns an error if cli or topic is nil, or
// if managed-label config is missing.
func New(cli EventsClient, topic *pubsub.Topic[DockerEvent], opts Options) (*Feeder, error) {
	if cli == nil {
		return nil, errors.New("dockerevents: EventsClient is required")
	}
	if topic == nil {
		return nil, errors.New("dockerevents: topic is required")
	}
	if opts.ManagedLabelKey == "" {
		return nil, errors.New("dockerevents: ManagedLabelKey is required")
	}
	if opts.ManagedLabelValue == "" {
		return nil, errors.New("dockerevents: ManagedLabelValue is required")
	}
	if opts.Logger == nil {
		opts.Logger = logger.Nop()
	}
	if opts.ReconnectMin <= 0 {
		opts.ReconnectMin = defaultReconnectMin
	}
	if opts.ReconnectMax <= 0 {
		opts.ReconnectMax = defaultReconnectMax
	}
	return &Feeder{
		cli:                 cli,
		topic:               topic,
		log:                 opts.Logger.With("component", "dockerevents"),
		opts:                opts,
		containers:          make(map[string]bool),
		networks:            make(map[string]bool),
		networksNeedRecheck: make(map[string]bool),
	}, nil
}

// Run drives the feeder until ctx cancels. It blocks. The expected
// caller pattern is `go feeder.Run(ctx)`. Returns ctx.Err() on cancel.
//
// Backoff applies to both reconcile failures and non-EOF stream
// errors. A clean io.EOF (moby closed the stream cleanly) resets
// backoff to ReconnectMin — common during routine moby behaviour and
// shouldn't compound delay for what is not a real failure.
func (f *Feeder) Run(ctx context.Context) (err error) {
	// CP no-panic discipline: the feeder is the sole producer onto the
	// docker topic and the busiest serve-path goroutine (fires on every
	// Docker event). A nil-deref on an event actor map, or a moby
	// response-shape change in the dispatch chain, must NOT panic PID 1 —
	// that would skip drain-to-zero and strand the eBPF programs pinned
	// and unsupervised while the user believes the firewall is enforcing.
	// Convert any panic into a returned error so the launch site routes it
	// to serveFailed and the on-failure restart policy retriggers.
	defer func() {
		if r := recover(); r != nil {
			f.log.Error().
				Interface("panic", r).
				Bytes("stack", debug.Stack()).
				Str("event", "dockerevents_feeder_panic").
				Msg("dockerevents feeder goroutine panicked")
			err = fmt.Errorf("dockerevents feeder panic: %v", r)
		}
	}()

	backoff := f.opts.ReconnectMin
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Anchor the events Since timestamp BEFORE listing. Any event
		// that fires between t0 and the listing landing on the bus will
		// be replayed on the events channel — state-projecting
		// subscribers keep their apply idempotent so the duplicate is
		// harmless.
		t0 := time.Now()

		if rerr := f.reconcile(ctx); rerr != nil {
			f.log.Error().Err(rerr).Msg("reconcile failed; backing off")
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			backoff = nextBackoff(backoff, f.opts.ReconnectMax)
			continue
		}

		streamErr := f.runStream(ctx, t0)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if streamErr != nil && !errors.Is(streamErr, io.EOF) {
			f.log.Warn().Err(streamErr).Msg("events stream errored; backing off and reconnecting")
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			backoff = nextBackoff(backoff, f.opts.ReconnectMax)
			continue
		}

		// Clean EOF (or nil): brief pause, reset backoff, reopen.
		f.log.Info().Msg("events stream closed; reconciling and reopening")
		backoff = f.opts.ReconnectMin
		if !sleepCtx(ctx, f.opts.ReconnectMin) {
			return ctx.Err()
		}
	}
}

// Supervise runs the feeder to completion (Run) and routes a real
// failure onto failed. It is the long-lived goroutine wrapper the CP
// orchestrator launches as `go feeder.Supervise(ctx, serveFailed)`.
//
// Cancel-vs-error discrimination: Run returns ctx.Err() on a clean
// SIGTERM / drain-to-zero cancel — that is the expected stop, not a
// failure, so Supervise swallows it. Any other return means the Run
// loop (which retries internally on every reconcile/stream error) gave
// up: a wiring bug, an unrecoverable contract violation, or a recovered
// panic (Run's deferred recover converts a panic into a returned
// error). Such a return is surfaced to failed so the daemon exits
// non-zero and the on-failure restart policy retriggers.
//
// The send is non-blocking (failed is buffered and the serve select may
// already be draining a prior error) so a late feeder failure can never
// wedge this goroutine and strand the eBPF programs.
func (f *Feeder) Supervise(ctx context.Context, failed chan<- error) {
	err := f.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	f.log.Error().Err(err).Msg("dockerevents feeder exited with error")
	select {
	case failed <- fmt.Errorf("dockerevents feeder: %w", err):
	default:
	}
}

// runStream opens an events stream anchored at since and dispatches
// every received event until the stream errors or ctx cancels.
// Returns nil only when ctx is done; otherwise returns the underlying
// stream error so the caller knows whether to log warn vs info.
func (f *Feeder) runStream(ctx context.Context, since time.Time) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	res := f.cli.Events(streamCtx, client.EventsListOptions{
		// Server-side prune to types we care about. label= is omitted
		// deliberately — network connect/disconnect actor attributes
		// don't carry the network's labels (verified vs moby
		// daemon/events.go), so the only safe filter shared across
		// container + network events is `type`. Volume + image events
		// are not subscribed to: no domain consumes them today, so the
		// stream filter is narrowed to container + network to avoid
		// publishing envelopes nothing reads.
		Filters: client.Filters{}.
			Add("type",
				string(events.ContainerEventType),
				string(events.NetworkEventType),
			),
		Since: strconv.FormatInt(since.UnixNano(), 10),
	})

	f.log.Info().Time("since", since).Msg("events stream open")
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-res.Messages:
			if !ok {
				return drainErrAfterClose(ctx, res.Err)
			}
			f.dispatch(ctx, ev)
		case err := <-res.Err:
			if err == nil {
				return io.EOF
			}
			return err
		}
	}
}

// drainErrAfterClose surfaces a delayed res.Err that may not have
// landed before res.Messages closed. Without the brief grace window,
// connection-reset / TLS-expiry / permission-revoked failures look
// identical to a clean EOF and the operator sees an INFO-level
// reconnect loop forever.
func drainErrAfterClose(ctx context.Context, errCh <-chan error) error {
	const grace = 100 * time.Millisecond
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-errCh:
		if err == nil {
			return io.EOF
		}
		return err
	case <-timer.C:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

// sleepCtx returns true if it slept the full duration, false if ctx
// cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (f *Feeder) isManaged(labels map[string]string) bool {
	if v, ok := labels[f.opts.ManagedLabelKey]; ok {
		return v == f.opts.ManagedLabelValue
	}
	return false
}
