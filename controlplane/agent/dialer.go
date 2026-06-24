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
// Cert chain verification and registry thumbprint classification
// outcomes are captured on the establishResult and surfaced through
// the typed event surface — SessionConnected carries flat
// PeerAgentFullName/PeerThumbprint fields (purely diagnostic, never a
// gate); AgentRegistered/AgentUntrusted carry the policy outcomes.
// Subscribers consume those events to enact policy (containment,
// alerting, eviction); the dialer holds no policy itself.
//
// Why permissive: CP must always be able to reach clawkerd to issue
// containment commands (iptables lock, network detach, container kill).
// A compromised clawkerd presenting a bad cert is exactly when the
// channel must be UP so CP can react. Aborting on cert grounds would
// strand CP exactly at the moment governance is most needed.
//
// The asymmetric counterpart lives in clawkerd/listener.go: the
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
	"github.com/schmitthub/clawker/controlplane/pubsub"
	"github.com/schmitthub/clawker/internal/auth"
	"github.com/schmitthub/clawker/internal/consts"
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

// CloseErrCeiling bounds the number of consecutive conn.Close()
// failures the dial loop will tolerate before giving up on the
// target. A non-zero close error usually means a transport-level
// state machine the gRPC client can't unwind cleanly (broken
// keepalive, half-closed socket); each one leaks a file descriptor
// + a couple of background goroutines. Five gives a transient
// hiccup room to recover without letting a structurally-broken
// peer accumulate resources without bound. Successful Close
// anywhere in the loop resets the counter.
const CloseErrCeiling = 5

// Dialer captures the CP-side material every dial needs. Construct
// once at CP startup; share across all agent dials.
//
// dialing is the dedup + cancel map: containerIDs currently being
// dialed (or already-Session-established) mapped to the cancel func
// for their per-dial ctx. Initial poll and the dockerevents subscriber
// both call DialAgent for the same running container; the dedup keeps
// the second call from spinning a duplicate goroutine against an
// already-open Session. Membership lasts the lifetime of the dial
// goroutine — after the Session closes (peer drop, ctx cancel, retry
// timeout), the entry is removed and a future event for the same
// containerID dials fresh.
//
// CancelDial uses the stored cancel func to tear down a Session
// synchronously with a registry-evict (container/destroy) — without
// it, the dialer's runDial loop only notices the disappearance on the
// next reconnect attempt via outcomeContainerGone, leaving a doomed
// stream open during the interval.
type Dialer struct {
	Log    *logger.Logger
	Docker mobyclient.APIClient
	Topic  *pubsub.Topic[AgentEvent]
	// Agents is the CP-owned agentregistry (read+write). The dialer
	// reads it at handshake time to classify the peer cert against
	// the registered row: match (registered), miss (drives Register
	// handshake), thumbprint mismatch (untrusted). Connection stays
	// open in all cases. The Register flow re-reads after RegisterDone
	// to confirm the row landed. Required (non-nil) — wiring bug if
	// unset.
	Agents Registry

	// Executor dispatches the CP-driven init plan after
	// dispatchAgentEvents and before drainStream. nil disables init
	// (entrypoint hangs on its fifo until timeout) — runInit logs a
	// warning so the misconfiguration is observable. Set at
	// construction; immutable after Start.
	Executor *Executor

	CpClientCert tls.Certificate
	CaPool       *x509.CertPool

	mu      sync.Mutex
	Dialing map[string]context.CancelFunc
}

// NewDialer constructs a Dialer. Returns an error if the CP client cert /
// key cannot be loaded — better to fail at CP startup than to defer
// the failure to the first dial. Returns (nil, error) on any nil
// required dependency; never panics, per the CP serve-path contract.
//
// topic is required: the dialer publishes the unified AgentEvent
// (session connecting/connected/failed/broken, registry registered,
// trust untrusted) so other CP domains can subscribe to the agent axis
// without coupling to the dialer directly. Pass a real
// *pubsub.Topic[AgentEvent]; tests can construct one cheaply.
//
// agents is required: every successful dial cross-checks the peer
// cert against the registry row keyed by container_id and dispatches
// the registered / untrusted AgentEvents accordingly. nil agents would
// strand worldview consumers without a registration signal.
func NewDialer(log *logger.Logger, docker mobyclient.APIClient, topic *pubsub.Topic[AgentEvent], agents Registry, certPath, keyPath string, caPool *x509.CertPool, executor *Executor) (*Dialer, error) {
	if log == nil {
		log = logger.Nop()
	}
	if topic == nil {
		return nil, errors.New("agent.New: topic is required")
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
		Log:          log,
		Docker:       docker,
		Topic:        topic,
		Agents:       agents,
		Executor:     executor,
		CpClientCert: cert,
		CaPool:       caPool,
		Dialing:      make(map[string]context.CancelFunc),
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
	dialCtx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	if _, exists := d.Dialing[containerID]; exists {
		d.mu.Unlock()
		cancel()
		return
	}
	d.Dialing[containerID] = cancel
	d.mu.Unlock()

	go func() {
		// Defer order is load-bearing (LIFO): recover registered
		// first so it runs LAST, wrapping the dedup cleanup. A
		// panic inside cleanup is then also caught instead of
		// killing PID 1 and stranding eBPF programs. See
		// ./controlplane/CLAUDE.md "Resilience contract".
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
			if d.Agents != nil {
				if entry, lerr := d.Agents.LookupByContainerID(containerID); lerr == nil && entry != nil {
					agentName, project = entry.AgentName.String(), entry.Project.String()
				}
			}
			d.Log.Error().
				Interface("panic", r).
				Bytes("stack", debug.Stack()).
				Str("container_id", containerID).
				Str("agent", agentName).
				Str("project", project).
				Str("event", "agentdial_panic").
				Msg("agent.dial: dial goroutine panicked; CP otherwise unaffected. Publishing synthetic SessionFailed so worldview consumers see a terminal lifecycle event.")
			// Publish unconditionally (not via publishFailed, which
			// short-circuits when the dial ctx is done): the panic path
			// needs the worldview transition to land even during
			// shutdown — operators are about to lose every other signal.
			// publish is a no-op on a closed/full topic (returns false;
			// never panics), so this is safe from the recover.
			Publish(d.Topic, newAgentEvent(
				Agent{ContainerID: containerID, AgentName: agentName, Project: project},
				Message{
					Type:   DialerEventType,
					Action: ActionFailed,
					Reason: ReasonFailed,
					Detail: fmt.Sprintf("dial_goroutine_panic: %v", r),
				},
			))
		}()
		defer func() {
			d.mu.Lock()
			delete(d.Dialing, containerID)
			d.mu.Unlock()
			// Release the per-dial ctx resources. Safe to call after
			// CancelDial already cancelled it — context.CancelFunc is
			// idempotent.
			cancel()
		}()
		d.runDial(ctx, dialCtx, containerID)
	}()
}

// Initial-dial poll parameters. These bound the one-shot reconcile that
// dials every already-running agent at CP boot. The list call is the
// only thing retried — DialAgent itself is fire-and-forget. Three
// attempts with exponential backoff from initialDialListBackoff absorbs
// the transient docker-daemon hiccup that is the dominant failure mode
// at boot without delaying readiness materially.
const (
	initialDialMaxAttempts = 3
	initialDialListBackoff = 100 * time.Millisecond
)

// DialAllRunning dials every already-running agent container at CP boot.
// It spawns its own goroutine and returns immediately — it MUST NOT
// block CP readiness, and MUST NOT fail CP if the list call errors.
//
// The list is retried with bounded exponential backoff
// (initialDialMaxAttempts attempts from initialDialListBackoff) to
// absorb the transient docker-daemon hiccup that dominates boot-time
// failures. Each resolved container ID is handed to the fire-and-forget
// DialAgent; the dialing dedup map keeps a later runtime ContainerStarted
// event from spinning a duplicate dial against an already-open Session.
//
// The goroutine recovers: a panic deep in DialAgent (or the list path)
// must not strand the initial-poll dispatch silently or take down PID 1
// and freeze eBPF — runtime ContainerStarted handlers are unaffected.
func (d *Dialer) DialAllRunning(ctx context.Context, lister *ContainerLister, opts ListOpts) {
	go func() {
		// recover so a panic deep in DialAgent doesn't silently strand
		// every initial-poll agent without surfacing — this goroutine
		// has no other observer.
		defer func() {
			if r := recover(); r != nil {
				d.Log.Error().
					Interface("panic", r).
					Str("event", "agentdial_initial_poll_panic").
					Str("component", "cp.agentdial").
					Msg("initial agent dial goroutine panicked; initial-poll dispatch aborted (runtime ContainerStarted handlers unaffected)")
			}
		}()
		backoff := initialDialListBackoff
		var initialAgents []string
		var listErr error
		for attempt := 1; attempt <= initialDialMaxAttempts; attempt++ {
			initialAgents, listErr = lister.List(ctx, opts)
			if listErr == nil {
				if attempt > 1 {
					d.Log.Info().Int("attempt", attempt).Str("event", "agentdial_initial_list_recovered").Msg("list agent containers recovered after retry")
				}
				break
			}
			if attempt == initialDialMaxAttempts {
				break
			}
			d.Log.Warn().Err(listErr).Int("attempt", attempt).Dur("backoff", backoff).Str("event", "agentdial_initial_list_retry").Msg("list agent containers failed; retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
		}
		if listErr != nil {
			d.Log.Error().Err(listErr).Str("event", "agentdial_initial_list_failed").Msg("list agent containers")
			return
		}
		for _, id := range initialAgents {
			d.DialAgent(ctx, id)
		}
		d.Log.Info().Int("count", len(initialAgents)).Str("event", "agentdial_initial_poll_dispatched").Msg("dispatched initial CP→clawkerd dials")
	}()
}

// CancelDial synchronously cancels the in-flight Session for
// containerID, if any. Called by the registry-evict subscriber on
// container/destroy so the dialer tears down the doomed stream
// immediately rather than waiting for the next reconnect to classify
// outcomeContainerGone. Safe to call when no dial is in flight
// (no-op) and concurrent-safe (mu-guarded). The goroutine's own
// cleanup runs the deferred delete after runDial returns.
func (d *Dialer) CancelDial(containerID string) {
	d.mu.Lock()
	cancel, ok := d.Dialing[containerID]
	d.mu.Unlock()
	if !ok {
		return
	}
	cancel()
}

func (d *Dialer) runDial(cpCtx context.Context, dialCtx context.Context, containerID string) {
	log := d.Log.With("container_id", containerID, "component", "agentdial")
	closeErrCount := 0

	for cycle := 1; ; cycle++ {
		if dialCtx.Err() != nil {
			return
		}

		// Establishment loop: re-inspects on every attempt so the
		// State.Running check, the addr, and the labels are read
		// at the moment of dial — never a stale snapshot. If the
		// container dies mid-cycle (laptop sleep, manual stop,
		// docker daemon hiccup), the next attempt's inspect catches
		// it instead of burning the 5min retry budget against a
		// dead IP.
		res := d.establishWithRetry(dialCtx, containerID, log.With("cycle", cycle))
		switch res.Outcome {
		case outcomeCtxDone:
			return
		case outcomeContainerGone:
			d.publishFailed(dialCtx, containerID, res.Agent, res.Project, res.Addr, "container_not_running", res.Attempt)
			return
		case outcomeAddrInvalid:
			d.publishFailed(dialCtx, containerID, res.Agent, res.Project, res.Addr, "clawker_net_endpoint_missing", res.Attempt)
			return
		case outcomeRetryExhausted:
			d.publishFailed(dialCtx, containerID, res.Agent, res.Project, res.Addr, "connect_total_timeout", res.Attempt)
			return
		case OutcomeSuccess:
			// fallthrough — handled below
		default:
			log.Error().Int("outcome", int(res.Outcome)).Msg("agentdial: unrecognized establish outcome; treating as failure")
			d.publishFailed(dialCtx, containerID, res.Agent, res.Project, res.Addr, "internal_unknown_outcome", res.Attempt)
			return
		}

		cycleLog := log.With("agent", res.Agent, "project", res.Project, "addr", res.Addr, "cycle", cycle)
		d.PublishConnected(dialCtx, containerID, res.Agent, res.Project, res.Addr, res.Attempt, res.PeerInfo)

		// Classify the peer cert against the registry. Drives the
		// agent-axis events (AgentRegistered for fresh registrations
		// on Miss, AgentUntrusted for mismatch outcomes). The Session
		// stream stays open in all cases — CP must remain reachable
		// for containment commands even when the agent is untrusted.
		d.DispatchAgentEvents(dialCtx, containerID, res, cycleLog)

		// Init ran only on first start
		if ok, err := shouldAgentInit(res); ok {
			d.runPlan(cpCtx, containerID, res, cycleLog, InitPlan, "init")
		} else if err != nil {
			cycleLog.Error().Err(err).Msg("agentdial: failed to determine if agent should init")
		}

		// Boot ran every time
		if ok, err := shouldAgentBoot(res); ok {
			d.runPlan(cpCtx, containerID, res, cycleLog, BootPlan, "boot")
		} else if err != nil {
			cycleLog.Error().Err(err).Msg("agentdial: failed to determine if agent should boot")
		}

		// Lifecycle invariant: CmdRunning implies Initialized — the CMD
		// forks only after init completes. The inverse is structurally
		// impossible; observing it means a CP bug or an out-of-band CMD
		// fork. Track it on the trust axis. The dialer stays permissive
		// (asymmetric trust): the Session stays open for containment and
		// subscribers enact policy.
		if agentInitBypassed(res) {
			cycleLog.Warn().
				Str("event", "agent_untrusted").
				Str("reason", string(ReasonInitBypassed)).
				Msg("CMD running while init never ran; agent untrusted")
			Publish(d.Topic, newAgentEvent(
				dialAgent(containerID, res),
				Message{
					Type:   RegistryEventType,
					Action: ActionUntrusted,
					Reason: ReasonInitBypassed,
					Detail: "cmd_running without initialized",
				},
			))
		}

		drain := d.drainStream(dialCtx, res.Stream, cycleLog)
		// Cancel the stream-scoped ctx so any goroutine still parked on
		// stream.Recv (e.g. a leftover from a driveRegister timeout that
		// preceded drainStream) is guaranteed to unblock before the next
		// cycle dials a new stream.
		if res.StreamCancel != nil {
			res.StreamCancel()
		}
		if d.CloseAndCheckLeak(res.Conn, &closeErrCount, cycleLog) {
			d.publishFailed(dialCtx, containerID, res.Agent, res.Project, res.Addr,
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
		if !ShouldReconnect(dialCtx, drain) {
			return
		}
		d.publishBroken(dialCtx, containerID, res.Agent, res.Project, res.Addr, drain.Reason)
		cycleLog.Info().
			Str("event", "agentdial_session_reconnecting").
			Str("reason", drain.Reason).
			Msg("CP→clawkerd Session broken; will reconnect")
	}
}

// ShouldReconnect classifies the post-drain decision: re-enter
// establishWithRetry (true) or return from runDial (false). False
// on intentional teardown — parent ctx cancelled (CP shutdown) or
// drain reported the same outcome — so neither SessionBroken nor a
// fresh establish cycle is published while the bus is on its way
// down. Pure function; unit-testable independent of the dial path.
func ShouldReconnect(ctx context.Context, drain DrainResult) bool {
	if ctx.Err() != nil {
		return false
	}
	if drain.Outcome == DrainCtxCanceled {
		return false
	}
	return true
}

// CloseAndCheckLeak closes the conn and tracks consecutive close
// failures against CloseErrCeiling. Returns true iff the caller
// should bail the dial loop (ceiling reached). A successful close
// resets the counter so a transient hiccup does not poison the
// ledger across the lifetime of the cycle. Extracted for
// testability — production callers always pass a *grpc.ClientConn,
// but the closeable interface lets unit tests inject a controlled
// error sequence without standing up a real gRPC channel.
func (d *Dialer) CloseAndCheckLeak(c closeable, count *int, log *logger.Logger) bool {
	if cerr := c.Close(); cerr != nil {
		*count++
		log.Error().Err(cerr).
			Int("close_err_count", *count).
			Int("close_err_ceiling", CloseErrCeiling).
			Str("event", "agentdial_conn_close_failed").
			Msg("close clawkerd conn")
		return *count >= CloseErrCeiling
	}
	*count = 0
	return false
}

// closeable is the minimum surface closeAndCheckLeak needs; satisfied
// by *grpc.ClientConn and by simple test fakes.
type closeable interface {
	Close() error
}

// EstablishOutcome classifies the terminal state of one
// establishWithRetry call. Replaces the previous (gone bool, ok
// bool) pair where (ok=true, gone=true) was structurally illegal
// but compiled.
type EstablishOutcome int

const (
	// OutcomeSuccess: Hello + HelloAck completed. Conn / Stream are
	// non-nil; Attempt records the number of tries.
	OutcomeSuccess EstablishOutcome = iota
	// outcomeContainerGone: docker reports the container is truly gone
	// (errdefs.IsNotFound) or its State.Running flipped to false.
	// Terminal — there is nothing to retry against. A generic inspect
	// API failure (daemon transient hiccup, perms revoked) does NOT
	// land here; it stays in the retry loop and surfaces as
	// outcomeRetryExhausted on deadline.
	outcomeContainerGone
	// outcomeAddrInvalid: clawker network contract violation — the
	// container exists and is running but has no clawker network endpoint
	// (no NetworkSettings, missing endpoint, or invalid IP). Terminal:
	// retrying won't fix a misconfigured network attachment, and
	// subscribers driving containment policy off Reason need this to
	// be distinct from "container_not_running".
	outcomeAddrInvalid
	// outcomeRetryExhausted: every attempt failed and
	// connectTotalTimeout elapsed. Conn / Stream are nil; Addr / Agent /
	// Project carry the latest inspect's view for the failure event.
	outcomeRetryExhausted
	// outcomeCtxDone: parent ctx cancelled mid-retry. No Publish.
	outcomeCtxDone
)

// ErrContainerStopped is the sentinel resolveAgent returns when the
// inspect succeeded but State.Running is false. Distinct from a
// generic inspect error: a stopped container is terminal (no point
// retrying), a generic inspect error is transient (retry within the
// connect-total budget).
var ErrContainerStopped = errors.New("container not running")

// EstablishResult is the typed return of establishWithRetry.
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
type EstablishResult struct {
	HelloAck     *clawkerdv1.HelloAck
	Conn         *grpc.ClientConn
	Stream       clawkerdv1.ClawkerdService_SessionClient
	StreamCancel context.CancelFunc
	Agent        string
	Project      string
	Addr         string
	Attempt      int
	Outcome      EstablishOutcome
	PeerInfo     PeerInfo
}

// PeerInfo is the connection-time identity capture from the TLS
// handshake. Registry-outcome state lives in the local registryOutcome
// enum, not on this struct, since the dial flow drives event
// publication directly off the outcome rather than threading a
// unified payload.
//
// PeerAgentFullName is the AgentFullName
// ("clawker.<project>.<agent>") read from the peer's URI SAN
// (urn:clawker:agent:<agent_full_name>) — NOT from Subject.CommonName,
// which is the deterministic consts.ContainerClawkerd literal and
// carries no per-agent information.
type PeerInfo struct {
	PeerAgentFullName string
	PeerThumbprint    [sha256.Size]byte
	ChainVerified     bool
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
	// OutcomeRegistryNotQueried is the zero value. Set when the
	// registry could not be queried at all (lookup error).
	OutcomeRegistryNotQueried registryOutcome = iota
	// OutcomeRegistryMatch — row exists and thumbprint agrees with
	// the peer cert. Trusted, registered.
	OutcomeRegistryMatch
	// OutcomeRegistryMiss — no row for this container_id. Drives the
	// Register handshake (RegisterRequired Command on the Session
	// stream).
	OutcomeRegistryMiss
	// OutcomeRegistryThumbprintMismatch — row exists but thumbprint
	// disagrees with the live peer cert. Untrusted; AgentUntrusted
	// fires with ReasonThumbprintMismatch.
	OutcomeRegistryThumbprintMismatch
)

// establishWithRetry runs the inner exponential-backoff retry loop
// until either Hello+HelloAck succeeds, the inspect at the start of
// an attempt reports the container is gone / not running, or
// connectTotalTimeout elapses.
//
// TOCTOU defense: each attempt re-inspects the container BEFORE
// Dialing. The inspect → dial → handshake sequence within a single
// attempt is the smallest atomic unit; we cannot eliminate the
// race entirely, but we bound it to one attempt's lifetime. A
// container that dies between attempts (sleep, manual stop) is
// caught by the next attempt's inspect and surfaces as
// outcomeContainerGone, which the caller maps to a terminal
// "container_not_running" failure rather than burning the retry
// budget.
func (d *Dialer) establishWithRetry(ctx context.Context, containerID string, log *logger.Logger) EstablishResult {
	deadline := time.Now().Add(connectTotalTimeout)
	backoff := connectInitialBackoff
	publishedConnecting := false

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return EstablishResult{Attempt: attempt, Outcome: outcomeCtxDone}
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
			if cerrdefs.IsNotFound(err) || errors.Is(err, ErrContainerStopped) {
				log.With("agent", "<unknown>", "project", "<unknown>").Info().Err(err).
					Int("attempt", attempt).
					Str("event", "agentdial_attempt_resolve_failed").
					Msg("container truly gone or stopped; exiting retry loop")
				return EstablishResult{Attempt: attempt, Outcome: outcomeContainerGone}
			}
			if time.Now().After(deadline) {
				log.With("agent", "<unknown>", "project", "<unknown>").Error().Err(err).
					Int("attempt", attempt).
					Str("event", "agentdial_inspect_timeout").
					Msg("gave up on inspect after total timeout")
				return EstablishResult{Attempt: attempt, Outcome: outcomeRetryExhausted}
			}
			sleep := backoffSleep(backoff)
			log.With("agent", "<unknown>", "project", "<unknown>").Warn().Err(err).
				Int("attempt", attempt).
				Dur("retry_in", sleep).
				Str("event", "agentdial_inspect_retry").
				Msg("inspect failed transiently; will retry with backoff")
			select {
			case <-ctx.Done():
				return EstablishResult{Attempt: attempt, Outcome: outcomeCtxDone}
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
				Msg(consts.Network + " address extraction failed; aborting cycle")
			return EstablishResult{Agent: agent, Project: project, Attempt: attempt, Outcome: outcomeAddrInvalid}
		}

		// Every log line from here forward carries agent + project +
		// addr so an operator reading retry/timeout/error events in
		// the structured log surface doesn't have to cross-reference
		// the container_id against docker inspect to know which agent
		// we're dialing.
		attemptLog := log.With("agent", agent, "project", project, "addr", addr)

		// Publish "connecting" once per cycle, on the first
		// successful inspect — gives consumers a useful event with
		// the address we'll be dialing rather than emitting on
		// every retry attempt.
		if !publishedConnecting {
			d.publishConnecting(ctx, containerID, agent, project, addr)
			publishedConnecting = true
		}

		conn, stream, streamCancel, peer, helloAck, dialErr := d.tryEstablish(ctx, addr, attemptLog)
		if dialErr == nil {
			return EstablishResult{
				Conn:         conn,
				Stream:       stream,
				StreamCancel: streamCancel,
				Agent:        agent,
				Project:      project,
				Addr:         addr,
				Attempt:      attempt,
				Outcome:      OutcomeSuccess,
				PeerInfo:     peer,
				HelloAck:     helloAck,
			}
		}

		if time.Now().After(deadline) {
			attemptLog.Error().
				Str("dial_target", addr).
				Err(dialErr).
				Int("attempt", attempt).
				Str("event", "agentdial_connect_timeout").
				Msg("gave up on Session establishment after total timeout")
			return EstablishResult{Agent: agent, Project: project, Addr: addr, Attempt: attempt, Outcome: outcomeRetryExhausted}
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
			return EstablishResult{Agent: agent, Project: project, Addr: addr, Attempt: attempt, Outcome: outcomeCtxDone}
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
// PeerInfo (cert-related fields populated by VerifyPeerCertificate)
// on success, or an error describing which Step failed.
//
// Cert-related fields (PeerAgentFullName, PeerThumbprint, ChainVerified) are
// captured during the TLS handshake via VerifyPeerCertificate that
// always returns nil — the dialer is permissive and never aborts on
// cert grounds. The dial flow drives registry classification +
// event publication off the captured PeerInfo.
func (d *Dialer) tryEstablish(ctx context.Context, addr string, log *logger.Logger) (*grpc.ClientConn, clawkerdv1.ClawkerdService_SessionClient, context.CancelFunc, PeerInfo, *clawkerdv1.HelloAck, error) {
	var peer PeerInfo
	conn, err := d.dial(ctx, addr, &peer)
	if err != nil {
		return nil, nil, nil, PeerInfo{}, nil, err
	}

	// Stream-scoped ctx so driveRegister can cancel just this stream
	// (not the whole runDial cycle) when RegisterDone times out.
	streamCtx, streamCancel := context.WithCancel(ctx)
	client := clawkerdv1.NewClawkerdServiceClient(conn)
	stream, err := client.Session(streamCtx)
	if err != nil {
		streamCancel()
		_ = conn.Close()
		return nil, nil, nil, PeerInfo{}, nil, fmt.Errorf("open Session stream: %w", err)
	}

	helloAck, err := d.helloHandshake(stream, log)
	if err != nil {
		streamCancel()
		_ = conn.Close()
		return nil, nil, nil, PeerInfo{}, nil, fmt.Errorf("Hello handshake: %w", err)
	}
	return conn, stream, streamCancel, peer, helloAck, nil
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
	res, err := d.Docker.ContainerInspect(ctx, containerID, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return mobycontainer.InspectResponse{}, fmt.Errorf("inspect: %w", err)
	}
	c := res.Container
	if c.State == nil || !c.State.Running {
		state := "<nil-state>"
		if c.State != nil {
			state = string(c.State.Status)
		}
		return mobycontainer.InspectResponse{}, fmt.Errorf("%w (state=%s)", ErrContainerStopped, state)
	}
	return c, nil
}

// clawkerNetAddr extracts the host:port dial target from an inspect
// response. Containers without a clawker network endpoint are a
// contract violation — every managed agent container is attached at
// create time.
func clawkerNetAddr(c mobycontainer.InspectResponse) (string, error) {
	if c.NetworkSettings == nil {
		return "", errors.New("container has no NetworkSettings (" + consts.Network + " contract violation)")
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

// ClassifyRegistry cross-checks the captured peer cert thumbprint
// against the agentregistry row keyed by container_id and returns the
// typed outcome plus any diagnostic detail. The dial flow uses the
// outcome to drive event publication (Match → SessionConnected only;
// Miss → drives Register handshake; ThumbprintMismatch →
// AgentUntrusted).
//
// Identity comparison is between (a) the live peer cert thumbprint
// and (b) the registry row's Thumbprint. The cert SAN AgentFullName
// vs label-derived AgentFullName check lives upstream in the
// IdentityInterceptor — the dialer is the OUTBOUND CP→clawkerd path,
// where trust on the agent side comes from clawkerd's listener
// pinning CP's CN, not from CP re-deriving an AgentFullName from a
// registry row. The row IS the identity once written; later label
// edits cannot drift identity without invalidating the cert.
//
// Connection NEVER aborts here. Lookup errors that are NOT
// "no such row" return OutcomeRegistryNotQueried and a non-empty
// detail string — these indicate a sqlite/IO regression visible to
// operators even though the connection proceeds.
//
// ErrMalformedEntry (a stored row that no longer re-validates as a
// typed identity) is classified as OutcomeRegistryMiss so the dial
// drives the Register handshake — the Register handler will evict
// the malformed row and re-write it from the middleware-resolved
// identity. Without this, a malformed row self-perpetuates: the
// dialer would Publish AgentUntrusted on every reconnect and never
// trigger the cleanup path.
func (d *Dialer) ClassifyRegistry(peerThumbprint [sha256.Size]byte, containerID string) (registryOutcome, string) {
	if d.Agents == nil {
		// Wiring bug — New rejected nil agents, so this can only
		// happen in a test that bypassed New.
		return OutcomeRegistryNotQueried, "registry not wired"
	}

	entry, err := d.Agents.LookupByContainerID(containerID)
	switch {
	case err == nil:
		// fall through to entry checks below.
	case errors.Is(err, ErrUnknownAgent):
		return OutcomeRegistryMiss, ""
	case errors.Is(err, ErrMalformedEntry):
		// Recover by driving Register — handler evicts + rewrites.
		return OutcomeRegistryMiss, ""
	default:
		return OutcomeRegistryNotQueried, "registry lookup error: " + err.Error()
	}
	if entry == nil {
		return OutcomeRegistryMiss, ""
	}

	if entry.Thumbprint != peerThumbprint {
		return OutcomeRegistryThumbprintMismatch, ""
	}
	return OutcomeRegistryMatch, ""
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
// peer is populated during the handshake with PeerAgentFullName,
// PeerThumbprint, and ChainVerified. The handshake is lazy under
// grpc.NewClient — these fields are not filled until the first RPC
// (Session open) triggers the underlying TLS dial.
//
// Keepalive parameters are sourced from internal/consts so server
// (clawkerd) and client (CP) cannot drift.
func (d *Dialer) dial(_ context.Context, addr string, peer *PeerInfo) (*grpc.ClientConn, error) {
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{d.CpClientCert},
		RootCAs:      d.CaPool,
		MinVersion:   tls.VersionTLS13,
		// InsecureSkipVerify disables the stdlib's hostname + chain
		// gate so VerifyPeerCertificate can run as a permissive
		// data-capture hook (see capturePeer + package doc).
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			d.CapturePeer(rawCerts, peer)
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

// CapturePeer populates PeerAgentFullName / PeerThumbprint / ChainVerified on
// peer from the peer's TLS handshake material. Permissive: every
// code path returns without error; outcomes flow into peer fields
// (and peer.CaptureReason for unusual cases). Extracted from the
// dial() callback so tests can drive it directly without standing
// up a TLS server.
func (d *Dialer) CapturePeer(rawCerts [][]byte, peer *PeerInfo) {
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
	// Source PeerAgentFullName from the urn:clawker:agent:<agent_full_name> URI
	// SAN. Subject.CommonName is the deterministic clawkerd binary
	// literal (consts.ContainerClawkerd) and would yield the same
	// string for every agent — the per-agent identity lives in the
	// SAN. The dialer-side classifyRegistry compares only thumbprints;
	// this field rides on SessionConnected purely as a
	// diagnostic so subscribers can log "which agent connected" without
	// a separate registry lookup. SAN-vs-label drift detection lives
	// upstream in IdentityInterceptor.
	peer.PeerAgentFullName, _ = auth.AgentFullNameFromCert(leaf)
	peer.PeerThumbprint = sha256.Sum256(rawCerts[0])

	// Chain-verify against the CLI CA. Outcome is a data point;
	// failure does NOT abort.
	opts := x509.VerifyOptions{Roots: d.CaPool, Intermediates: x509.NewCertPool()}
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
func (d *Dialer) helloHandshake(stream clawkerdv1.ClawkerdService_SessionClient, log *logger.Logger) (*clawkerdv1.HelloAck, error) {
	if err := stream.Send(&clawkerdv1.Command{
		CommandId: "hello",
		Payload:   &clawkerdv1.Command_Hello{Hello: &clawkerdv1.Hello{}},
	}); err != nil {
		return nil, fmt.Errorf("send Hello: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv HelloAck: %w", err)
	}
	ackPayload, ok := resp.Payload.(*clawkerdv1.Response_HelloAck)
	if !ok {
		log.Error().
			Str("event", "agentdial_hello_unexpected_response").
			Str("got_type", fmt.Sprintf("%T", resp.Payload)).
			Msg("clawkerd returned non-HelloAck for Hello")
		return nil, fmt.Errorf("expected HelloAck, got %T", resp.Payload)
	}
	// Return the ACK clawkerd actually sent — it carries Initialized /
	// CmdRunning, which shouldAgentInit / shouldAgentBoot read to make the
	// init / boot plans one-shot. Returning a fresh empty HelloAck here
	// (the prior behavior) discarded those flags, so CP re-ran both plans
	// on every (re)connect.
	ack := ackPayload.HelloAck
	if ack == nil {
		ack = &clawkerdv1.HelloAck{}
	}
	return ack, nil
}

// drainOutcome classifies why drainStream returned. Replaces the
// previous string-sentinel return ("eof" / "ctx_done" / err.Error()),
// where the caller dispatched on a string compare.
type drainOutcome int

const (
	// DrainGracefulEOF peer (clawkerd) closed the Session cleanly.
	DrainGracefulEOF drainOutcome = iota
	// DrainCtxCanceled parent ctx cancelled (CP shutdown).
	// publishBroken is suppressed for this outcome — the bus is on
	// its way down anyway.
	DrainCtxCanceled
	// DrainStreamErr Recv returned a non-EOF error and ctx is still
	// live. Treat as transient (re-establish on next cycle).
	DrainStreamErr
)

// DrainResult is the typed return of drainStream. Reason carries a
// short classification string for the SessionBroken event when the
// outcome is drainGracefulEOF or drainStreamErr.
type DrainResult struct {
	Outcome drainOutcome
	Reason  string
}

// drainStream holds the Session open. Reads each Response and
// discards. Exits on EOF (peer close), ctx cancel (CP shutdown), or error.
func (d *Dialer) drainStream(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, log *logger.Logger) DrainResult {
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			log.Info().Str("event", "agentdial_session_eof").Msg("clawkerd closed Session")
			return DrainResult{Outcome: DrainGracefulEOF, Reason: "eof"}
		}
		if err != nil {
			if ctx.Err() != nil {
				log.Info().Str("event", "agentdial_session_ctx_done").Msg("CP-side teardown")
				return DrainResult{Outcome: DrainCtxCanceled, Reason: "ctx_done"}
			}
			log.Error().Err(err).Str("event", "agentdial_session_recv_failed").Msg("Session.Recv")
			return DrainResult{Outcome: DrainStreamErr, Reason: err.Error()}
		}
		log.Debug().
			Str("event", "agentdial_unexpected_response").
			Str("type", fmt.Sprintf("%T", resp.Payload)).
			Str("command_id", resp.CommandId).
			Msg("ignoring unsolicited Response from clawkerd")
	}
}

// RegisterRequiredTimeout caps how long the dispatch path waits for
// clawkerd's RegisterDone Response after sending RegisterRequired on
// the Session stream. Hydra exchange + mTLS dial + Register handler
// is the chain on the agent side; 30s is comfortably more than the
// chained latency under normal conditions.
const RegisterRequiredTimeout = 30 * time.Second

// DispatchAgentEvents drives agent-axis event publication after the
// Session is up. Branches on the registry classification:
//
//   - Match → no extra event (SessionConnected already updated the
//     trusted state; the durable Registered=true flag persists
//     because the row exists)
//   - Miss → drive the Register handshake (send RegisterRequired,
//     wait for RegisterDone, re-lookup), publish AgentRegistered
//     and AgentUntrusted{ReasonRegisterFailed} on failure
//   - ThumbprintMismatch → publish AgentUntrusted{ReasonThumbprintMismatch}
//   - NotQueried (lookup error) → publish AgentUntrusted with detail
//
// Caller must have already called publishConnected — the agent state
// projection is populated by the time we evaluate trust outcomes.
func (d *Dialer) DispatchAgentEvents(ctx context.Context, containerID string, res EstablishResult, log *logger.Logger) {
	outcome, detail := d.ClassifyRegistry(res.PeerInfo.PeerThumbprint, containerID)

	switch outcome {
	case OutcomeRegistryMatch:
		// Nothing to publish — agent is already provenanced and
		// SessionConnected.ApplyTo set Trusted=true. The durable
		// Registered=true is reflected via the Hello-time row
		// existence; no event needed for the steady-state case.
		return
	case OutcomeRegistryMiss:
		// CP just observed a never-before-seen container. Drive the
		// CP-triggered Register flow: send RegisterRequired on the
		// Session bidi stream, wait for RegisterDone from clawkerd,
		// re-lookup the registry to confirm the row landed.
		d.DriveRegister(ctx, containerID, res, log)
	case OutcomeRegistryThumbprintMismatch:
		log.Warn().
			Str("event", "agent_untrusted").
			Str("reason", string(ReasonThumbprintMismatch)).
			Msg("registered cert thumbprint differs from live peer cert; agent untrusted")
		Publish(d.Topic, newAgentEvent(
			dialAgent(containerID, res),
			Message{
				Type:   RegistryEventType,
				Action: ActionUntrusted,
				Reason: ReasonThumbprintMismatch,
			},
		))
	default:
		// outcomeRegistryNotQueried is the explicit lookup-error /
		// wiring-bug case; fallthrough catches any future outcome
		// added without dispatch wiring (fail-closed). Both publish
		// the same AgentUntrusted{ReasonCertInvalid} payload — the
		// difference surfaces in the Detail string for operator
		// triage. Asymmetric trust holds: connection still proceeds.
		if outcome != OutcomeRegistryNotQueried {
			log.Error().
				Int("outcome", int(outcome)).
				Str("event", "agent_dispatch_unknown_outcome").
				Msg("registryOutcome added without dispatch wiring; failing closed")
			detail = fmt.Sprintf("unknown registryOutcome %d", int(outcome))
		} else {
			log.Warn().
				Str("event", "agent_untrusted").
				Str("detail", detail).
				Msg("registry classification could not be determined; agent untrusted")
		}
		Publish(d.Topic, newAgentEvent(
			dialAgent(containerID, res),
			Message{
				Type:   RegistryEventType,
				Action: ActionUntrusted,
				Reason: ReasonCertInvalid,
				Detail: detail,
			},
		))
	}
}

func (d *Dialer) runPlan(ctx context.Context, containerID string, res EstablishResult, log *logger.Logger, plan []Step, planName string) {
	if d.Executor == nil {
		log.Warn().
			Str("event", fmt.Sprintf("agent_%s_executor_unset", planName)).
			Msgf("agent.%s: no Executor wired on dialer; entrypoint will hang on its fifo until timeout", planName)
		return
	}
	target := ExecTarget{
		ContainerID: containerID,
		AgentName:   res.Agent,
		Project:     res.Project,
	}
	if err := d.Executor.Run(ctx, res.Stream, target, plan, planName); err != nil {
		log.Warn().Err(err).
			Str("event", fmt.Sprintf("agent_%s_run_failed", planName)).
			Msgf("agent.%s: Executor.Run returned error; Session held open for containment", planName)
	}
}

// DriveRegister sends RegisterRequired on the Session stream and
// waits for the matching RegisterDone Response. After the agent
// reports completion, re-looks up the registry to confirm the row
// landed and publishes AgentRegistered (success or failure) plus
// AgentUntrusted on failure.
//
// Concurrent-Recv safety: gRPC streams are NOT safe for concurrent
// stream.Recv. DriveRegister is called BEFORE drainStream and must
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
func (d *Dialer) DriveRegister(ctx context.Context, containerID string, res EstablishResult, log *logger.Logger) {
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
	waitCtx, cancel := context.WithTimeout(ctx, RegisterRequiredTimeout)
	defer cancel()

	type recvResult struct {
		resp *clawkerdv1.Response
		err  error
	}
	ch := make(chan recvResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Recover so a panic in this spawned Recv goroutine can't crash
		// PID 1 and strand eBPF — the parent DialAgent recover does NOT
		// catch panics in goroutines it launches. On panic, signal the
		// select below with a synthetic error so driveRegister unblocks
		// (rather than waiting out the full register timeout) and the
		// failure is classified, consistent with the package's other
		// goroutine recovers. See controlplane/CLAUDE.md
		// "Resilience contract".
		defer func() {
			if r := recover(); r != nil {
				log.Error().
					Interface("panic", r).
					Bytes("stack", debug.Stack()).
					Str("container_id", containerID).
					Str("event", "agentdial_register_recv_panic").
					Msg("agent.dial: RegisterRequired Recv goroutine panicked; CP otherwise unaffected")
				// Non-blocking send: ch is buffered (cap 1) and a prior
				// successful path may already have sent; never block the
				// recover.
				select {
				case ch <- recvResult{err: fmt.Errorf("RegisterRequired Recv panicked: %v", r)}:
				default:
				}
			}
		}()
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
			// A Response_Error addressed to our register command_id is a
			// terminal failure from clawkerd (e.g. INVALID_REQUEST). The
			// recv loop would otherwise re-loop and time out, hiding a
			// concrete server-side rejection behind an opaque
			// "RegisterDone timeout" — surface it directly so the
			// AgentUntrusted reason carries the ErrorCode + detail.
			if errPayload, ok := r.Payload.(*clawkerdv1.Response_Error); ok && r.CommandId == commandID {
				ch <- recvResult{err: fmt.Errorf(
					"clawkerd rejected RegisterRequired: %s: %s",
					errPayload.Error.GetCode().String(),
					errPayload.Error.GetMessage(),
				)}
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
	entry, err := d.Agents.LookupByContainerID(containerID)
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
	Publish(d.Topic, newAgentEvent(
		dialAgent(containerID, res),
		Message{
			Type:       RegistryEventType,
			Action:     ActionRegistered,
			RegisterOk: true,
		},
	))
}

func (d *Dialer) publishRegisterFailure(containerID string, res EstablishResult, reason string, log *logger.Logger) {
	log.Warn().
		Str("event", "agent_register_failed").
		Str("reason", reason).
		Msg("CP-driven Register did not complete; agent unprovenanceable")
	Publish(d.Topic, newAgentEvent(
		dialAgent(containerID, res),
		Message{
			Type:       RegistryEventType,
			Action:     ActionRegistered,
			RegisterOk: false,
			Detail:     reason,
		},
	))
	Publish(d.Topic, newAgentEvent(
		dialAgent(containerID, res),
		Message{
			Type:   RegistryEventType,
			Action: ActionUntrusted,
			Reason: ReasonRegisterFailed,
			Detail: reason,
		},
	))
}

// publishConnecting records the start of a dial attempt. Status is
// "connecting" until the dial succeeds (→ connected) or the retry
// budget exhausts (→ failed). agent + project may be empty if the
// container is missing labels (still Publish — gives consumers a way
// to surface "we're Dialing something we can't identify").
//
// Logging is delegated to the orchestrator's audit hook on the Topic so
// a single line appears per event, instead of one here AND one from the
// hook.
func (d *Dialer) publishConnecting(ctx context.Context, containerID, agent, project, addr string) {
	if ctx.Err() != nil {
		return
	}
	Publish(d.Topic, newAgentEvent(
		Agent{ContainerID: containerID, AgentName: agent, Project: project},
		Message{
			Type:    DialerEventType,
			Action:  ActionConnecting,
			Address: addr,
		},
	))
}

// PublishConnected records that the Session handshake succeeded
// (mTLS + Hello + HelloAck) on the given attempt. peer carries the
// captured cert identity (PeerAgentFullName, PeerThumbprint) flat on the event.
// Trust/registration outcomes are published via separate events
// (AgentRegistered, AgentUntrusted).
func (d *Dialer) PublishConnected(ctx context.Context, containerID, agent, project, addr string, attempt int, peer PeerInfo) {
	if ctx.Err() != nil {
		return
	}
	Publish(d.Topic, newAgentEvent(
		Agent{ContainerID: containerID, AgentName: agent, Project: project},
		Message{
			Type:              DialerEventType,
			Action:            ActionConnected,
			Address:           addr,
			Attempts:          attempt,
			PeerAgentFullName: peer.PeerAgentFullName,
			PeerThumbprint:    peer.PeerThumbprint,
		},
	))
}

// publishFailed records that the retry budget exhausted before any
// attempt established a Session. reason carries the last error
// message; attempts is the count when we gave up (0 if we never
// even reached a dial because resolveAgent failed).
func (d *Dialer) publishFailed(ctx context.Context, containerID, agent, project, addr, reason string, attempts int) {
	if ctx.Err() != nil {
		return
	}
	Publish(d.Topic, newAgentEvent(
		Agent{ContainerID: containerID, AgentName: agent, Project: project},
		Message{
			Type:     DialerEventType,
			Action:   ActionFailed,
			Address:  addr,
			Attempts: attempts,
			Reason:   ReasonFailed,
			Detail:   reason,
		},
	))
}

// publishBroken records that an established Session terminated.
// reason classifies the cause: "eof" (clawkerd graceful close),
// "ctx_done" (CP teardown), or the underlying Recv error message.
//
// Skips bus emit when ctx is already done — the bus is on its way
// down anyway and subscribers are about to be released.
// TODO: cleanup all these Publish wrappers
func (d *Dialer) publishBroken(ctx context.Context, containerID, agent, project, addr, reason string) {
	if ctx.Err() != nil {
		return
	}
	Publish(d.Topic, newAgentEvent(
		Agent{ContainerID: containerID, AgentName: agent, Project: project},
		Message{
			Type:    DialerEventType,
			Action:  ActionBroken,
			Address: addr,
			Detail:  reason,
		},
	))
}

// dialAgent projects an EstablishResult onto the AgentEvent identity
// triple. Used by the registry/trust-axis publishers, which key off the
// inspect-derived agent/project labels captured during establishment.
func dialAgent(containerID string, res EstablishResult) Agent {
	return Agent{
		ContainerID: containerID,
		AgentName:   res.Agent,
		Project:     res.Project,
	}
}
