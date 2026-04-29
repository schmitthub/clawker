# Task 12 — docs: rewrite agentslots/CLAUDE.md for container_id-keyed model

**Status**: pending
**Claimed by**: —
**Blocks**: —
**Blocked by**: none
**Parallel-safe**: yes (docs-only)

## Findings covered

- **Stale doc** — `internal/controlplane/agentslots/CLAUDE.md` describes `slotKey{Thumbprint, AgentName, Project}` composite-key model. Actual code (`agentslots/registry.go:131`) keys slots by `map[string]Slot` on `ContainerID` only. The composite story was replaced mid-refactor; the doc lies.

## Decision

Rewrite `agentslots/CLAUDE.md` to describe the current container_id-keyed model. Document the rationale for the simplification: container_id is the universally unique key; (project, agent_name) is non-unique across container restarts (new container, new ID, but same agent name) and across projects with the same short agent name. Storing keyed by container_id eliminates a class of stale-key bugs.

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/agentslots/CLAUDE.md` | Full rewrite. |

## Implementation plan

1. **Read current file in full** to preserve still-true content (test patterns, mock locations, dockerevents subscription wiring, TTL janitor semantics).
2. **Read `internal/controlplane/agentslots/registry.go`** in full to extract the actual current model:
   - `Slot` struct shape
   - `Reserve` signature + invariants
   - `Consume` signature + ContainerID-keyed lookup
   - `EvictByContainerID` semantics
   - TTL janitor behavior
   - Subscribe → dockerevents wiring
3. **Read sibling docs** for reference:
   - `internal/controlplane/agentregistry/CLAUDE.md` (the registry doc — same architectural family)
   - `internal/controlplane/agent/CLAUDE.md` (consumer of slots; documents the Connect handler's slot Consume call)
4. **Rewrite sections**:
   - **Overview**: pre-Connect slot reservation, container_id keyed, PKCE-bound. NOT a composite (thumbprint, agent_name, project) key — that was an earlier design that lost to the simpler container_id-only model.
   - **Why container_id**: container is the natural unit of slot lifetime. UNIQUE in docker, evictable via dockerevents, always available at Reserve time (CLI just created the container) and at Consume time (clawkerd announces with its own container_id via env var).
   - **Why NOT (project, agent_name)**: not unique across restarts or across projects with reused short names. Slot would collide on legitimate parallel agents.
   - **Why NOT thumbprint**: thumbprint is mint-time data; clawkerd doesn't know it until it reads its bootstrap material. Slot must be reservable BEFORE clawkerd starts (clawker run does it pre-docker-start), so thumbprint isn't available yet.
   - **Slot struct**: AgentName, Project, ContainerID, Verifier (PKCE S256), Challenge (PKCE), ExpiresAt. AgentName + Project are STORED as cross-check fields used by the Connect handler's label/CN cross-check; they are NOT identity.
   - **Reserve / Consume / EvictByContainerID API**: signatures + semantics + error contracts.
   - **TTL janitor**: sweep semantics, default TTL, what happens to stale slots.
   - **Subscribe**: dockerevents wiring (under per-kind subscribe API from Task #4).
   - **Test patterns**: in-memory registry, mock structures, fake clock.
   - **Imports + Used by**: keep current accurate.
   - **Known limitations**: if any.
5. **Cross-link**: reference `agentregistry/CLAUDE.md` for the post-Connect counterpart (slots is pre-Connect, registry is post-Connect — explicit lifecycle handoff).

## Test requirements

None. Doc-only.

## Verification

```bash
# Doc sanity: file builds as markdown
test -f internal/controlplane/agentslots/CLAUDE.md && cat internal/controlplane/agentslots/CLAUDE.md | head -1

# Manual review: read the rewritten doc end-to-end against registry.go
# Verify every API call documented matches the actual exported surface
go doc ./internal/controlplane/agentslots/

# CLAUDE.md freshness check (project-level)
bash scripts/check-claude-freshness.sh
```

## Dependencies

None for the rewrite itself. **Coordinate with Task #9** which touches `agentslots/registry.go` (sweep log fields). If #9 adds new public surface, document it here. If #9 lands first, just incorporate any new methods into the rewrite.

## Risks / gotchas

- **Don't restate code mechanics line-by-line** — the doc is for orientation, not duplication. Aim for the conceptual model + API contracts + cross-references to siblings.
- **Don't propagate the stale slotKey description** even as a "history note" — it confuses future readers. The decision is to remove it; if future archaeology needs it, git log has it.
- **Don't accidentally edit other files** — this task is pure docs. Resist drive-by code changes.
- **The "agent + project as cross-check" framing is load-bearing** — it's the architectural truth that informs Tasks #1, #2, #5. Get it right here so future readers don't re-conflate identity with cross-check.

## Reference reading

- `internal/controlplane/agentslots/CLAUDE.md` (CURRENT — to be rewritten)
- `internal/controlplane/agentslots/registry.go` (source of truth)
- `internal/controlplane/agentslots/subscribe.go`
- `internal/controlplane/agentregistry/CLAUDE.md` (sibling — match style)
- `internal/controlplane/agent/CLAUDE.md` (consumer — documents the Connect handler's Consume call)
- Project root `CLAUDE.md` `<critical_clarification>` block on CP/firewall + identity model

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
