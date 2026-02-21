# Refactor: Eliminate Hardcoded Paths and Env Var Names

## Status: COMMITTED & PUSHED — Pending Audit Sweep

Commit `42ba559` on branch `refactor/configapocalypse`, pushed to origin.

## What Was Done

Added `ProjectConfigFileName()`, `SettingsFileName()`, `ProjectRegistryFileName()` to the `Config` interface so callers use accessors instead of hardcoded strings like `"clawker.yaml"`, `"projects.yaml"`, or `"CLAWKER_CONFIG_DIR"`.

### Files Modified (18 total)

| File | Change |
|---|---|
| `internal/config/config.go` | Added 3 methods to Config interface |
| `internal/config/consts.go` | Added 3 method implementations on configImpl; renamed `clawkerConfigFileName` → `clawkerProjectConfigFileName` |
| `internal/config/export_test.go` | Updated ForTest constant to use renamed internal constant |
| `internal/config/mocks/config_mock.go` | Regenerated via `go generate ./internal/config/` |
| `internal/config/mocks/stubs.go` | Wired 3 new mock Func fields |
| `internal/config/config_test.go` | Replaced 6 hardcoded `"settings.yaml"`/`"projects.yaml"` with ForTest constants |
| `internal/config/CLAUDE.md` | Added "Filename accessors" documentation section |
| `internal/docker/client.go` | `"clawker-net"` → `c.cfg.ClawkerNetwork()` |
| `internal/project/registry.go` | `"projects.yaml"` → `r.cfg.ProjectRegistryFileName()` |
| `internal/cmd/config/check/check.go` | Fixed line 68 to use param; wired `opts.Config = f.Config` in NewCmdCheck |
| `internal/cmd/config/check/check_test.go` | Added `Config: blankConfigProvider()` to all CheckOptions; fixed broken unknownFields test; added 2 new tests |
| `internal/cmd/project/init/init.go` | `"clawker.yaml"` → `cfgGateway.ProjectConfigFileName()` |
| `internal/cmd/project/register/register.go` | `"clawker.yaml"` → `cfgGateway.ProjectConfigFileName()` |
| `test/harness/harness.go` | Used `configmocks.NewBlankConfig()` for env var names + filenames (3 locations) |
| `test/agents/run_test.go` | Used configmocks accessor for env var name (2 locations) |
| `test/commands/worktree_test.go` | Used accessor for `"projects.yaml"` |
| `internal/project/register_test.go` | Used `cfg.ProjectRegistryFileName()` |
| `internal/project/worktree_test_helpers_test.go` | Used `cfg.ProjectRegistryFileName()` |

### Test Results
- `go build ./...` passes
- 0 new test failures (13 pre-existing failures unchanged)
- All affected packages pass: config, project, docker, cmd/config/check, test/harness

### Pre-existing Failures (not caused by this refactor)
- `internal/cmd/container/create`, `run`, `shared`, `start` — nil pointer panics (missing Config/TUI wiring in tests)
- These are tracked separately

### Lessons Learned
- `check.go` had `resolveConfigTarget(clawkerConfigFileName, filePath)` signature but tests only passed 1 arg — needed to add the first arg to all test calls
- `NewCmdCheck` didn't wire `opts.Config = f.Config` — needed to add it since `checkRun` now calls `cfg.ProjectConfigFileName()`
- `TestCheckRun_unknownFields` was already failing before changes — `ReadFromString` uses `UnmarshalExact` which rejects unknown keys, contradicting the old test's "silently ignores" comment

### Plan file reference
Original plan transcript: `/Users/andrew/.claude/projects/-Users-andrew-Code-clawker/a7cb77cc-bb7c-4202-890c-c6ccb5e035f7.jsonl`

## TODO Sequence

- [x] Step 1: Add 3 new methods to Config interface + implementations in consts.go
- [x] Step 2: Regenerate moq mock + wire stubs
- [x] Step 3: Fix production code (docker/client, project/registry, check, init, register)
- [x] Step 4: Fix test infrastructure (harness, agents, commands, project tests, config_test)
- [x] Step 5: Update CLAUDE.md docs, add new tests for unknown field rejection
- [x] Step 6: Verify build + tests (0 regressions)
- [x] Step 7: Commit and push
- [x] **Step 8: AUDIT SWEEP** — Completed. All 18 modified files audited + straggler sweep.
- [x] **Step 9: FIX STRAGGLERS** — Fixed test isolation + doc accessor references.

### Step 8+9 Findings & Fixes

**Test isolation fix:**
- `test/commands/worktree_test.go` — `newWorktreeTestFactory` was missing `CLAWKER_STATE_DIR`/`CLAWKER_DATA_DIR` isolation. Added `t.Setenv` for both via `configmocks.NewBlankConfig()` accessors.
- `test/harness/harness_test.go:260` — Replaced hardcoded `"clawker.yaml"` with `_blankCfg.ProjectConfigFileName()`.

**Developer doc fixes (accessor cross-references added):**
- `CLAUDE.md` — Configuration section now shows `cfg.SettingsFileName()`, `cfg.ProjectRegistryFileName()`, `cfg.ProjectConfigFileName()` in headings + added accessor guidance block.
- `internal/config/CLAUDE.md` — File loading section uses accessor names; added `DataDir`/`StateDir` env var documentation.
- `internal/project/CLAUDE.md` — `projects.yaml` → `cfg.ProjectRegistryFileName()`.
- `internal/cmd/project/CLAUDE.md` — Both subcommand descriptions use accessors incl. `cfg.ClawkerIgnoreName()`.
- `internal/cmd/init/CLAUDE.md` — Uses `cfg.SettingsFileName()` + `config.ConfigDir()`.
- `.claude/docs/DESIGN.md` — Config files table replaced with accessor+default table; registry lifecycle + persistence model updated.
- `.claude/docs/CLI-VERBS.md` — 8 references updated to use accessors.
- `.claude/docs/ARCHITECTURE.md` — Write target resolution uses accessor.

**Not changed (intentionally):**
- User-facing docs (`docs/*.mdx`, `README.md`, `examples/llm.md`) — must show actual filenames for users.
- `internal/docs/yaml_test.go` — Tests doc generation filenames derived from cobra command names, unrelated to config.
- `internal/config/config_test.go:974` — Tests the accessor itself, hardcoded expected value is correct.
- `.claude/rules/code-style.md` — Already directs agents to use accessors.

## Status: COMPLETE

All TODO items done. This memory can be deleted when the branch is merged.

---

**IMPERATIVE**: Always check with the user before proceeding with the next todo item. If all work is done, ask the user if they want to delete this memory.
