# Controlplane Agent Subpackage

Hosts the cross-cutting `IdentityInterceptor` for the CP's agent gRPC
listener. AgentService is empty in this branch — Register was retired
alongside agentslots/AnnounceAgent — so the package's only live surface
is the interceptor + its supporting helpers.

The package stays in place because the listener mTLS + interceptor
chain are still wired in `cmd/clawker-cp/main.go`: a future inbound
`clawkerd→CP` RPC (e.g. agent-emitted telemetry) lands without
re-wiring.

## Files

| File | Purpose |
|------|---------|
| `handler.go` | Package doc + `peerIdentity{Raw, CommonName}` projection + `peerIdentityFromContext` helper extracting the peer leaf cert from a gRPC context. The historical Register handler + Docker inspector + container-IP cross-check were retired with the AnnounceAgent/agentslots layer. |
| `identity_interceptor.go` | `IdentityInterceptor(reg, optedOut, log) (UnaryServerInterceptor, StreamServerInterceptor)` resolves peer cert thumbprint to an `agentregistry.Entry` on every non-opted-out method; `IdentityOptedOutMethods()` returns an empty map today (AgentService has no inbound RPCs); `WithEntry` / `EntryFromContext` for downstream RPC handlers. |
| `identity_interceptor_test.go` | Branch coverage: registry-hit (with the load-bearing wrapped-Context() check on the stream wrapper), lookup-miss, no-peer-cert; nil-entry panic; stale-opt-out-key panic; empty opt-out roster sanity. |

## Identity interceptor

`IdentityInterceptor` runs AFTER `AuthInterceptor` on the agent listener (token + scope first, identity second). Wired in `cmd/clawker-cp/main.go` via `grpc.ChainUnaryInterceptor` / `ChainStreamInterceptor`.

- The opt-out map is empty today — every method falls through to the registry-lookup path. A future inbound RPC that authenticates itself adds its full method path via `IdentityOptedOutMethods`.
- The stream wrapper `identityServerStream` defines `Context()` on the wrapper, NOT promoted from the embedded `grpc.ServerStream` — promotion silently breaks identity binding for streaming RPCs. The test `TestIdentityInterceptor_Stream_RegistryHit_WrappedContextCarriesEntry` reads `wrapped.Context()` so a regression that drops the override fails fast.
- `WithEntry(nil)` panics — typed-nil pointers survive `(*Entry)(nil)` type assertions as `(nil, true)`, a silent identity vacuum that downstream handlers would dereference. `EntryFromContext` also returns `ok=false` when the stored entry is nil for belt-and-suspenders.
- Lookup-error log differentiation: `errors.Is(err, ErrUnknownAgent)` logs at Warn (operator-expected); other errors log at Error (unexpected internal failure). Wire response stays generic `PermissionDenied` for both.
- Stale-opt-out-key validation runs at construction: every key in the opt-out map must match a real `AgentService_ServiceDesc` method/stream — a typo or rename without a matching update panics at startup, not silently at request time.

## Wiring

`cmd/clawker-cp/main.go` step 8 chains the identity interceptor on the agent listener:

```go
identityUnary, identityStream := agent.IdentityInterceptor(
    agentReg, agent.IdentityOptedOutMethods(), log,
)
agentServer := grpc.NewServer(
    grpc.Creds(credentials.NewTLS(agentTLSCfg)),
    grpc.ChainUnaryInterceptor(authInterceptor.UnaryInterceptor(), identityUnary),
    grpc.ChainStreamInterceptor(authInterceptor.StreamInterceptor(), identityStream),
)
agentv1.RegisterAgentServiceServer(agentServer, &agentv1.UnimplementedAgentServiceServer{})
```

The agent registry subscribes to typed `dockerevents.ContainerRemoved` events on the Overseer bus:

```go
cancelAgentSub := agentregistry.Subscribe(watcherCtx, agentReg, bus, log)
defer cancelAgentSub()
```

## Imports

**Uses**: `api/agent/v1`, `internal/controlplane/agentregistry`, `internal/logger`, `google.golang.org/grpc/{codes,credentials,peer,status}`, `crypto/sha256`.

**Used by**: `cmd/clawker-cp` (interceptor chain + registry resolution on the agent gRPC listener).
