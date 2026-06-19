package netlogger

import (
	"context"

	"github.com/schmitthub/clawker/internal/logger"
)

// processor is the userspace half of the kernel→OTel pipeline. It
// consumes raw bytes the reader pushed onto the queue, decodes each
// into an Event, layers userspace enrichment ({container_id, agent,
// project, domain}) onto it, and hands the result to the Sink.
//
// Single goroutine. Run on Service.Start; exits when the queue is
// closed (Service.Stop closes the channel after the reader has
// drained) OR when ctx is cancelled.
type processor struct {
	queue   <-chan []byte
	cache   *LabelCache
	revDNS  *ReverseDNSMap
	sink    Sink
	metrics *Metrics
	log     *logger.Logger
}

// run drives the consume loop. Recovers from any panic so a bad
// record cannot kill the goroutine and silently strand the BPF
// programs pinned with no userspace consumer.
//
// ctx is honoured separately from queue closure because the reader
// goroutine closes the queue after drain returns (Service.Stop closes
// the ringbuf, which causes drain to return on ErrClosed); ctx
// cancellation is the outer-scope signal (Service.Stop also cancels
// the inner ctx for belt-and-braces).
func (p *processor) run(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			p.log.Error().
				Interface("panic", rec).
				Str("event", "netlogger_processor_panic").
				Msg("netlogger processor panicked — netlogger will be unavailable")
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-p.queue:
			if !ok {
				return
			}
			p.metrics.QueueReceived.Inc()
			ev, err := parseEvent(raw)
			if err != nil {
				p.metrics.ParseErrors.Inc()
				p.log.Warn().
					Err(err).
					Str("event", "netlogger_parse_error").
					Msg("decode egress event — sustained growth indicates BPF/Go ABI drift")
				continue
			}
			p.enrich(&ev)
			p.sink.Emit(ctx, ev)
			p.metrics.EmitSucceeded.Inc()
		}
	}
}

// enrich layers userspace attribution onto an Event parsed from the
// ringbuf. LabelCache resolves cgroup_id to {container_id, agent,
// project}; ReverseDNSMap resolves domain_hash to a human-readable
// domain (returns "" for unattributed hashes — see ReverseDNSMap doc).
func (p *processor) enrich(ev *Event) {
	if p.cache != nil {
		if cid, agent, project, ok := p.cache.Lookup(ev.CgroupID); ok {
			ev.ContainerID = cid
			ev.Agent = agent
			ev.Project = project
		}
	}
	if p.revDNS != nil {
		ev.Domain = p.revDNS.Lookup(ev.DomainHash)
	}
}
