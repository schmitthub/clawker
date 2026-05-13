# Controlplane Agent Package

The unified CP-side surface for everything keyed on a clawker-managed
agent: persisted identity registry, AgentService.Register handler,
per-RPC IdentityInterceptor, CP→clawkerd Session dialer, and the
agent-axis event types (`SessionConnecting/Connected/Failed/Broken`,
`AgentRegistered`, `AgentUntrusted`).

This package replaces the prior split between `agentdial`,
`agentregistry`, and a stub `agent` package. One axis (the agent), one
package, one umbrella entry point (`agent.Start`).

## Single entry point: `agent.Start`

`cmd/clawker-cp/main.go` Step 8 wires the entire agent surface with one
call:

```go
agentCleanup, err := agent.Start(watcherCtx, agent.StartDeps{
    Registry:     agentReg,            // CP-owned sqlite writer
    DockerLister: listAgentIDsAll,     // returns purpose=agent containers, All:true
    Dialer:       dialer,              // agent.New(...) for CP→clawkerd
    Bus:          bus,
    Log:          log.With("component", "agent"),
})
defer agentCleanup()
```

`Start` does, in order:

1. **Reap orphan registry rows.** Lists `purpose=agent` containers
   (`All: true`, includes stopped) and evicts rows whose container_id
   is gone. Heals the registry against `docker rm`s that landed while
   CP was down.
2. **Subscribe to `dockerevents.DockerEvent` for evict.** Filter on
   `container/destroy`; consumer evicts the registry row. Stop/die/
   kill do NOT evict — a stopped container can be `docker start`-ed
   back into life.
3. **Subscribe to `dockerevents.DockerEvent` for dial.** Filter on
   `container/start|restart|unpause` with `purpose=agent`; consumer
   calls `dialer.DialAgent(ctx, containerID)`.

The previously-fragmented `Reap`/`Subscribe`/`Subscribe` exports are
now unexported helpers behind `Start`.

## Files

| File | Purpose |
|------|---------|
| `start.go` | `Start(ctx, StartDeps)` umbrella + shared panic-loop guardrails for the two subscribers + private `reapOrphans` |
| `registry.go` | `Registry` interface, `Entry`, `ErrUnknownAgent`, `NewRegistry` (in-memory test impl) |
| `registry_sqlite.go` | sqlite-backed `Registry`: `NewSQLiteWriter`, `EnsureSchema`, schema apply with migration support |
| `dialer.go` | `Dialer.DialAgent` — CP-side outbound mTLS dial to `ClawkerdService.Session`. Permissive trust posture (asymmetric: CP must always be reachable). Drives Register handshake on Miss, publishes Session* + AgentRegistered + AgentUntrusted events |
| `events_session.go` | `SessionConnecting`, `SessionConnected`, `SessionFailed`, `SessionBroken` — all implement `overseer.applier` mutating `State.Agents` |
| `events_agent.go` | `AgentRegistered{Ok, Reason}`, `AgentUntrusted{Reason UntrustedReason, Detail}` — also implement `applier` |
| `register_handler.go` | `Handler` (AgentService.Register handler) + `ContainerInspector` interface + `NewMobyContainerInspector` |
| `peer_lookup.go` | `ContainerByPeerIP` interface + `ResolvedContainer` struct + sentinels (`ErrNoContainerForPeerIP`, `ErrInvalidAgentLabels`) — peer-IP-grounded trust resolver |
| `peer_lookup_moby.go` | `MobyPeerLookup`, the production `ContainerByPeerIP` backed by the Docker daemon |
| `handler.go` | `peerIdentity` projection + `peerIdentityFromContext` (used by IdentityInterceptor) |
| `identity_interceptor.go` | `IdentityInterceptor(reg, optedOut, log)` + `IdentityOptedOutMethods()` (Register is opt-out — registry row doesn't exist pre-call) + `WithEntry` / `EntryFromContext` |
| `registry_mock_test.go` | moq-generated `RegistryMock` (test-only file; lives in `agent` package itself to break import cycle that prevented an `agent/mocks` subpackage from working) |

## Identity contract

A row's identity is `(thumbprint, container_id)` — both UNIQUE in
sqlite. The CN composed from `(project, agent_name)` is stored
pre-computed (`canonical_cn` column) and compared via
`subtle.ConstantTimeCompare` inside `Lookup`. No reconstruction at
read time.

CP is the SOLE writer of registry rows. The Register handler captures
the live mTLS peer's cert thumbprint at handler entry and writes the
row. The CLI never opens the sqlite DB — that's what fixes the WAL
coherence bug across the macOS bind-mount boundary.

## Register flow (CP-driven, one-time per container)

1. **Container creation** (`clawker run`): CLI mints a leaf cert with
   `Subject.CommonName = consts.ContainerClawkerd` (binary identity),
   the canonical agent identity in a `urn:clawker:agent:<canonical>`
   URI SAN, and the container_id in a `urn:clawker:container:<id>`
   URI SAN. Tars cert+key+ca+assertion JWT into the container, starts
   it. No registry row written here.
2. **Session establishment**: CP dials clawkerd via `agentdial.Dialer`,
   completes mTLS + Hello, runs `classifyRegistry` → returns
   `outcomeRegistryMiss` because no row exists.
3. **RegisterRequired dispatch**: dialer sends
   `Command{RegisterRequired{}}` on the Session bidi stream.
4. **Agent-side handshake** (in clawkerd, see `cmd/clawkerd/register.go`):
   `registerCoordinator.Run` exchanges the single-use Hydra
   `client_assertion` JWT for an access token, mTLS-dials CP's
   AgentPort with `bearerCreds`, calls `AgentService.Register`.
5. **CP Register handler**: captures peer thumbprint, reads
   container_id from `urn:clawker:container:` URI SAN, reads canonical
   agent identity from `urn:clawker:agent:` URI SAN and constant-time
   compares against `CanonicalAgentCN(project, agent)` from the
   request. The `Subject.CommonName == consts.ContainerClawkerd` pin
   already ran in `IdentityInterceptor` (universal — no opt-out for
   that check). Inspects container via `ContainerInspector`,
   cross-checks labels + peer-IP-vs-clawker-net IP, idempotently writes
   the row, returns Welcome.
6. **clawkerd replies** with `Response{RegisterDone{ok:true}}`.
7. **CP confirms** by re-looking-up the row, publishes
   `AgentRegistered{Ok:true}` on the bus.

Subsequent Sessions (after stop/start, CP restart, etc.) hit
`outcomeRegistryMatch` at Hello time — no Register handshake fires.
`AgentRegistered` is one-time per container; subscribers needing
"this agent is currently registered" should query
`State.Agents[containerID].Registered` instead.

## Asymmetric trust (load-bearing)

CP is the overlord. The dialer NEVER aborts on cert/identity grounds
— Session stays open even when the agent is `AgentUntrusted` so CP
can still dispatch containment commands. The Register handler DOES
reject (`PermissionDenied`) on cert/IP/label mismatches because it's
the gate that decides whether to write a row, not whether to keep
the channel up.

The clawkerd-side counterpart (`cmd/clawkerd/listener.go`) is STRICT:
CP CN pin + Client-Auth EKU + CA chain enforced at TLS layer.

## Trust outcomes via overseer events

| Hello-time outcome | Events published |
|---|---|
| Match | `SessionConnected` (with PeerAgentFullName/PeerThumbprint) |
| Miss | `SessionConnected` → drive Register handshake → `AgentRegistered` (+ `AgentUntrusted{ReasonRegisterFailed}` on failure) |
| ThumbprintMismatch | `SessionConnected` + `AgentUntrusted{ReasonThumbprintMismatch}` |
| CNMismatch | `SessionConnected` + `AgentUntrusted{ReasonCNMismatch}` |
| Lookup error | `SessionConnected` + `AgentUntrusted{ReasonCertInvalid, Detail: <err>}` |

`SessionConnected.ApplyTo` populates `State.Agents[containerID]` with
session lifecycle + identity fields. `AgentRegistered.ApplyTo` sets
`Registered`. `AgentUntrusted.ApplyTo` sets `Trusted=false` +
`UntrustedReason`.

## Method scopes + interceptor opt-out

- `controlplane.AgentMethodScopes()` maps
  `/clawker.agent.v1.AgentService/Register` →
  `consts.ScopeAgentSelfRegister`. AuthInterceptor on the agent
  listener fails closed on unmapped methods.
- `IdentityInterceptor` runs a two-stage gate on every RPC:
  1. **Universal CN pin** — `leaf.Subject.CommonName` must equal
     `consts.ContainerClawkerd` (constant-time compare). Applies to
     every method including Register. Rejects any peer presenting a
     non-clawkerd cert before reaching any handler.
  2. **Registry lookup** — for non-opt-out methods, the interceptor
     hashes `leaf.Raw` to a thumbprint, reads the canonical agent
     identity from the `urn:clawker:agent:` URI SAN, and calls
     `Registry.Lookup(thumbprint, agentFullName)`. The resolved
     `*Entry` is attached to ctx via `WithEntry`.
- `IdentityOptedOutMethods()` includes the Register method path —
  but opt-out applies ONLY to the registry-lookup half. The
  universal CN pin still runs. Justification for the registry-lookup
  opt-out: the row keyed by peer thumbprint doesn't exist yet at
  Register-call time — that's the entire purpose of the call.
  Going through Lookup would reject every legitimate Register with
  `PermissionDenied`. The Register handler does its own
  SAN-canonical + container_id SAN + peer-IP + label cross-checks,
  so opt-out relocates the registry gate from interceptor to
  handler, not strips it.

## Imports

**Uses**: `internal/auth` (CanonicalAgentCN, NewAgentName,
NewProjectSlug, ContainerIDFromCert, AgentCanonicalFromCert,
AgentSANScheme, ContainerSANScheme), `internal/consts` (Network,
LabelAgent/Project/Purpose, ContainerCP, ContainerClawkerd,
ScopeAgentSelfRegister),
`internal/controlplane/dockerevents`, `internal/controlplane/overseer`,
`internal/logger`, `api/agent/v1`, `api/clawkerd/v1`,
`modernc.org/sqlite`, `github.com/moby/moby/api/types/{container,
network}`, `github.com/moby/moby/client`,
`google.golang.org/grpc/{credentials,peer,status}`, stdlib
`crypto/{sha256,subtle,tls,x509}`, `database/sql`, `encoding/hex`,
`net/netip`.

**Used by**: `cmd/clawker-cp` (agent.Start, agent.NewSQLiteWriter,
agent.NewHandler, agent.IdentityInterceptor, agent.New for the
dialer), `internal/controlplane/server.go` (`agent.Registry` type
on adminServer for ListAgents).

## Test seam

- `Registry` — moq-generated `RegistryMock` in `registry_mock_test.go`
  (test-only, lives in the `agent` package itself).
- `ContainerInspector` — hand-rolled `fakeContainerInspector` in
  `register_handler_test.go`.
- `Dialer` doesn't have a moq mock — tests construct `*Dialer`
  directly with the fields they need.

## Regenerating the mock

```bash
cd internal/controlplane/agent && go generate ./...
```

The `go:generate` directive on `Registry` writes
`registry_mock_test.go` directly into the package. moq doesn't know
the package's own type names; the generated file imports
`agent` and references `agent.Entry` / `agent.Registry`. Strip the
self-import and replace `agent.X` with `X` after regeneration (see
the existing file for the post-edit shape — it's idempotent).
