# Initiative: CP Restart Resilience

> **Status:** Tracked. Not scheduled. Sized as its own focused branch (~400-500 LOC + tests).
> **Prerequisite for:** B5 streaming RPC eviction broadcast + any production-readiness story.
> **Surfaced during:** Branch 4 follow-up planning, 2026-04-25.

## Problem

When the control plane (CP) restarts — due to user error (`clawker controlplane down`), CP-process crash, host reboot, or a bug — all in-memory state is wiped:

- `agentregistry` (per-agent thumbprint → Entry map): gone.
- `agentslots` (pending PKCE reservations): gone.
- All open `Connect` streams: torn down on the wire when CP's listener dies.

clawkerd processes inside agent containers are still alive. They have on-disk material that survived (cert + key + CA + Hydra assertion — verifier was deleted on first-Connect success). But there's no path for them to re-establish identity with the new CP because:

- The original AnnounceAgent slot is gone (consumed at first Connect, never re-created).
- clawkerd has no verifier to present.
- Connect handler's only path today is slot-consume.

**Net effect:** every CP restart requires the user to kill + recreate every running agent container. Unacceptable for any non-trivial workflow.

## Why this is a separate initiative, not part of Branch 4 follow-up

- Requires registry persistence layer — net-new infrastructure.
- Requires CP startup reconcile against Docker state — non-trivial sequencing.
- Requires Connect handler to grow a second auth path (registry-lookup-first).
- Requires clawkerd reconnect-with-backoff loop.
- Requires `clawker controlplane down` safety guard.
- Combined ~400-500 LOC + tests. Doesn't fit cleanly inside the existing CLI-wiring branch.

But the design is **already compatible** with Branch 4's auth handshake — Branch 4 doesn't lock us out:

- cert/key/CA/assertion persist on disk by design (verifier deleted, rest stays for redial).
- mTLS handshake works with the existing per-agent leaf cert.
- Bearer token from `private_key_jwt` is reusable until expiry; signing key persists for re-mint.
- `agentregistry` is already keyed by cert thumbprint — the right key for reconnect lookup.
- `agentslots` composite key (thumbprint + agent_name) still works for first-connect; reconnect bypasses slot entirely.
- Four of the five Connect cross-checks (thumbprint, peer IP, label, CN) work without slot. PKCE consume is the only one that drops on reconnect.
- `agentregistry.Subscribe` to dockerevents already evicts dead-container entries — runs at CP startup before reconnect path goes live, so stale persisted entries don't authenticate ghost agents.

The proto already preserves the seam: `ConnectRequest.code_verifier` doc explicitly notes empty verifier is reserved for the future reconnect path.

## Components

### 1. Registry persistence layer

**Location:** `internal/controlplane/agentregistry/persist.go` (new file).

**Approach:** snapshot-on-write to a JSON or YAML file in CP's data subdir (`FirewallDataSubdir`-equivalent for agent state). Atomic write (temp+fsync+rename). Schema:

```go
type Snapshot struct {
    Version int                          `yaml:"version"`
    Entries []agentregistry.PersistEntry `yaml:"entries"`
    SavedAt time.Time                    `yaml:"saved_at"`
}

type PersistEntry struct {
    AgentName    string    `yaml:"agent_name"`
    ContainerID  string    `yaml:"container_id"`
    Thumbprint   string    `yaml:"thumbprint_hex"`
    RegisteredAt time.Time `yaml:"registered_at"`
}
```

**Write triggers:** `Add` and `EvictByContainerID` schedule a debounced flush (e.g., 250ms). Synchronous flush on graceful shutdown.

**Privacy:** thumbprint is non-sensitive (it's a cert hash). No keys/tokens persist. File ownership root:root, mode 0600 inside the CP container's data volume.

### 2. CP startup reconcile

**Location:** `internal/controlplane/startup.go` (extend existing startup orchestrator).

**Sequence:**

1. Load snapshot from disk. Empty → start fresh.
2. For each persisted entry: `dockerCli.ContainerInspect(entry.ContainerID)`.
   - Container exists, is running, labels match: rehydrate registry entry.
   - Container exists but is stopped/dead: drop entry.
   - Container does not exist (Docker returns NotFound): drop entry.
   - Inspect API error: log warn, skip entry (defensive — don't auth-grant on uncertainty).
3. Persist the post-reconcile state back to disk.
4. **Then** mark CP ready and start the `Connect` listener.

**Ordering matters:** reconcile MUST complete before any `Connect` call is accepted, otherwise an evicted-but-stale entry could authenticate a ghost reconnect. `agentregistry.Subscribe` to dockerevents starts AFTER reconcile finishes — combined with the inspect-based reconcile, this closes the window.

### 3. Connect handler grows reconnect branch

**File:** `internal/controlplane/agent/handler.go` (existing, modify).

```go
func (h *Handler) Connect(req *ConnectRequest, stream Server) error {
    ctx := stream.Context()
    peerCert, peerIP, err := peerCertAndIP(ctx)
    if err != nil { /* PermissionDenied */ }
    thumbprint := sha256.Sum256(peerCert.Raw)

    // CN cross-check (unchanged from B4 follow-up).
    if !ctConstantTimeCompareCN(peerCert, req.AgentName) { /* reject */ }

    // RECONNECT PATH: registry already has this thumbprint?
    if entry, ok := h.registry.Lookup(thumbprint); ok {
        // No slot consume. Verify cross-checks still pass against
        // current Docker state.
        if err := h.reconnectCrossChecks(ctx, entry, peerIP, req.AgentName); err != nil {
            return err
        }
        return h.serveStream(stream, entry)
    }

    // FIRST-CONNECT PATH (existing B4 follow-up logic).
    if req.CodeVerifier == "" {
        // No registry entry AND no verifier — rejected. Reconnect attempt
        // against a CP that has no record (e.g. snapshot lost, container
        // never registered).
        return status.Error(codes.PermissionDenied, "registration rejected")
    }
    slot, err := h.slots.Consume(thumbprint, req.AgentName, req.CodeVerifier)
    if err != nil { /* ... */ }
    // ... existing 5 cross-checks ...
    h.registry.Add(...)
    return h.serveStream(stream, entry)
}
```

`reconnectCrossChecks` runs the same Docker inspect + IP + label checks as first-connect. Difference: no slot consume, no PKCE.

### 4. clawkerd reconnect-with-backoff

**File:** `cmd/clawkerd/main.go` (existing, modify).

Replace today's "stream broken → exit" with a reconnect loop:

```go
backoff := initialBackoff // e.g. 1s
maxBackoff := 60 * time.Second

for {
    if err := openConnectStream(ctx, ...); err != nil {
        log.Warn().Err(err).Msg("connect stream failed, reconnecting after backoff")
        select {
        case <-time.After(backoff):
            backoff = min(backoff*2, maxBackoff)
        case <-ctx.Done():
            return
        }
        continue
    }
    backoff = initialBackoff // successful connection resets backoff
    // openConnectStream ran the full Recv loop until stream closed
}
```

`openConnectStream` builds a fresh ConnectRequest with empty `code_verifier` (single-use, was deleted on first-connect success). CP's reconnect path matches by thumbprint.

If the very first connect fails (verifier still on disk, no successful first-connect ever happened), clawkerd uses the verifier. Once first-connect succeeds and verifier is deleted, all subsequent attempts go through the reconnect path.

### 5. `clawker controlplane down` safety guard

**File:** `internal/cmd/controlplane/down.go` (existing, modify).

Pre-flight: `agentregistry.Snapshot()` via `f.AdminClient`. If non-empty, refuse with:

```
Refusing to stop control plane: 3 agent(s) still running.
Stopping the control plane while agents are running breaks their connections.
After this initiative ships, reconnect-with-backoff will recover them automatically.

To stop anyway: clawker controlplane down --force
To stop the agents first: clawker container stop <names>
```

`--force` overrides. Doc the behavior in the command's help text.

### 6. `clawker volume prune` safety

**Why it's part of this initiative:** introducing a CP persistence layer means CP state lives in a Docker volume. Today's `clawker volume prune` would happily nuke it alongside agent volumes — users would lose CP state any time they cleaned up, then complain that CP is forgetting their agents on every restart. The persistence work is meaningless if `volume prune` regularly destroys the snapshot.

**Default behavior change:** `clawker volume prune` (no flags) prunes ONLY agent container volumes (`clawker.<project>.<agent>-<purpose>` named volumes from stopped containers). It leaves alone:

- CP persistence volumes (`clawker.controlplane.*` or whatever naming we land on).
- Any global `clawker-<purpose>` infra volumes (firewall data, monitor stack data, etc.).
- Any volume not labeled as agent-owned.

**Opt-in to nuke everything:** `--all` / `-a` flag (matches Docker CLI conventions for `prune`). With it, the command behaves as today — purges any clawker-managed volume not in use. Use case: full reset, troubleshooting.

**Implementation:**

- `internal/docker/` already has volume-listing with label filters. Extend to support a "purpose-class" filter: agent (`dev.clawker.purpose` matches an agent purpose) vs infra (CP, firewall data, monitor).
- Volume label scheme grows a class indicator if needed (e.g., `dev.clawker.class=agent|infra`). Or derive from existing `dev.clawker.purpose` values if those already segregate cleanly.
- Update `internal/cmd/volume/prune/prune.go` for the new default + `--all` flag.

**Tests:**

- Unit: prune with no flags removes agent volumes only, preserves infra volumes by mock-listing both classes.
- Unit: prune with `--all` removes both classes.
- E2E: bring CP up, populate a fake snapshot, run `clawker volume prune`, restart CP, verify state intact. Then `volume prune --all`, restart CP, verify state lost (this is the user-explicit nuke case).

**User-facing UX note:** the pruned-by-default change is a minor breaking change vs current behavior. Document loudly in release notes; the safer default is worth it.

### 7. Streaming RPC eviction broadcast

Once registry persistence + reconnect land, the eviction-broadcast hole closes naturally for the Connect stream: when an agent is evicted, CP can cancel the per-agent stream context. This requires `agentregistry` to expose a watch/notify mechanism so the streaming handler can react. Approaches:

- **Per-agent cancel func registry.** When `serveStream` opens, it registers a cancel func keyed by thumbprint. `EvictByContainerID` looks up and fires matching cancel funcs.
- **Eviction channel.** `agentregistry.Subscribe()` (consumer-side, not the dockerevents Subscribe) returns a channel of evicted thumbprints. `serveStream` selects on this channel.

Either approach also covers future client-streams (`Events`) and any other long-lived per-agent RPC. Bundling this into the same initiative makes sense — the persistence + reconcile work is the prerequisite, eviction broadcast is the natural finishing touch.

## Test strategy

### Unit

- Persistence: write+read round-trip; corrupt file handled gracefully (start fresh + log warn); concurrent flush serialized.
- Reconcile: persisted entry with live container → kept; with dead container → dropped; with NotFound from Docker → dropped; with inspect error → kept-but-flagged (lean defensive: drop).
- Connect handler reconnect path: thumbprint hit + cross-checks pass → serveStream without slot consume; thumbprint hit + IP mismatch → PermissionDenied; thumbprint miss + empty verifier → PermissionDenied; thumbprint miss + valid verifier → first-connect path.
- clawkerd reconnect loop: stream death triggers backoff retry; successful reconnect resets backoff; ctx cancel exits cleanly.
- Eviction broadcast: registry evict triggers stream cancel; multiple streams for same thumbprint all cancel.

### E2E

- **CP restart cycle:** start CP, run `clawker run`, kill CP, wait, start CP, verify clawkerd reconnects within 10s and registry shows the agent.
- **Long outage:** CP down for >24h (token expired). clawkerd re-mints token from signing key, reconnects.
- **CP restart with stale snapshot:** CP saves snapshot, agent container is killed during outage, CP comes back. Reconcile drops the dead entry; subsequent reconnect attempt from a (somehow-running) clawkerd with that thumbprint fails first-connect path (no verifier).
- **Concurrent reconnect:** kill CP with N agents running, restart CP, verify all N reconnect without thrashing.
- **`clawker controlplane down` safety:** with agents running → refused; with `--force` → stops; with no agents → stops without prompt.

## Open design questions

1. **Snapshot file location.** Probably `<dataDir>/controlplane/agentregistry.yaml`. Confirm with config layout.
2. **Snapshot format: YAML vs JSON vs gob.** Lean YAML for human-readability during debugging.
3. **Hydra token re-mint trigger.** clawkerd needs to detect token expiry vs handshake-rejected and re-exchange the assertion. Probably a `codes.Unauthenticated` retry path.
4. **Bearer token caching strategy.** Cache the access token across reconnect attempts within its TTL window; only re-exchange on expiry.
5. **What happens if Docker is itself unhealthy at CP startup.** Reconcile can't run. Lean: refuse to mark ready until Docker is responsive. Document the user-visible failure mode.
6. **Snapshot encryption.** Thumbprints aren't sensitive but containerIDs + agent_names paint a partial picture of user activity. Probably file-level mode 0600 inside the CP container's data volume is sufficient. Revisit if threat model elevates.

## When to schedule this

Before any production-readiness story. This is the gap between "alpha works in the happy path" and "alpha survives operator error." Likely candidate for a Branch 5b or Branch 6 slot, depending on B5's command-channel scope.

## Cross-references

- Spec'd seams: `cp-initiative-branch-4-followup-spec` (proto comment on `ConnectRequest.code_verifier`; cert/key/assertion-on-disk persistence; thumbprint-keyed registry).
- Compatible: `agentregistry.Subscribe` already evicts dead containers via dockerevents; reconcile reuses this signal.
