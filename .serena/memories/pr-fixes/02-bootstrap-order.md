# Task 02 — container_create: move reg.Add to last step

**Status**: pending
**Claimed by**: —
**Blocks**: —
**Blocked by**: 01

## Findings covered

- **C7** — `InstallAgentBootstrap` writes registry row at `agent_bootstrap.go:271`, THEN `container_create.go:1712` calls `InjectPostInitScript` which can fail. On post-init failure: `ContainerRemove` runs (best-effort), but registry row stays until dockerevents catches up. Race window for same-name `clawker run` to hit UNIQUE collision.

## Decision

Move the registry `Add` call to AFTER `InjectPostInitScript`. Registry row signifies "container fully ready" (not "bootstrapped, may not be ready"). No orphan rows possible since the failure paths run before Add.

## Affected files

| File | Change |
|------|--------|
| `internal/cmd/container/shared/agent_bootstrap.go` | Split `InstallAgentBootstrap` into two functions: `InstallAgentBootstrapMaterial` (mint cert + tar into container, no registry write) and `RegisterAgentInRegistry` (registry write only). OR: keep one function but accept a `skipRegistryWrite` flag, and add a separate `RegisterAgentInRegistry` call. **Prefer the split** — clearer contract. |
| `internal/cmd/container/shared/container_create.go` | Reorder around L1691–1726: 1) `InstallAgentBootstrapMaterial`, 2) `InjectPostInitScript`, 3) `RegisterAgentInRegistry`. ContainerRemove on failure of any of (1)/(2) cleans up cleanly; failure of (3) means container is otherwise fully ready but registry write failed — that's a real recovery problem, log Error and ContainerRemove. |
| `internal/cmd/container/shared/agent_bootstrap.go` doc comment at L220-234 | Rewrite to describe new order. The "any failure past the registry write" comment becomes "any failure before registry write triggers caller cleanup; registry write is the last step". |
| `internal/cmd/container/shared/agent_bootstrap_test.go` | Update tests for split functions. Add: bootstrap-material-success-then-registry-failure → ContainerRemove called, no orphan row. |

## Implementation plan

1. Read `internal/cmd/container/shared/agent_bootstrap.go` `InstallAgentBootstrap` function in full. Identify the boundary: everything up to and including `WriteAgentBootstrapToContainer` is "material delivery"; the `agentregistry.NewSQLiteWriter` + `reg.Add` block is "registry write".
2. Extract two functions:
   - `InstallAgentBootstrapMaterial(ctx, caCertPath, caKeyPath, signingKey, opts) (*AgentBootstrap, error)` — generates + writes material to container, returns the bootstrap struct so the caller has `ExpectedCertThumbprint` for the registry write step. No DB I/O.
   - `RegisterAgentInRegistry(ctx, opts InstallAgentBootstrapOptions, bootstrap *AgentBootstrap) error` — opens sqlite, calls `reg.Add`. Self-contained.
3. Update `container_create.go` flow at the call site:
   ```go
   bootstrap, err := InstallAgentBootstrapMaterial(...)
   if err != nil { /* ContainerRemove + return */ }

   if projectCfg.Agent.PostInit != "" {
       if err := InjectPostInitScript(...); err != nil { /* ContainerRemove + return */ }
   }

   if err := RegisterAgentInRegistry(ctx, opts, bootstrap); err != nil {
       // Container is fully built but we can't track it. Best-effort
       // cleanup; surface the registry-write failure clearly.
       if _, rmErr := client.ContainerRemove(...); rmErr != nil {
           log.Warn().Err(rmErr).Msg("cleanup after registry write failure")
       }
       return nil, fmt.Errorf("register agent in registry: %w", err)
   }
   ```
4. Update package doc comment at L220-234 in agent_bootstrap.go: registry row is now the LAST step, not in the middle. The orphan window has been closed.
5. Update tests in `agent_bootstrap_test.go`:
   - Rename existing `TestInstallAgentBootstrap_*` to `TestInstallAgentBootstrapMaterial_*` for material-delivery paths.
   - Add `TestRegisterAgentInRegistry_*` for the registry write + Add-failure paths.
   - The integration test that exercised "material + registry in one call" should split or delete; the new contract is that the caller composes them.

## Test requirements

- `TestRegisterAgentInRegistry_DBFailure_ReturnsErr` — registry sqlite open fails → error returned, no panic.
- `TestRegisterAgentInRegistry_AddSucceeds_NoErr` — happy path; row persisted.
- `TestInstallAgentBootstrapMaterial_DoesNotTouchRegistry` — verify no sqlite writes during material delivery (use a registry stub that panics on Add).
- Integration in `container_create_test.go` if it exists: verify the full ordered flow.

## Verification

```bash
go build ./...
go test ./internal/cmd/container/shared/... -race -v
make test
```

## Dependencies

- **Task #1** must complete first because it changes `agentregistry.Registry.EvictByContainerID` signature (returns error). Task 02's failure-path `ContainerRemove` should be paired with a logged `EvictByContainerID` call now that the signature surfaces errors.

## Risks / gotchas

- The `agentregistry.NewSQLiteWriter` open is currently inside `InstallAgentBootstrap`. After the split, `RegisterAgentInRegistry` opens it again. Two opens of the same DB by the same process: fine (sqlite driver dedupes connections via the connection pool). But if the call frequency goes up, consider passing a pre-opened Registry into both halves via an opts field.
- The `defer closer.Close()` pattern in `InstallAgentBootstrap` (L264-268) currently catches the registry close. After the split, only `RegisterAgentInRegistry` needs it.
- Don't introduce a circular Registry close: `EnsureSchema` (sqlite.go:102) opens a writer just to apply schema — don't accidentally call it twice during a single create.
- The user explicitly said "cli shouldn't create registry row before the actual container is created" — we DO create container first (resp.ID exists by L1691). The user's concern is specifically about the registry row's lifecycle relative to the bootstrap-then-postinit sequence. This task addresses exactly that.

## Reference reading

- `internal/cmd/container/shared/agent_bootstrap.go` L175-288 (current InstallAgentBootstrap)
- `internal/cmd/container/shared/container_create.go` L1680-1730 (current call site)
- `internal/controlplane/agentregistry/CLAUDE.md` "Writers" section
- Task #1 file (must be complete first — depends on new EvictByContainerID signature)

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
