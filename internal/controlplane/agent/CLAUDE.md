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

`cmd/clawker-cp/main.go` wires the agent gRPC listener and registry in
Step 8, then calls `agent.Start` after Step 9 (SetReady) once the
dialer is constructed:

```go
agentCleanup, err := agent.Start(watcherCtx, agent.StartDeps{
    Registry:     agentReg,            // CP-owned sqlite writer
    DockerLister: func(ctx context.Context) ([]string, error) {
        return listAgentIDs(ctx, listAgentsOpts{All: true})
    },                                 // returns purpose=agent containers, All:true
    PeerLookup:   agentPeerLookup,     // peer-IP→Docker→labels resolver (required)
    Dialer:       dialer,              // agent.New(...) for CP→clawkerd
    Bus:          bus,
    Log:          log.With("component", "agent"),
})
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
3. **Subscribe to `dockerevents.DockerEvent` for Session cancel.**
   Filter on `container/{die,stop,kill,oom,destroy}`; consumer calls
   `dialer.CancelDial(containerID)` so the in-flight CP→clawkerd
   `Session` is torn down synchronously on any exit transition
   instead of lingering until the next reconnect attempt notices
   the container is gone. Independent of the evict subscriber:
   evict reflects row state (destroy only), cancel reflects
   connectivity state (any exit transition). Both fire off the
   same docker event bus — neither drives the other.
4. **Subscribe to `dockerevents.DockerEvent` for dial.** Filter on
   `container/start|restart|unpause` with `purpose=agent`; consumer
   calls `dialer.DialAgent(ctx, containerID)`.

The previously-fragmented `Reap`/`Subscribe`/`Subscribe` exports are
now unexported helpers behind `Start`.

## Files

| File | Purpose |
|------|---------|
| `start.go` | `Start(ctx, StartDeps)` umbrella + shared panic-loop guardrails for the two subscribers + private `reapOrphans` |
| `registry.go` | `Registry` interface, `Entry`, `ErrUnknownAgent`, `NewRegistry` (in-memory test impl) |
| `registry_sqlite.go` | sqlite-backed `Registry`: `NewSQLiteWriter`, `EnsureSchema`. Schema applied via goose migrations embedded from `migrations/*.sql` (see `applySchema`) |
| `dialer.go` | `Dialer.DialAgent` — CP-side outbound mTLS dial to `ClawkerdService.Session`. Permissive trust posture (asymmetric: CP must always be reachable). Drives Register handshake on Miss, publishes Session* + AgentRegistered + AgentUntrusted events |
| `events_session.go` | `SessionConnecting`, `SessionConnected`, `SessionFailed`, `SessionBroken` — all implement `overseer.applier` mutating `State.Agents` |
| `events_agent.go` | `AgentRegistered{Ok, Reason}`, `AgentUntrusted{Reason UntrustedReason, Detail}` — also implement `applier` |
| `register_handler.go` | `Handler` (AgentService.Register handler) — consumes middleware-resolved identity from ctx, captures cert thumbprint, cross-checks cert container SAN + request fields against resolved truth, writes the registry row |
| `peer_lookup.go` | `ContainerByPeerIP` interface + `ResolvedContainer` struct + sentinels (`ErrNoContainerForPeerIP`, `ErrInvalidAgentLabel`, `ErrAmbiguousPeerIP`) — peer-IP-grounded trust resolver. `ErrInvalidAgentLabel` fires only on a missing/malformed `dev.clawker.agent` label; a missing `dev.clawker.project` label is the legitimate global-scope-agent signal (2-segment naming) and resolves cleanly |
| `peer_lookup_moby.go` | `MobyPeerLookup`, the production `ContainerByPeerIP` backed by the Docker daemon |
| `handler.go` | `peerIdentity` projection + `peerIdentityFromContext` + `peerLeafFromContext` + `WithResolvedContainer` / `ResolvedContainerFromContext` ctx helpers |
| `identity_interceptor.go` | `IdentityInterceptor(peerLookup, log)` — universal peer-IP-grounded identity gate applied to every AgentService RPC (no opt-out) |
| `init.go` | `Executor` + static `plan()` of `ShellCommand` init steps dispatched to clawkerd over the Session before the terminal `agent-ready`. Step order: `docker-socket`, `config`, `git`, `git-credentials`, `ssh`, `post-init` (once, marker-gated), **`pre-run`** (every start), `agent-ready`. `pre-run` runs `preRunScript` via `userStage` — no marker (runs every start), the defensive `[ -x … ] \|\| exit 0` guard no-ops when the file is missing yet propagates the exit code when present (the script is delivered fresh each start by the CLI's `BootstrapServicesPreStart`). A non-zero exit is fatal: the plan halts and `agent-ready` is never sent. The pinned step vocabulary lives in `expectedInitStepNames` (init_test.go); step names map 1:1 to the TTY progress labels in `cmd/clawkerd/progress.go` (`initStepLabels`). |
| `registry_mock_test.go` | moq-generated `RegistryMock` (test-only file; lives in `agent` package itself to break import cycle that prevented an `agent/mocks` subpackage from working) |

## Identity contract

The trust anchor is the kernel-attested peer IP, NOT cert claims.
`IdentityInterceptor` resolves the peer IP to a `purpose=agent`
container via `ContainerByPeerIP`, reads the project/agent labels as
the authoritative identity source, and constant-time-compares the
label-derived AgentFullName against the cert's `urn:clawker:agent:`
URI SAN. The cert's SAN claim is VERIFIED against this independent
ground truth, never the basis of lookup.

A registry row's identity is `(thumbprint, container_id)` — both
UNIQUE in sqlite. The handler captures the thumbprint at the gate
that writes it (defense-in-depth: no surfacing via ctx, so a future
interceptor change can't substitute the value the registry stores).
Rows store the `(thumbprint, container_id, project, agent_name,
registered_at, last_seen)` tuple; `Snapshot()` and `ListAgents`
reconstruct the displayed AgentFullName on demand from project +
agent_name. There is no precomputed identity column on the row.

`Entry.Project` and `Entry.AgentName` are typed
(`auth.ProjectSlug` / `auth.AgentName`). Construction goes through
`auth.NewProjectSlug` / `auth.NewAgentName` at the wire boundary,
and `scanEntry` re-validates on sqlite read so a hand-edited or
corrupted row cannot land an invariant-violating value — `Snapshot()`
logs and skips rows that fail validation (`event=agentregistry_row_skipped`
plus a per-snapshot `event=agentregistry_snapshot_skipped_rows`
summary).

CP is the SOLE writer of registry rows. The CLI never opens the
sqlite DB — that's what fixes the WAL coherence bug across the
macOS bind-mount boundary.

## Register flow (CP-driven, one-time per container)

1. **Container creation** (`clawker run`): CLI mints a leaf cert with
   `Subject.CommonName = consts.ContainerClawkerd` (binary identity),
   the per-agent `AgentFullName` in a `urn:clawker:agent:<full-name>`
   URI SAN, and the container_id in a `urn:clawker:container:<id>`
   URI SAN. Tars cert+key+ca+assertion JWT into the container, starts
   it. No registry row written here.
2. **Session establishment**: CP dials clawkerd via `agent.Dialer`,
   completes mTLS + Hello, runs `classifyRegistry` → returns
   `outcomeRegistryMiss` because no row exists.
3. **RegisterRequired dispatch**: dialer sends
   `Command{RegisterRequired{}}` on the Session bidi stream.
4. **Agent-side handshake** (in clawkerd, see `cmd/clawkerd/register.go`):
   `registerCoordinator.Run` exchanges the single-use Hydra
   `client_assertion` JWT for an access token, mTLS-dials CP's
   AgentPort with `bearerCreds`, calls `AgentService.Register`.
5. **CP Register handler**: `IdentityInterceptor` has already run on
   this RPC — pinned `Subject.CommonName == consts.ContainerClawkerd`,
   resolved the peer IP to a `purpose=agent` container, and verified
   that the cert's `urn:clawker:agent:` URI SAN matches the
   label-derived AgentFullName. The interceptor attached the resolved
   `(containerID, project, agentName)` triple to ctx via
   `WithResolvedContainer`. The handler reads it via
   `ResolvedContainerFromContext`, captures the live peer cert
   thumbprint (the registry's UNIQUE key), cross-checks the cert's
   `urn:clawker:container:` SAN against `resolved.ContainerID`,
   cross-checks the request body against `resolved.{Project, AgentName}`,
   idempotently writes the row using the label-derived (authoritative)
   identity, returns Welcome.
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
| Lookup error | `SessionConnected` + `AgentUntrusted{ReasonCertInvalid, Detail: <err>}` |

Note: cert SAN AgentFullName vs label-derived AgentFullName drift is
NOT classified here — the dialer is the OUTBOUND CP→clawkerd path and
compares only thumbprints against the row. Trust-anchor drift is
caught upstream by `IdentityInterceptor` on the agent's next inbound
RPC (e.g. a subsequent Register retry).

`SessionConnected.ApplyTo` populates `State.Agents[containerID]` with
session lifecycle + identity fields. `AgentRegistered.ApplyTo` sets
`Registered`. `AgentUntrusted.ApplyTo` sets `Trusted=false` +
`UntrustedReason`.

## Method scopes + interceptor

- `agentv1.AgentMethodScopes()` (in `api/agent/v1`, beside the proto
  bindings) maps `/clawker.agent.v1.AgentService/Register` →
  `agentv1.ScopeSelfRegister`. AuthInterceptor on the agent
  listener fails closed on unmapped methods.
- `IdentityInterceptor` runs a universal three-stage gate on every
  AgentService RPC (no opt-out — applies to Register too):
  1. **Universal CN pin** — `leaf.Subject.CommonName` must equal
     `consts.ContainerClawkerd` (constant-time compare). Rejects any
     peer presenting a non-clawkerd cert before reaching any handler.
  2. **Peer IP → Docker → labels** — resolves the kernel-attested
     peer IP to the `purpose=agent` container owning that endpoint
     on the clawker network via `ContainerByPeerIP`, reads the
     `dev.clawker.{project,agent}` labels as the authoritative
     identity source.
  3. **Cert SAN ↔ label cross-check** — composes the label-derived
     AgentFullName and constant-time compares it against the cert's
     `urn:clawker:agent:` URI SAN.
- On success, the interceptor attaches the resolved
  `ResolvedContainer{ContainerID, Project, AgentName}` to ctx via
  `WithResolvedContainer`. Handlers read it via
  `ResolvedContainerFromContext`; the Register handler additionally
  cross-checks the cert's `urn:clawker:container:` SAN and the RPC
  body against the resolved truth.

## Imports

**Uses**: `internal/auth` (AgentFullName, NewAgentName,
NewProjectSlug, ContainerIDFromCert, AgentFullNameFromCert),
`internal/consts` (Network, LabelAgent/Project/Purpose,
ContainerClawkerd),
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
- `ContainerByPeerIP` — hand-rolled `fakePeerLookup` in
  `identity_interceptor_test.go`; the Register handler's tests stub
  the resolved container via `WithResolvedContainer` directly.
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
