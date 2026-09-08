---
paths: ["**/*.go"]
---

# Testing rules

Helper APIs, fixtures, tiers, and examples: [TESTING-REFERENCE.md](../docs/TESTING-REFERENCE.md).
Commands and completion checks: [development.md](../docs/development.md).

## Docker integration tests are first-class

Docker is always available. Never defer, skip, or treat Docker-based tests as
optional. When a change touches containers, networks, or volumes, write the
integration test in the same task.

## Test categories

| Category | Directory | Docker | Purpose |
|----------|-----------|:---:|---------|
| Unit | `*_test.go` (co-located) | No | Pure logic, fakes, mocks |
| E2E | `test/e2e/` | Yes | Full-stack integration (firewall, mounts, migrations, presets) |
| Whail | `test/whail/` | Yes+BuildKit | Engine-level image builds |

No build tags; directory separation only. Name tests `TestFunctionName`
(unit), `TestFeature_Integration`, or `TestFeature_E2E`.

## Constraints

1. Each package in the dependency DAG provides its own test utilities. If a node lacks them, add them first.
2. Use unique agent names with random suffixes; parallel tests share one Docker daemon.
3. Stop containers before removing them. Always register cleanup with `t.Cleanup()`.
4. Use `context.Background()` in cleanup functions.
5. Gate Docker tests with `RequireDocker(t)` or `SkipIfNoDocker(t)`.
6. Never discard errors. Log cleanup failures with `t.Logf`.
7. Co-located `*_test.go` files never import `test/e2e/harness`; it pulls in the Docker SDK.
8. Never call `factory.New()` outside `internal/clawkercmd/cmd.go`. Build `&cmdutil.Factory{}` literals with test doubles.
9. Add no production code (variadic options, hooks) only to serve a test seam. Test doubles adapt to production, not the reverse.
