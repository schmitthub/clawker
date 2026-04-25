# Branch 4 Follow-up: End-to-End CLI Integration + Agent Lifetime Channel

> **Status:** Spec finalized. Ready for implementation.
> **Branch:** continue on `feat/clawkerd-init`.

## Goal

Wire the disconnected pieces from Branch 4 into a working end-to-end happy path **and** make architectural decisions that grow into the long-term concept rather than serving only this branch.

After this branch:
- `clawker run` produces a container that successfully completes the full handshake: AnnounceAgent → bootstrap delivery → clawkerd Connect → registry add → command stream open and waiting for B5 commands.
- The proto + handler shape match the long-term design (`Connect` server-streaming) so B5 can fill commands without proto migration.
- Identity revocation is real-time on every per-agent RPC via interceptor (not per-handler discipline).
- Slot collision on retry-within-TTL is solved via composite key + dockerevents eviction.
- Defensive cross-checks tightened (cert CN, CN-vs-agent-name).

## Scope

### IN

1. Convert `AgentService.Register` to `AgentService.Connect`, server-streaming. Stub `AgentService.Events` (client-streaming) for B5.
2. `agentslots`: composite key (thumbprint + agent_name); `EvictByContainerID`; dockerevents `Subscribe`.
3. `agent.Handler.Connect`: CN cross-check + composite `Consume`; preserve all existing cross-checks.
4. `controlplane.adminServer.AnnounceAgent`: full handler implementation.
5. `AgentIdentityInterceptor` (unary + stream) on `agent` listener with fail-secure opt-out map.
6. `cmd/clawker-cp/main.go`: hoist slot registry construction; wire identity interceptor; subscribe slots to dockerevents.
7. CLI `run`/`start`: populate `RuntimeEnvOpts.Clawkerd*` env vars; new `prepareAgentBootstrap` helper between `BootstrapServicesPreStart` and `docker.ContainerStart`.
8. Documentation pass: `agent/CLAUDE.md`, `agentslots/CLAUDE.md`, `KEY-CONCEPTS.md`, `controlplane/CLAUDE.md`, status memo.

### OUT (deferred to dedicated initiatives)

- **CP restart resilience** — registry persistence, reconnect path on Connect handler, clawkerd reconnect-with-backoff. See `cp-initiative-cp-restart-resilience` memory.
- **Streaming RPC eviction broadcast** — per-thumbprint cancel func registry, registry subscribe/notify channel, per-stream cancellation on evict. See same memory; same prerequisite.
- **`Events` RPC implementation** — only the proto stub lands here. Body, payload shape, CP-side consumer all defer to B5 alongside the first concrete event type (log scrape, error report, etc.).
- **`Command` payload variants** — only the `Welcome` variant is defined here. Shell/Stop/Reload/etc. defer to B5 alongside CP-side producers.
- **`ClawkerdConfiguration` content** — empty placeholder message here. B5 fills in OTEL endpoint, file logging settings, project/agent context.
- **clawkerd-side gRPC server** — explicitly NOT needed under the single-server topology this branch commits to.

## Architectural decisions made during planning

### D1. Single-server topology (CP serves; clawkerd is gRPC client only)

POC's two-server pattern (clawkerd runs `AgentCommandService`, CP dials back via Docker inspect) was K8s-inspired but unnecessary for clawker's actual needs (single CP↔agent pair, agent lifetime = container lifetime, no ad-hoc dial-back required). Single-server with streaming RPCs covers everything:

```
clawkerd (gRPC client only)              CP (gRPC server only)
   │                                          │
   ├── Connect(req) ──────────────────────▶   │
   │   ◀──── stream Command ─────────────────  (server-streams forever)
   │                                          │
   ├── Events(stream Event) ──────────────▶   │
   │   ◀──── EventAck ───────────────────────  (client-streams)
```

Two TCP connections from clawkerd to CP, both client-mode. clawkerd never runs a server. Eliminates port resolution dance, mTLS-both-directions complexity, and Docker-network-IP discovery.

### D2. `Connect` is server-streaming for the agent's lifetime

Previously `Register` was unary — that's tech debt against the long-term design. The connection IS the channel. clawkerd sends one `ConnectRequest` (auth handshake material); CP server-streams `Command` messages for the lifetime of the agent. First message after auth is `Welcome` (carries `ClawkerdConfiguration`); subsequent messages are commands.

Naming: `Connect` (not `Register`) because the bulk of the RPC's lifetime is the open command channel, not the one-shot register call.

### D3. Composite slot key (thumbprint + agent_name)

`agentslots` was keyed by `agent_name` only. Same name within slot TTL = `ErrSlotExists`. With composite (thumbprint, agent_name):
- Each retry mints fresh cert → fresh thumbprint → fresh key → no collision.
- The slot's identity IS the (thumbprint, agent_name) pair — agent_name cross-check folds into the lookup itself.
- Cert thumbprint = 256-bit random keyspace (collision-resistant).
- Slot lookup probing requires CLI-CA-signed cert (vs guess-the-agent-name).

### D4. dockerevents eviction on `agentslots`

`agentregistry` already subscribes to dockerevents and evicts entries on container death. `agentslots` had no equivalent — only TTL janitor. With dockerevents subscription, retry-after-failed-create succeeds in <1s instead of waiting up to TTL.

### D5. CN cross-check at Connect

`auth.MintAgentCert` sets cert CN to `agentName`. The Connect handler currently doesn't verify `peerCert.Subject.CommonName == req.AgentName`. Adding a constant-time compare catches a "announce mismatch" attack class (cert minted for X but announce payload says Y). Threat realism is low (in-process attack only) but it's cheap defense-in-depth.

### D6. Identity resolution via interceptor (fail-secure opt-out)

Per-handler `registry.Lookup(thumbprint)` discipline is fragile. Replaced with an `AgentIdentityInterceptor` that runs after `AuthInterceptor` on the agent listener. Resolves identity once, attaches `*agentregistry.Entry` to ctx, handlers retrieve via `agent.EntryFromContext(ctx)`.

`AgentIdentityOptedOutMethods()` map (fail-secure): default REQUIRES identity; explicit opt-outs only for bootstrap RPCs that authenticate themselves (`Connect` via slot consume). Adding a new RPC and forgetting to declare opt-out → it goes through identity check by default. Build-time test walks proto descriptor asserting every method has a policy entry.

### D7. CP ≠ firewall — bootstrap delivery is unconditional

CLI's `prepareAgentBootstrap` runs for every agent container regardless of `security.firewall.enable`. CP is unconditional infrastructure; firewall is one optional subsystem. (See project-root `CLAUDE.md` "CP ≠ firewall" callout.)

### D8. `ConnectRequest.code_verifier` semantics preserved for future reconnect

The proto comment must note that empty `code_verifier` is reserved for the future reconnect path (CP restart resilience initiative). Today's handler still requires it on first-connect; the future patch will branch on registry-already-has-thumbprint.

## Detailed plan

### Task 1 — Proto: rename Register → Connect, server-streaming, stub Events

**File:** `api/agent/v1/agent.proto`

Replace existing service definition with:

```proto
service AgentService {
  // Connect opens the agent's lifetime command channel. clawkerd sends
  // a ConnectRequest at startup (agent_name + PKCE verifier); CP
  // authenticates (slot consume + 5 cross-checks: thumbprint, peer IP,
  // container labels, cert CN), pins the agent in the registry,
  // server-streams Command messages for the lifetime of the agent.
  // First message after auth is Welcome (config delivery). Subsequent
  // messages carry commands as they are issued (B5+ adds payload
  // variants). Stream closes on eviction (container dies →
  // dockerevents → cancel) or clawkerd disconnect.
  rpc Connect(ConnectRequest) returns (stream Command);

  // Events streams runtime telemetry from clawkerd to CP: log scrapes,
  // error events, monitoring data. Client-streaming; CP returns a
  // single EventAck on close. Identity-bound — caller must already be
  // registered via Connect (AgentIdentityInterceptor enforces). Stub
  // in this branch; B5 defines the Event payload shape and CP-side
  // consumer alongside the first concrete event type.
  rpc Events(stream Event) returns (EventAck);
}

message ConnectRequest {
  // agent_name is the canonical full name "clawker.<project>.<agent>".
  // CP looks up the slot by (cert_thumbprint, agent_name).
  string agent_name = 1;
  // code_verifier is the PKCE secret matching the slot's S256 challenge.
  // CLI delivers it via the bootstrap directory.
  //
  // RECONNECT PATH (future, see cp-initiative-cp-restart-resilience):
  // Empty verifier is reserved for the future reconnect flow after CP
  // restart. clawkerd deletes verifier on first-connect success
  // (single-use); a reconnect attempt has no verifier to send. Today's
  // handler requires verifier; the future patch will branch on
  // registry-already-has-thumbprint to skip slot consume.
  string code_verifier = 2;
}

message Command {
  oneof payload {
    Welcome welcome = 1;
    // B5+ adds: ShellCommand shell = 2; Stop stop = 3; ReloadConfig
    // reload = 4; etc. Adding payload variants requires no proto
    // migration — just new oneof tags.
  }
}

message Welcome {
  // Empty in this branch — placeholder for B5's ClawkerdConfiguration
  // payload (OTEL endpoint, file logging, project/agent context).
  ClawkerdConfiguration config = 1;
}

message ClawkerdConfiguration {
  // Empty in this branch. B5 adds OTEL/logging/identity context fields.
}

message Event {
  // Empty in this branch. B5 defines event types alongside CP consumers.
}

message EventAck {}
```

Regenerate `agent.pb.go` and `agent_grpc.pb.go` via project's existing buf workflow.

### Task 2 — agent.Handler.Connect: server-streaming + CN cross-check + composite Consume

**File:** `internal/controlplane/agent/handler.go`

Rename method `Register` → `Connect`. New signature:

```go
func (h *Handler) Connect(req *agentv1.ConnectRequest, stream agentv1.AgentService_ConnectServer) error {
    if req == nil || req.AgentName == "" || req.CodeVerifier == "" {
        return status.Error(codes.InvalidArgument, "agent_name and code_verifier required")
    }

    ctx := stream.Context()
    peerCert, peerIP, err := peerCertAndIP(ctx)
    if err != nil { /* ... PermissionDenied ... */ }
    thumbprint := sha256.Sum256(peerCert.Raw)

    // (a) Cert CN cross-check — defense vs announce-payload tampering.
    if subtle.ConstantTimeCompare(
        []byte(peerCert.Subject.CommonName),
        []byte(req.AgentName),
    ) != 1 {
        h.log.Warn().Str("agent", req.AgentName).Str("cn", peerCert.Subject.CommonName).
            Msg("agent connect: cert CN does not match request agent_name")
        return status.Error(codes.PermissionDenied, "registration rejected")
    }

    // (b) Composite slot consume — implicit thumbprint+agent_name match.
    slot, err := h.slots.Consume(thumbprint, req.AgentName, req.CodeVerifier)
    if err != nil { /* ... PermissionDenied (logged) ... */ }

    // (c) Docker cross-check: container exists, has clawker-net IP.
    info, err := h.docker.Inspect(ctx, slot.ContainerID)
    if err != nil { /* same as today ... */ }

    // (d) Peer IP must match container's clawker-net IP.
    if info.NetworkIP == nil || !info.NetworkIP.Equal(peerIP) { /* ... */ }

    // (e) Label cross-check.
    if got := info.Labels[consts.LabelAgent]; !strings.EqualFold(got, req.AgentName) { /* ... */ }

    // Pin to registry.
    now := h.clock()
    h.registry.Add(agentregistry.Entry{
        AgentName:    req.AgentName,
        ContainerID:  slot.ContainerID,
        Thumbprint:   thumbprint,
        RegisteredAt: now,
        LastSeen:     now,
    })

    // Send Welcome — first command stream message after auth.
    if err := stream.Send(&agentv1.Command{
        Payload: &agentv1.Command_Welcome{
            Welcome: &agentv1.Welcome{Config: &agentv1.ClawkerdConfiguration{}},
        },
    }); err != nil {
        return fmt.Errorf("send welcome: %w", err)
    }

    h.log.Info().Str("agent", req.AgentName).Str("container_id", slot.ContainerID).
        Msg("agent connect: registered")

    // Idle on stream — wait for ctx cancellation (eviction or client disconnect).
    // B5+ adds command-pushing here via select on stream.Context().Done()
    // and a per-agent command queue.
    <-ctx.Done()
    return nil
}
```

Drops the old standalone thumbprint check (b in current handler) — it's now implicit in the composite Consume.

### Task 3 — agentslots: composite key + EvictByContainerID + dockerevents Subscribe

**File:** `internal/controlplane/agentslots/registry.go`

```go
type slotKey struct {
    Thumbprint [sha256.Size]byte
    AgentName  string
}

type registryImpl struct {
    slots map[slotKey]Slot
    // ... other fields unchanged
}

type Registry interface {
    Reserve(slot Slot) error
    // Consume verifies S256(verifier) == slot.Challenge atomically and
    // removes the slot on success. Composite key (thumbprint, agentName)
    // implicitly cross-checks identity at lookup time.
    Consume(thumbprint [sha256.Size]byte, agentName, verifier string) (*Slot, error)
    Len() int
    Stop()
    // EvictByContainerID removes any slot whose ContainerID matches.
    // Called by dockerevents subscriber on container death. Linear scan;
    // realistic clawker host scales (single-digit slots) make this fine.
    EvictByContainerID(containerID string)
}

// Reserve collision check uses composite key:
key := slotKey{slot.ExpectedCertThumbprint, slot.AgentName}
if _, exists := r.slots[key]; exists { return ErrSlotExists }
r.slots[key] = slot

// Consume looks up by composite:
slot, ok := r.slots[slotKey{thumbprint, agentName}]

// EvictByContainerID:
func (r *registryImpl) EvictByContainerID(containerID string) {
    r.mu.Lock(); defer r.mu.Unlock()
    for k, slot := range r.slots {
        if slot.ContainerID == containerID { delete(r.slots, k) }
    }
}
```

`ErrSlotExists` doc updated: "Composite collision (same thumbprint AND same agent_name) is effectively impossible — would require SHA-256 collision. Treated as fatal misuse, not benign retry."

**New file:** `internal/controlplane/agentslots/subscribe.go`

Mirrors `internal/controlplane/agentregistry/subscribe.go` body — same recover-and-resume goroutine pattern, same delta types (`DeltaRemoved`, `DeltaUpdated{Lifecycle=Stopped}` → `EvictByContainerID`).

**Mock regen:** `cd internal/controlplane/agentslots && go generate ./...`

### Task 4 — controlplane.adminServer.AnnounceAgent

**File:** `internal/controlplane/server.go`

```go
type adminServer struct {
    *fwhandler.Handler
    agents agentregistry.Registry
    slots  agentslots.Registry  // NEW
    clock  func() time.Time     // NEW (test seam)
}

func NewAdminServer(
    fw *fwhandler.Handler,
    agents agentregistry.Registry,
    slots agentslots.Registry,
    clock func() time.Time,  // pass time.Now in production
) adminv1.AdminServiceServer {
    if clock == nil { clock = time.Now }
    return &adminServer{Handler: fw, agents: agents, slots: slots, clock: clock}
}

func (s *adminServer) AnnounceAgent(ctx context.Context, req *adminv1.AnnounceAgentRequest) (*adminv1.AnnounceAgentResult, error) {
    // Validation (all → InvalidArgument):
    //   - agent_name nonempty
    //   - container_id nonempty
    //   - expected_cert_thumbprint = 64-char hex
    //   - code_challenge nonempty
    //   - code_challenge_method == "S256"
    if req == nil { return nil, status.Error(codes.InvalidArgument, "request required") }
    if req.AgentName == "" { return nil, status.Error(codes.InvalidArgument, "agent_name required") }
    // ... validation cases ...

    var thumbprint [sha256.Size]byte
    raw, err := hex.DecodeString(req.ExpectedCertThumbprint)
    if err != nil || len(raw) != sha256.Size {
        return nil, status.Error(codes.InvalidArgument, "expected_cert_thumbprint must be 64 lowercase hex characters")
    }
    copy(thumbprint[:], raw)

    if req.CodeChallengeMethod != string(consts.ChallengeMethodS256) {
        return nil, status.Error(codes.InvalidArgument, "code_challenge_method must be S256")
    }

    now := s.clock()
    slot := agentslots.Slot{
        AgentName:              req.AgentName,
        ContainerID:            req.ContainerId,
        ExpectedCertThumbprint: thumbprint,
        Challenge:              req.CodeChallenge,
        ChallengeMethod:        consts.ChallengeMethod(req.CodeChallengeMethod),
        ReservedAt:             now,
        ExpiresAt:              now.Add(consts.AgentSlotTTL),
    }
    if err := s.slots.Reserve(slot); err != nil {
        if errors.Is(err, agentslots.ErrSlotExists) {
            return nil, status.Error(codes.AlreadyExists, "agent already announced")
        }
        return nil, status.Error(codes.Internal, "slot reservation failed")
    }

    return &adminv1.AnnounceAgentResult{ExpiresAtUnix: slot.ExpiresAt.Unix()}, nil
}
```

### Task 5 — AgentIdentityInterceptor

**New file:** `internal/controlplane/agent/identity_interceptor.go`

```go
package agent

import (
    "context"
    "crypto/sha256"
    "github.com/schmitthub/clawker/internal/controlplane/agentregistry"
    "github.com/schmitthub/clawker/internal/logger"
    agentv1 "github.com/schmitthub/clawker/api/agent/v1"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

// IdentityOptedOutMethods is the set of methods that authenticate themselves
// (via slot consume) and have no registry entry at call time. Default for
// every other method is REQUIRE identity. Adding a new RPC means it goes
// through registry.Lookup automatically — explicit opt-out is required to
// bypass the interceptor, which forces a deliberate decision visible at
// code review.
func IdentityOptedOutMethods() map[string]bool {
    const svc = "/" + agentv1.ServiceName + "/"
    return map[string]bool{
        svc + "Connect": true, // bootstrap — slot consume is the auth
    }
}

type entryCtxKey struct{}

func WithEntry(ctx context.Context, e *agentregistry.Entry) context.Context {
    return context.WithValue(ctx, entryCtxKey{}, e)
}

func EntryFromContext(ctx context.Context) (*agentregistry.Entry, bool) {
    e, ok := ctx.Value(entryCtxKey{}).(*agentregistry.Entry)
    return e, ok
}

// IdentityInterceptor returns paired unary + stream interceptors that
// resolve agent identity from the peer mTLS cert thumbprint and attach
// the registry Entry to the handler ctx. Methods listed in optedOut are
// passed through (bootstrap RPCs that auth themselves).
func IdentityInterceptor(reg agentregistry.Registry, optedOut map[string]bool, log *logger.Logger) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
    if log == nil { log = logger.Nop() }

    unary := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
        if optedOut[info.FullMethod] { return handler(ctx, req) }
        ctx2, err := injectIdentity(ctx, reg, log, info.FullMethod)
        if err != nil { return nil, err }
        return handler(ctx2, req)
    }

    stream := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
        if optedOut[info.FullMethod] { return handler(srv, ss) }
        ctx2, err := injectIdentity(ss.Context(), reg, log, info.FullMethod)
        if err != nil { return err }
        return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx2})
    }

    return unary, stream
}

func injectIdentity(ctx context.Context, reg agentregistry.Registry, log *logger.Logger, method string) (context.Context, error) {
    peerCert, _, err := peerCertAndIP(ctx)
    if err != nil {
        log.Warn().Err(err).Str("method", method).Msg("agent identity: missing peer cert")
        return nil, status.Error(codes.PermissionDenied, "registration rejected")
    }
    thumbprint := sha256.Sum256(peerCert.Raw)
    entry, err := reg.Lookup(thumbprint)
    if err != nil {
        log.Warn().Err(err).Str("method", method).Msg("agent identity: registry lookup failed (evicted or unknown)")
        return nil, status.Error(codes.PermissionDenied, "registration rejected")
    }
    return WithEntry(ctx, entry), nil
}

type wrappedStream struct {
    grpc.ServerStream
    ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
```

**New file:** `internal/controlplane/agent/identity_interceptor_test.go`

Walks `agentv1.AgentService_ServiceDesc` proto descriptor; asserts every method either appears in `IdentityOptedOutMethods()` (with explicit value) OR is implicitly require-identity. Catches a future RPC added without an identity policy decision at build time.

### Task 6 — cmd/clawker-cp/main.go wiring

**File:** `cmd/clawker-cp/main.go`

Reorder construction so `slotRegistry` exists before `NewAdminServer`:

```go
slotRegistry := agentslots.NewRegistry(time.Now, 0, log.With("component", "agentslots"))
defer slotRegistry.Stop()
agentReg := agentregistry.NewRegistry(log.With("component", "agentregistry"))

adminv1.RegisterAdminServiceServer(grpcServer, controlplane.NewAdminServer(handler, agentReg, slotRegistry, time.Now))

// ... agent listener block ...
identityUnary, identityStream := agent.IdentityInterceptor(agentReg, agent.IdentityOptedOutMethods(), log.With("component", "agent-identity"))
agentServer := grpc.NewServer(
    grpc.Creds(credentials.NewTLS(agentTLSCfg)),
    grpc.ChainUnaryInterceptor(authInterceptor.Unary, identityUnary),
    grpc.ChainStreamInterceptor(authInterceptor.Stream, identityStream),
)

agentInspector := agent.MobyInspector{Client: dockerCli.APIClient}
agentHandler := agent.NewHandler(slotRegistry, agentReg, agentInspector, log.With("component", "agent-handler"))
agentv1.RegisterAgentServiceServer(agentServer, agentHandler)

// dockerevents subscriptions for both registries:
agentregistry.Subscribe(ctx, agentReg, informer, log.With("component", "agentreg-sub"))
agentslots.Subscribe(ctx, slotRegistry, informer, log.With("component", "agentslots-sub"))
```

### Task 7 — clawkerd consumes server-stream

**File:** `cmd/clawkerd/main.go`

After successful Connect call, replace the current "idle on `<-ctx.Done()`" pattern with a stream Recv loop:

```go
client := agentv1.NewAgentServiceClient(conn)
stream, err := client.Connect(ctx, &agentv1.ConnectRequest{
    AgentName:    agentName,
    CodeVerifier: verifier,
})
if err != nil { /* fatal */ }

// Delete verifier from disk on stream open success (single-use).
_ = os.Remove(consts.BootstrapVerifierPath)

for {
    cmd, err := stream.Recv()
    if err == io.EOF { return } // stream cleanly closed
    if err != nil {
        // Stream broken — log + exit. Reconnect logic is the
        // CP-restart-resilience initiative's job, not this branch.
        log.Warn().Err(err).Msg("connect stream broken")
        return
    }

    switch payload := cmd.Payload.(type) {
    case *agentv1.Command_Welcome:
        // Welcome carries ClawkerdConfiguration (empty in B4).
        // B5+ uses welcome.Config to init logger.
        _ = payload // no-op in this branch

    default:
        log.Debug().Str("type", fmt.Sprintf("%T", payload)).Msg("received unknown command type (B5+ defines variants)")
    }
}
```

### Task 8 — CLI run/start wiring

**File:** `internal/cmd/container/shared/container_create.go`

In `buildCreateTimeEnv`, populate the three `Clawkerd*` env-var fields **unconditionally** (not gated on firewall):

```go
envOpts := docker.RuntimeEnvOpts{
    // ... existing fields ...
    ClawkerdAgentName: agentName,
    ClawkerdAgentAddr: net.JoinHostPort(consts.ContainerCP, strconv.Itoa(opts.Config.Settings().ControlPlane.AgentPort)),
    ClawkerdHydraURL:  fmt.Sprintf("https://%s/oauth2/token", net.JoinHostPort(consts.ContainerCP, strconv.Itoa(opts.Config.Settings().ControlPlane.HydraPublicPort))),
}
```

**File:** `internal/cmd/container/shared/container_start.go`

Add `AgentName string` to `CommandOpts`. Populate at run/start call sites from `CreateContainerResult.AgentName`.

Insert new helper between `BootstrapServicesPreStart` and `client.ContainerStart`:

```go
func ContainerStart(ctx context.Context, cmdOpts CommandOpts, startOpts docker.ContainerStartOptions) (mobyClient.ContainerStartResult, error) {
    if err := BootstrapServicesPreStart(ctx, startOpts.ContainerID, cmdOpts); err != nil {
        return mobyClient.ContainerStartResult{}, err
    }

    // NEW: deliver agent bootstrap material + announce slot.
    // Hard-fails on any error — clawker-net containers either fully
    // integrate or don't start at all.
    if err := prepareAgentBootstrap(ctx, cmdOpts, startOpts.ContainerID); err != nil {
        return mobyClient.ContainerStartResult{}, fmt.Errorf("agent bootstrap: %w", err)
    }

    client, err := cmdOpts.Client(ctx)
    // ... rest unchanged ...
}

func prepareAgentBootstrap(ctx context.Context, cmdOpts CommandOpts, containerID string) error {
    cfg, err := cmdOpts.Config(); if err != nil { return err }
    settings := cfg.Settings()

    caCertPath, err := consts.AuthCACertPath(); if err != nil { return err }
    caKeyPath, err := consts.AuthCAKeyPath(); if err != nil { return err }
    signingKey, err := auth.LoadSigningKey(); if err != nil { return err }

    hydraTokenURL := fmt.Sprintf("https://%s/oauth2/token",
        net.JoinHostPort(consts.ContainerCP, strconv.Itoa(settings.ControlPlane.HydraPublicPort)))

    bootstrap, err := GenerateAgentBootstrap(caCertPath, caKeyPath, cmdOpts.AgentName, hydraTokenURL, signingKey)
    if err != nil { return err }

    admin, err := cmdOpts.AdminClient(ctx); if err != nil { return err }
    if err := AnnounceAgent(ctx, admin, bootstrap, cmdOpts.AgentName, containerID); err != nil {
        return err
    }

    client, err := cmdOpts.Client(ctx); if err != nil { return err }
    return WriteAgentBootstrapToContainer(ctx, containerID, NewCopyToContainerFn(client), bootstrap)
}
```

### Task 9 — Documentation pass

Update:
- `internal/controlplane/agent/CLAUDE.md`:
  - Connect is server-streaming; full handler narrative.
  - Identity resolution is interceptor-driven (`IdentityInterceptor`), NOT per-handler.
  - Fail-secure opt-out map.
  - Known limitations: streaming RPC eviction broadcast (B5 add); CP restart resilience (separate initiative).
- `internal/controlplane/agentslots/CLAUDE.md`:
  - Composite key (thumbprint + agent_name).
  - dockerevents subscription mirrors agentregistry pattern.
  - `EvictByContainerID` linear scan justified by realistic scales.
- `.claude/docs/KEY-CONCEPTS.md`:
  - Update `agentslots.Registry` entry (composite key).
  - Add `agent.IdentityInterceptor` entry.
  - Add `agent.WithEntry`/`EntryFromContext` entries.
  - Update `agent.Handler` entry (Connect not Register).
- `internal/controlplane/CLAUDE.md`:
  - Step 8 updated: AnnounceAgent handler now real, agent listener has identity interceptor chained.
  - Remove "Known follow-up: AnnounceAgent" — it's done.
- `.serena/memories/cp-initiative-status.md`:
  - Mark Branch 4 follow-up complete; flag CP restart resilience as next.

Replace the seed memo `cp-initiative-branch-4-followup-cli-integration.md` with this spec memory's location pointer (or delete and rely on this doc).

## Test strategy

### Unit tests (no Docker)

| Component | Cases |
|-----------|-------|
| `agentslots.Registry` | Reserve+Consume happy path with composite key; collision returns ErrSlotExists; wrong-verifier preserves slot; EvictByContainerID removes matching slots; TTL janitor unchanged |
| `agentslots.Subscribe` | DeltaRemoved evicts; DeltaUpdated{Stopped} evicts; recover-from-hook-panic; cancel func drains |
| `agent.Handler.Connect` | Happy path sends Welcome + idles; missing fields → InvalidArgument; CN mismatch → PermissionDenied; thumbprint mismatch (composite Consume miss) → PermissionDenied; IP mismatch → PermissionDenied; label mismatch → PermissionDenied; ctx cancellation closes stream cleanly |
| `controlplane.adminServer.AnnounceAgent` | Happy path Reserve called with right Slot, returns ExpiresAtUnix; each validation branch → InvalidArgument; ErrSlotExists → AlreadyExists; mocked slot registry |
| `agent.IdentityInterceptor` (unary + stream) | Connect opted-out → handler called without lookup; non-opted-out method → registry.Lookup called, Entry attached to ctx; ErrUnknownAgent → PermissionDenied; descriptor walker test asserts every method has a policy decision |
| `prepareAgentBootstrap` | Mocked AdminClient + capturing CopyFn; verifies Generate → Announce → WriteToContainer order; AnnounceAgent error propagates as ContainerStart hard-fail |

### E2E tests (Docker required)

Extend `test/e2e/clawkerd_register_test.go` (or rename to `clawkerd_connect_test.go`) for the streaming Connect:

- Happy path: `clawker run` → CP up → AnnounceAgent succeeds → container starts → clawkerd consumes Welcome from stream → registry has the agent → `clawker controlplane agents` lists it.
- Container kill: `docker kill <id>` → dockerevents fires → both registry and slot evict → `agents` reports empty.
- Slot reuse after container death: kill + immediate retry with same agent name → composite-key + dockerevents path lets new attempt succeed within TTL.

Adversarial cases from `clawkerd_failures_test.go` adapt to the streaming Connect signature; most still skip pending the harness mTLS-dial helper.

### Build-time tests

- `TestAgentMethodScopes_CoversAllRPCs` (existing) — already walks proto descriptor.
- `TestAgentIdentityOptedOutMethods_CoversAllRPCs` (NEW, parallel) — walks proto descriptor; asserts every method either has explicit `optedOut[m] = true` OR falls through to require-identity. Adding a new RPC without a policy decision fails the build.

## Risks

1. **Proto rename Register → Connect breaks any external callers.** None exist; clawkerd is the only caller and ships in lockstep.
2. **Composite slot key change is technically a wire-compatible refactor** (Slot fields unchanged; just internal lookup map keying changes). Tests rewritten to match new Consume signature.
3. **Identity interceptor + stream wrapping** has a known gRPC pitfall: `wrappedStream.Context()` must be returned from a method on the WRAPPED type, not the embedded `grpc.ServerStream`. Verified via test that handler sees the augmented ctx, not the original.
4. **clawkerd's stream Recv loop** must NOT delete the verifier until the FIRST stream message (Welcome) is received — receiving the first message implies the auth handshake succeeded server-side. Deleting earlier risks a race where the stream open succeeded transport-wise but the handler rejected auth, and clawkerd has already discarded its credentials.

## Success criteria

- [ ] `make test` passes (unit suite, ~4625 cases + new ones).
- [ ] `go vet ./...` and `go vet ./test/e2e/...` clean.
- [ ] `clawker run @` end-to-end: container starts, clawkerd registers, `clawker controlplane agents` lists it, `docker kill` evicts within seconds.
- [ ] Build-time descriptor-walking tests fail if a new RPC is added without identity-policy or scope-map entry.
- [ ] Proto comments preserve the future reconnect path semantics.
- [ ] All package CLAUDE.md files updated; no "TODO Branch 4" markers left.

## Out-of-scope follow-ups (cross-references)

- `cp-initiative-cp-restart-resilience` — registry persistence, Connect reconnect path, clawkerd reconnect-with-backoff, streaming RPC eviction broadcast, `clawker controlplane down` safety guard.
- B5 work (Command payload variants, Events implementation, ClawkerdConfiguration content, init migration off entrypoint scripts).
