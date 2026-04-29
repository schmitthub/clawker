# Task 03 — cmd/controlplane/agents: remove DBPath field

**Status**: pending
**Claimed by**: —
**Blocks**: 11
**Blocked by**: 01

## Findings covered

- **C6** — `internal/cmd/controlplane/agents.go:32-33,55` — `opts.DBPath = consts.ControlPlaneDBPath` is a function ref to a package-level function carried on `AgentsOptions`. Bad precedent for future `controlplane *` commands.

## Decision (corrected from review session via user pushback)

**Remove `DBPath` field from `AgentsOptions` entirely.** Call `consts.ControlPlaneDBPath()` directly inside `agentsRun`. Tests use `testenv.New(t)` which sets `CLAWKER_DATA_DIR` via `t.Setenv` — `consts.ControlPlaneDBPath()` then auto-resolves to the temp path.

Rationale: `consts.ControlPlaneDBPath()` is a `consts` accessor, not a stateful resource that needs Factory DI. Tests don't need to mock it; they just need an isolated env where the env-var-aware accessor resolves to a temp dir naturally.

## Affected files

| File | Change |
|------|--------|
| `internal/cmd/controlplane/agents.go` | Delete `DBPath func() (string, error)` field from `AgentsOptions` struct (~L33). Delete the `DBPath: consts.ControlPlaneDBPath,` wiring inside `NewCmdAgents` (~L55). Inside `agentsRun`, replace `opts.DBPath()` calls with `consts.ControlPlaneDBPath()` directly. |
| `internal/cmd/controlplane/agents_test.go` | Remove all `opts.DBPath = func() (string, error) { return tmpPath, nil }` overrides. Replace with `testenv.New(t)` at the top of each test — the env-var setup makes `consts.ControlPlaneDBPath()` resolve to the testenv's `data/` subdir automatically. Use `agentregistry.NewSQLiteWriter(consts.ControlPlaneDBPath())` (or whatever helper) to seed test data. |
| `internal/cmd/controlplane/CLAUDE.md` (if updated by Task #4 or #6 first) | No change required for this task. |

## Implementation plan

1. Read `internal/cmd/controlplane/agents.go` in full. Note every site that calls `opts.DBPath()`.
2. Read `internal/cmd/controlplane/agents_test.go` in full. Note every test that sets `opts.DBPath` and what value it injects.
3. Edit agents.go:
   - Remove `DBPath` field from `AgentsOptions`.
   - Remove `DBPath: consts.ControlPlaneDBPath,` from `NewCmdAgents`.
   - In `agentsRun`, change `path, err := opts.DBPath()` to `path, err := consts.ControlPlaneDBPath()`. Adjust error wrapping.
4. Edit agents_test.go — for each test:
   - At top: `env := testenv.New(t)` (no options needed; just env-var isolation).
   - Construct the registry path via `consts.ControlPlaneDBPath()` inside the test (it now reads the testenv-set `CLAWKER_DATA_DIR`).
   - Use `agentregistry.NewSQLiteWriter(path, logger.Nop())` (or `EnsureSchema`) to seed rows.
   - Drop the `opts.DBPath = ...` line entirely.
5. Update tests that import unused packages (the `consts` import on agents_test.go may now be unused — clean up).
6. Verify no other callers reference `AgentsOptions.DBPath`. Run `grep -rn 'opts.DBPath\|\.DBPath' internal/cmd/controlplane/`.

## Test requirements

Existing tests rewritten per above. No new test categories required — this is a structural refactor that preserves behavior.

Verify each test:
- Still seeds the same fixture data
- Still asserts the same output
- Now uses testenv for isolation instead of options injection

## Verification

```bash
go build ./...
go vet ./internal/cmd/controlplane/...
go test ./internal/cmd/controlplane/... -race -v

# Confirm DBPath field is gone
grep -rn 'DBPath' internal/cmd/controlplane/
# Should return zero matches (or only in legitimate non-options contexts)

make test
```

## Dependencies

- **Task #1** must complete first if it changes the `agentregistry.NewSQLiteWriter` or `NewSQLiteReader` signatures (which it might — check Task #1's resolution).
- This task does not block any other code-change task; it does block **Task #11** (close swallows) which touches the same file (`agents.go:120-124`).

## Risks / gotchas

- **`consts.ControlPlaneDBPath()` ensures the parent subdir exists**. Reading `internal/consts/consts.go:486` — it calls `ControlPlaneSubdir()` which calls `subdirPath(controlPlaneDir, DataDir)` which `os.MkdirAll`s the path. So tests don't need to pre-create the subdir; first call creates it. Good.
- **testenv sets `CLAWKER_DATA_DIR`** via `t.Setenv`. `consts.DataDir()` reads it at call time (NOT at init). Verified at `internal/consts/consts.go:404-405`. So `consts.ControlPlaneDBPath()` from inside a `testenv.New(t)` test resolves correctly without extra setup.
- **`runF` trapdoor pattern**: existing tests may use `NewCmdAgents(f, runF)` with a captured-opts `runF` to inspect the wired Options struct. After this change, the inspection no longer sees `opts.DBPath` (it's gone). Update those assertions.
- **Don't reintroduce a Factory noun** for this. The user's pushback was explicit: it's just a consts accessor; tests use real fs testenv.
- **The `runF` parameter on `NewCmdAgents`** is an existing test seam — keep it.

## Reference reading

- `internal/cmd/controlplane/agents.go` (current implementation)
- `internal/cmd/controlplane/CLAUDE.md` (package conventions)
- `internal/testenv/CLAUDE.md` (how testenv isolation works)
- `internal/config/mocks/stubs.go` `NewIsolatedTestConfig(t)` (analogous pattern: testenv + env-var-aware accessor)
- `internal/consts/consts.go:402-492` (`DataDir`, `ControlPlaneSubdir`, `ControlPlaneDBPath`)

## Resolution

(Filled in on completion.)

- Commit SHA:
- Notes:
