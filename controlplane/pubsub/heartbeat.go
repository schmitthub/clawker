package pubsub

import (
	"context"
	"time"

	"github.com/schmitthub/clawker/internal/logger"
)

// DefaultStatsInterval is the heartbeat period used when NewStatsHeartbeat is
// given a non-positive interval. It matches the cadence the CP orchestrator
// historically used for its pub/sub stats heartbeat.
const DefaultStatsInterval = 30 * time.Second

// Heartbeat log messages and structured field keys. These are the only strings
// the heartbeat emits; like the engine, the heartbeat itself spells no domain
// identity beyond what an injected StatsSource chooses to log.
const (
	logMsgStatsHeartbeat     = "pubsub stats heartbeat"
	logMsgWorldviewHeartbeat = "agent worldview heartbeat"
	logMsgHeartbeatPanic     = "pubsub stats heartbeat panicked"

	logFieldTopic            = "topic"
	logFieldSubscribers      = "subscribers"
	logFieldQueueCapacity    = "queue_capacity"
	logFieldPublishedTotal   = "published_total"
	logFieldDroppedTotal     = "dropped_total"
	logFieldPanicsRecovered  = "panics_recovered"
	logFieldHookPanics       = "hook_panics_recovered"
	logFieldAgentWorldview   = "agent_worldview_size"
	logFieldHeartbeatEvent   = "event"
	eventStatsHeartbeatPanic = "pubsub_stats_heartbeat_panic"
)

// StatsSource is a read-only telemetry source the heartbeat samples once per
// tick and writes to the CP log. The heartbeat holds no state of its own: every
// number it reports is pulled, at tick time, from a source the orchestrator
// injects. Sources are the orchestrator's domain knowledge (a topic's transport
// counters, the agent domain's observed-now worldview size); the heartbeat is
// the dumb timer that samples and logs them. A source must be safe to call from
// the heartbeat goroutine and must never block.
type StatsSource interface {
	// LogSnapshot samples the source's current values and writes them as a
	// single structured log line. It is called once per heartbeat tick. It must
	// not panic on its own account; if it does, the heartbeat goroutine's
	// recover contains it (so a buggy source can't strand eBPF), but the loop
	// continues and other sources are unaffected only until the panicking tick —
	// keep LogSnapshot non-panicking and non-blocking.
	LogSnapshot(log *logger.Logger)
}

// TopicStatsSource adapts a topic's Stats() accessor to a StatsSource. It logs
// the named topic's transport counters — the exact field set Topic.Stats
// reports — on every tick. The accessor is a func rather than a *Topic[T] so the
// heartbeat stays type-parameter-free and can sample heterogeneous topics
// (docker, agent, ebpf_enrolled) through one slice.
type TopicStatsSource struct {
	// Name labels the topic in the log line (e.g. "docker", "agent").
	Name string
	// Stats samples the topic's current transport counters. Typically the
	// topic's own Stats method value (topic.Stats).
	Stats func() Stats
}

// NewTopicStatsSource builds a StatsSource that logs name's transport counters,
// sampling them through stats once per tick.
func NewTopicStatsSource(name string, stats func() Stats) TopicStatsSource {
	return TopicStatsSource{Name: name, Stats: stats}
}

// LogSnapshot writes one "pubsub stats heartbeat" line carrying the topic's six
// transport counters. The field set and message are byte-for-byte the ones the
// CP orchestrator emitted before this was extracted.
func (s TopicStatsSource) LogSnapshot(log *logger.Logger) {
	st := s.Stats()
	log.Info().
		Str(logFieldTopic, s.Name).
		Int(logFieldSubscribers, st.Subscribers).
		Int(logFieldQueueCapacity, st.QueueCapacity).
		Int64(logFieldPublishedTotal, st.PublishedTotal).
		Int64(logFieldDroppedTotal, st.DroppedTotal).
		Int64(logFieldPanicsRecovered, st.PanicsRecovered).
		Int64(logFieldHookPanics, st.HookPanicsRecovered).
		Msg(logMsgStatsHeartbeat)
}

// WorldviewStatsSource adapts a domain's read-only worldview-size accessor to a
// StatsSource. The pub/sub pipe holds zero application state, so a domain that
// owns the faithful home of a count (the agent Repository's observed-now store)
// exposes its size through a read-only accessor; this source samples it. It is
// generic over which domain via the injected Size closure.
type WorldviewStatsSource struct {
	// Topic labels the worldview in the log line (e.g. "agent").
	Topic string
	// Size returns the domain's current observed-now worldview size. Typically
	// a read-only accessor like agentRepo.Agents.Len.
	Size func() int
}

// NewWorldviewStatsSource builds a StatsSource that logs the domain worldview
// size for topic, sampling it through size once per tick.
func NewWorldviewStatsSource(topic string, size func() int) WorldviewStatsSource {
	return WorldviewStatsSource{Topic: topic, Size: size}
}

// LogSnapshot writes one "agent worldview heartbeat" line carrying the domain's
// observed-now worldview size. The field set and message are byte-for-byte the
// ones the CP orchestrator emitted before this was extracted.
func (s WorldviewStatsSource) LogSnapshot(log *logger.Logger) {
	log.Info().
		Str(logFieldTopic, s.Topic).
		Int(logFieldAgentWorldview, s.Size()).
		Msg(logMsgWorldviewHeartbeat)
}

// StatsHeartbeat is a periodic, recover-guarded telemetry loop. It samples each
// injected StatsSource once per interval and writes the result to the CP log,
// giving an operator tailing the log a coarse health signal without a dedicated
// metrics surface. It holds no domain state: every number it logs is pulled, at
// tick time, from a source the orchestrator owns.
//
// Like every long-lived CP goroutine, its loop runs under recover: in the
// clawker CP a panic is a security incident, not an availability one, so a
// future panic in a StatsSource must not silently kill the heartbeat and leave
// the operator without telemetry (CP CLAUDE.md, hard rule 3).
type StatsHeartbeat struct {
	log      *logger.Logger
	interval time.Duration
	sources  []StatsSource
}

// NewStatsHeartbeat constructs a heartbeat that samples sources every interval.
// A non-positive interval falls back to DefaultStatsInterval. It never panics;
// a nil logger or empty source list yields a heartbeat whose Start loop simply
// has nothing to log. Construction does not start the loop — call Start.
func NewStatsHeartbeat(log *logger.Logger, interval time.Duration, sources ...StatsSource) *StatsHeartbeat {
	if interval <= 0 {
		interval = DefaultStatsInterval
	}
	return &StatsHeartbeat{
		log:      log,
		interval: interval,
		sources:  sources,
	}
}

// Start launches the heartbeat loop on its own goroutine and returns
// immediately. The loop ticks every interval, sampling and logging each source,
// and exits when ctx is cancelled. The loop body runs under recover so a panic
// in a StatsSource is contained and audited (event=pubsub_stats_heartbeat_panic)
// rather than killing the goroutine and stranding the operator without
// telemetry. The heartbeat owns no resources beyond the ticker, which it stops
// on exit; ctx cancellation is the only stop signal.
func (h *StatsHeartbeat) Start(ctx context.Context) {
	go func() {
		// recover so a future StatsSource panic doesn't silently kill the
		// heartbeat loop and leave the operator without telemetry.
		defer func() {
			if r := recover(); r != nil {
				h.log.Error().
					Interface(logFieldPanic, r).
					Str(logFieldHeartbeatEvent, eventStatsHeartbeatPanic).
					Msg(logMsgHeartbeatPanic)
			}
		}()
		ticker := time.NewTicker(h.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, src := range h.sources {
					src.LogSnapshot(h.log)
				}
			}
		}
	}()
}
