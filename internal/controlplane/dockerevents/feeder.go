// Package dockerevents subscribes to the Docker engine's event stream
// and pushes a clawker-managed view of the realm into an
// informer.Interface.
//
// Scope (v1)
//
// Resources mirrored:
//   - container, network, volume, image
//
// Events mirrored: container create/start/die/destroy/rename/oom/health,
// network create/destroy/connect/disconnect, volume create/destroy,
// image pull/tag/untag/delete. Exec, attach, copy, top, archive-path
// and other diagnostic actions are intentionally dropped — they are
// high-volume and not realm state.
//
// # Filtering policy
//
// Only objects carrying dev.clawker.managed=true matter. Containers,
// volumes, and images carry their labels in the event Actor.Attributes,
// so the managed check happens directly on the event. Network actor
// attributes do NOT carry network labels (verified vs moby
// daemon/events.go LogNetworkEventWithAttributes), so the feeder
// maintains an in-memory networkSet of managed network IDs, populated
// at reconcile time and on every observed network create event via
// NetworkInspect.
//
// # Reconnect protocol
//
// The Docker events stream is a single long-lived HTTP connection.
// Any error on the error channel kills the stream — the feeder rebuilds
// it via reconcile + reopen. Reconcile rebuilds the four managed sets
// from scratch and re-upserts every managed object. Informer Upsert is
// idempotent (key-by-key merge) and Remove is idempotent (soft-delete
// only on first transition), so a reconcile on top of partially-up-to-
// date informer state is safe.
package dockerevents

import (
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/schmitthub/clawker/internal/controlplane/informer"
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
	VolumeList(ctx context.Context, options client.VolumeListOptions) (client.VolumeListResult, error)
	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
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

// Resource Kind strings owned by this feeder. Other feeders use their
// own vocabulary; informer treats Kind as opaque.
const (
	KindContainer = "container"
	KindNetwork   = "network"
	KindVolume    = "volume"
	KindImage     = "image"
)

// Relation Kind owned by this feeder.
const (
	RelationAttachedTo = "attached-to" // container → network
)

// Verb prefix on every Transition.Verb so co-resident feeders cannot
// collide in resource history rings.
const verbPrefix = "docker."

const transitionSource = "dockerevents"

// Feeder maintains a clawker-managed mirror of Docker state inside an
// informer.Interface.
type Feeder struct {
	cli  EventsClient
	inf  informer.Interface
	log  *logger.Logger
	opts Options

	// managed object sets — only mutated by Run's single goroutine, no
	// lock needed. Populated by reconcile and patched by event dispatch.
	containers map[string]bool
	networks   map[string]bool
	volumes    map[string]bool
	images     map[string]bool
}

func New(cli EventsClient, inf informer.Interface, opts Options) (*Feeder, error) {
	if cli == nil {
		return nil, errors.New("dockerevents: EventsClient is required")
	}
	if inf == nil {
		return nil, errors.New("dockerevents: informer is required")
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
		cli:        cli,
		inf:        inf,
		log:        opts.Logger.With("component", "dockerevents"),
		opts:       opts,
		containers: make(map[string]bool),
		networks:   make(map[string]bool),
		volumes:    make(map[string]bool),
		images:     make(map[string]bool),
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
		// that fires between t0 and the listing landing in the informer
		// will be replayed on the events channel — Upsert is
		// idempotent so the duplicate is harmless.
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
		// daemon/events.go), so the only safe filter shared across all
		// four event types is `type`.
		Filters: client.Filters{}.
			Add("type",
				string(events.ContainerEventType),
				string(events.NetworkEventType),
				string(events.VolumeEventType),
				string(events.ImageEventType),
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
