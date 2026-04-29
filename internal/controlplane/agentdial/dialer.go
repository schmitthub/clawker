// Package agentdial holds the CP-side outbound mTLS dial logic for the
// CP→clawkerd Session channel.
//
// Single entry point: Dialer.DialAgent(ctx, containerID). The same
// function is invoked at CP boot (over the result of listAgentIDs)
// and from the typed-event subscriber on dockerevents.ContainerStarted
// when an agent container starts at runtime — so two callers, one
// dial path.
//
// DialAgent is fire-and-forget: it spawns a goroutine that owns the
// dial, the Session stream, and the lifetime drain loop. All failures
// are logged at the Error level — callers don't need to handle errors.
// A failed dial leaves no resources behind; a successful dial is held
// open until ctx cancels (CP shutdown) or the peer closes.
//
// TLS verification: chain-only against the clawker CA. ServerName is
// NOT pinned to the agent's canonical CN. clawkerd cannot issue
// arbitrary requests to the CP (the only thing it ever sends on this
// channel is Responses to CP-initiated Commands), so a stale or
// misconfigured listener is a noisy-but-bounded failure mode, not a
// security one. Locking CP out of a misconfigured agent container
// would be worse than connecting and letting the operator see the
// noise.
package agentdial

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- non-security random for connect-retry jitter
	"net"
	"strconv"
	"sync"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	clawkerdv1 "github.com/schmitthub/clawker/api/clawkerd/v1"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/controlplane/agentregistry"
	"github.com/schmitthub/clawker/internal/controlplane/agentslots"
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
	// agents is the CLI-written agentregistry (RO from CP's POV). The
	// dialer reads it post-handshake to record a provenance data
	// point: peer thumbprint vs registry-row thumbprint vs no-row.
	// Outcome flows into Overseer publishes + logs only — connection
	// stays open in all cases. Downstream decision-making (which
	// commands to dispatch) consumes the data point in a future PR.
	// May be nil; nil short-circuits the lookup.
	agents agentregistry.Registry
	// slots is the per-start CLI-attestation token store. AnnounceAgent
	// reserves; the dialer consumes when it successfully dials the
	// container's clawkerd listener. A consumed slot means "this start
	// was clawker-CLI-initiated"; absence means raw `docker start` or a
	// CP that came up after the slot TTL elapsed. Data point only —
	// connection stays open either way. May be nil.
	slots agentslots.Registry

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
// to agentdial directly. Pass a real *overseer.Overseer; tests can
// use an in-memory bus (it's cheap).
func New(log *logger.Logger, docker mobyclient.APIClient, bus *overseer.Overseer, agents agentregistry.Registry, slots agentslots.Registry, certPath, keyPath string, caPool *x509.CertPool) (*Dialer, error) {
	if log == nil {
		log = logger.Nop()
	}
	if bus == nil {
		return nil, errors.New("agentdial.New: overseer is required")
	}
	if caPool == nil {
		return nil, errors.New("agentdial.New: caPool is required")
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
		slots:        slots,
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
		defer func() {
			d.mu.Lock()
			delete(d.dialing, containerID)
			d.mu.Unlock()
		}()
		d.runDial(ctx, containerID)
	}()
}

func (d *Dialer) runDial(ctx context.Context, containerID string) {
	log := d.log.With("container_id", containerID, "component", "agentdial")

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
		conn, stream, attempts, agent, project, addr, gone, ok := d.establishWithRetry(ctx, containerID, log.With("cycle", cycle))
		if !ok {
			reason := "connect_total_timeout"
			if gone {
				reason = "container_not_running"
			}
			d.publishFailed(ctx, containerID, agent, project, addr, reason, attempts)
			return
		}
		cycleLog := log.With("agent", agent, "project", project, "addr", addr, "cycle", cycle)

		d.publishConnected(ctx, containerID, agent, project, addr, attempts)
		drainErr := d.drainStream(ctx, stream, cycleLog)
		if cerr := conn.Close(); cerr != nil {
			cycleLog.Error().Err(cerr).Str("event", "agentdial_conn_close_failed").Msg("close clawkerd conn")
		}

		// Don't reconnect on intentional teardown — the parent ctx
		// is cancelled (CP shutdown) or the drain reported the same.
		// Anything else (peer EOF, transport break from laptop sleep,
		// stream error) drops back into the loop to re-establish.
		// Skip publishBroken on intentional teardown: the Overseer
		// is already closing during CP shutdown so the Publish would
		// no-op (returns false). Subscribers don't need to know
		// about CP-side shutdown either.
		if ctx.Err() != nil || drainErr == "ctx_done" {
			return
		}
		d.publishBroken(ctx, containerID, agent, project, addr, drainErr)
		cycleLog.Info().
			Str("event", "agentdial_session_reconnecting").
			Str("reason", drainErr).
			Msg("CP→clawkerd Session broken; will reconnect")
	}
}

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
// caught by the next attempt's inspect and surfaces as gone=true,
// which the caller maps to a terminal "container_not_running"
// failure rather than burning the retry budget.
//
// Return values:
//   - conn, stream: non-nil only on success
//   - attempt: count when we exited (success or give-up)
//   - agent, project, addr: the LATEST inspect's facts (used for
//     the publish event whether success or failure)
//   - gone: true if we exited because resolveAgent reported the
//     container is no longer running (different from "retry budget
//     exhausted" — caller publishes a different reason)
//   - ok: true on success
func (d *Dialer) establishWithRetry(ctx context.Context, containerID string, log *logger.Logger) (*grpc.ClientConn, clawkerdv1.ClawkerdService_SessionClient, int, string, string, string, bool, bool) {
	deadline := time.Now().Add(connectTotalTimeout)
	backoff := connectInitialBackoff
	publishedConnecting := false

	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return nil, nil, attempt, "", "", "", false, false
		}

		// Inspect at the start of every attempt — never a stale
		// snapshot from earlier in the cycle.
		inspect, err := d.resolveAgent(ctx, containerID)
		if err != nil {
			// Inspect failed — container is gone or the daemon is
			// blind. We don't have agent/project here (they live in
			// labels we can't read), so mark with a sentinel
			// "<unknown>" so downstream queries at least see *something*
			// rather than an empty field.
			log.With("agent", "<unknown>", "project", "<unknown>").Info().Err(err).
				Int("attempt", attempt).
				Str("event", "agentdial_attempt_resolve_failed").
				Msg("container not running at attempt time; exiting retry loop")
			return nil, nil, attempt, "", "", "", true, false
		}
		agent, project := agentLabels(inspect)
		addr, err := clawkerNetAddr(inspect)
		if err != nil {
			log.With("agent", agent, "project", project).Error().Err(err).
				Int("attempt", attempt).
				Str("event", "agentdial_attempt_addr_extract_failed").
				Msg("clawker-net address extraction failed")
			return nil, nil, attempt, agent, project, "", true, false
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

		var peerThumbprint [sha256.Size]byte
		var peerCN string
		conn, stream, dialErr := d.tryEstablish(ctx, addr, attemptLog, &peerThumbprint, &peerCN)
		if dialErr == nil {
			d.recordRegistryProvenance(containerID, peerThumbprint, peerCN, attemptLog)
			d.consumeAnnounceSlot(containerID, attemptLog)
			return conn, stream, attempt, agent, project, addr, false, true
		}

		if time.Now().After(deadline) {
			attemptLog.Error().Err(dialErr).
				Int("attempt", attempt).
				Str("event", "agentdial_connect_timeout").
				Msg("gave up on Session establishment after total timeout")
			return nil, nil, attempt, agent, project, addr, false, false
		}

		// Full jitter: sleep ∈ [0, backoff). Prevents thundering-herd
		// on CP boot when many clawkerds came up together and all
		// failed their first dial in lockstep.
		sleep := time.Duration(0)
		if backoff > 0 {
			sleep = time.Duration(rand.Int64N(int64(backoff)))
		}
		attemptLog.Warn().Err(dialErr).
			Int("attempt", attempt).
			Dur("retry_in", sleep).
			Str("event", "agentdial_connect_retry").
			Msg("Session establishment failed; will retry with backoff")

		select {
		case <-ctx.Done():
			return nil, nil, attempt, agent, project, addr, false, false
		case <-time.After(sleep):
		}
		backoff *= 2
		if backoff > connectMaxBackoff {
			backoff = connectMaxBackoff
		}
	}
}

// tryEstablish runs one connection attempt: dial → open stream →
// Hello handshake. Returns the open conn + stream on success, or an
// error describing which step failed (caller decides retry vs.
// give-up). On success, thumbprintOut + cnOut are populated with the
// peer's leaf cert SHA-256 + CN — used by the caller to record a
// provenance data point against agentregistry.
func (d *Dialer) tryEstablish(ctx context.Context, addr string, log *logger.Logger, thumbprintOut *[sha256.Size]byte, cnOut *string) (*grpc.ClientConn, clawkerdv1.ClawkerdService_SessionClient, error) {
	conn, err := d.dial(ctx, addr, thumbprintOut, cnOut)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	client := clawkerdv1.NewClawkerdServiceClient(conn)
	stream, err := client.Session(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("open Session stream: %w", err)
	}

	if err := d.helloHandshake(stream, log); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("Hello handshake: %w", err)
	}
	return conn, stream, nil
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
		return mobycontainer.InspectResponse{}, fmt.Errorf("container not running (state=%s)", state)
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

// recordRegistryProvenance compares the peer cert thumbprint + CN
// observed at the TLS handshake against the agentregistry row keyed
// by container_id and emits a single log line capturing the outcome:
//
//   - registry_match — row exists and thumbprint+CN agree (provenance
//     confirmed: this clawkerd was created by the CLI we trust)
//   - registry_thumbprint_mismatch — row exists but thumbprint differs
//     (cert in writable layer was swapped, or container ID was reused
//     by an externally-created container)
//   - registry_cn_mismatch — row exists, thumbprint matches, CN does
//     not (defense-in-depth — should never happen since cert pins
//     thumbprint to CN at mint time)
//   - registry_miss — no row for this container_id (raw `docker run`
//     of an agent image without going through the clawker CLI, or DB
//     was wiped)
//
// This is a data point only. The connection stays open in every case.
// Downstream consumers (future command-dispatch logic) read the
// outcome via the Overseer to decide what's appropriate to send.
func (d *Dialer) recordRegistryProvenance(containerID string, peerThumbprint [sha256.Size]byte, peerCN string, log *logger.Logger) {
	if d.agents == nil {
		return
	}
	entry, err := d.agents.LookupByContainerID(containerID)
	switch {
	case err != nil || entry == nil:
		log.Warn().
			Str("container_id", containerID).
			Str("peer_thumbprint", hex.EncodeToString(peerThumbprint[:])).
			Str("peer_cn", peerCN).
			Str("provenance", "registry_miss").
			Msg("agentdial: dialed clawkerd has no registry row (untracked container)")
	case entry.Thumbprint != peerThumbprint:
		log.Warn().
			Str("container_id", containerID).
			Str("peer_thumbprint", hex.EncodeToString(peerThumbprint[:])).
			Str("registry_thumbprint", hex.EncodeToString(entry.Thumbprint[:])).
			Str("peer_cn", peerCN).
			Str("provenance", "registry_thumbprint_mismatch").
			Msg("agentdial: peer cert thumbprint disagrees with registry row")
	default:
		log.Info().
			Str("container_id", containerID).
			Str("peer_cn", peerCN).
			Str("agent", entry.AgentName).
			Str("project", entry.Project).
			Str("provenance", "registry_match").
			Msg("agentdial: registry row confirms peer cert provenance")
	}
}

// consumeAnnounceSlot retires the per-start AnnounceAgent slot for
// the container. The slot's existence at consume time is the data
// point that says "this start was initiated by the clawker CLI".
// Missing slot is the legitimate raw-`docker start` case (or a CP
// that came up after the slot TTL elapsed); record + carry on.
func (d *Dialer) consumeAnnounceSlot(containerID string, log *logger.Logger) {
	if d.slots == nil {
		return
	}
	if slot, err := d.slots.Consume(containerID); err == nil {
		log.Info().
			Str("container_id", containerID).
			Time("reserved_at", slot.ReservedAt).
			Str("provenance", "announce_slot_consumed").
			Msg("agentdial: dial confirms CLI-attested start")
	} else if !errors.Is(err, agentslots.ErrSlotInvalid) {
		log.Warn().Err(err).
			Str("container_id", containerID).
			Msg("agentdial: announce slot consume failed")
	} else {
		log.Info().
			Str("container_id", containerID).
			Str("provenance", "announce_slot_missing").
			Msg("agentdial: dial sees no announce slot (start was not CLI-attested)")
	}
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

// dial builds the mTLS gRPC client connection. Chain-only validation
// (no hostname pin) against the clawker CA. Keepalive parameters
// are sourced from internal/consts so server (clawkerd) and client
// (CP) cannot drift.
//
// thumbprintOut is filled with SHA-256 over the leaf cert DER on a
// successful TLS handshake. Used by the caller to record a
// provenance data point against agentregistry.
func (d *Dialer) dial(_ context.Context, addr string, thumbprintOut *[sha256.Size]byte, cnOut *string) (*grpc.ClientConn, error) {
	verify := func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if err := d.verifyChainOnly(rawCerts, verifiedChains); err != nil {
			return err
		}
		// Capture the leaf thumbprint + CN for the post-handshake
		// registry data-point recording. verifyChainOnly already
		// validated len(rawCerts) > 0 + parseable.
		if thumbprintOut != nil && len(rawCerts) > 0 {
			*thumbprintOut = sha256.Sum256(rawCerts[0])
		}
		if cnOut != nil && len(rawCerts) > 0 {
			if leaf, err := x509.ParseCertificate(rawCerts[0]); err == nil {
				*cnOut = leaf.Subject.CommonName
			}
		}
		return nil
	}
	tlsCfg := &tls.Config{
		Certificates:          []tls.Certificate{d.cpClientCert},
		RootCAs:               d.caPool,
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // hostname pin disabled — see package doc
		VerifyPeerCertificate: verify,
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

// verifyChainOnly enforces that the peer cert chains to the clawker
// CA without checking hostname. Required because we dial by IP and
// clawkerd's leaf cert CN is the canonical agent name (no IP SAN).
// Hostname pinning was deliberately dropped — see package doc.
func (d *Dialer) verifyChainOnly(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return errors.New("agentdial: peer presented no certs")
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		c, err := x509.ParseCertificate(raw)
		if err != nil {
			return fmt.Errorf("agentdial: parse peer cert: %w", err)
		}
		certs = append(certs, c)
	}
	opts := x509.VerifyOptions{Roots: d.caPool, Intermediates: x509.NewCertPool()}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	if _, err := certs[0].Verify(opts); err != nil {
		return fmt.Errorf("agentdial: chain verify: %w", err)
	}
	return nil
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

// drainStream holds the Session open. Reads each Response and
// discards (CP doesn't dispatch any further Commands in this commit).
// Exits on EOF (peer close), ctx cancel (CP shutdown), or error.
// Returns the underlying reason as a string for the broken-event
// publish — empty for graceful EOF, "ctx_done" for CP teardown,
// otherwise the wrapped Recv error.
func (d *Dialer) drainStream(ctx context.Context, stream clawkerdv1.ClawkerdService_SessionClient, log *logger.Logger) string {
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			log.Info().Str("event", "agentdial_session_eof").Msg("clawkerd closed Session")
			return "eof"
		}
		if err != nil {
			if ctx.Err() != nil {
				log.Info().Str("event", "agentdial_session_ctx_done").Msg("CP-side teardown")
				return "ctx_done"
			}
			log.Error().Err(err).Str("event", "agentdial_session_recv_failed").Msg("Session.Recv")
			return err.Error()
		}
		log.Debug().
			Str("event", "agentdial_unexpected_response").
			Str("type", fmt.Sprintf("%T", resp.Payload)).
			Str("command_id", resp.CommandId).
			Msg("ignoring unsolicited Response from clawkerd")
	}
}

// publishConnecting records the start of a dial attempt. Status is
// "connecting" until the dial succeeds (→ connected) or the retry
// budget exhausts (→ failed). agent + project may be empty if the
// container is missing labels (still publish — gives consumers a way
// to surface "we're dialing something we can't identify").
func (d *Dialer) publishConnecting(_ context.Context, containerID, agent, project, addr string) {
	d.log.Info().
		Str("event", "agentdial_session_connecting").
		Str("container_id", containerID).
		Str("agent", agent).
		Str("project", project).
		Str("addr", addr).
		Msg("CP→clawkerd dial starting")
	overseer.Publish(d.bus, SessionConnecting{
		ContainerID: containerID,
		AgentName:   agent,
		Project:     project,
		Address:     addr,
		At:          time.Now(),
	})
}

// publishConnected records that the Session handshake succeeded
// (mTLS + Hello + HelloAck) on the given attempt.
func (d *Dialer) publishConnected(_ context.Context, containerID, agent, project, addr string, attempt int) {
	d.log.Info().
		Str("event", "agentdial_session_connected").
		Str("container_id", containerID).
		Str("agent", agent).
		Str("project", project).
		Str("addr", addr).
		Int("attempts", attempt).
		Msg("CP→clawkerd Session connected")
	overseer.Publish(d.bus, SessionConnected{
		ContainerID: containerID,
		AgentName:   agent,
		Project:     project,
		Address:     addr,
		Attempts:    attempt,
		At:          time.Now(),
	})
}

// publishFailed records that the retry budget exhausted before any
// attempt established a Session. reason carries the last error
// message; attempts is the count when we gave up (0 if we never
// even reached a dial because resolveAgent failed).
func (d *Dialer) publishFailed(_ context.Context, containerID, agent, project, addr, reason string, attempts int) {
	d.log.Error().
		Str("event", "agentdial_session_failed").
		Str("container_id", containerID).
		Str("agent", agent).
		Str("project", project).
		Str("addr", addr).
		Int("attempts", attempts).
		Str("reason", reason).
		Msg("CP→clawkerd dial gave up")
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
func (d *Dialer) publishBroken(_ context.Context, containerID, agent, project, addr, reason string) {
	d.log.Info().
		Str("event", "agentdial_session_broken").
		Str("container_id", containerID).
		Str("agent", agent).
		Str("project", project).
		Str("addr", addr).
		Str("reason", reason).
		Msg("CP→clawkerd Session terminated")
	overseer.Publish(d.bus, SessionBroken{
		ContainerID: containerID,
		AgentName:   agent,
		Project:     project,
		Address:     addr,
		Reason:      reason,
		At:          time.Now(),
	})
}
