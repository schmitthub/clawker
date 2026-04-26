# clawkerd End-to-End CLI Integration + Agent Lifetime Channel

**Branch:** `feat/clawkerd-init`

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Proto — rename `Register` → `Connect` (server-streaming), stub `Events` RPC | `complete` | claude-opus-4-7 |
| Task 2: `agentslots` — composite key + `EvictByContainerID` + dockerevents `Subscribe` | `complete` | claude-opus-4-7 |
| Task 3: `agent.Handler.Connect` — server-streaming + CN cross-check + composite Consume | `complete` | claude-opus-4-7 |
| Task 4: `controlplane.adminServer.AnnounceAgent` handler | `complete` | claude-opus-4-7 |
| Task 5: `AgentIdentityInterceptor` (unary + stream) with fail-secure opt-out map | `complete` | claude-opus-4-7 |
| Task 6: `cmd/clawker-cp/main.go` wiring (slot registry hoist, identity interceptor chain, dockerevents subscriptions) | `complete` | claude-opus-4-7 |
| Task 7: `cmd/clawkerd/main.go` — consume `Connect` server-stream | `complete` | claude-opus-4-7 |
| Task 8: CLI `run`/`start` wiring — `RuntimeEnvOpts` Clawkerd* fields + `prepareAgentBootstrap` helper | `pending` | — |
| Task 9: Documentation pass — package CLAUDE.md files + KEY-CONCEPTS + status memo | `pending` | — |

## Key Learnings

### Task 1
- `make proto` is the regen target. Running it the first time installs the pinned `buf` toolchain (v1.47.2) on demand. The build files (`agent.pb.go`, `agent_grpc.pb.go`) are tracked, not gitignored.
- `AgentService_ServiceDesc` for streaming RPCs lives under `desc.Streams`, NOT `desc.Methods`. The existing scope-walk test (`TestAgentMethodScopes_CoversAllRPCs`) already iterates both buckets — no change needed there. But any test that walks ONLY one bucket needs updating when streaming RPCs land.
- `consts.ScopeAgentSelfRegister` doc was stale post-rename — falls into Task 1's cascade since no later task touches `internal/consts/`.
- The agent OAuth2 client in Hydra is registered with a single scope (`agent:self:register`). Adding new agent RPCs with new scopes requires updating Hydra registration in lockstep — for now, both `Connect` and `Events` reuse the existing scope.
- Silent-failure-hunter caught: a per-entry-loop test on a map silently passes if the map is empty. `require.NotEmpty` before the loop closes the gap. Pattern worth applying anywhere a test "asserts every entry has property X."
- Streaming-direction (`ServerStreams` / `ClientStreams` flags on `grpc.StreamDesc`) is a load-bearing protocol invariant that NO test pins by default. The proto descriptor walks treat all streams interchangeably. Pin direction explicitly in proto_structure_test when streaming RPCs are added.

### Task 2
- `Reserve` must panic on zero `ExpectedCertThumbprint` (and empty `Challenge`) — mirrors `agentregistry.Add`'s programming-error-loud posture. Without this guard, a buggy AnnounceAgent caller files all slots under the all-zero key, silently breaking the "fresh cert per retry → no collision" invariant. `subtle.ConstantTimeCompare("", "")` would also trivially pass on an empty challenge.
- ErrSlotExists framing: avoid "SHA-256 collision" language. For an honest CLI, the bootstrap mints fresh cert per AnnounceAgent — duplicates indicate caller misuse, not a cryptographic event. Surface as `codes.AlreadyExists` and DO NOT retry under a fresh challenge (the existing slot is still consumable).
- Composite slot key folds the agent_name cross-check into the lookup. Caller cannot reuse a slot reserved for a different agent_name even with a stolen verifier.
- Subscribe panic-recovery rationale here is weaker than agentregistry's — `agentslots` has a TTL janitor that bounds the leak floor. The integration is still load-bearing because dockerevents-driven eviction is what makes both registries identically driven by the same deltas.
- subscribe.go is a deliberate mirror of agentregistry/subscribe.go — both packages have package-local `drainOnce`, `handleDelta`, `panicOnceRegistry` helpers. This is intentional duplication; do NOT collapse into a shared helper without sharper interface analysis.
- moq regen is straight `go generate ./...` from the package directory; it picks up Consume signature changes and new EvictByContainerID method automatically.

### Task 3
- Send Welcome BEFORE registry.Add — eliminates the "registry has orphan entry but agent never received Welcome" partial-failure window. The original initiative ordering had Add before Send; T3 reordered after silent-failure-hunter caught the gap. Send-failure path also wraps in `status.Error(codes.Unavailable, ...)` so the wire code matches the rest of the handler's discipline (no leaked fmt-string -> codes.Unknown).
- `peerCertAndIP` was renamed to `peerIdentityAndIP` and returns `*peerIdentity{Raw, CommonName}` — a narrow projection of the trusted cert fields. Returning `*x509.Certificate` would expose ~24 fields and weaken the "we trust only Raw and CN" invariant from a compile-time guarantee to a doc-comment claim. The narrow type also forces deliberate review when a future RPC needs a third trusted field.
- Streaming-handler test pattern: `connectStreamFake` embeds `grpc.ServerStream` (nil interface) so any drift beyond Context+Send panics loudly. Optional `sendErr` for the failure path; `welcomed chan struct{}` closed on first Send eliminates busy-wait sync. `runConnect` goroutine wrapper has a 2s deadline guard so a regression in the idle-on-ctx.Done path fails fast instead of hanging.
- `context.Canceled` / `DeadlineExceeded` from Docker Inspect during Connect is "client disconnect mid-handshake" not a Docker fault — log at Debug to avoid log noise that misleads operators chasing phantom Docker outages.
- mTLS test (`TestMTLSAgent_ValidCLIClientCert_HandshakeSucceeds`) now relies on the new CN-mismatch check to produce post-handshake PermissionDenied (CLI cert CN is "clawker-cli", request body says "clawker.unregistered-agent"). The Unavailable-vs-PermissionDenied discrimination is the load-bearing assertion.

### Task 4
- `hex.DecodeString` is case-insensitive but the proto contract for `expected_cert_thumbprint` is lowercase. Enforce explicitly with `strings.ToLower(s) != s` so two equally-valid CLI builds can't disagree on case and so a future strict comparator elsewhere doesn't silently break the tolerant branch.
- `slots` is a required dependency at `NewAdminServer` (panic on nil). Mirrors `agent.NewHandler`'s panic-on-nil-deps discipline. The runtime `codes.Internal` fallback was unreachable in production (cmd/clawker-cp always constructs slotRegistry) and obscured wiring regressions as opaque per-request errors instead of startup panics. `agents` stays nil-tolerant for `ListAgents` because the partial-build use case is real for that path.
- AdminService.AnnounceAgent's wire-error mapping: InvalidArgument (validation), AlreadyExists (`ErrSlotExists`), Internal (other Reserve failures). Caller-supplied `ReservedAt`/`ExpiresAt` on the Slot are silently overwritten by `agentslots.Reserve` from its own clock — handler doesn't pre-stamp them.
- The handler's `clock` produces `ExpiresAtUnix` for the response (CLI uses for logging only); `agentslots.Reserve` uses its own clock for the slot's actual `ExpiresAt`. In production both are `time.Now`; in tests the two clocks diverge by design (handler test verifies wire response, agentslots test verifies slot stamping).
- Slot registry hoist in `cmd/clawker-cp/main.go`: hoisted above `NewAdminServer` to share across both AdminService and AgentService listeners. T6 adds the dockerevents subscription.

### Task 5
- Stream wrapper pitfall: `identityServerStream.Context()` MUST be defined on the wrapper, NOT promoted from the embedded `grpc.ServerStream`. Promotion lets the embedded type's Context() win — handler reads original ctx without entry, silently breaking identity binding for every streaming RPC. Test reads `wrapped.Context()` (not resolve-time newCtx) so the regression manifests as a test failure, not silent.
- `WithEntry(nil)` is a real silent-identity-vacuum path: typed-nil pointer survives `(*Entry)(nil)` type assertion as `(nil, true)`. Mirror agentregistry.Add's panic-on-misuse + defend EntryFromContext with `ok && entry != nil`.
- `TestIdentityInterceptor_AllMethodsHavePolicy` was tautological w.r.t. its docstring claim — the implicit identity-required default routes new RPCs through registry-lookup automatically; the test cannot fail when a new RPC is added. Renamed to `TestIdentityOptedOut_NoStaleEntriesAndConnectLocked` and tightened docstring to reflect what the test actually enforces (stale-entry detection + Connect lock-in). Code-review conversation is the real forcing function for new opt-outs, not this test.
- `optedOut nil` defaults to empty map → identity-required for ALL methods. Worst-case wiring regression is fail-secure (every RPC requires identity), not fail-open.
- Lookup-error log differentiation: `errors.Is(err, ErrUnknownAgent)` logs at Warn; other errors log at Error (with the wrapped error). Wire response stays generic PermissionDenied — attackers learn nothing — but operator log fidelity is preserved for future Lookup contracts that may grow I/O paths.
- Local var `peer` in resolve closure renamed to `pid` to avoid confusion with the imported `peer` package (which the same file uses for `peer.FromContext` via `peerIdentityAndIP`).

### Task 6
- gRPC's `ChainUnaryInterceptor(a, b)` runs interceptors in declaration order: a is outer (runs first), b is inner. So `(agentInterceptor.UnaryInterceptor(), identityUnary)` correctly puts auth first → identity second. Identity sees only requests that already passed token + scope.
- `agentslots.Subscribe` runs through the same `watcherCtx` as `agentregistry.Subscribe` so drain-to-zero tears both down identically. Defer order: subscriber cancel funcs run before `slotRegistry.Stop()` (LIFO unwind), so eviction goroutines drain before the TTL janitor closes its stop channel.
- The slot subscribe rationale: TTL janitor is the floor, but immediate dockerevents-driven eviction prevents `ErrSlotExists` collisions on quick re-announce after a failed ContainerStart.
- T7 still needs to land before tree-wide `go build ./...` is clean — `cmd/clawkerd/main.go` calls `agentClient.Register` which the T1 proto rename broke. T6's acceptance only requires `./cmd/clawker-cp/...` and `./internal/controlplane/...` to be clean.

### Task 7
- gRPC unary client interceptors do NOT cover streaming RPCs. `bearerInterceptor` (the previous unary interceptor) silently skipped Connect (server-streaming) and would have caused codes.Unauthenticated rejection at the CP's AuthInterceptor before clawkerd ever saw Welcome. Switched to `credentials.PerRPCCredentials` via `WithPerRPCCredentials` — single hook covers both unary AND streaming, future-proofs Events.
- Welcome-before-delete-verifier is the load-bearing ordering: Recv first, type-assert Command_Welcome, only then `os.Remove(verifier)`. A regression where delete moves before Recv would burn the verifier on every transient connection failure.
- `welcomeTimeout` (30s) bounds ONLY the wait for the first Recv, not the stream's lifetime. After Welcome the loop drains on the parent ctx so SIGTERM tears down cleanly. `recvWithCtx` helper races welcomeCtx against gRPC's separate per-RPC ctx.
- Goroutine in `recvWithCtx` is single-shot-on-error-path: it parks in `stream.Recv()` after ctx.Cancel until the deferred `conn.Close()` errors the stream out. Buffered channel (cap 1) lets the goroutine send-and-exit cleanly even if the caller already returned. Not reusable safely without conn cleanup.
- SIGTERM-during-handshake exits zero (clean teardown), not 1 (crash). Mirrors the post-Welcome loop's `errors.Is(ctx.Err(), context.Canceled)` discipline so a `restart: on-failure` policy doesn't retrigger on shutdown.

---

## Context Window Management

**After completing each task, run the gates and continue to the next task automatically.** No user handoff is required. For each task:

1. Run acceptance criteria for the completed task. All tests must pass; all build/vet checks must be clean.
2. Update the Progress Tracker in this memory: task status → `complete`.
3. Append any non-obvious key learnings to the Key Learnings section (only what's useful for future sessions; don't narrate routine work).
4. Run the subagent review pipeline against the task's diff:
   - `code-reviewer`
   - `silent-failure-hunter`
   - `test-hunter`
   - `code-simplifier`
   - `comment-analyzer`
   - `type-design-analyzer`

   Each subagent invocation MUST include the directive: "Read `~/.claude/CLAUDE.md` and follow its required tooling and behavioral guidelines (Serena MCP, simplicity, surgical changes, no speculative scope)."
5. Fix any and all findings from the subagent reviews. Re-run acceptance criteria after fixes. Repeat reviews if a fix introduces new code surface.
6. Commit the task's changes with a descriptive conventional-commits message. Use the project's HEREDOC pattern with the Co-Authored-By footer.
7. Proceed immediately to the next pending task. Do not pause for user approval.

When all 9 tasks are complete, run the final integration check (`make test`, `go build ./...`, `go vet ./...`, `go vet ./test/e2e/...`) and report completion to the user with a summary of what landed.

---

## Context for All Agents

### Background

Branch 4 (clawkerd auth) landed the auth foundation: `AdminService.AnnounceAgent` proto, `AgentService.Register` proto + handler with five identity-binding cross-checks, `agentslots` registry, `agentregistry` keyed by cert thumbprint with dockerevents-driven eviction, CLI bootstrap helpers (`shared.GenerateAgentBootstrap` / `AnnounceAgent` / `WriteAgentBootstrapToContainer`), CP agent listener with mTLS + per-listener `AuthInterceptor`, `cmd/clawkerd` daemon binary, embed-and-launch via the bundler.

**What's broken end-to-end today:**

- `AdminService.AnnounceAgent` falls through to `UnimplementedAdminServiceServer` — every CLI announce attempt fails with `codes.Unimplemented`.
- `run`/`start` never call `shared.GenerateAgentBootstrap` / `AnnounceAgent` / `WriteAgentBootstrapToContainer`. `RuntimeEnvOpts.ClawkerdHydraURL` / `AgentAddr` / `AgentName` are unpopulated.
- Net effect: every clawker-managed container boots without `/run/clawker/bootstrap`. Entrypoint's `[ -d /run/clawker/bootstrap ]` gate skips clawkerd. Agent never registers. `clawker controlplane agents` always reports empty.

**What this initiative ships:**

A working end-to-end happy path PLUS architectural decisions that grow into the long-term concept rather than serving only this branch.

### Architectural decisions (made during planning)

1. **Single-server topology.** clawkerd is gRPC client only; CP serves. Eliminates the POC's two-server pattern (clawkerd-side `AgentCommandService`, CP dials back via Docker inspect). Single TCP connection per stream RPC, all clawkerd-initiated. POC's K8s-flavored choice was overkill for clawker's actual needs.

2. **`Register` → `Connect`, server-streaming.** The connection IS the agent's lifetime command channel. clawkerd sends one `ConnectRequest` (auth handshake material); CP server-streams `Command` messages forever. First message after auth is `Welcome` (carries `ClawkerdConfiguration` placeholder); subsequent messages are commands (B5+ adds payload variants). `Register` was a misnomer once the RPC became long-lived.

3. **Composite slot key (thumbprint + agent_name).** Replaces AgentName-only key. Each retry mints fresh cert → fresh thumbprint → fresh slot key → no collision. agent_name cross-check folds into the lookup itself. Cert thumbprint = 256-bit random keyspace, collision-resistant.

4. **`agentslots.EvictByContainerID` + dockerevents `Subscribe`.** Mirrors the existing `agentregistry` pattern. Slot eviction is real-time on container death, not just TTL-bounded.

5. **CN cross-check at Connect.** `auth.MintAgentCert` sets cert CN to `agentName`. The handler verifies `peerCert.Subject.CommonName == req.AgentName` constant-time. Defense vs announce-payload tampering between mint and announce.

6. **Identity resolution via interceptor (fail-secure opt-out).** `AgentIdentityInterceptor` (unary + stream forms) runs after `AuthInterceptor` on the agent listener. Resolves cert thumbprint → registry entry → ctx-attached `*agentregistry.Entry`. Default REQUIRES identity; explicit opt-out only for bootstrap RPCs that authenticate themselves (`Connect` via slot consume). Build-time test walks proto descriptor.

7. **CP ≠ firewall.** Bootstrap delivery is unconditional. NOT gated on `security.firewall.enable`. CP is unconditional infrastructure; firewall is one optional subsystem CP manages. (See project-root `CLAUDE.md` "CP ≠ firewall" callout.)

8. **`ConnectRequest.code_verifier` semantics preserve future reconnect path.** Empty verifier reserved for the future reconnect flow (CP restart resilience initiative — see `cp-initiative-cp-restart-resilience` memo). Today's handler still requires verifier on first-connect; future patch will branch on registry-already-has-thumbprint.

### Key files

| File | Why it matters |
|------|----------------|
| `api/agent/v1/agent.proto` | RPC definition rename + streaming change (Task 1) |
| `internal/controlplane/agent/handler.go` | Connect handler (Task 3) and home for `IdentityInterceptor` (Task 5) |
| `internal/controlplane/agentslots/registry.go` | Composite key refactor (Task 2) |
| `internal/controlplane/agentslots/subscribe.go` (NEW) | dockerevents subscription mirror (Task 2) |
| `internal/controlplane/server.go` | `adminServer.AnnounceAgent` (Task 4); `NewAdminServer` signature change |
| `cmd/clawker-cp/main.go` | Wiring all of the above (Task 6) |
| `cmd/clawkerd/main.go` | Stream consumer (Task 7) |
| `internal/cmd/container/shared/container_create.go` | `buildCreateTimeEnv` populates Clawkerd* env vars (Task 8) |
| `internal/cmd/container/shared/container_start.go` | `prepareAgentBootstrap` helper (Task 8) + `CommandOpts.AgentName` field |
| `internal/cmd/container/shared/agent_bootstrap.go` | Existing helpers — read these for context, don't modify |

### Design patterns (this codebase)

- **Factory DI:** `cmdutil.Factory` is a struct with closure fields (no methods). Commands cherry-pick closures into per-command Options structs. Run functions accept `*Options` only. Constructor in `internal/cmd/factory/`.
- **Function-field fakes:** `whail.FakeAPIClient`, `docker.FakeClient`, `configmocks.ConfigMock` (moq-generated). NOT gomock. Configure by setting `FooFn` fields.
- **Composite fakes:** `docker.FakeClient` wraps `whailtest.FakeAPIClient`. `agentslots/mocks/` and `agentregistry/mocks/` are moq-generated for the Registry interfaces.
- **`testenv` package:** isolated XDG dirs for tests requiring real filesystem. `testenv.New(t)` + `WithConfig()` + `WithProjectManager(gf)`.
- **Test helpers:** `configmocks.NewBlankConfig()`, `NewFromString(yaml)`, `NewIsolatedTestConfig(t)`. Import as `configmocks`.
- **gRPC server-streaming handler signature:** `func (h *Handler) Foo(req *FooRequest, stream FooServer) error`. Stream context is the request lifetime; idle on `<-stream.Context().Done()` to hold the stream open.
- **Constant-time compares:** `subtle.ConstantTimeCompare([]byte(a), []byte(b)) != 1` for any auth-related string match (CN, thumbprint, verifier).
- **Fail-secure data-driven policy maps:** see `AgentMethodScopes()` in `internal/controlplane/agent_method_scopes.go`. Mirror the pattern for `IdentityOptedOutMethods()` (Task 5).

### Rules — read these before starting

- Project root `CLAUDE.md` (mantra, workflow, "CP ≠ firewall" callout, design decisions table)
- `~/.claude/CLAUDE.md` (global behavioral guidelines — required tooling, simplicity-first, surgical changes, goal-driven execution)
- `.claude/rules/code-style.md`
- `.claude/rules/testing.md` (if present)
- `.claude/rules/container-commands.md` (relevant for Task 8)
- `internal/controlplane/agent/CLAUDE.md` (existing handler doc)
- `internal/controlplane/agentslots/CLAUDE.md` (existing registry doc)
- `internal/controlplane/agentregistry/CLAUDE.md` (read for the `Subscribe` pattern Task 2 mirrors)
- `internal/cmd/container/shared/CLAUDE.md` (CreateContainer, ContainerStart entry points)

### Required tooling

- **Serena** for code exploration. Run `mcp__serena__check_onboarding_performed` and `mcp__serena__list_memories` first. Use `find_symbol`, `get_symbols_overview`, `search_for_pattern`, `find_referencing_symbols` for navigation. Read symbol bodies only when needed. Do NOT read whole files when symbolic tools suffice.
- **deepwiki** / **Context7** when you need external library documentation.

### Cross-references

- Out-of-scope follow-up: `cp-initiative-cp-restart-resilience` — registry persistence, Connect reconnect path, clawkerd reconnect-with-backoff, `volume prune` safety, `controlplane down` safety, streaming RPC eviction broadcast. The proto comment on `ConnectRequest.code_verifier` (Task 1) MUST preserve this seam.

---

## Task 1: Proto — rename `Register` → `Connect` (server-streaming), stub `Events` RPC

**Creates/modifies:** `api/agent/v1/agent.proto`, regenerated `agent.pb.go` + `agent_grpc.pb.go`. Cascading callsite changes wherever `agentv1.AgentServiceClient.Register` / `agentv1.AgentService_RegisterServer` / etc. are referenced — these will mostly fall out as build failures fixed in subsequent tasks.

**Depends on:** none (foundational)

### Implementation Phase

1. Replace the existing `service AgentService` and message definitions in `api/agent/v1/agent.proto` with:

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
     // Welcome carries the ClawkerdConfiguration payload (B5 fills in OTEL
     // endpoint, file logging, project/agent context). Empty placeholder
     // in this branch.
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

2. Regenerate `agent.pb.go` and `agent_grpc.pb.go` via the project's existing buf workflow (check `buf.gen.yaml` and the `make proto` target if one exists, or run `buf generate` directly).

3. Address the cascading build failures in this task ONLY for files that won't be touched in later tasks. The handler (`internal/controlplane/agent/handler.go`), clawkerd (`cmd/clawkerd/main.go`), wiring (`cmd/clawker-cp/main.go`), and the descriptor-walking tests are all addressed in their respective tasks. For now, only update files that import the generated types but aren't on any later task's edit path (e.g., shared test helpers, mocks generated by moq, or any unrelated callsites). Run `go build ./...` and inspect failures; fix only those that aren't claimed by another task.

### Acceptance Criteria

```bash
# Proto regeneration succeeded and committed
git diff --stat api/agent/v1/agent.proto api/agent/v1/agent.pb.go api/agent/v1/agent_grpc.pb.go

# Generated code compiles (other tasks' files may still fail to build at this point — that's expected)
go build ./api/...

# Proto descriptor is well-formed
go vet ./api/...
```

Note: `go build ./...` will fail until later tasks update the handler and clawkerd. That's acceptable for this task's gate as long as the proto + generated code are clean and `./api/...` builds.

### Wrap Up

1. Update Progress Tracker: Task 1 → `complete`.
2. Append any key learnings (e.g., proto regeneration command if non-obvious).
3. Run the subagent review pipeline (`code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`, `type-design-analyzer`) against this task's diff. Each subagent prompt MUST include: "Read `~/.claude/CLAUDE.md` and follow its required tooling and behavioral guidelines."
4. Fix all findings.
5. Commit with a message like `feat(agent/v1): rename Register → Connect (server-streaming) + stub Events`.
6. Proceed to Task 2.

---

## Task 2: `agentslots` — composite key + `EvictByContainerID` + dockerevents `Subscribe`

**Creates/modifies:** `internal/controlplane/agentslots/registry.go` (modify), `internal/controlplane/agentslots/registry_test.go` (modify), `internal/controlplane/agentslots/subscribe.go` (NEW), `internal/controlplane/agentslots/subscribe_test.go` (NEW), `internal/controlplane/agentslots/mocks/registry_mock.go` (regenerate via `go generate`).

**Depends on:** none (foundational, can run in parallel with Task 1 conceptually but ordered after it for ease)

### Implementation Phase

1. **Composite key refactor.** Change `registryImpl.slots` map type from `map[string]Slot` to `map[slotKey]Slot` where `slotKey` is an unexported struct:

   ```go
   type slotKey struct {
       Thumbprint [sha256.Size]byte
       AgentName  string
   }
   ```

2. **`Reserve` collision check** uses composite key: `r.slots[slotKey{slot.ExpectedCertThumbprint, slot.AgentName}]`.

3. **`Consume` signature changes** from `Consume(agentName, verifier string)` to `Consume(thumbprint [sha256.Size]byte, agentName, verifier string)`. Update the constant-time PKCE compare path. The verifier-hashed-before-lookup invariant from the existing handler stays — preserve it.

4. **`ErrSlotExists` doc comment** updates: "Composite collision (same thumbprint AND same agent_name) is effectively impossible — would require SHA-256 collision. Treated as fatal misuse, not benign retry."

5. **Add `EvictByContainerID(containerID string)` method** to the `Registry` interface and impl. Linear scan over slots, delete any whose `ContainerID == containerID`. Call out in the comment that linear scan is fine for realistic clawker host scales (single-digit slots).

6. **New file `subscribe.go`** mirrors `internal/controlplane/agentregistry/subscribe.go`:
   - `func Subscribe(ctx context.Context, reg Registry, inf informer.Interface, log *logger.Logger) func()`
   - Same recover-and-resume goroutine pattern.
   - Same delta types: `DeltaRemoved` → `EvictByContainerID(d.After.ID || d.Before.ID)`; `DeltaUpdated` with `Lifecycle == LifecycleStopped` → `EvictByContainerID(d.After.ID)`.
   - Returns a cancel func that drains.

7. **New file `subscribe_test.go`** mirrors `agentregistry/subscribe_test.go`:
   - Live informer (NOT mocked — replaces the very integration the test asserts).
   - Test that DeltaRemoved evicts.
   - Test that DeltaUpdated{Stopped} evicts.
   - Test that hook panic is recovered (analog of `TestSubscribe_RecoversFromHookPanic`).

8. **Update `registry_test.go`** for the new Consume signature. Existing cases (composite key collision returns ErrSlotExists; wrong-verifier preserves slot; TTL janitor; sync.Once Stop) all stay valid — just retarget the Consume call sites.

9. **Add a new test case** for `EvictByContainerID` that reserves multiple slots, evicts by one container_id, and verifies only matching slots are removed.

10. **Regenerate the mock:** `cd internal/controlplane/agentslots && go generate ./...`.

### Acceptance Criteria

```bash
# Package builds + vets clean
go build ./internal/controlplane/agentslots/...
go vet ./internal/controlplane/agentslots/...

# Unit tests pass (this package only — handler tests will fail until Task 3)
go test ./internal/controlplane/agentslots/... -count=1

# Mock regenerated and committed
git diff --stat internal/controlplane/agentslots/mocks/
```

### Wrap Up

1. Progress Tracker: Task 2 → `complete`.
2. Append key learnings.
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `refactor(agentslots): composite key + EvictByContainerID + dockerevents Subscribe`.
5. Proceed to Task 3.

---

## Task 3: `agent.Handler.Connect` — server-streaming + CN cross-check + composite Consume

**Creates/modifies:** `internal/controlplane/agent/handler.go`, `internal/controlplane/agent/handler_test.go`.

**Depends on:** Task 1 (proto types), Task 2 (composite Consume signature).

### Implementation Phase

1. **Rename method** `(*Handler).Register` → `(*Handler).Connect`.

2. **New signature** for server-streaming:

   ```go
   func (h *Handler) Connect(req *agentv1.ConnectRequest, stream agentv1.AgentService_ConnectServer) error {
       if req == nil || req.AgentName == "" || req.CodeVerifier == "" {
           return status.Error(codes.InvalidArgument, "agent_name and code_verifier required")
       }

       ctx := stream.Context()
       peerCert, peerIP, err := peerCertAndIP(ctx)
       if err != nil {
           h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent connect: missing peer auth info")
           return status.Error(codes.PermissionDenied, "registration rejected")
       }
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

       // (b) Composite slot consume — implicit thumbprint + agent_name match.
       slot, err := h.slots.Consume(thumbprint, req.AgentName, req.CodeVerifier)
       if err != nil {
           h.log.Warn().Err(err).Str("agent", req.AgentName).Msg("agent connect: slot consume rejected")
           return status.Error(codes.PermissionDenied, "registration rejected")
       }

       // (c)/(d)/(e) Docker cross-checks — UNCHANGED from prior implementation.
       info, err := h.docker.Inspect(ctx, slot.ContainerID)
       if err != nil { /* same as before — distinguish errMissingNetworkSettings */ }
       if info.NetworkIP == nil || !info.NetworkIP.Equal(peerIP) { /* same as before */ }
       if got := info.Labels[consts.LabelAgent]; !strings.EqualFold(got, req.AgentName) { /* same as before */ }

       // Pin to registry.
       now := h.clock()
       h.registry.Add(agentregistry.Entry{
           AgentName:    req.AgentName,
           ContainerID:  slot.ContainerID,
           Thumbprint:   thumbprint,
           RegisteredAt: now,
           LastSeen:     now,
       })

       // Send Welcome — first message on the command stream after auth.
       if err := stream.Send(&agentv1.Command{
           Payload: &agentv1.Command_Welcome{
               Welcome: &agentv1.Welcome{Config: &agentv1.ClawkerdConfiguration{}},
           },
       }); err != nil {
           return fmt.Errorf("send welcome: %w", err)
       }

       h.log.Info().
           Str("agent", req.AgentName).
           Str("container_id", slot.ContainerID).
           Msg("agent connect: registered")

       // Idle on stream — wait for ctx cancellation (eviction or client disconnect).
       // B5+ replaces this block with a select on a per-agent command queue.
       <-ctx.Done()
       return nil
   }
   ```

3. **Drop the standalone thumbprint check** that was previously check (b) — it's now implicit in the composite Consume lookup.

4. **Update `handler_test.go`** to use the streaming server harness. Reference any existing in-tree streaming-server test (if there is one) for the pattern; otherwise use a minimal `bufconn` listener + real `grpc.Server` to drive the handler. Update each existing case:
   - Happy path: Connect succeeds → Welcome received → handler idles until ctx cancellation.
   - Missing fields → InvalidArgument (no stream open).
   - **NEW**: CN mismatch → PermissionDenied.
   - Composite Consume miss (wrong thumbprint OR wrong name) → PermissionDenied.
   - IP mismatch / label mismatch → PermissionDenied (existing cases adapt).
   - **NEW**: ctx cancellation closes stream cleanly.

5. The fixture helper that prepares a Slot + cert + inspector (`fixtureOpts`-style if it exists) needs to set `ExpectedCertThumbprint` so composite Consume keys match.

### Acceptance Criteria

```bash
go build ./internal/controlplane/agent/...
go vet ./internal/controlplane/agent/...
go test ./internal/controlplane/agent/... -count=1 -race
```

### Wrap Up

1. Progress Tracker: Task 3 → `complete`.
2. Append key learnings.
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `refactor(agent): Connect server-streaming + CN cross-check + composite Consume`.
5. Proceed to Task 4.

---

## Task 4: `controlplane.adminServer.AnnounceAgent` handler

**Creates/modifies:** `internal/controlplane/server.go`, `internal/controlplane/server_test.go`.

**Depends on:** Task 2 (`agentslots.Registry` with `Reserve` accepting composite-key slots).

### Implementation Phase

1. **Extend `adminServer` struct** in `internal/controlplane/server.go`:

   ```go
   type adminServer struct {
       *fwhandler.Handler
       agents agentregistry.Registry
       slots  agentslots.Registry  // NEW
       clock  func() time.Time     // NEW (test seam — pass time.Now in production)
   }
   ```

2. **Update `NewAdminServer` signature**:

   ```go
   func NewAdminServer(
       fw *fwhandler.Handler,
       agents agentregistry.Registry,
       slots agentslots.Registry,
       clock func() time.Time,
   ) adminv1.AdminServiceServer {
       if clock == nil {
           clock = time.Now
       }
       return &adminServer{Handler: fw, agents: agents, slots: slots, clock: clock}
   }
   ```

3. **Implement `(*adminServer).AnnounceAgent`**:

   ```go
   func (s *adminServer) AnnounceAgent(ctx context.Context, req *adminv1.AnnounceAgentRequest) (*adminv1.AnnounceAgentResult, error) {
       if req == nil {
           return nil, status.Error(codes.InvalidArgument, "request required")
       }
       if req.AgentName == "" {
           return nil, status.Error(codes.InvalidArgument, "agent_name required")
       }
       if req.ContainerId == "" {
           return nil, status.Error(codes.InvalidArgument, "container_id required")
       }
       if req.CodeChallenge == "" {
           return nil, status.Error(codes.InvalidArgument, "code_challenge required")
       }
       if req.CodeChallengeMethod != string(consts.ChallengeMethodS256) {
           return nil, status.Error(codes.InvalidArgument, "code_challenge_method must be S256")
       }

       raw, err := hex.DecodeString(req.ExpectedCertThumbprint)
       if err != nil || len(raw) != sha256.Size {
           return nil, status.Error(codes.InvalidArgument, "expected_cert_thumbprint must be 64 lowercase hex characters")
       }
       var thumbprint [sha256.Size]byte
       copy(thumbprint[:], raw)

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

4. **Update sole caller of `NewAdminServer`** in `cmd/clawker-cp/main.go` to pass the new arguments. Note: that file is the focus of Task 6 — for this task, just unblock the build with a minimal change (slots may need a temporary placeholder var; Task 6 properly hoists construction).

5. **Add tests** in `server_test.go`:
   - `TestAdminServer_AnnounceAgent_Reserves` — happy path, mock slot registry verifies Reserve called with exact Slot fields, returned ExpiresAtUnix matches.
   - One case per validation branch (missing each required field, malformed hex, wrong challenge method) → `codes.InvalidArgument`.
   - `ErrSlotExists` from Reserve → `codes.AlreadyExists`.
   - Other Reserve error → `codes.Internal`.

   Use `agentslots/mocks.RegistryMock` for the slot registry. Inject a deterministic clock.

### Acceptance Criteria

```bash
go build ./internal/controlplane/...
go vet ./internal/controlplane/...
go test ./internal/controlplane/... -count=1 -race
```

### Wrap Up

1. Progress Tracker: Task 4 → `complete`.
2. Append key learnings.
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `feat(controlplane): implement AdminService.AnnounceAgent handler`.
5. Proceed to Task 5.

---

## Task 5: `AgentIdentityInterceptor` (unary + stream) with fail-secure opt-out map

**Creates/modifies:** `internal/controlplane/agent/identity_interceptor.go` (NEW), `internal/controlplane/agent/identity_interceptor_test.go` (NEW).

**Depends on:** Task 1 (proto types — `agentv1.ServiceName`), Task 3 (handler infrastructure including `peerCertAndIP`).

### Implementation Phase

1. **New file `internal/controlplane/agent/identity_interceptor.go`** with:
   - `IdentityOptedOutMethods() map[string]bool` — returns `{"/" + agentv1.ServiceName + "/Connect": true}`.
   - `entryCtxKey struct{}` (unexported), `WithEntry(ctx, *agentregistry.Entry) context.Context`, `EntryFromContext(ctx) (*agentregistry.Entry, bool)`.
   - `IdentityInterceptor(reg agentregistry.Registry, optedOut map[string]bool, log *logger.Logger) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor)`.
   - Unary form: pass through if optedOut; otherwise computes thumbprint via `peerCertAndIP`, calls `reg.Lookup(thumbprint)`, wraps ctx with WithEntry, calls handler.
   - Stream form: same, but wraps `grpc.ServerStream` with a `wrappedStream{ServerStream; ctx}` whose `Context()` returns the augmented ctx. CRITICAL: the `Context()` method MUST be on the wrapper, not the embedded ServerStream — otherwise the handler sees the original ctx.
   - All rejections return `status.Error(codes.PermissionDenied, "registration rejected")` — same generic envelope as Connect's other rejections.

   Reuse `peerCertAndIP` from `handler.go` (same package).

2. **New file `internal/controlplane/agent/identity_interceptor_test.go`** with:
   - `TestIdentityInterceptor_Unary_OptedOut`: a method in the optedOut map skips lookup; handler called with original ctx (no entry).
   - `TestIdentityInterceptor_Unary_RegistryHit`: non-opted-out method computes thumbprint, calls Lookup, attaches Entry, handler retrieves via EntryFromContext.
   - `TestIdentityInterceptor_Unary_LookupMiss`: ErrUnknownAgent → PermissionDenied.
   - `TestIdentityInterceptor_Stream_*`: parallel cases for the stream interceptor. Use a `bufconn`-driven gRPC server or mock `grpc.ServerStream` directly to verify the wrapped stream's `Context()` carries the entry.
   - `TestIdentityInterceptor_AllMethodsHavePolicy`: walks `agentv1.AgentService_ServiceDesc`. For every method, asserts either `optedOut[m] == true` (explicit opt-out) OR is implicitly require-identity. Catches a future RPC added without a deliberate policy decision. Mirror `TestAgentMethodScopes_CoversAllRPCs`.

   Use `agentregistry/mocks.RegistryMock` for the registry.

### Acceptance Criteria

```bash
go build ./internal/controlplane/agent/...
go vet ./internal/controlplane/agent/...
go test ./internal/controlplane/agent/... -count=1 -race
```

### Wrap Up

1. Progress Tracker: Task 5 → `complete`.
2. Append key learnings (e.g., the `wrappedStream.Context()` pitfall if any subagent flagged it).
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `feat(agent): identity interceptor with fail-secure opt-out map`.
5. Proceed to Task 6.

---

## Task 6: `cmd/clawker-cp/main.go` wiring

**Creates/modifies:** `cmd/clawker-cp/main.go`.

**Depends on:** Tasks 2, 4, 5.

### Implementation Phase

1. **Hoist `slotRegistry` construction** above the `NewAdminServer` call. Currently `slotRegistry` is constructed at ~line 420; `NewAdminServer` is called at ~line 388. Reorder so:

   ```go
   slotRegistry := agentslots.NewRegistry(time.Now, 0, log.With("component", "agentslots"))
   defer slotRegistry.Stop()
   agentReg := agentregistry.NewRegistry(log.With("component", "agentregistry"))

   adminv1.RegisterAdminServiceServer(grpcServer, controlplane.NewAdminServer(handler, agentReg, slotRegistry, time.Now))
   ```

2. **Chain `IdentityInterceptor` onto the agent listener** after the existing `AuthInterceptor`. Find where `agentServer := grpc.NewServer(...)` is constructed; replace with:

   ```go
   identityUnary, identityStream := agent.IdentityInterceptor(
       agentReg,
       agent.IdentityOptedOutMethods(),
       log.With("component", "agent-identity"),
   )
   agentServer := grpc.NewServer(
       grpc.Creds(credentials.NewTLS(agentTLSCfg)),
       grpc.ChainUnaryInterceptor(authUnary, identityUnary),
       grpc.ChainStreamInterceptor(authStream, identityStream),
   )
   ```

   Match the actual variable names from existing code — `authUnary`/`authStream` are placeholders.

3. **Add the slots dockerevents subscription** alongside the existing agentregistry one:

   ```go
   agentregistry.Subscribe(ctx, agentReg, informer, log.With("component", "agentreg-sub"))
   agentslots.Subscribe(ctx, slotRegistry, informer, log.With("component", "agentslots-sub"))
   ```

4. The `agentInspector` and `agentHandler := agent.NewHandler(...)` calls move alongside (or stay) — verify the order works with the new interceptor wiring.

5. Smoke-test the wiring locally if Docker is available; otherwise rely on existing CP integration tests + Task 9's e2e to verify.

### Acceptance Criteria

```bash
go build ./cmd/clawker-cp/...
go vet ./cmd/clawker-cp/...

# Whole tree should now build cleanly (other tasks have landed before this one)
go build ./...
go vet ./...
```

### Wrap Up

1. Progress Tracker: Task 6 → `complete`.
2. Append key learnings (informer-availability ordering, etc.).
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `feat(controlplane): wire slot registry, identity interceptor, slots dockerevents subscription`.
5. Proceed to Task 7.

---

## Task 7: `cmd/clawkerd/main.go` — consume `Connect` server-stream

**Creates/modifies:** `cmd/clawkerd/main.go`. Possibly `cmd/clawkerd/main_test.go` if existing tests break.

**Depends on:** Task 1 (proto Connect signature), Task 3 (server-side Welcome semantics).

### Implementation Phase

1. **Replace the unary `Register` call** with the streaming `Connect` call:

   ```go
   client := agentv1.NewAgentServiceClient(conn)
   stream, err := client.Connect(ctx, &agentv1.ConnectRequest{
       AgentName:    agentName,
       CodeVerifier: verifier,
   })
   if err != nil {
       return fmt.Errorf("connect to CP: %w", err)
   }
   ```

2. **Receive the first message before deleting verifier.** Receiving Welcome implies server-side auth fully succeeded. Deleting before receiving risks a race where the stream was opened transport-wise but the handler rejected auth.

   ```go
   first, err := stream.Recv()
   if err != nil {
       return fmt.Errorf("connect: recv welcome: %w", err)
   }
   if _, ok := first.Payload.(*agentv1.Command_Welcome); !ok {
       return fmt.Errorf("connect: expected Welcome as first message, got %T", first.Payload)
   }
   // Welcome received → auth succeeded → safe to delete single-use verifier.
   if path, _ := consts.BootstrapVerifierPath(); path != "" {
       _ = os.Remove(path)
   }
   // (B5+ uses welcome.Config to init logger from CP-delivered ClawkerdConfiguration.)
   ```

3. **Loop on `stream.Recv()` for the agent's lifetime**, dispatching on the Command oneof:

   ```go
   for {
       cmd, err := stream.Recv()
       if err == io.EOF {
           log.Info().Msg("connect stream closed cleanly")
           return nil
       }
       if err != nil {
           // Stream broken — log + exit. Reconnect logic is the
           // CP-restart-resilience initiative's job, not this branch.
           log.Warn().Err(err).Msg("connect stream broken")
           return err
       }

       switch payload := cmd.Payload.(type) {
       case *agentv1.Command_Welcome:
           log.Warn().Msg("received unexpected second Welcome — ignoring")
       default:
           log.Debug().Str("type", fmt.Sprintf("%T", payload)).Msg("received unknown command type (B5+ defines variants)")
       }
   }
   ```

4. Replace the previous "idle on `<-ctx.Done()`" logic with the Recv loop above.

5. Update any tests in `cmd/clawkerd/` that mocked the unary Register — adapt to the streaming consumer pattern.

### Acceptance Criteria

```bash
go build ./cmd/clawkerd/...
go vet ./cmd/clawkerd/...

# Existing E2E (clawkerd_register_test.go) may need rename/updates — Task 9
# covers the docs pass; functional rename of the test file (if needed) is
# in scope here.
go test ./cmd/clawkerd/... -count=1
```

### Wrap Up

1. Progress Tracker: Task 7 → `complete`.
2. Append key learnings (verifier-delete-after-Welcome rationale, etc.).
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `refactor(clawkerd): consume Connect server-stream`.
5. Proceed to Task 8.

---

## Task 8: CLI `run`/`start` wiring — `RuntimeEnvOpts` Clawkerd* fields + `prepareAgentBootstrap` helper

**Creates/modifies:** `internal/cmd/container/shared/container_create.go`, `internal/cmd/container/shared/container_start.go`. Possibly `internal/cmd/container/run/run.go`, `internal/cmd/container/start/start.go` (callsites). Tests for `prepareAgentBootstrap`.

**Depends on:** Task 4 (AnnounceAgent handler must be implemented for the CLI to actually announce successfully).

### Implementation Phase

1. **`buildCreateTimeEnv` (`container_create.go`)** populates the three `Clawkerd*` env-var fields **unconditionally** — NOT gated on firewall:

   ```go
   envOpts := docker.RuntimeEnvOpts{
       // ... existing fields ...
       ClawkerdAgentName: agentName,
       ClawkerdAgentAddr: net.JoinHostPort(consts.ContainerCP, strconv.Itoa(opts.Config.Settings().ControlPlane.AgentPort)),
       ClawkerdHydraURL:  fmt.Sprintf("https://%s/oauth2/token",
           net.JoinHostPort(consts.ContainerCP, strconv.Itoa(opts.Config.Settings().ControlPlane.HydraPublicPort))),
   }
   ```

   Verify the values render correctly when `firewall.enable: false` — the CP, clawker-net, and Hydra are still up regardless. (See "CP ≠ firewall" callout in project-root `CLAUDE.md`.)

2. **Extend `CommandOpts` (`container_start.go`)** with `AgentName string`. Populate at run/start callsites from `CreateContainerResult.AgentName`.

3. **Add `prepareAgentBootstrap` helper** in `container_start.go`. Insert the call between `BootstrapServicesPreStart` and `client.ContainerStart`:

   ```go
   func ContainerStart(ctx context.Context, cmdOpts CommandOpts, startOpts docker.ContainerStartOptions) (mobyClient.ContainerStartResult, error) {
       if err := BootstrapServicesPreStart(ctx, startOpts.ContainerID, cmdOpts); err != nil {
           return mobyClient.ContainerStartResult{}, err
       }

       if err := prepareAgentBootstrap(ctx, cmdOpts, startOpts.ContainerID); err != nil {
           return mobyClient.ContainerStartResult{}, fmt.Errorf("agent bootstrap: %w", err)
       }

       // ... rest unchanged: client.ContainerStart, BootstrapServicesPostStart ...
   }

   func prepareAgentBootstrap(ctx context.Context, cmdOpts CommandOpts, containerID string) error {
       cfg, err := cmdOpts.Config()
       if err != nil { return err }
       settings := cfg.Settings()

       caCertPath, err := consts.AuthCACertPath()
       if err != nil { return err }
       caKeyPath, err := consts.AuthCAKeyPath()
       if err != nil { return err }
       signingKey, err := auth.LoadSigningKey()
       if err != nil { return err }

       hydraTokenURL := fmt.Sprintf("https://%s/oauth2/token",
           net.JoinHostPort(consts.ContainerCP, strconv.Itoa(settings.ControlPlane.HydraPublicPort)))

       bootstrap, err := GenerateAgentBootstrap(caCertPath, caKeyPath, cmdOpts.AgentName, hydraTokenURL, signingKey)
       if err != nil { return err }

       admin, err := cmdOpts.AdminClient(ctx)
       if err != nil { return err }
       if err := AnnounceAgent(ctx, admin, bootstrap, cmdOpts.AgentName, containerID); err != nil {
           return err
       }

       client, err := cmdOpts.Client(ctx)
       if err != nil { return err }
       return WriteAgentBootstrapToContainer(ctx, containerID, NewCopyToContainerFn(client), bootstrap)
   }
   ```

4. **Hard-fail policy.** Any error in `prepareAgentBootstrap` returns from `ContainerStart` BEFORE Docker's `client.ContainerStart` fires. The container is created but not started. Caller's existing cleanup (if any) handles teardown.

5. **Callsite updates.** `internal/cmd/container/run/run.go` and `internal/cmd/container/start/start.go` populate `cmdOpts.AgentName` from the `CreateContainerResult.AgentName`. Verify both paths.

6. **Tests.** Add a unit test for `prepareAgentBootstrap` using a mock `AdminClient`, capturing `CopyToContainer` to assert tar contents land at the right path. Verify:
   - Generate → Announce → Write order.
   - AnnounceAgent error propagates; Docker start does NOT fire.
   - Write error propagates.

### Acceptance Criteria

```bash
go build ./internal/cmd/container/...
go vet ./internal/cmd/container/...
go test ./internal/cmd/container/... -count=1 -race
```

### Wrap Up

1. Progress Tracker: Task 8 → `complete`.
2. Append key learnings.
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `feat(container): wire AnnounceAgent + bootstrap delivery into run/start`.
5. Proceed to Task 9.

---

## Task 9: Documentation pass

**Creates/modifies:** `internal/controlplane/agent/CLAUDE.md`, `internal/controlplane/agentslots/CLAUDE.md`, `internal/controlplane/CLAUDE.md`, `.claude/docs/KEY-CONCEPTS.md`, `.serena/memories/cp-initiative-status.md`. Possibly `internal/cmd/container/shared/CLAUDE.md`.

**Depends on:** all previous tasks (all the code shape this documents).

### Implementation Phase

1. **`internal/controlplane/agent/CLAUDE.md`**:
   - Update Connect handler narrative for server-streaming.
   - Document CN cross-check as the new check.
   - Document the `IdentityInterceptor` (location, opt-out map, fail-secure semantics).
   - "Known limitations" section: streaming RPC eviction broadcast deferred (cross-reference `cp-initiative-cp-restart-resilience`); CP restart resilience deferred (same cross-reference).

2. **`internal/controlplane/agentslots/CLAUDE.md`**:
   - Document composite key (thumbprint + agent_name).
   - Document `EvictByContainerID` + dockerevents Subscribe.
   - Note that `ErrSlotExists` is now effectively impossible (collision-resistant by construction).

3. **`internal/controlplane/CLAUDE.md`**:
   - Update step 8 narrative: AnnounceAgent handler is now real; agent listener has identity interceptor chained; slot registry has dockerevents subscription.
   - Remove "Known follow-up: AnnounceAgent" — done.

4. **`.claude/docs/KEY-CONCEPTS.md`**:
   - Update `agentslots.Registry` entry (composite key).
   - Update `agent.Handler` entry (Connect, not Register).
   - Add `agent.IdentityInterceptor` entry.
   - Add `agent.WithEntry` / `agent.EntryFromContext` entries.

5. **`.serena/memories/cp-initiative-status.md`**:
   - Update Branch 4 row: follow-up complete.
   - Remove the "Active follow-ups" entry for this initiative (it's done).
   - Add a brief delivery-summary section for this initiative if useful.

6. **Run `bash scripts/check-claude-freshness.sh`** to catch any other CLAUDE.md files that drifted.

### Acceptance Criteria

```bash
# Final integration check across the whole tree
go build ./...
go vet ./...
go vet ./test/e2e/...
make test

# Docs freshness
bash scripts/check-claude-freshness.sh
```

### Wrap Up

1. Progress Tracker: Task 9 → `complete`. Mark all 9 tasks `complete`.
2. Append key learnings.
3. Subagent review pipeline (with `~/.claude/CLAUDE.md` directive). Fix findings.
4. Commit: `docs(controlplane,agent,agentslots): update for end-to-end CLI integration`.
5. **Initiative complete.** Report to the user with a summary: what shipped, key decisions implemented, what's next (B5 / CP restart resilience).
