package controlplane

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// AgentWatcher polls the Docker daemon for clawker-managed agent
// containers and invokes a drain-to-zero callback once the agent count
// has been zero across MissedThreshold consecutive polls past the grace
// period. The CP uses this to self-terminate when no agents remain —
// there is no reason to keep the control plane (Envoy + CoreDNS + CP
// daemon) alive after the last agent drains.
//
// INV-B2-007: strict drain-to-zero ordering is enforced by the caller
// via the onDrainToZero callback. The watcher guarantees only that the
// callback fires at most once, before Run returns.
type AgentWatcher struct {
	log *logger.Logger

	pollInterval    time.Duration
	missedThreshold int
	gracePeriod     time.Duration
	listErrCeiling  int

	listAgents    func(context.Context) (int, error)
	onDrainToZero func(context.Context) error

	started atomic.Bool
}

// AgentWatcherOptions overrides default watcher tuning. Zero values
// select defaults (PollInterval 30s, MissedThreshold 2, GracePeriod
// 60s, ListErrCeiling 20). Negative values panic — silently snapping
// to a default would hide caller misconfig.
type AgentWatcherOptions struct {
	PollInterval    time.Duration
	MissedThreshold int
	GracePeriod     time.Duration
	// ListErrCeiling bounds how many consecutive listAgents errors the
	// watcher tolerates before returning an error from Run. Prevents
	// "Docker is wedged" from leaving the CP blind and permanent.
	ListErrCeiling int
}

// NewAgentWatcher constructs an AgentWatcher. Nil callbacks or
// negative option values panic — misconfig must fail loudly.
func NewAgentWatcher(
	log *logger.Logger,
	listAgents func(context.Context) (int, error),
	onDrainToZero func(context.Context) error,
	opts AgentWatcherOptions,
) *AgentWatcher {
	if log == nil {
		log = logger.Nop()
	}
	if listAgents == nil {
		panic("controlplane: AgentWatcher requires a non-nil listAgents")
	}
	if onDrainToZero == nil {
		panic("controlplane: AgentWatcher requires a non-nil onDrainToZero")
	}
	if opts.PollInterval < 0 || opts.MissedThreshold < 0 || opts.GracePeriod < 0 || opts.ListErrCeiling < 0 {
		panic("controlplane: AgentWatcher options must not be negative")
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 30 * time.Second
	}
	if opts.MissedThreshold == 0 {
		opts.MissedThreshold = 2
	}
	if opts.GracePeriod == 0 {
		opts.GracePeriod = 60 * time.Second
	}
	if opts.ListErrCeiling == 0 {
		opts.ListErrCeiling = 20
	}
	return &AgentWatcher{
		log:             log,
		pollInterval:    opts.PollInterval,
		missedThreshold: opts.MissedThreshold,
		gracePeriod:     opts.GracePeriod,
		listErrCeiling:  opts.ListErrCeiling,
		listAgents:      listAgents,
		onDrainToZero:   onDrainToZero,
	}
}

// Run blocks until ctx is cancelled, the drain-to-zero condition fires,
// or consecutive list errors exceed ListErrCeiling. On drain, Run
// invokes onDrainToZero synchronously and returns its error. On ctx
// cancel, returns ctx.Err(). On error ceiling, returns a wrapped error
// surfacing the last list failure.
//
// The grace period is measured on wall-clock time from Run entry — any
// zero-count polls during the grace window count toward the miss
// streak, but the streak cannot reach the threshold before grace
// expires. This prevents a race where the CP starts up before any
// agents have been enrolled and immediately drains.
//
// Run must be called at most once per watcher; a second call returns
// an error rather than spinning up a second poll loop.
func (w *AgentWatcher) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return fmt.Errorf("controlplane: AgentWatcher.Run already called")
	}
	start := time.Now()
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	missed := 0
	consecutiveErrs := 0
	var lastErr error
	w.log.Info().
		Dur("poll_interval", w.pollInterval).
		Int("missed_threshold", w.missedThreshold).
		Dur("grace_period", w.gracePeriod).
		Int("list_err_ceiling", w.listErrCeiling).
		Msg("agent watcher starting")

	for {
		select {
		case <-ctx.Done():
			w.log.Info().Err(ctx.Err()).Msg("agent watcher cancelled")
			return ctx.Err()
		case <-ticker.C:
			count, err := w.listAgents(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				consecutiveErrs++
				lastErr = err
				w.log.Warn().Err(err).Int("consecutive", consecutiveErrs).Int("ceiling", w.listErrCeiling).
					Msg("agent watcher: list agents failed; resetting miss streak")
				missed = 0
				if consecutiveErrs >= w.listErrCeiling {
					return fmt.Errorf("agent watcher: %d consecutive list failures: %w", consecutiveErrs, lastErr)
				}
				continue
			}
			consecutiveErrs = 0

			if count > 0 {
				if missed > 0 {
					w.log.Info().Int("count", count).Int("prev_missed", missed).Msg("agent count non-zero; resetting miss streak")
				}
				missed = 0
				continue
			}

			missed++
			inGrace := time.Since(start) < w.gracePeriod
			w.log.Info().Int("missed", missed).Int("threshold", w.missedThreshold).Bool("in_grace", inGrace).Msg("agent count zero")

			if inGrace || missed < w.missedThreshold {
				continue
			}

			w.log.Info().Msg("agent drain-to-zero triggered; invoking shutdown callback")
			if err := w.onDrainToZero(ctx); err != nil {
				return fmt.Errorf("drain-to-zero callback: %w", err)
			}
			return nil
		}
	}
}
