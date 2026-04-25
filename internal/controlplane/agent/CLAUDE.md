# Controlplane Agent Subpackage

Implements the `clawker.agent.v1.AgentService` gRPC surface — the
clawkerd-facing handler that completes the announce-then-register PKCE
handshake.

## Files

| File | Purpose |
|------|---------|
| `handler.go` | `Handler` (`agentv1.AgentServiceServer`) + `NewHandler(slots, reg, inspector, log)`; `ContainerInspector` narrow Docker dependency + `MobyInspector` adapter; `peerCertAndIP` extracts the mTLS peer cert + IPv4 from the gRPC context |
| `handler_test.go` | Happy-path + every adversarial branch (missing fields, no peer cert, wrong verifier, cert swap, peer-IP mismatch, label tampering, Docker inspect error) |
| `mocks/` | moq-generated `ContainerInspectorMock` for external consumers |

## Register flow (load-bearing)

1. AuthInterceptor (Task 10) verifies the bearer token + `agent:self:register` scope. mTLS itself is enforced by the listener; the handler reads the peer cert from `peer.FromContext`.
2. `req` is validated for non-empty `agent_name` + `code_verifier`. Empty fields return `codes.InvalidArgument` so a confused client gets a clear failure mode rather than the generic registration-rejected envelope.
3. Slot consume — atomic, constant-time PKCE compare. Mismatch leaves the slot intact (TTL evicts) so a benign retry can succeed.
4. Cert thumbprint vs slot.expected_thumbprint — constant-time. Defends cert swap in the bootstrap path between announce and clawkerd boot.
5. Docker inspect: peer IP must match `clawker-net` IP for `slot.container_id`. Defends cert+verifier theft replayed from a different container.
6. Label cross-check: `dev.clawker.agent` must equal the canonical agent name. Defends label tampering after announce.
7. On success: registry keyed by SHA-256 over `peer_cert.Raw`. Future per-agent RPCs resolve identity by recomputing the thumbprint.

Every failure returns a single `codes.PermissionDenied` with a generic message — no leak about which check failed.

## Wiring

`cmd/clawker-cp/main.go` step 8 instantiates the handler:

```go
slotRegistry := agentslots.NewRegistry(time.Now, 0, log)
agentReg := agentregistry.NewRegistry(log)
agentInspector := agent.MobyInspector{Client: dockerCli.APIClient}
agentHandler := agent.NewHandler(slotRegistry, agentReg, agentInspector, log)
agentv1.RegisterAgentServiceServer(agentServer, agentHandler)
```

The dockerevents → registry subscription lives below in step 9a:

```go
cancelAgentSub := agentregistry.Subscribe(watcherCtx, agentReg, inf)
defer cancelAgentSub()
```

## Test pattern

Handler tests use a closure-backed `inspectorFn` (in-package, no
moq) because the moq generated for `ContainerInspector` lives in
`agent/mocks/` and importing it from `_test.go` would create an import
cycle. The mock under `mocks/` exists for external consumers (later
branches that integrate the agent handler into other subsystems).

Tests use a fixed-time clock for the slot registry so `Reserve.ExpiresAt`
and the registry's `now()` agree — without this, the registry's default
`time.Now` evaluates against a fake-time slot and reports it expired
instantly.

## Imports

**Uses**: `api/agent/v1`, `internal/consts`, `internal/controlplane/agentregistry`, `internal/controlplane/agentslots`, `internal/logger`, `google.golang.org/grpc/{codes,credentials,peer,status}`, `crypto/{sha256,subtle}`, `encoding/hex`.

**Used by**: `cmd/clawker-cp` (handler registration on the agent gRPC listener).
