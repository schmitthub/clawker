# CP-Driven Agent Register + Unified Agent State — Shipped

Foundation work that restores AgentService.Register as a CP-triggered
handshake and consolidates the agent-axis package + worldview state.
Branch: `feat/clawkerd-commands`. Replaces the
`restore-cp-driven-agent-register.md` memo's plan; that memo can now
be removed/archived.

## What changed

### Proto + cert
- `api/agent/v1/agent.proto`: re-added `service AgentService { rpc Register(RegisterRequest) returns (Welcome); }`. `RegisterRequest{agent_name, project}` — container_id is read from cert SAN, not the request body.
- `api/clawkerd/v1/clawkerd.proto`: added `RegisterRequired` to `Command.payload` oneof and `RegisterDone{ok, error}` to `Response.payload` oneof.
- `internal/auth/agent_cert.go`: `MintAgentCert` now takes `containerID` and embeds it as a `urn:clawker:container:<id>` URI SAN. `auth.ContainerIDFromCert(cert)` reads it back. `auth.ContainerSANScheme` and `auth.BuildContainerSAN` are exported helpers.

### PKCE retired entirely
- `AgentBootstrap` struct: dropped `verifier`, `Challenge`, `Method`, `ExpectedCertThumbprint`. Kept `CertPEM`, `KeyPEM`, `CACertPEM`, `Assertion`.
- Bootstrap tar shrinks from 5 → 4 files (no verifier file).
- `consts.BootstrapVerifierFile`, `consts.ChallengeMethod`, `consts.ChallengeMethodS256` deleted.

### CP becomes sole sqlite writer
- `internal/cmd/container/shared/agent_bootstrap.go`: `RegisterAgentInRegistry` deleted; CLI no longer opens the registry DB at all. Bootstrap install is mint + tar only.
- `internal/cmd/container/shared/container_create.go`: dropped the `RegisterAgentInRegistry` call (line 1738 area). Container creation ends after `WriteAgentBootstrapToContainer` + post-init injection.
- `agent.NewSQLiteReader` + the `sqliteOpenReader` mode deleted; `sqliteOpenMode` enum collapsed to a single mode.
- `agent.EnsureSchema` moved from `cpboot/bootstrap.go` (host-side) to `cmd/clawker-cp/main.go` Step 8, immediately before `NewSQLiteWriter`.
- `internal/cmd/controlplane/agents.go` rerouted through `f.AdminClient(ctx).ListAgents` — no more local sqlite read from the CLI.

### Package consolidation: agentdial + agentregistry → agent
- `internal/controlplane/agentdial/` and `internal/controlplane/agentregistry/` directories deleted; their contents moved into `internal/controlplane/agent/`.
- Standalone exports collapsed into a single umbrella:
  - `agentregistry.Reap` (gone) → internal `reapOrphans` inside `agent.Start`
  - `agentregistry.Subscribe` (gone) → internal `subscribeEvict` inside `agent.Start`
  - `agentdial.Subscribe` (gone) → internal `subscribeDial` inside `agent.Start`
- `cmd/clawker-cp/main.go` Step 8 shrinks from four wiring calls to one `agent.Start(ctx, agent.StartDeps{Registry, DockerLister, Dialer, Bus, Log})`.
- moq mocks generated as `registry_mock_test.go` (test-only file in the agent package itself) to break the import cycle that prevented an `agent/mocks` subpackage from working.

### Unified worldview: State.Agents
- `overseer.State.AgentSessions` removed. `SessionView` type deleted.
- New `overseer.Agent` struct holds session lifecycle (`SessionStatus`, `Address`, `Attempts`, `LastError`) + identity (`AgentName`, `Project`, `Thumbprint`) + trust state (`Registered`, `Trusted`, `UntrustedReason`).
- `State.Agents map[string]Agent` is the single per-agent map.
- `agentdial.Provenance` struct + `RegistryOutcome` enum dropped entirely. `SessionConnected` carries flat `PeerCN`+`PeerThumbprint` fields instead.

### New typed events
- `AgentRegistered{ContainerID, AgentName, Project, Ok bool, Reason string, At time.Time}` — fires only when a Register handshake actually runs (success or failure). Steady-state "row already exists" does NOT fire this.
- `AgentUntrusted{ContainerID, AgentName, Project, Reason UntrustedReason, Detail, At}` — fires on any non-trusted classification at Hello time or post-register failure.
- `UntrustedReason` enum constants on the overseer package: `cert_thumbprint_mismatch`, `cert_container_id_mismatch`, `cert_invalid`, `cert_cn_mismatch`, `peer_ip_mismatch`, `register_failed`.

### Dialer Register dispatch
- `fillRegistryProvenance` replaced with `classifyRegistry` returning a typed local outcome (`outcomeRegistryMatch` / `Miss` / `ThumbprintMismatch` / `CNMismatch` / `NotQueried`).
- `tryEstablish` returns `peerInfo` (PeerCN/PeerThumbprint/ChainVerified/CaptureReason) instead of `Provenance`.
- After Hello + `publishConnected`, the dialer calls `dispatchAgentEvents`:
  - Match → no extra event
  - Miss → `driveRegister`: send `Command{RegisterRequired{}}`, wait `Response{RegisterDone}` with 30s timeout, re-lookup row, publish `AgentRegistered{ok}` (+ `AgentUntrusted{ReasonRegisterFailed}` on failure)
  - ThumbprintMismatch → `AgentUntrusted{ReasonThumbprintMismatch}`
  - CNMismatch → `AgentUntrusted{ReasonCNMismatch}`
  - NotQueried (lookup error) → `AgentUntrusted{ReasonCertInvalid, Detail: <err>}`
- Stream stays open in all cases — asymmetric trust (CP must always be reachable) preserved.

### Register handler
- `internal/controlplane/agent/register_handler.go`: `Handler{registry, inspector, log, clock}` with `agentv1.UnimplementedAgentServiceServer` embedded.
- Steps: validate identity → capture peer cert + IP via `peer.FromContext` (using `credentials.TLSInfo`) → constant-time CN compare → read container_id from cert URI SAN → `inspector.Inspect(containerID)` → label cross-check (`dev.clawker.agent`/`dev.clawker.project`) → peer-IP-vs-clawker-net-IP via `netip.Addr.Unmap()` → idempotent retry: existing row + matching thumbprint = silent Welcome, different thumbprint = PermissionDenied → `registry.Add`.
- All rejection paths return `codes.PermissionDenied` with a generic envelope; the structured log line carries the specific failure classification.
- `ContainerInspector` interface + `NewMobyContainerInspector(moby APIClient)` adapter live in the same file.

### Method scopes + interceptor opt-out
- `AgentMethodScopes()` now maps `/clawker.agent.v1.AgentService/Register` → `consts.ScopeAgentSelfRegister` (already in `internal/consts`).
- `IdentityOptedOutMethods()` now contains the Register method path. Justification: the registry row keyed by peer thumbprint doesn't exist yet at Register time — the call's purpose is to CREATE it. The handler does its own cert + IP + label cross-checks, so opt-out doesn't strip security; it relocates the gate from interceptor to handler.

### clawkerd outbound dial restored
- `cmd/clawkerd/register.go` (new): `registerCoordinator{boot, hydraURL, agentAddr, agentName, project}` does Hydra `client_assertion` exchange (single-use), mTLS-dial to CP `AgentPort` with `bearerCreds` (PerRPCCredentials covers unary + future streaming), `AgentService.Register` call. Coordinator records the outcome and short-circuits on subsequent triggers (assertion is single-use; replay would always fail at Hydra).
- `cmd/clawkerd/main.go`: constructs the coordinator at boot, threads it through `startClawkerdListener` → `clawkerdServer{register}` → `runSession(stream, log, register)` → `session{register}`.
- `cmd/clawkerd/session.go` `dispatch`: new `case *clawkerdv1.Command_RegisterRequired` calls `s.handleRegisterRequired(ctx, commandID)`, which spawns a goroutine running `s.register.Run(ctx, log)` and replies with `Response{RegisterDone{ok, error}}`.

### CP-startup hydration
NOT a separate step. Existing infrastructure handles it:
- `dockerevents.Feeder.reconcile()` already lists running purpose=agent containers at startup and publishes synthetic start envelopes.
- `agent.Start`'s dial-path subscription picks them up and calls `dialer.DialAgent` for each. State.Agents fills in organically as Sessions establish.
- Reap-orphan-rows runs FIRST inside `agent.Start` — sweeps registry against the all=true docker list, evicts rows whose container_id is gone.

## Files

### Created
- `internal/controlplane/agent/start.go` — single `agent.Start` entry point (reap + 2 subscriptions + shared panic-loop guardrails)
- `internal/controlplane/agent/events_session.go` — moved+rewritten from agentdial/events.go (Provenance gone, PeerCN/PeerThumbprint flat)
- `internal/controlplane/agent/events_agent.go` — `AgentRegistered`, `AgentUntrusted`, `UntrustedReason` constants ApplyTo State.Agents
- `internal/controlplane/agent/register_handler.go` — Register RPC handler + ContainerInspector seam
- `internal/controlplane/agent/register_handler_test.go` — table-driven tests (happy / CN mismatch / no SAN / label mismatch / peer-IP mismatch / idempotent retry / thumbprint replay)
- `internal/controlplane/agent/registry_mock_test.go` — moq-generated mock as test-only file in the package itself
- `cmd/clawkerd/register.go` — `registerCoordinator` + Hydra exchange + bearerCreds

### Moved (from agentdial/agentregistry into agent/)
- `dialer.go`, `dialer_test.go`, `events.go`→`events_session.go` (rewritten)
- `registry.go`, `registry_sqlite.go`, `registry_test.go`, etc.

### Deleted
- `internal/controlplane/agentdial/` and `internal/controlplane/agentregistry/` directories
- `internal/cmd/container/shared/agent_bootstrap.go`: `RegisterAgentInRegistry`, PKCE-related fields/functions
- `consts.BootstrapVerifierFile`, `consts.ChallengeMethod`, `consts.ChallengeMethodS256`

### Modified
- `internal/auth/agent_cert.go` — container_id SAN
- `internal/cmd/container/shared/agent_bootstrap.go` + `_test.go` — drop PKCE, drop registry write
- `internal/cmd/container/shared/container_create.go` — drop registry-write callsite
- `internal/cmd/controlplane/agents.go` + `_test.go` — AdminClient.ListAgents path
- `internal/controlplane/cpboot/bootstrap.go` — remove EnsureSchema host-side call
- `internal/controlplane/server.go` — registry interface name change (agentregistry.Registry → agent.Registry)
- `internal/controlplane/agent_method_scopes.go` — Register scope mapping
- `internal/controlplane/agent/identity_interceptor.go` — Register opt-out
- `internal/controlplane/overseer/state.go` — Agent struct, State.Agents map
- `cmd/clawker-cp/main.go` — schema apply + agent.Start + Register handler wiring
- `cmd/clawkerd/main.go`, `listener.go`, `session.go` — registerCoordinator threading + RegisterRequired handler + drop verifier read

## Out of scope (deferred to follow-up)
- entrypoint.sh refactor — stays as-is. Migration of init steps (config seed, git, ssh, post-init, ready) to CP-dispatched Session commands is the next branch.
- ContainerInit / SetupGit / SetupSSH / Ready Command vocabulary — not added in this commit.
- Cert rotation flow — explicit destroy+recreate is the only supported path.
- AgentUntrusted consumer (containment / alerting) — event surface lands now; consumers in follow-ups.

## Verification status
- `go build ./...` clean
- `go vet ./...` clean
- `go test ./... -timeout 180s` (excluding `test/e2e`/`test/whail`) — all packages pass
- `make test` cannot run inside a clawker container (no docker socket); host-side run pending
- Manual flow per plan §verification step 3 (`make restart` + `clawker monitor up` + `clawker run --rm --agent test`) — pending host execution

## Branch
`feat/clawkerd-commands` — this commit lands the foundation. The branch's
remaining scope (per the user's framing of the branch name) is the
container-init Command vocabulary that replaces entrypoint.sh's setup
steps; that work plugs into the AgentRegistered event surface this
commit ships.
