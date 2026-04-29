# Controlplane Agent Subpackage

Implements the `clawker.agent.v1.AgentService` gRPC surface — the
clawkerd-facing handler that opens the lifetime command channel after
the PKCE-bound identity handshake.

## Files

| File | Purpose |
|------|---------|
| `handler.go` | `Handler` (`agentv1.AgentServiceServer`) + `NewHandler(slots, reg, inspector, log)`; `ContainerInspector` narrow Docker dependency + `MobyInspector` adapter; `peerIdentityAndIP` extracts a narrow `peerIdentity{Raw, CommonName}` projection + IPv4 from the gRPC context |
| `handler_test.go` | Happy-path + every adversarial branch (missing fields, no peer cert, CN mismatch, wrong verifier, cert swap, peer-IP mismatch, label tampering, Docker inspect error, Send-failure orphan defense, ctx-cancel idle teardown) |
| `identity_interceptor.go` | `IdentityInterceptor(reg, optedOut, log) (UnaryServerInterceptor, StreamServerInterceptor)` resolves peer cert thumbprint to a registry entry; `IdentityOptedOutMethods()` lists bootstrap RPCs that authenticate themselves; `WithEntry` / `EntryFromContext` for downstream RPC handlers |
| `identity_interceptor_test.go` | Branch coverage for opt-out, registry-hit (with the load-bearing wrapped-Context() check), lookup-miss, and no-peer-cert; nil-entry panic; opt-out roster sanity vs proto descriptor |
| `mocks/` | moq-generated `ContainerInspectorMock` for external consumers |

## Connect flow (load-bearing)

The Connect RPC is server-streaming. After auth, the handler sends
exactly one `Welcome` and idles on `stream.Context().Done()` for the
agent's lifetime — eviction (dockerevents → cancel) or clawkerd
disconnect closes the stream.

1. AuthInterceptor verifies the bearer token + `agent:self:register` scope. mTLS itself is enforced by the listener; the handler reads the peer cert from `peer.FromContext`.
2. `req` is validated for non-empty `agent_name` + `code_verifier`. Empty fields return `codes.InvalidArgument` so a confused client gets a clear failure mode rather than the generic registration-rejected envelope.
3. **Cert CN cross-check** — `subtle.ConstantTimeCompare(peerCert.Subject.CommonName, auth.CanonicalAgentCN(req.Project, req.AgentName))`. Defends announce-payload tampering between cert mint and the ConnectRequest body — a tampered project OR agent on the wire produces a different canonical and fails this check. Runs BEFORE slot consume so a CN mismatch can't burn a legitimate slot.
4. **Composite slot consume** — `slots.Consume(thumbprint, agent_name, project, verifier)`. The (thumbprint, agent_name, project) lookup folds the cert-thumbprint cross-check into the map key, eliminating the separate post-Consume thumbprint compare. PKCE compare is constant-time inside `agentslots`. Mismatch leaves the slot for benign retry (TTL evicts).
5. **Docker inspect** — peer IP must match `clawker-net` IP for `slot.container_id`. Defends cert+verifier theft replayed from a different container.
6. **Label cross-check** — BOTH `dev.clawker.agent` AND `dev.clawker.project` must equal the slot's `AgentName` and `Project`. Defends label tampering after announce; checking only the agent half would let an attacker who relabeled the project (but kept the agent name) ride a slot for the wrong project.
7. **Send Welcome** — first message after auth. Receipt by clawkerd implies server-side auth fully succeeded and authorizes deletion of the single-use PKCE verifier. **Send fires BEFORE** `registry.Add` so a transport failure leaves no orphan registry entry.
8. **Pin to registry** — keyed by SHA-256 over `peer_cert.Raw`. Per-agent RPCs in later branches resolve identity by recomputing the thumbprint.
9. **Idle on `<-ctx.Done()`** — the connection is the agent's lifetime command channel. B5+ replaces this with a select on a per-agent command queue.

Every failure returns a single `codes.PermissionDenied` with a generic message — no leak about which check failed. Send-failure is `codes.Unavailable` (auth succeeded, channel was the problem); never bare `fmt.Errorf` (would surface as `codes.Unknown`).

## Identity interceptor

`IdentityInterceptor` runs AFTER `AuthInterceptor` on the agent listener (token + scope first, identity second). Wired in `cmd/clawker-cp/main.go` via `grpc.ChainUnaryInterceptor` / `ChainStreamInterceptor`.

- Connect is on the opt-out list (it authenticates itself via slot consume + cross-checks). Every other agent RPC must be registry-bound by the time the handler sees it.
- The stream wrapper `identityServerStream` defines `Context()` on the wrapper, NOT promoted from the embedded `grpc.ServerStream` — promotion silently breaks identity binding for streaming RPCs. The test `TestIdentityInterceptor_Stream_RegistryHit_WrappedContextCarriesEntry` reads `wrapped.Context()` so a regression that drops the override fails fast.
- `WithEntry(nil)` panics — typed-nil pointers survive `(*Entry)(nil)` type assertions as `(nil, true)`, a silent identity vacuum that downstream handlers would dereference. `EntryFromContext` also returns `ok=false` when the stored entry is nil for belt-and-suspenders.
- Lookup-error log differentiation: `errors.Is(err, ErrUnknownAgent)` logs at Warn (operator-expected); other errors log at Error (unexpected internal failure). Wire response stays generic `PermissionDenied` for both.

## Wiring

`cmd/clawker-cp/main.go` step 8 instantiates the handler and chains the identity interceptor on the agent listener:

```go
slotRegistry := agentslots.NewRegistry(time.Now, 0, log)
defer slotRegistry.Stop()
agentReg := agentregistry.NewRegistry(log)

identityUnary, identityStream := agent.IdentityInterceptor(
    agentReg, agent.IdentityOptedOutMethods(), log,
)
agentServer := grpc.NewServer(
    grpc.Creds(credentials.NewTLS(agentTLSCfg)),
    grpc.ChainUnaryInterceptor(authInterceptor.UnaryInterceptor(), identityUnary),
    grpc.ChainStreamInterceptor(authInterceptor.StreamInterceptor(), identityStream),
)
agentInspector := agent.MobyInspector{Client: dockerCli.APIClient}
agentHandler := agent.NewHandler(slotRegistry, agentReg, agentInspector, log)
agentv1.RegisterAgentServiceServer(agentServer, agentHandler)
```

The agent registry subscribes to typed `dockerevents.ContainerRemoved` events on the Overseer bus (step 9a):

```go
cancelAgentSub := agentregistry.Subscribe(watcherCtx, agentReg, bus, log)
defer cancelAgentSub()
```

`agentslots` is intentionally **not** an Overseer subscriber — its TTL janitor is the sole correctness floor for stuck pre-Connect slots; container lifecycle is irrelevant to slot eviction.

## Test pattern

Handler tests use a closure-backed `inspectorFn` (in-package, no
moq) because the moq generated for `ContainerInspector` lives in
`agent/mocks/` and importing it from `_test.go` would create an import
cycle. The mock under `mocks/` exists for external consumers.

Streaming Connect tests use a `connectStreamFake` that embeds
`grpc.ServerStream` (nil interface — drift panics). Optional `sendErr`
covers the Send-failure path; a `welcomed chan struct{}` closed on
first Send eliminates busy-wait synchronization. `runConnect`
goroutine wrapper has a 2s deadline guard so a regression in the
idle-on-ctx.Done path fails fast instead of hanging.

Tests use a fixed-time clock for the slot registry so `Reserve.ExpiresAt`
and the registry's `now()` agree — without this, the registry's default
`time.Now` evaluates against a fake-time slot and reports it expired
instantly.

## Known limitations

- **Streaming RPC eviction broadcast deferred.** Today, eviction at the registry level (dockerevents → `EvictByContainerID`) does not cancel the per-agent Connect stream — clawkerd holds the stream open until the underlying mTLS connection's TCP socket dies on container exit. Closing this gap requires a per-agent cancel-func registry or an eviction channel; tracked in `cp-initiative-cp-restart-resilience`.
- **CP restart resilience deferred.** When the CP restarts, the in-memory `agentregistry` and `agentslots` are lost; clawkerd's open stream tears down on the wire and the daemon exits. Reconnect requires registry persistence + a Connect handler reconnect branch (registry-already-has-thumbprint → skip slot consume) + clawkerd reconnect-with-backoff. The proto comment on `ConnectRequest.code_verifier` preserves the empty-verifier seam for the future reconnect path; full design in `cp-initiative-cp-restart-resilience`.

## Imports

**Uses**: `api/agent/v1`, `internal/auth` (CanonicalAgentCN), `internal/consts`, `internal/controlplane/agentregistry`, `internal/controlplane/agentslots`, `internal/logger`, `google.golang.org/grpc/{codes,credentials,peer,status}`, `crypto/{sha256,subtle}`.

**Used by**: `cmd/clawker-cp` (handler registration + identity interceptor chain on the agent gRPC listener).
