# CP Bootstrap Package

Host-side orchestration for the clawker control plane container. Split out of `internal/controlplane/` so `cmd/clawker-cp` can import the parent package for `SubprocessManager`, `AdminServer`, `AgentWatcher`, etc. without pulling in the `//go:embed` directives that would require the daemon binary to embed itself during its own build.

## Responsibilities

1. Embed the `clawker-cp` + `ebpf-manager` Linux binaries into the clawker CLI release via `//go:embed`.
2. Build the clawker-cp Docker image on demand from the embedded binaries (multi-stage recipe, pinned digests).
3. Reconcile the `clawker-controlplane` container lifecycle — create, start, health-wait, stop/remove. Drift gate: adopt when `consts.LabelCPBinarySHA` matches the host binary's embedded clawker-cp + ebpf-manager hash; force-remove + recreate on any mismatch (including legacy containers without the label). Cross-process race recovery (Docker 409) compares `consts.LabelImageCreated` timestamps — peer-newer adopts, ours-newer replaces, equal ties to peer (favors stability). Mount spec is not inspected: mounts derive from compile-time constants, so any drift implies a host rebuild caught by the SHA. Clawker is single-host by design; cross-machine concurrent bootstrap is not supported.
4. Expose a `Manager` interface that wraps the bootstrap functions with lazy Factory closures so `f.ControlPlane()` can be consumed by CLI commands.

## Files

| File | Purpose |
|------|---------|
| `embed_cp.go` | `ClawkerCPBinary []byte` — `//go:embed assets/clawker-cp` |
| `embed_ebpf.go` | `EBPFManagerBinary []byte` — `//go:embed assets/ebpf-manager` |
| `bootstrap.go` | `EnsureRunning(ctx, EnsureOpts)` / `Stop(ctx, dc)` / `CPRunning(ctx, dc)` host-side lifecycle; `EnsureOpts` bundles `Docker` / `Config` / `Logger` / `HostDirs`. Drift gate: `cpBinaryHash` + `consts.LabelCPBinarySHA`. Image build: `cpImageDockerfile` recipe with content-derived tag (`cpImageRef`) and OCI provenance LABELs; `ensureCPImage` / `cpBuildContext`; `pruneStaleCPImages` post-build cleanup. Concurrent-bootstrap recovery: `recoverFromNameConflict` resolves Docker 409 via SHA match → image-creation-time ordering (`cpImageCreatedAt`) → retry sentinel `errCPRecoveryRetry`. Healthz: `waitForCPHealthz` + `CPHealthTimeoutError`. |
| `cp_container.go` | `BuildCPContainerConfig(cfg, CPContainerOpts)` → `*CPContainerConfig` — port bindings, mounts, labels, restart policy (INV-B1-005/006/008/009/015/017/018/020); defines `HostDirs{Config,Data,State,Cache}` + `Validate()`; injects the four `CLAWKER_HOST_*_DIR` env vars so the CP can compute sibling container bind `Mount.Source` values from host-FS paths |
| `manager.go` | `Manager` interface (`EnsureRunning` / `Stop` / `IsRunning` / `ProbeHealthz`) + `NewManager(client, cfg, log)` constructor. Holds lazy Factory closures so callers who never touch the CP never resolve Docker/Config/Logger. |
| `bootstrap_test.go` | Unit tests for `EnsureRunning` happy-path, idempotency, existing-stopped start-without-recreate, name-conflict recovery, healthz timeout, concurrent callers (INV-B2-006) |
| `container_config_test.go` | Unit tests asserting `BuildCPContainerConfig` invariants (INV-B1-005/006/008/009/015/017/018/020) |
| `ebpf_regression_test.go` | Port-publishing coverage + CP purpose-label exclusion from `container_map` (INV-B1-017) |
| `mocks/manager_mock.go` | moq-generated `ManagerMock` for CLI tests that drive `controlplane up/down/status` without a real CP |
| `assets/` | **Gitignored.** Holds the pre-compiled Linux binaries produced by `make cp-binary` / `make ebpf-binary` (plain `GOOS=linux` cross-compile after `make ebpf` stages the bpf2go bindings). Never committed. |

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

**Used by**: `internal/cmdutil` (Factory field type), `internal/cmd/factory/default.go` (`ensureRunning` seam + `controlPlaneFunc`), `internal/cmd/controlplane/{up,down,status}.go`, `internal/cmd/firewall/{down,status}.go` (`CPRunning` short-circuit), `test/e2e/harness/factory.go`, `test/e2e/controlplane_cli_test.go`.

**Does NOT import** `internal/controlplane` — no circular dependency.

## Host path injection into the CP

The CP runs inside the `clawker-controlplane` container with `CLAWKER_CONFIG_DIR` / `CLAWKER_DATA_DIR` pointing at container-local paths (`/etc/clawker/config`, `/usr/local/share/clawker`). Those paths are bind-mounted from the host XDG dirs — writes from the CP land on the host — but Docker-outside-of-Docker calls that spawn Envoy/CoreDNS siblings require **host-FS** `Mount.Source` values, not container-local ones.

`EnsureOpts.HostDirs` (required, validated in `HostDirs.Validate`) carries the host-resolved `Config` / `Data` / `State` / `Cache` dirs through `BuildCPContainerConfig`. They get serialized onto the CP container's env as `CLAWKER_HOST_{CONFIG,DATA,STATE,CACHE}_DIR`. The `internal/consts/controlplane.go` package then exposes `HostConfigDir` / `HostDataDir` / `HostStateDir` / `HostCacheDir` package vars (plus composed `HostFirewallDataSubdir` / `HostFirewallCertSubdir` / `HostEnvoyConfigPath` / `HostCorefilePath`) for the firewall Stack to read when it builds sibling container specs.

CLI callers resolve `HostDirs` via `consts.{ConfigDir,DataDir,StateDir,CacheDir}()` (host-side) before invoking `EnsureRunning`. Unit tests use the `testHostDirs()` helper in `bootstrap_test.go`; Stack unit tests override the `consts.Host*` package vars directly via `t.Cleanup`-scoped helpers because package init happens before `testenv` sets the env vars.
