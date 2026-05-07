// Package dockerevents subscribes to the Docker engine's event stream
// and publishes typed lifecycle events for clawker-managed containers
// and their network attachments to an *overseer.Overseer bus.
//
// Scope (v1)
//
// Resources mirrored:
//   - container (Started/Stopped/Removed)
//   - container→network edges (Attached/Detached)
//
// Events not republished:
//   - volume create/destroy — no consumer; internal bookkeeping was
//     dropped along with the publishes (Overseer doesn't track volumes
//     in its worldview).
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
// ContainerStarted event for every running container; Overseer's
// applier hooks idempotently set Status=running.
package dockerevents

import (
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// EventsClient is the slice of moby's APIClient surface the feeder
// uses. Real callers pass *docker.Client (which embeds *whail.Engine
// which embeds client.APIClient). Tests can substitute a fake.
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

// Feeder maintains a clawker-managed mirror of Docker container and
// container→network state in an *overseer.Overseer.
type Feeder struct {
	cli  EventsClient
	bus  *overseer.Overseer
	log  *logger.Logger
	opts Options

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

// New constructs a Feeder. Returns an error if cli or bus is nil, or
// if managed-label config is missing.
func New(cli EventsClient, bus *overseer.Overseer, opts Options) (*Feeder, error) {
	if cli == nil {
		return nil, errors.New("dockerevents: EventsClient is required")
	}
	if bus == nil {
		return nil, errors.New("dockerevents: overseer is required")
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
		bus:                 bus,
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
func (f *Feeder) Run(ctx context.Context) error {
	backoff := f.opts.ReconnectMin
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Anchor the events Since timestamp BEFORE listing. Any event
		// that fires between t0 and the listing landing on the bus will
		// be replayed on the events channel — Overseer applier hooks
		// are idempotent so the duplicate is harmless.
		t0 := time.Now()

		if err := f.reconcile(ctx); err != nil {
			f.log.Error().Err(err).Msg("reconcile failed; backing off")
			if !sleepCtx(ctx, backoff) {
				return ctx.Err()
			}
			backoff = nextBackoff(backoff, f.opts.ReconnectMax)
			continue
		}

		err := f.runStream(ctx, t0)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, io.EOF) {
			f.log.Warn().Err(err).Msg("events stream errored; backing off and reconnecting")
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
		// are no longer subscribed to (Overseer doesn't track them).
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
