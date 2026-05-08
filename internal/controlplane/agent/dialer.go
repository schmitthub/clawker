// Dialer-side helpers in package agent: CP-side outbound mTLS dial
// logic for the CP→clawkerd Session channel.
//
// Single entry point: Dialer.DialAgent(ctx, containerID). The same
// function is invoked at CP boot (over the result of listAgentIDs)
// and from the typed-event subscriber on dockerevents.DockerEvent
// (filtered to container start/restart/unpause for purpose=agent
// containers) when an agent container reaches running state at
// runtime — so two callers, one dial path.
//
// DialAgent is fire-and-forget: it spawns a goroutine that owns the
// dial, the Session stream, and the lifetime drain loop. All failures
// are logged at the Error level — callers don't need to handle errors.
// A failed dial leaves no resources behind; a successful dial is held
// open until ctx cancels (CP shutdown) or the peer closes.
//
// # Asymmetric trust model — load-bearing
//
// CP is the overlord. The dial NEVER aborts on cert / identity grounds.
// Cert chain verification, peer CN match, and registry classification
// outcomes are captured on the establishResult and surfaced through
// the typed event surface — SessionConnected carries flat
// PeerCN/PeerThumbprint fields; AgentRegistered/AgentUntrusted carry
// the policy outcomes. Subscribers consume those events to enact
// policy (containment, alerting, eviction); the dialer holds no
// policy itself.
//
// Why permissive: CP must always be able to reach clawkerd to issue
// containment commands (iptables lock, network detach, container kill).
// A compromised clawkerd presenting a bad cert is exactly when the
// channel must be UP so CP can react. Aborting on cert grounds would
// strand CP exactly at the moment governance is most needed.
//
// The asymmetric counterpart lives in cmd/clawkerd/listener.go: the
// clawkerd-side listener is STRICT — CP CN pin + Client-Auth EKU + CA
// chain enforced at TLS layer. clawkerd refuses any peer that isn't
// CP. This pairs with the dialer's permissive client posture.
//
// Connection establishment failures still happen on connectivity
// grounds (TCP timeout, container gone, retry exhausted, ctx
// cancelled) — those drive establishOutcome and SessionFailed.
//
// FD-leak ceiling: a successful dial whose conn.Close() repeatedly
// fails would accumulate file descriptors and gRPC keepalive
// goroutines indefinitely. After closeErrCeiling consecutive close
// failures the dial loop bails for the target with a SessionFailed
// event (Reason carries the "fd-leak-ceiling" classification) so
// operators see the outcome instead of a silent leak. A successful
// close anywhere in the loop resets the counter.
package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- non-security random for connect-retry jitter
	"net"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"

	"github.com/schmitthub/clawker/internal/controlplane/overseer"
	"github.com/schmitthub/clawker/internal/logger"
)

// Connect retry parameters. Exponential backoff with full jitter
// bounds the per-attempt sleep; connectTotalTimeout bounds the whole
// retry budget so a permanently-broken clawkerd doesn't keep us
// retrying forever — eventually we give up and the next start event
// (or the next CP boot) is the trigger to try again. 60s is enough
// to outlast clawkerd's typical startup hiccups (bootstrap read,
// listener bind) without leaving consumers in "connecting" purgatory.
const (
	connectInitialBackoff = 500 * time.Millisecond
	connectMaxBackoff     = 10 * time.Second
	connectTotalTimeout   = 60 * time.Second
)

// closeErrCeiling bounds the number of consecutive conn.Close()
// failures the dial loop will tolerate before giving up on the
// target. A non-zero close error usually means a transport-level
// state machine the gRPC client can't unwind cleanly (broken
// keepalive, half-closed socket); each one leaks a file descriptor
// + a couple of background goroutines. Five gives a transient
// hiccup room to recover without letting a structurally-broken
// peer accumulate resources without bound. Successful Close
// anywhere in the loop resets the counter.
const closeErrCeiling = 5

// Dialer captures the CP-side material every dial needs. Construct
// once at CP startup; share across all agent dials.
//
// dialing is the dedup set: containerIDs currently being dialed (or
// already-Session-established). Initial poll and the dockerevents
// subscriber both call DialAgent for the same running container; the
// dedup keeps the second call from spinning a duplicate goroutine
// against an already-open Session. Membership lasts the lifetime of
// the dial goroutine — after the Session closes (peer drop, ctx
// cancel, retry timeout), the entry is removed and a future event
// for the same containerID dials fresh.
type Dialer struct {
	log    *logger.Logger
	docker mobyclient.APIClient
	bus    *overseer.Overseer
	// agents is the CP-owned agentregistry (read+write). The dialer
	// reads it at handshake time to classify the peer cert against
	// the registered row: match (registered), miss (drives Register
	// handshake), thumbprint mismatch (untrusted), CN mismatch
	// (untrusted). Connection stays open in all cases. The Register
	// flow re-reads after RegisterDone to confirm the row landed.
	// Required (non-nil) — wiring bug if unset.
	agents Registry

	// initExec dispatches the CP-driven init plan after
	// dispatchAgentEvents and before drainStream. nil disables init
	// (entrypoint hangs on its fifo until timeout) — runInit logs a
	// warning so the misconfiguration is observable. Set at
	// construction; immutable after Start.
	initExec *Executor

	cpClientCert tls.Certificate
	caPool       *x509.CertPool

	mu      sync.Mutex
	dialing map[string]struct{}
}

// New constructs a Dialer. Returns an error if the CP client cert /
// key cannot be loaded — better to fail at CP startup than to defer
// the failure to the first dial.
//
// bus is required: the dialer publishes typed Session* events
// (SessionConnecting / Connected / Failed / Broken) so other CP
// components can subscribe to connection lifecycle without coupling
// to the dialer directly. Pass a real *overseer.Overseer; tests can
// use an in-memory bus (it's cheap).
//
// agents is required: every successful dial cross-checks the peer
// cert against the registry row keyed by container_id and dispatches
// the typed AgentRegistered / AgentUntrusted events accordingly. nil
// agents would strand worldview consumers without a registration
// signal.
func New(log *logger.Logger, docker mobyclient.APIClient, bus *overseer.Overseer, agents Registry, certPath, keyPath string, caPool *x509.CertPool, initExec *Executor) (*Dialer, error) {
	if log == nil {
		log = logger.Nop()
	}
	if bus == nil {
		return nil, errors.New("agent.New: overseer is required")
	}
	if agents == nil {
		return nil, errors.New("agent.New: agents registry is required")
	}
	if caPool == nil {
		return nil, errors.New("agent.New: caPool is required")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load CP client cert: %w", err)
	}
	return &Dialer{
		log:          log,
		docker:       docker,
		bus:          bus,
		agents:       agents,
		initExec:     initExec,
		cpClientCert: cert,
		caPool:       caPool,
		dialing:      make(map[string]struct{}),
	}, nil
}

// DialAgent opens a Session stream to the clawkerd listener inside
// the given agent container, sends Hello, awaits HelloAck, and holds
// the stream open until ctx cancels or the peer closes. Returns
// immediately — the dial + lifetime drain run on a background
// goroutine. All failures are logged.
//
// Dedup: a no-op if a dial for the same containerID is already in
// flight. Initial poll + dockerevents subscriber both reach this
// function with overlapping IDs at CP startup; dedup keeps a second
// call from spinning a duplicate goroutine.
//
// Retry: the dial goroutine retries connection establishment with
// exponential backoff + full jitter (cap connectMaxBackoff) until
// either the Session is established (Hello + HelloAck) or
// connectTotalTimeout elapses. Once established, no retry from the
// drain — a stream break ends the goroutine and removes the dedup
// entry, so a subsequent restart event re-dials.
func (d *Dialer) DialAgent(ctx context.Context, containerID string) {
	d.mu.Lock()
	if _, exists := d.dialing[containerID]; exists {
		d.mu.Unlock()
		return
	}
	d.dialing[containerID] = struct{}{}
	d.mu.Unlock()

	go func() {
		// Defer order is load-bearing (LIFO): recover wraps the
		// dedup cleanup so a panic in cleanup is also caught and
		// re-dial unblocks after a panicked cycle. See
		// internal/controlplane/CLAUDE.md "Resilience contract".
		defer func() {
			d.mu.Lock()
			delete(d.dialing, containerID)
			d.mu.Unlock()
		}()
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			// Best-effort registry lookup so the synthetic
			// SessionFailed and the structured log carry the
			// agent/project identity — without this, downstream
			// metrics indexed by (project, agent) lose track of the
			// affected agent during exactly the failure mode
			// (CP-internal panic) when high-quality signal matters
			// most. The lookup is best-effort: if registry I/O is
			// also broken, the empty fields are the audit signal.
			agentName, project := "", ""
			if d.agents != nil {
				if entry, lerr := d.agents.LookupByContainerID(containerID); lerr == nil && entry != nil {
					agentName, project = entry.AgentName, entry.Project
				}
			}
			d.log.Error().
				Interface("panic", r).
				Bytes("stack", debug.Stack()).
				Str("container_id", containerID).
				Str("agent", agentName).
				Str("project", project).
				Str("event", "agentdial_panic").
				Msg("agent.dial: dial goroutine panicked; CP otherwise unaffected. Publishing synthetic SessionFailed so worldview consumers see a terminal lifecycle event.")
			// Publish directly with context.Background() instead of
			// the dial ctx: publishFailed short-circuits when the
			// ctx is done, but the panic path needs the worldview
			// transition to land even during shutdown — operators
			// are about to lose every other signal. overseer.Publish
			// is no-op on a closed bus (returns false; doesn't
			// panic), so this is safe to call from the recover.
			overseer.Publish(d.bus, SessionFailed{
				ContainerID: containerID,
				AgentName:   agentName,
				Project:     project,
				Reason:      fmt.Sprintf("dial_goroutine_panic: %v", r),
				At:          time.Now(),
			})
		}()
		d.runDial(ctx, containerID)
	}()
}

func (d *Dialer) runDial(ctx context.Context, containerID string) {
	log := d.log.With("container_id", containerID, "component", "agentdial")
	closeErrCount := 0

	for cycle := 1; ; cycle++ {
		if ctx.Err() != nil {
			return
		}

		// Establishment loop: re-inspects on every attempt so the
		// State.Running check, the addr, and the labels are read
		// at the moment of dial — never a stale snapshot. If the
		// container dies mid-cycle (laptop sleep, manual stop,
		// docker daemon hiccup), the next attempt's inspect catches
		// it instead of burning the 5min retry budget against a
		// dead IP.
		res := d.establishWithRetry(ctx, containerID, log.With("cycle", cycle))
		switch res.Outcome {
		case outcomeCtxDone:
			return
		case outcomeContainerGone:
			d.publishFailed(ctx, containerID, res.Agent, res.Project, res.Addr, "container_not_running", res.Attempt)
			return
		case outcomeAddrInvalid:
			d.publishFailed(ctx, containerID, res.Agent, res.Project, res.Addr, "clawker_net_endpoint_missing", res.Attempt)
			return
		case outcomeRetryExhausted:
			d.publishFailed(ctx, containerID, res.Agent, res.Project, res.Addr, "connect_total_timeout", res.Attempt)
			return
		case outcomeSuccess:
			// fallthrough — handled below
		default:
			log.Error().Int("outcome", int(res.Outcome)).Msg("agentdial: unrecognized establish outcome; treating as failure")
			d.publishFailed(ctx, containerID, res.Agent, res.Project, res.Addr, "internal_unknown_outcome", res.Attempt)
			return
		}

		cycleLog := log.With("agent", res.Agent, "project", res.Project, "addr", res.Addr, "cycle", cycle)
		d.publishConnected(ctx, containerID, res.Agent, res.Project, res.Addr, res.Attempt, res.PeerInfo)

		// Classify the peer cert against the registry. Drives the
		// agent-axis events (AgentRegistered for fresh registrations
		// on Miss, AgentUntrusted for mismatch outcomes). The Session
		// stream stays open in all cases — CP must remain reachable
		// for containment commands even when the agent is untrusted.
		d.dispatchAgentEvents(ctx, containerID, res, cycleLog)

		// Init owns Recv during its phase; failures publish InitFailed
		// but never close the Session (asymmetric trust). Re-runs on
		// every Session reconnect — see Executor.
		d.runInit(ctx, containerID, res, cycleLog)

		drain := d.drainStream(ctx, res.Stream, cycleLog)
		// Cancel the stream-scoped ctx so any goroutine still parked on
		// stream.Recv (e.g. a leftover from a driveRegister timeout that
		// preceded drainStream) is guaranteed to unblock before the next
		// cycle dials a new stream.
		if res.StreamCancel != nil {
			res.StreamCancel()
		}
		if d.closeAndCheckLeak(res.Conn, &closeErrCount, cycleLog) {
			d.publishFailed(ctx, containerID, res.Agent, res.Project, res.Addr,
				fmt.Sprintf("fd-leak-ceiling: %d consecutive close failures", closeErrCount),
				res.Attempt)
			return
		}

		// Don't reconnect on intentional teardown — the parent ctx
		// is cancelled (CP shutdown) or the drain reported the same.
		// Anything else (peer EOF, transport break from laptop sleep,
		// stream error) drops back into the loop to re-establish.
		// publishBroken itself skips on ctx-done (defense in depth);
		// shouldReconnect is the single decision point and the only
		// thing the unit test for the reconnect-vs-teardown contract
		// pins down.
		if !shouldReconnect(ctx, drain) {
			return
		}
		d.publishBroken(ctx, containerID, res.Agent, res.Project, res.Addr, drain.Reason)
		cycleLog.Info().
			Str("event", "agentdial_session_reconnecting").
			Str("reason", drain.Reason).
			Msg("CP→clawkerd Session broken; will reconnect")
	}
}

// shouldReconnect classifies the post-drain decision: re-enter
// establishWithRetry (true) or return from runDial (false). False
// on intentional teardown — parent ctx cancelled (CP shutdown) or
// drain reported the same outcome — so neither SessionBroken nor a
// fresh establish cycle is published while the bus is on its way
// down. Pure function; unit-testable independent of the dial path.
func shouldReconnect(ctx context.Context, drain drainResult) bool {
	if ctx.Err() != nil {
		return false
	}
	if drain.Outcome == drainCtxCanceled {
		return false
	}
	return true
}

// closeAndCheckLeak closes the conn and tracks consecutive close
// failures against closeErrCeiling. Returns true iff the caller
// should bail the dial loop (ceiling reached). A successful close
// resets the counter so a transient hiccup does not poison the
// ledger across the lifetime of the cycle. Extracted for
// testability — production callers always pass a *grpc.ClientConn,
// but the closeable interface lets unit tests inject a controlled
// error sequence without standing up a real gRPC channel.
func (d *Dialer) closeAndCheckLeak(c closeable, count *int, log *logger.Logger) bool {
	if cerr := c.Close(); cerr != nil {
		*count++
		log.Error().Err(cerr).
			Int("close_err_count", *count).
			Int("close_err_ceiling", closeErrCeiling).
			Str("event", "agentdial_conn_close_failed").
			Msg("close clawkerd conn")
		return *count >= closeErrCeiling
	}
	*count = 0
	return false
}

// closeable is the minimum surface closeAndCheckLeak needs; satisfied
// by *grpc.ClientConn and by simple test fakes.
type closeable interface {
	Close() error
}

// establishOutcome classifies the terminal state of one
// establishWithRetry call. Replaces the previous (gone bool, ok
// bool) pair where (ok=true, gone=true) was structurally illegal
// but compiled.
type establishOutcome int

const (
	// outcomeSuccess: Hello + HelloAck completed. Conn / Stream are
	// non-nil; Attempt records the number of tries.
	outcomeSuccess establishOutcome = iota
	// outcomeContainerGone: docker reports the container is truly gone
	// (errdefs.IsNotFound) or its State.Running flipped to false.
	// Terminal — there is nothing to retry against. A generic inspect
	// API failure (daemon transient hiccup, perms revoked) does NOT
	// land here; it stays in the retry loop and surfaces as
	// outcomeRetryExhausted on deadline.
	outcomeContainerGone
	// outcomeAddrInvalid: clawker-net contract violation — the
	// container exists and is running but has no clawker-net endpoint
	// (no NetworkSettings, missing endpoint, or invalid IP). Terminal:
	// retrying won't fix a misconfigured network attachment, and
	// subscribers driving containment policy off Reason need this to
	// be distinct from "container_not_running".
	outcomeAddrInvalid
	// outcomeRetryExhausted: every attempt failed and
	// connectTotalTimeout elapsed. Conn / Stream are nil; Addr / Agent /
	// Project carry the latest inspect's view for the failure event.
	outcomeRetryExhausted
	// outcomeCtxDone: parent ctx cancelled mid-retry. No publish.
	outcomeCtxDone
)

// errContainerStopped is the sentinel resolveAgent returns when the
// inspect succeeded but State.Running is false. Distinct from a
// generic inspect error: a stopped container is terminal (no point
// retrying), a generic inspect error is transient (retry within the
// connect-total budget).
var errContainerStopped = errors.New("container not running")

// establishResult is the typed return of establishWithRetry.
// Conn / Stream are populated only when Outcome is outcomeSuccess.
// Agent / Project / Addr carry the latest inspect's view; Attempt
// records the cycle's attempt count (for both success and failure
// publishing). PeerInfo captures the connection-time identity outcomes
// (chain verify, CN pin, registry cross-check) populated during the
// TLS handshake + post-handshake registry lookup.
//
// StreamCancel cancels the gRPC stream's underlying context. The dial
// flow uses ONE Recv consumer at a time: helloHandshake then either
// drainStream alone (Match) or driveRegister followed by drainStream
// (Miss / mismatch). When driveRegister times out it must cancel the
// stream to unblock its own Recv goroutine BEFORE returning — that
// avoids two concurrent stream.Recv() callers (gRPC streams are not
// safe for concurrent Recv). runDial defers StreamCancel at end of
// cycle so the stream is always torn down even on early returns.
type establishResult struct {
	Conn         *grpc.ClientConn
	Stream       clawkerdv1.ClawkerdService_SessionClient
	StreamCancel context.CancelFunc
	Agent        string
	Project      string
	Addr         string
	Attempt      int
	Outcome      establishOutcome
	PeerInfo     peerInfo
}

// peerInfo is the connection-time identity capture from the TLS
// handshake. Registry-outcome state lives in the local registryOutcome
// enum, not on this struct, since the dial flow drives event
// publication directly off the outcome rather than threading a
// unified payload.
type peerInfo struct {
	PeerCN         string
	PeerThumbprint [sha256.Size]byte
	ChainVerified  bool
	// CaptureReason is set when capturePeer hit an unusual case (no
	// peer certs, leaf parse failed, chain verify failed). Empty on
	// the happy path. Stays purely diagnostic — the dialer never
	// aborts on cert grounds.
	CaptureReason string
}

// registryOutcome classifies the result of cross-checking the peer
// cert against the agentregistry row keyed by container_id. Internal
// to the dialer — drives event publication; not exposed on any
// public event type.
type registryOutcome int

const (
	// outcomeRegistryNotQueried is the zero value. Set when the
	// registry could not be queried at all (lookup error).
	outcomeRegistryNotQueried registryOutcome = iota
	// outcomeRegistryMatch — row exists, thumbprint AND canonical_cn
	// agree with the peer cert. Trusted, registered.
	outcomeRegistryMatch
	// outcomeRegistryMiss — no row for this container_id. Drives the
	// Register handshake (RegisterRequired Command on the Session
	// stream).
	outcomeRegistryMiss
	// outcomeRegistryThumbprintMismatch — row exists but thumbprint
	// disagrees with the live peer cert. Untrusted; AgentUntrusted
	// fires with ReasonThumbprintMismatch.
	outcomeRegistryThumbprintMismatch
	// outcomeRegistryCNMismatch — row exists, thumbprints agree, but
	// canonical_cn doesn't match the peer's CN. Untrusted;
	// AgentUntrusted fires with ReasonCNMismatch.
	outcomeRegistryCNMismatch
)

// establishWithRetry runs the inner exponential-backoff retry loop
// until either Hello+HelloAck succeeds, the inspect at the start of
// an attempt reports the container is gone / not running, or
// connectTotalTimeout elapses.
//
// TOCTOU defense: each attempt re-inspects the container BEFORE
// dialing. The inspect → dial → handshake sequence within a single
// attempt is the smallest atomic unit; we cannot eliminate the
// race entirely, but we bound it to one attempt's lifetime. A
// container that dies between attempts (sleep, manual stop) is
// caught by the next attempt's inspect and surfaces as
// outcomeContainerGone, which the caller maps to a terminal
// "container_not_running" failure rather than burning the retry
// budget.
func (d *Dialer) establishWithRetry(ctx context.Context, containerID string, log *logger.Logger) establishResult {
	deadline := time.Now().Add(connectTotalTimeout)
	backoff := connectInitialBackoff
	publishedConnecting := false

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return establishResult{Attempt: attempt, Outcome: outcomeCtxDone}
		}

		// Inspect at the start of every attempt — never a stale
		// snapshot from earlier in the cycle.
		inspect, err := d.resolveAgent(ctx, containerID)
		if err != nil {
			// Two flavors of inspect failure:
			//   (a) container truly gone (errdefs.IsNotFound) or stopped
			//       (errContainerStopped sentinel) — terminal, nothing to
			//       retry against.
			//   (b) generic inspect error (daemon transient, perms,
			//       network blip) — keep trying within the per-cycle
			//       retry budget; eventual deadline expiry surfaces as
			//       outcomeRetryExhausted with a transport-specific
			//       Reason rather than misclassifying as "container
			//       gone".
			if cerrdefs.IsNotFound(err) || errors.Is(err, errContainerStopped) {
				log.With("agent", "<unknown>", "project", "<unknown>").Info().Err(err).
					Int("attempt", attempt).
					Str("event", "agentdial_attempt_resolve_failed").
					Msg("container truly gone or stopped; exiting retry loop")
				return establishResult{Attempt: attempt, Outcome: outcomeContainerGone}
			}
			if time.Now().After(deadline) {
				log.With("agent", "<unknown>", "project", "<unknown>").Error().Err(err).
					Int("attempt", attempt).
					Str("event", "agentdial_inspect_timeout").
					Msg("gave up on inspect after total timeout")
				return establishResult{Attempt: attempt, Outcome: outcomeRetryExhausted}
			}
			sleep := backoffSleep(backoff)
			log.With("agent", "<unknown>", "project", "<unknown>").Warn().Err(err).
				Int("attempt", attempt).
				Dur("retry_in", sleep).
				Str("event", "agentdial_inspect_retry").
				Msg("inspect failed transiently; will retry with backoff")
			select {
			case <-ctx.Done():
				return establishResult{Attempt: attempt, Outcome: outcomeCtxDone}
			case <-time.After(sleep):
			}
			backoff = nextBackoff(backoff)
			continue
		}
		agent, project := agentLabels(inspect)
		addr, err := clawkerNetAddr(inspect)
		if err != nil {
			log.With("agent", agent, "project", project).Error().Err(err).
				Int("attempt", attempt).
				Str("event", "agentdial_attempt_addr_extract_failed").
				Msg("clawker-net address extraction failed; aborting cycle")
			return establishResult{Agent: agent, Project: project, Attempt: attempt, Outcome: outcomeAddrInvalid}
		}

		// Every log line from here forward carries agent + project +
		// addr so an operator reading retry/timeout/error events in
		// Grafana doesn't have to cross-reference the container_id
		// against docker inspect to know which agent we're dialing.
		attemptLog := log.With("agent", agent, "project", project, "addr", addr)

		// Publish "connecting" once per cycle, on the first
		// successful inspect — gives consumers a useful event with
		// the address we'll be dialing rather than emitting on
		// every retry attempt.
		if !publishedConnecting {
			d.publishConnecting(ctx, containerID, agent, project, addr)
			publishedConnecting = true
		}

		conn, stream, streamCancel, peer, dialErr := d.tryEstablish(ctx, addr, attemptLog)
		if dialErr == nil {
			return establishResult{
				Conn:         conn,
				Stream:       stream,
				StreamCancel: streamCancel,
				Agent:        agent,
				Project:      project,
				Addr:         addr,
				Attempt:      attempt,
				Outcome:      outcomeSuccess,
				PeerInfo:     peer,
			}
		}

		if time.Now().After(deadline) {
			attemptLog.Error().
				Str("dial_target", addr).
				Err(dialErr).
				Int("attempt", attempt).
				Str("event", "agentdial_connect_timeout").
				Msg("gave up on Session establishment after total timeout")
			return establishResult{Agent: agent, Project: project, Addr: addr, Attempt: attempt, Outcome: outcomeRetryExhausted}
		}

		// dial_target + the unwrapped err separate "where we tried
		// to connect" from "what surfaced when we opened the stream".
		// grpc.NewClient is non-blocking, so the first observed
		// transport error always lands on the Session() open path or
		// on the Hello handshake — wrapping addr into the err string
		// duplicated the structured field and obscured the actual
		// cause.
		sleep := backoffSleep(backoff)
		attemptLog.Warn().
			Str("dial_target", addr).
			Err(dialErr).
			Int("attempt", attempt).
			Dur("retry_in", sleep).
			Str("event", "agentdial_connect_retry").
			Msg("Session establishment failed; will retry with backoff")

		select {
		case <-ctx.Done():
			return establishResult{Agent: agent, Project: project, Addr: addr, Attempt: attempt, Outcome: outcomeCtxDone}
		case <-time.After(sleep):
		}
		backoff = nextBackoff(backoff)
	}
}

// backoffSleep returns a full-jitter sleep ∈ [0, backoff). Prevents
// thundering-herd on CP boot when many clawkerds came up together
// and all failed their first dial in lockstep.
func backoffSleep(backoff time.Duration) time.Duration {
	if backoff <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(backoff)))
}

// nextBackoff doubles the current backoff up to connectMaxBackoff.
func nextBackoff(backoff time.Duration) time.Duration {
	backoff *= 2
	if backoff > connectMaxBackoff {
		backoff = connectMaxBackoff
	}
	return backoff
}

// tryEstablish runs one connection attempt: dial → open stream →
// Hello handshake. Returns the open conn + stream + the captured
// peerInfo (cert-related fields populated by VerifyPeerCertificate)
// on success, or an error describing which step failed.
//
// Cert-related fields (PeerCN, PeerThumbprint, ChainVerified) are
// captured during the TLS handshake via VerifyPeerCertificate that
// always returns nil — the dialer is permissive and never aborts on
// cert grounds. The dial flow drives registry classification +
// event publication off the captured peerInfo.
func (d *Dialer) tryEstablish(ctx context.Context, addr string, log *logger.Logger) (*grpc.ClientConn, clawkerdv1.ClawkerdService_SessionClient, context.CancelFunc, peerInfo, error) {
	var peer peerInfo
	conn, err := d.dial(ctx, addr, &peer)
	if err != nil {
		return nil, nil, nil, peerInfo{}, err
	}

	// Stream-scoped ctx so driveRegister can cancel just this stream
	// (not the whole runDial cycle) when RegisterDone times out.
	streamCtx, streamCancel := context.WithCancel(ctx)
	client := clawkerdv1.NewClawkerdServiceClient(conn)
	stream, err := client.Session(streamCtx)
	if err != nil {
		streamCancel()
		_ = conn.Close()
		return nil, nil, nil, peerInfo{}, fmt.Errorf("open Session stream: %w", err)
	}

	if err := d.helloHandshake(stream, log); err != nil {
		streamCancel()
		_ = conn.Close()
		return nil, nil, nil, peerInfo{}, fmt.Errorf("Hello handshake: %w", err)
	}
	return conn, stream, streamCancel, peer, nil
}

// resolveAgent inspects the container and returns the moby
// InspectResponse after enforcing one precondition:
// State.Running == true. Stopped containers can keep stale entries
// in NetworkSettings, so checking the IP alone would happily try to
// dial a dead container; State.Running (and its companion Status
// enum from moby) is the authoritative aliveness signal.
//
// Returning the full InspectResponse instead of a cherry-picked
// projection keeps moby's schema as the source of truth — callers
// read whatever fields they need (IP, labels, state, restart count,
// health) without this package having to maintain a parallel
// schema or re-inspect.
func (d *Dialer) resolveAgent(ctx context.Context, containerID string) (mobycontainer.InspectResponse, error) {
	res, err := d.docker.ContainerInspect(ctx, containerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return mobycontainer.InspectResponse{}, fmt.Errorf("inspect: %w", err)
	}
	c := res.Container
	if c.State == nil || !c.State.Running {
		state := "<nil-state>"
		if c.State != nil {
			state = string(c.State.Status)
		}
		return mobycontainer.InspectResponse{}, fmt.Errorf("%w (state=%s)", errContainerStopped, state)
	}
	return c, nil
}

// clawkerNetAddr extracts the host:port dial target from an inspect
// response. Containers without a clawker-net endpoint are a
// contract violation — every managed agent container is attached at
// create time.
func clawkerNetAddr(c mobycontainer.InspectResponse) (string, error) {
	if c.NetworkSettings == nil {
		return "", errors.New("container has no NetworkSettings (clawker-net contract violation)")
	}
	endpoint, ok := c.NetworkSettings.Networks[consts.Network]
	if !ok || !endpoint.IPAddress.IsValid() {
		return "", fmt.Errorf("container has no %s endpoint", consts.Network)
	}
	ip := net.ParseIP(endpoint.IPAddress.String())
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip == nil {
		return "", fmt.Errorf("invalid IP for %s endpoint", consts.Network)
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(consts.DefaultClawkerdPort)), nil
}

// classifyRegistry cross-checks the captured peer cert against the
// agentregistry row keyed by container_id and returns the typed
// outcome plus any diagnostic detail. The dial flow uses the outcome
// to drive event publication (Match → SessionConnected only; Miss →
// drives Register handshake; mismatch outcomes → AgentUntrusted).
//
// Identity comparison is between (a) the live peer cert thumbprint /
// CN and (b) the registry row's Thumbprint / canonical_cn. The
// inspect labels (project, agent on the docker container) are NOT
// consulted here — they're consulted by the Register handler at row
// CREATE time. After the row exists, the row IS the identity; later
// label edits cannot drift identity without invalidating the cert.
//
// Connection NEVER aborts here. Lookup errors that are NOT
// "no such row" return outcomeRegistryNotQueried and a non-empty
// detail string — these indicate a sqlite/IO regression visible to
// operators even though the connection proceeds.
func (d *Dialer) classifyRegistry(peer peerInfo, containerID string) (registryOutcome, string) {
	if d.agents == nil {
		// Wiring bug — New rejected nil agents, so this can only
		// happen in a test that bypassed New.
		return outcomeRegistryNotQueried, "registry not wired"
	}

	entry, err := d.agents.LookupByContainerID(containerID)
	if err != nil && !errors.Is(err, ErrUnknownAgent) {
		return outcomeRegistryNotQueried, "registry lookup error: " + err.Error()
	}
	if entry == nil {
		return outcomeRegistryMiss, ""
	}

	if entry.Thumbprint != peer.PeerThumbprint {
		return outcomeRegistryThumbprintMismatch, ""
	}

	expectedCN, cnErr := canonicalCNFromStrings(entry.Project, entry.AgentName)
	if cnErr != nil {
		return outcomeRegistryNotQueried, "registry row CN compose failed: " + cnErr.Error()
	}
	if expectedCN != peer.PeerCN {
		return outcomeRegistryCNMismatch, ""
	}
	return outcomeRegistryMatch, ""
}

// computeCNPinMatch reports whether peerCN equals the canonical agent
// CN derived from the inspect labels (project, agent). Returns false
// if either label is missing/malformed (no panic — labels can be
// arbitrary user-supplied strings on a malicious or misconfigured
// container). Independent of the registry-row check.
func computeCNPinMatch(peerCN, project, agent string) bool {
	if peerCN == "" || agent == "" {
		return false
	}
	expected, err := canonicalCNFromStrings(project, agent)
	if err != nil {
		return false
	}
	return peerCN == expected
}

// canonicalCNFromStrings safely composes a canonical agent CN from
// raw strings, returning an error rather than panicking on malformed
// input. Wraps auth.CanonicalAgentCN with the err-returning typed
// constructors (auth.NewProjectSlug / auth.NewAgentName).
func canonicalCNFromStrings(project, agent string) (string, error) {
	proj, err := auth.NewProjectSlug(project)
	if err != nil {
		return "", err
	}
	ag, err := auth.NewAgentName(agent)
	if err != nil {
		return "", err
	}
	return auth.CanonicalAgentCN(proj, ag), nil
}

// agentLabels reads the (agent, project) labels from an inspect
// response. Either may be empty if the container was created
// without the standard clawker labels — callers must tolerate.
func agentLabels(c mobycontainer.InspectResponse) (agent, project string) {
	if c.Config == nil {
		return "", ""
	}
	return c.Config.Labels[consts.LabelAgent], c.Config.Labels[consts.LabelProject]
}

// dial builds the mTLS gRPC client connection. PERMISSIVE: the TLS
// handshake completes regardless of cert chain validity, hostname
// match, or expiry — VerifyPeerCertificate is a data-capture hook
// that ALWAYS returns nil. The asymmetric-trust rationale lives in
// the package doc; the short version is "CP must always reach
// clawkerd to issue containment commands; cert mismatch is a data
// point, not an abort condition".
//
// peer is populated during the handshake with PeerCN,
// PeerThumbprint, and ChainVerified. The handshake is lazy under
// grpc.NewClient — these fields are not filled until the first RPC
// (Session open) triggers the underlying TLS dial.
//
// Keepalive parameters are sourced from internal/consts so server
// (clawkerd) and client (CP) cannot drift.
func (d *Dialer) dial(_ context.Context, addr string, peer *peerInfo) (*grpc.ClientConn, error) {
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{d.cpClientCert},
		RootCAs:      d.caPool,
		MinVersion:   tls.VersionTLS13,
		// InsecureSkipVerify disables the stdlib's hostname + chain
		// gate so VerifyPeerCertificate can run as a permissive
		// data-capture hook (see capturePeer + package doc).
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			d.capturePeer(rawCerts, peer)
			return nil // ALWAYS — connection never aborts on cert grounds
		},
	}
	return grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                consts.ClawkerdKeepaliveClientPingInterval,
			Timeout:             consts.ClawkerdKeepalivePingTimeout,
			PermitWithoutStream: true,
		}),
	)
}

// capturePeer populates PeerCN / PeerThumbprint / ChainVerified on
// peer from the peer's TLS handshake material. Permissive: every
// code path returns without error; outcomes flow into peer fields
// (and peer.CaptureReason for unusual cases). Extracted from the
// dial() callback so tests can drive it directly without standing
// up a TLS server.
func (d *Dialer) capturePeer(rawCerts [][]byte, peer *peerInfo) {
	if len(rawCerts) == 0 {
		if peer.CaptureReason == "" {
			peer.CaptureReason = "peer presented no certs"
		}
		return
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			if peer.CaptureReason == "" {
				peer.CaptureReason = "leaf parse failed: " + err.Error()
			}
			return
		}
		certs = append(certs, c)
	}
	leaf := certs[0]
	peer.PeerCN = leaf.Subject.CommonName
	peer.PeerThumbprint = sha256.Sum256(rawCerts[0])

	// Chain-verify against the CLI CA. Outcome is a data point;
	// failure does NOT abort.
	opts := x509.VerifyOptions{Roots: d.caPool, Intermediates: x509.NewCertPool()}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	if _, err := leaf.Verify(opts); err == nil {
		peer.ChainVerified = true
	} else if peer.CaptureReason == "" {
		peer.CaptureReason = "chain verify: " + err.Error()
	}
}

// helloHandshake sends Hello and awaits HelloAck. Anything else as
// the first Response is treated as a protocol violation.
func (d *Dialer) helloHandshake(stream clawkerdv1.ClawkerdService_SessionClient, log *logger.Logger) error {
	if err := stream.Send(&clawkerdv1.Command{
		CommandId: "hello",
		Payload:   &clawkerdv1.Command_Hello{Hello: &clawkerdv1.Hello{}},
	}); err != nil {
		return fmt.Errorf("send Hello: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv HelloAck: %w", err)
	}
	if _, ok := resp.Payload.(*clawkerdv1.Response_HelloAck); !ok {
		log.Error().
			Str("event", "agentdial_hello_unexpected_response").
			Str("got_type", fmt.Sprintf("%T", resp.Payload)).
			Msg("clawkerd returned non-HelloAck for Hello")
		return fmt.Errorf("expected HelloAck, got %T", resp.Payload)
	}
	return nil
}

// drainOutcome classifies why drainStream returned. Replaces the
// previous string-sentinel return ("eof" / "ctx_done" / err.Error()),
// where the caller dispatched on a string compare.
type drainOutcome int

const (
	// drainGracefulEOF: peer (clawkerd) closed the Session cleanly.
	drainGracefulEOF drainOutcome = iota
	// drainCtxCanceled: parent ctx cancelled (CP shutdown).
	// publishBroken is suppressed for this outcome — the bus is on
	// its way down anyway.
	drainCtxCanceled
	// drainStreamErr: Recv returned a non-EOF error and ctx is still
	// live. Treat as transient (re-establish on next cycle).
	drainStreamErr
)

// drainResult is the typed return of drainStream. Reason carries a
// short classification string for the SessionBroken event when the
// outcome is drainGracefulEOF or drainStreamErr.
type drainResult struct {
	Outcome drainOutcome
	Reason  string
}

// drainStream holds the Session open. Reads each Response and
// discards (CP doesn't dispatch any further Commands in this commit).
// Exits on EOF (peer close), ctx cancel (CP shutdown), or error.
func (d *Dialer) drainStream(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, log *logger.Logger) drainResult {
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			log.Info().Str("event", "agentdial_session_eof").Msg("clawkerd closed Session")
			return drainResult{Outcome: drainGracefulEOF, Reason: "eof"}
		}
		if err != nil {
			if ctx.Err() != nil {
				log.Info().Str("event", "agentdial_session_ctx_done").Msg("CP-side teardown")
				return drainResult{Outcome: drainCtxCanceled, Reason: "ctx_done"}
			}
			log.Error().Err(err).Str("event", "agentdial_session_recv_failed").Msg("Session.Recv")
			return drainResult{Outcome: drainStreamErr, Reason: err.Error()}
		}
		log.Debug().
			Str("event", "agentdial_unexpected_response").
			Str("type", fmt.Sprintf("%T", resp.Payload)).
			Str("command_id", resp.CommandId).
			Msg("ignoring unsolicited Response from clawkerd")
	}
}

// registerRequiredTimeout caps how long the dispatch path waits for
// clawkerd's RegisterDone Response after sending RegisterRequired on
// the Session stream. Hydra exchange + mTLS dial + Register handler
// is the chain on the agent side; 30s is comfortably more than the
// chained latency under normal conditions.
const registerRequiredTimeout = 30 * time.Second

// dispatchAgentEvents drives agent-axis event publication after the
// Session is up. Branches on the registry classification:
//
//   - Match → no extra event (SessionConnected already updated the
//     trusted state; the durable Registered=true flag persists
//     because the row exists)
//   - Miss → drive the Register handshake (send RegisterRequired,
//     wait for RegisterDone, re-lookup), publish AgentRegistered
//     and AgentUntrusted{ReasonRegisterFailed} on failure
//   - ThumbprintMismatch → publish AgentUntrusted{ReasonThumbprintMismatch}
//   - CNMismatch → publish AgentUntrusted{ReasonCNMismatch}
//   - NotQueried (lookup error) → publish AgentUntrusted with detail
//
// Caller must have already called publishConnected — the agent state
// in overseer is populated by the time we evaluate trust outcomes.
func (d *Dialer) dispatchAgentEvents(ctx context.Context, containerID string, res establishResult, log *logger.Logger) {
	outcome, detail := d.classifyRegistry(res.PeerInfo, containerID)

	switch outcome {
	case outcomeRegistryMatch:
		// Nothing to publish — agent is already provenanced and
		// SessionConnected.ApplyTo set Trusted=true. The durable
		// Registered=true is reflected via the Hello-time row
		// existence; no event needed for the steady-state case.
		return
	case outcomeRegistryMiss:
		// CP just observed a never-before-seen container. Drive the
		// CP-triggered Register flow: send RegisterRequired on the
		// Session bidi stream, wait for RegisterDone from clawkerd,
		// re-lookup the registry to confirm the row landed.
		d.driveRegister(ctx, containerID, res, log)
	case outcomeRegistryThumbprintMismatch:
		log.Warn().
			Str("event", "agent_untrusted").
			Str("reason", string(overseer.UntrustedReasonThumbprintMismatch)).
			Msg("registered cert thumbprint differs from live peer cert; agent untrusted")
		overseer.Publish(d.bus, AgentUntrusted{
			ContainerID: containerID,
			AgentName:   res.Agent,
			Project:     res.Project,
			Reason:      overseer.UntrustedReasonThumbprintMismatch,
			At:          time.Now(),
		})
	case outcomeRegistryCNMismatch:
		log.Warn().
			Str("event", "agent_untrusted").
			Str("reason", string(overseer.UntrustedReasonCNMismatch)).
			Msg("registered canonical_cn differs from live peer CN; agent untrusted")
		overseer.Publish(d.bus, AgentUntrusted{
			ContainerID: containerID,
			AgentName:   res.Agent,
			Project:     res.Project,
			Reason:      overseer.UntrustedReasonCNMismatch,
			At:          time.Now(),
		})
	case outcomeRegistryNotQueried:
		// Lookup error or wiring bug. Connection proceeds (asymmetric
		// trust) but worldview reflects the unverifiable state.
		log.Warn().
			Str("event", "agent_untrusted").
			Str("detail", detail).
			Msg("registry classification could not be determined; agent untrusted")
		overseer.Publish(d.bus, AgentUntrusted{
			ContainerID: containerID,
			AgentName:   res.Agent,
			Project:     res.Project,
			Reason:      overseer.UntrustedReasonCertInvalid,
			Detail:      detail,
			At:          time.Now(),
		})
	default:
		// Exhaustive cases above; reaching this branch means a new
		// outcome value was added without updating dispatch. Log as
		// Error and treat as untrusted to fail closed.
		log.Error().
			Int("outcome", int(outcome)).
			Str("event", "agent_dispatch_unknown_outcome").
			Msg("registryOutcome added without dispatch wiring; failing closed")
		overseer.Publish(d.bus, AgentUntrusted{
			ContainerID: containerID,
			AgentName:   res.Agent,
			Project:     res.Project,
			Reason:      overseer.UntrustedReasonCertInvalid,
			Detail:      fmt.Sprintf("unknown registryOutcome %d", int(outcome)),
			At:          time.Now(),
		})
	}
}

// runInit invokes the wired Executor against the open Session. No-op
// (with a warning) if no Executor was set — operators see the
// misconfiguration in the structured log; the entrypoint will then
// time out on its fifo and the container will fail to launch CMD.
//
// Failure to complete init is NOT terminal for the Session: the stream
// stays open so subsequent containment commands can still be
// dispatched (asymmetric trust). The init failure is captured on the
// overseer worldview via InitFailed; subscribers (CLI WatchAgent,
// monitoring) consume that to surface to operators.
func (d *Dialer) runInit(ctx context.Context, containerID string, res establishResult, log *logger.Logger) {
	if d.initExec == nil {
		log.Warn().
			Str("event", "agent_init_executor_unset").
			Msg("agent.init: no Executor wired on dialer; entrypoint will hang on its fifo until timeout")
		return
	}
	target := InitTarget{
		ContainerID: containerID,
		AgentName:   res.Agent,
		Project:     res.Project,
	}
	if err := d.initExec.Run(ctx, res.Stream, target); err != nil {
		log.Warn().Err(err).
			Str("event", "agent_init_run_failed").
			Msg("agent.init: Executor.Run returned error; Session held open for containment")
	}
}

// driveRegister sends RegisterRequired on the Session stream and
// waits for the matching RegisterDone Response. After the agent
// reports completion, re-looks up the registry to confirm the row
// landed and publishes AgentRegistered (success or failure) plus
// AgentUntrusted on failure.
//
// Concurrent-Recv safety: gRPC streams are NOT safe for concurrent
// stream.Recv. driveRegister is called BEFORE drainStream and must
// guarantee that its inner Recv goroutine has fully exited before it
// returns — otherwise drainStream's Recv would race the leftover
// goroutine on the same stream. On timeout we cancel the stream-scoped
// ctx (res.StreamCancel) which unblocks the parked Recv with a ctx
// error; we then wait on `done` to confirm the goroutine exited. On
// the timeout path the stream is intentionally torn down — runDial
// will reconnect on the next cycle (a clawkerd that didn't respond to
// RegisterRequired in registerRequiredTimeout is unhealthy enough that
// re-establishing is the right reaction). Asymmetric trust is still
// preserved: we don't abort on cert grounds, only on protocol-level
// liveness.
func (d *Dialer) driveRegister(ctx context.Context, containerID string, res establishResult, log *logger.Logger) {
	commandID := "register-" + containerID
	if err := res.Stream.Send(&clawkerdv1.Command{
		CommandId: commandID,
		Payload:   &clawkerdv1.Command_RegisterRequired{RegisterRequired: &clawkerdv1.RegisterRequired{}},
	}); err != nil {
		d.publishRegisterFailure(containerID, res, "send RegisterRequired: "+err.Error(), log)
		return
	}

	// Wait for RegisterDone with timeout. Other Response types that
	// arrive in the interim are unexpected (clawkerd should serialize
	// per-command Responses by command_id) — discard and keep waiting.
	waitCtx, cancel := context.WithTimeout(ctx, registerRequiredTimeout)
	defer cancel()

	type recvResult struct {
		resp *clawkerdv1.Response
		err  error
	}
	ch := make(chan recvResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			r, err := res.Stream.Recv()
			if err != nil {
				ch <- recvResult{err: err}
				return
			}
			if _, ok := r.Payload.(*clawkerdv1.Response_RegisterDone); ok && r.CommandId == commandID {
				ch <- recvResult{resp: r}
				return
			}
		}
	}()

	select {
	case <-waitCtx.Done():
		// Cancel the stream so the parked Recv unblocks, then wait for
		// the goroutine to exit so drainStream can safely take over.
		if res.StreamCancel != nil {
			res.StreamCancel()
		}
		<-done
		d.publishRegisterFailure(containerID, res, "RegisterDone timeout: "+waitCtx.Err().Error(), log)
		return
	case got := <-ch:
		<-done
		if got.err != nil {
			d.publishRegisterFailure(containerID, res, "Recv RegisterDone: "+got.err.Error(), log)
			return
		}
		regDone := got.resp.GetRegisterDone()
		if regDone == nil || !regDone.GetOk() {
			detail := "clawkerd reported failure"
			if regDone != nil && regDone.GetError() != "" {
				detail = regDone.GetError()
			}
			d.publishRegisterFailure(containerID, res, detail, log)
			return
		}
	}

	// Re-lookup to confirm the row actually landed in sqlite.
	// Distinguish a sqlite I/O error (registry layer regression: the
	// CP-side handler reported success but the row isn't readable)
	// from "no row" (handler accepted Welcome but never wrote — also a
	// regression). Both publish failure but with different reasons so
	// containment subscribers can differentiate.
	entry, err := d.agents.LookupByContainerID(containerID)
	if err != nil && !errors.Is(err, ErrUnknownAgent) {
		d.publishRegisterFailure(containerID, res, "registry lookup error after RegisterDone: "+err.Error(), log)
		return
	}
	if entry == nil {
		d.publishRegisterFailure(containerID, res, "registry row missing after RegisterDone", log)
		return
	}

	log.Info().
		Str("event", "agent_registered").
		Msg("CP-driven Register completed; row written")
	overseer.Publish(d.bus, AgentRegistered{
		ContainerID: containerID,
		AgentName:   res.Agent,
		Project:     res.Project,
		Ok:          true,
		At:          time.Now(),
	})
}

func (d *Dialer) publishRegisterFailure(containerID string, res establishResult, reason string, log *logger.Logger) {
	log.Warn().
		Str("event", "agent_register_failed").
		Str("reason", reason).
		Msg("CP-driven Register did not complete; agent unprovenanceable")
	overseer.Publish(d.bus, AgentRegistered{
		ContainerID: containerID,
		AgentName:   res.Agent,
		Project:     res.Project,
		Ok:          false,
		Reason:      reason,
		At:          time.Now(),
	})
	overseer.Publish(d.bus, AgentUntrusted{
		ContainerID: containerID,
		AgentName:   res.Agent,
		Project:     res.Project,
		Reason:      overseer.UntrustedReasonRegisterFailed,
		Detail:      reason,
		At:          time.Now(),
	})
}

// publishConnecting records the start of a dial attempt. Status is
// "connecting" until the dial succeeds (→ connected) or the retry
// budget exhausts (→ failed). agent + project may be empty if the
// container is missing labels (still publish — gives consumers a way
// to surface "we're dialing something we can't identify").
//
// Logging is delegated to overseer's PublishHook so a single line
// appears per event from the bus loop, instead of one here AND one
// from the hook.
func (d *Dialer) publishConnecting(ctx context.Context, containerID, agent, project, addr string) {
	if ctx.Err() != nil {
		return
	}
	overseer.Publish(d.bus, SessionConnecting{
		ContainerID: containerID,
		AgentName:   agent,
		Project:     project,
		Address:     addr,
		At:          time.Now(),
	})
}

// publishConnected records that the Session handshake succeeded
// (mTLS + Hello + HelloAck) on the given attempt. peer carries the
// captured cert identity (PeerCN, PeerThumbprint) flat on the event.
// Trust/registration outcomes are published via separate events
// (AgentRegistered, AgentUntrusted).
func (d *Dialer) publishConnected(ctx context.Context, containerID, agent, project, addr string, attempt int, peer peerInfo) {
	if ctx.Err() != nil {
		return
	}
	overseer.Publish(d.bus, SessionConnected{
		ContainerID:    containerID,
		AgentName:      agent,
		Project:        project,
		Address:        addr,
		Attempts:       attempt,
		PeerCN:         peer.PeerCN,
		PeerThumbprint: peer.PeerThumbprint,
		At:             time.Now(),
	})
}

// publishFailed records that the retry budget exhausted before any
// attempt established a Session. reason carries the last error
// message; attempts is the count when we gave up (0 if we never
// even reached a dial because resolveAgent failed).
func (d *Dialer) publishFailed(ctx context.Context, containerID, agent, project, addr, reason string, attempts int) {
	if ctx.Err() != nil {
		return
	}
	overseer.Publish(d.bus, SessionFailed{
		ContainerID: containerID,
		AgentName:   agent,
		Project:     project,
		Address:     addr,
		Reason:      reason,
		Attempts:    attempts,
		At:          time.Now(),
	})
}

// publishBroken records that an established Session terminated.
// reason classifies the cause: "eof" (clawkerd graceful close),
// "ctx_done" (CP teardown), or the underlying Recv error message.
//
// Skips bus emit when ctx is already done — the bus is on its way
// down anyway and subscribers are about to be released.
func (d *Dialer) publishBroken(ctx context.Context, containerID, agent, project, addr, reason string) {
	if ctx.Err() != nil {
		return
	}
	overseer.Publish(d.bus, SessionBroken{
		ContainerID: containerID,
		AgentName:   agent,
		Project:     project,
		Address:     addr,
		Reason:      reason,
		At:          time.Now(),
	})
}
