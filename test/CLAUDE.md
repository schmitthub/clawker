# Test Package

Test infrastructure for all non-unit tests. Uses directory separation instead of build tags.

## Structure

```
test/
├── e2e/            # End-to-end tests (real Factory, real Docker, full Cobra pipeline)
│   └── harness/    # In-process CLI harness: Factory + root.NewCmdRoot + isolated dirs
├── whail/          # Whail BuildKit integration tests (Docker + BuildKit)
├── cli/            # Testscript-based CLI workflow tests (Docker)
├── commands/       # Command integration tests (Docker)
├── internals/      # Container scripts/services tests (Docker)
└── agents/         # Full agent E2E tests (Docker)
```

## Running Tests

```bash
make test                                        # Unit tests only (no Docker)
go test ./test/whail/... -v -timeout 5m          # Whail BuildKit integration
go test ./test/e2e/... -v -timeout 15m           # E2E (full CLI pipeline, real Docker)
go test ./test/cli/... -v -timeout 15m           # CLI workflow tests
go test ./test/commands/... -v -timeout 10m      # Command integration
go test ./test/internals/... -v -timeout 10m     # Internal integration
go test ./test/agents/... -v -timeout 15m        # Agent E2E
```

## Conventions

- **Golden files**: In `testdata/` next to tests. `GoldenAssert(t, name, actual)`, `CompareGolden(t, name, actual) error`. Update: `GOLDEN_UPDATE=1`
- **Fakes**: `internal/docker/dockertest/`, `pkg/whail/whailtest/`
- **Cleanup**: Always `t.Cleanup()` — never deferred functions
- **Self-cleaning tests**: Tests create their own projects via `h.Run("project", "init", ...)`, build their own images, and clean up their own containers/volumes via `h.Run("container", "stop/rm", ...)`
- **Whail labels**: `test/whail/` uses `com.whail.test.managed=true`; self-contained cleanup

## E2E Philosophy

E2E tests (`test/e2e/`) exercise the **full Cobra command pipeline** via `h.Run()` → `root.NewCmdRoot(factory)`. Every command runs exactly as a user would — through the same root command, flag parsing, validation, and execution path. The only difference from the shipped binary is dependency wiring: tests construct a `cmdutil.Factory` struct literal with real production dependencies (real Docker client, real config, real project manager) plus test isolation (isolated XDG dirs, test labels, `iostreams.Test()` for output capture).

Key principles:
- **No Docker SDK for operations** — all container/image/volume operations through `h.Run("container", ...)`, never moby client calls
- **Config via production flow** — `h.Run("project", "init", "name", "--yes")` scaffolds config, then `config.NewConfig()` + `ProjectStore().Set()` + `.Write()` for mutations
- **Each test is fully isolated** — no shared TestMain, no global state
- **`iostreams.Test()`** returns `*bytes.Buffer` backing stdout/stderr for output capture and assertion
