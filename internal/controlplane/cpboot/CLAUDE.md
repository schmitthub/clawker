# CP Bootstrap Package

Host-side orchestration for the clawker control plane container. Split out of `internal/controlplane/` so `cmd/clawker-cp` can import the parent package for `SubprocessManager`, `AdminServer`, `AgentWatcher`, etc. without pulling in the `//go:embed` directives that would require the daemon binary to embed itself during its own build.

## Responsibilities

1. Embed the `clawker-cp` + `ebpf-manager` Linux binaries into the clawker CLI release via `//go:embed`.
2. Build the clawker-cp Docker image on demand from the embedded binaries (multi-stage recipe, pinned digests).
3. Reconcile the `clawker-controlplane` container lifecycle — create, start, health-wait, mount-divergence recreation, stop/remove.
4. Expose a `Manager` interface that wraps the bootstrap functions with lazy Factory closures so `f.ControlPlane()` can be consumed by CLI commands.

## Files

| File | Purpose |
|------|---------|
| `embed_cp.go` | `ClawkerCPBinary []byte` — `//go:embed assets/clawker-cp` |
| `embed_ebpf.go` | `EBPFManagerBinary []byte` — `//go:embed assets/ebpf-manager` |
| `bootstrap.go` | `EnsureRunning(ctx, dc, cfg, log)` / `Stop(ctx, dc)` / `CPRunning(ctx, dc)` host-side lifecycle; `cpImageDockerfile` multi-stage recipe; `ensureCPImage` / `cpBuildContext` image build; `waitForCPHealthz` + `CPHealthTimeoutError` |
| `cp_container.go` | `BuildCPContainerConfig(cfg)` → `*CPContainerConfig` — port bindings, mounts, labels, restart policy (INV-B1-005/006/008/009/015/017/018/020) |
| `manager.go` | `Manager` interface (`EnsureRunning` / `Stop` / `IsRunning` / `ProbeHealthz`) + `NewManager(client, cfg, log)` constructor. Holds lazy Factory closures so callers who never touch the CP never resolve Docker/Config/Logger. |
| `bootstrap_test.go` | Unit tests for `EnsureRunning` happy-path, idempotency, mount-divergence recreation, name-conflict recovery, healthz timeout, concurrent callers (INV-B2-006) |
| `container_config_test.go` | Unit tests asserting `BuildCPContainerConfig` invariants (INV-B1-005/006/008/009/015/017/018/020) |
| `ebpf_regression_test.go` | Port-publishing coverage + CP purpose-label exclusion from `container_map` (INV-B1-017) |
| `mocks/manager_mock.go` | moq-generated `ManagerMock` for CLI tests that drive `controlplane up/down/status` without a real CP |
| `assets/` | **Gitignored.** Holds the pre-compiled Linux binaries produced by `make cp-binary` / `make ebpf-binary`. Never committed — reproduced on every invocation via `Dockerfile.controlplane`. |

## Test seams

Package-level vars in `bootstrap.go` let unit tests stub out side-effecting steps of `EnsureRunning`:

```go
var (
    ensureAuthFn    = auth.EnsureAuthMaterial
    ensureCPImageFn = ensureCPImage
    healthzFn       = waitForCPHealthz
)
```

Tests overwrite these vars, exercise the flow against `dockermocks.FakeClient`, then restore. See `bootstrap_test.go`'s fixture pattern.

## Why the split

`cmd/clawker-cp/main.go` imports `internal/controlplane` for `NewSubprocessManager`, `NewCPStartupOrchestrator`, `NewAdminServer`, `NewAgentWatcher`, etc. If the host-side embeds lived in the same package, Go would evaluate `//go:embed assets/clawker-cp` during stage 6 of `Dockerfile.controlplane` — which is the stage that *builds* `clawker-cp`. The asset file doesn't exist yet inside that stage, so the build fails:

```
internal/controlplane/embed_cp.go: pattern assets/clawker-cp: no matching files found
```

By moving the embeds + bootstrap + container config + Manager into this leaf subpackage, `cmd/clawker-cp` can still import the parent `controlplane` package for daemon-side symbols without pulling in the circular embed directives. `cpboot` is imported only by the host-side CLI (`internal/cmd/factory`, `internal/cmd/controlplane`, `internal/cmd/firewall`, `internal/cmd/container/shared`) and by the E2E test harness.

## Package imports

**Uses**: `internal/auth`, `internal/config`, `internal/consts`, `internal/controlplane/firewall` (for `fwcp.EnvoyStackName` etc.), `internal/docker`, `internal/logger`, `pkg/whail`, `github.com/moby/moby/api/types/{container,mount,network}`.

**Used by**: `internal/cmdutil` (Factory field type), `internal/cmd/factory/default.go` (`ensureRunning` seam + `controlPlaneFunc`), `internal/cmd/controlplane/{up,down,status}.go`, `internal/cmd/firewall/{down,status}.go` (`CPRunning` short-circuit), `test/e2e/harness/factory.go`, `test/e2e/cp_*_test.go`.

**Does NOT import** `internal/controlplane` — no circular dependency.
