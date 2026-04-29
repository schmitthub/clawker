# Task 10 — controlplane/server_test: nil-agents → empty ListAgents

**Status**: complete
**Claimed by**: claude-opus-4.7
**Blocks**: —
**Blocked by**: none
**Parallel-safe**: yes

## Findings covered

- **T7** — `internal/controlplane/server_test.go` has `TestNewAdminServer_NilSlotsPanics` for slots, but no parallel test that nil-`agents` produces empty `ListAgents` result (claimed nil-tolerant behavior per `internal/controlplane/CLAUDE.md` `server.go` row).

## Decision

Add `TestListAgents_NilRegistry_ReturnsEmpty` to lock down the documented nil-tolerant claim.

## Affected files

| File | Change |
|------|--------|
| `internal/controlplane/server_test.go` | Add new test. |

## Implementation plan

1. Read `internal/controlplane/server.go` to confirm the nil-tolerant code path:
   - `NewAdminServer(fw, agents, slots, clock)` accepts `agents` of type `agentregistry.Registry`. If nil, `ListAgents` should return an empty result (per CLAUDE.md).
2. Read the existing `TestNewAdminServer_NilSlotsPanics` for the test scaffolding pattern.
3. Add the new test:
   ```go
   func TestListAgents_NilRegistry_ReturnsEmpty(t *testing.T) {
       srv := controlplane.NewAdminServer(stubFW(t), nil, stubSlots(t), time.Now)
       resp, err := srv.ListAgents(context.Background(), &adminv1.ListAgentsRequest{})
       require.NoError(t, err)
       require.Empty(t, resp.Agents)
   }
   ```
   Adjust `stubFW` / `stubSlots` to whatever nearby tests use (likely small constructors that pass minimal valid deps).

## Test requirements

- `TestListAgents_NilRegistry_ReturnsEmpty` per above.

## Verification

```bash
go test ./internal/controlplane/ -race -v -run TestListAgents_NilRegistry
make test
```

## Dependencies

None.

## Risks / gotchas

- **If Task #1 is in-flight**, the `ListAgents` impl may temporarily change shape (different Registry interface). Schedule this task before or after #1, not in parallel.
- The CLAUDE.md description claims nil-tolerance for `agents`; verify this is actually the case before writing the test. If `NewAdminServer` panics on nil agents (mirroring NilSlots), update the doc instead and write a `TestNewAdminServer_NilAgentsPanics` test instead. **Read the code first — don't trust the doc blindly.**
- Use the same scaffolding pattern as `TestNewAdminServer_NilSlotsPanics` for consistency.

## Reference reading

- `internal/controlplane/server.go` `NewAdminServer` constructor
- `internal/controlplane/server_test.go` `TestNewAdminServer_NilSlotsPanics`
- `internal/controlplane/CLAUDE.md` `server.go` row (claims `agents` is nil-tolerant; `slots` panics)

## Resolution

- Commit SHA: d3485e7e
- Notes:
  - The runtime nil-agents test (`TestAdminServer_ListAgents_NilRegistry`) already existed via `&adminServer{}` zero-value. Added the symmetric constructor-level test `TestNewAdminServer_NilAgentsConstructorAcceptedListAgentsEmpty` that goes through `NewAdminServer(nil_fw, nil_agents, slots_mock, time.Now, nil_log)` and asserts no panic + empty `ListAgents` result, mirroring `TestNewAdminServer_NilSlotsPanics`.
  - Locks the documented nil-tolerant contract on the public API boundary, not just the internal struct.
