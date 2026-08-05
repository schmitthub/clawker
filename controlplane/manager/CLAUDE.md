# CP Bootstrap Package

Host-side orchestration for the clawker control plane container. Split out of `internal/controlplane/` so `cmd/clawkercp` can import the parent package for `SubprocessManager`, `AdminServer`, `AgentWatcher`, etc. without pulling in the `//go:embed` directives that would require the daemon binary to embed itself during its own build.

## Responsibilities

1. Embed the `clawkercp` + `ebpf-manager` Linux binaries into the clawker CLI release via `//go:embed`.
2. Build the clawkercp Docker image on demand from the embedded binaries (multi-stage recipe, pinned digests).
3. Reconcile the `clawker-controlplane` container lifecycle — create, start, health-wait, stop/remove. Drift gate: adopt when `consts.LabelCPBinarySHA` matches the host binary's embedded clawkercp + ebpf-manager hash; force-remove + recreate on any mismatch (including legacy containers without the label). Cross-process race recovery (Docker 409) compares `consts.LabelImageCreated` timestamps — peer-newer adopts, ours-newer replaces, equal ties to peer (favors stability). Mount spec is not inspected: mounts derive from compile-time constants, so any drift implies a host rebuild caught by the SHA. Clawker is single-host by design; cross-machine concurrent bootstrap is not supported.
4. Expose a `Manager` interface over already-resolved dependencies so `f.ControlPlane(ctx)` can hand CLI commands a mockable CP lifecycle noun.

## Files

| File | Purpose |
|------|---------|
| `embed_cp.go` | `ClawkerCPBinary []byte` — `//go:embed assets/clawkercp` |
| `embed_ebpf.go` | `EBPFManagerBinary []byte` — `//go:embed assets/ebpf-manager` |
| `embed_bpffs.go` | `BPFFSDelegateBinary []byte` — `//go:embed assets/bpffs-delegate`. The one embed that never goes into a container image: it runs on the Docker host under sudo to complete BPF filesystem setup on a rootless daemon. Staged and run by the CLI-side SOS assist (`internal/cmd/controlplane/shared`); the embed itself only carries the bytes. Excluded from `cpBinaryHash` — it is not part of the CP image, so it must not drive CP container drift. |
| `bootstrap.go` | `ensureRunning(ctx, EnsureOpts) error` — the bringup itself, driven by `Manager.Start` (`EnsureOpts` carries the already-resolved `Docker`/`Config`/`Logger` plus `HostDirs`; a nil logger degrades to `logger.Nop`). Steps: `reconcileExistingCP` (find, adopt on matching `consts.LabelCPBinarySHA`, else `refuseUpgradeWhileActive` then force-remove), `placeCPOnNetwork`, `createCPContainer`, `awaitCPReady` (the clock-sync step is a readiness gate, not a value source — it blocks until host↔CP clocks align and surfaces no offset). Also `Stop(ctx, dc)` / `CPRunning(ctx, dc)` host-side lifecycle. Drift gate: `cpBinaryHash` + `consts.LabelCPBinarySHA`. Image build: `cpImageDockerfile` recipe with content-derived tag (`cpImageRef`) and OCI provenance LABELs; `ensureCPImage` / `cpBuildContext`; `pruneStaleCPImages` post-build cleanup. Concurrent-bootstrap recovery: `recoverFromNameConflict` resolves Docker 409 via SHA match → image-creation-time ordering (`cpImageCreatedAt`) → retry sentinel `errCPRecoveryRetry`. Readiness gate: `cpReady` = `waitForCPHealthz` (typed errors: `CPHealthTimeoutError` on budget expiry — carrying last probe + container-lookup diagnostics — plus the fail-fast `CPExitedError` / `CPGoneError` when the CP container terminally exits or disappears mid-wait, via `cpTerminalError`) then `waitForCPClockSync` (polls `adminclient.ProbeCPTime`). |
| `cp_container.go` | `BuildCPContainerConfig(cfg, CPContainerOpts)` → `*CPContainerConfig` — port bindings, mounts, labels, restart policy (INV-B1-005/006/008/009/015/017/018/020); defines `HostDirs{Config,Data,State,Cache}` + `Validate()`; injects the four `CLAWKER_HOST_*_DIR` env vars so the CP can compute sibling container bind `Mount.Source` values from host-FS paths, plus `consts.EnvCPBinarySHA` carrying the same embedded-binary hash as the `LabelCPBinarySHA` label so `firewall.Stack` can stamp it as a sibling drift label (`stack_build_sha`) — an upgraded CP recreates Envoy/CoreDNS instead of adopting stale ones. Validates the daemon address is bind-mountable (package-local `validateMountableHost` over `clawker.MountableHostSchemes`, `ErrUnsupportedDockerHost`; `ensureRunning` runs the same check earlier, before any image work) and pins `DOCKER_HOST` to the conventional in-container path — the socket mount remaps the host's actual socket there, so the CP daemon (built with `docker.WithEnvHost()`) never consults the mounted host settings for its address |
| `manager.go` | `Manager` interface (`Start` / `Stop` / `IsRunning` / `ProbeHealthz`) + `NewManager(dc, cfg, log)` constructor over already-resolved dependencies — resolution happens where the manager is constructed (`controlPlaneFunc` in `internal/cmd/factory`). `Start` drives `ensureRunning`; a boot the CP cannot finish alone surfaces as `*CPSOSError`, and acting on it is the caller's job (see `internal/cmd/controlplane/shared.AssistSOS`). |
| `bootstrap_test.go` | Unit tests for `ensureRunning` happy-path, idempotency, existing-stopped start-without-recreate, name-conflict recovery, healthz timeout, exited/removed-container fail-fast (`TestCPTerminalError`, `TestWaitForCPHealthz_ExitedContainer_FailsFast`), concurrent callers (INV-B2-006) |
| `clocksync_test.go` | Unit tests for `waitForCPClockSync`: caught-up on first probe, convergence after drift/retries, non-convergence within the timeout returns an error |
| `container_config_test.go` | Unit tests asserting `BuildCPContainerConfig` invariants (INV-B1-005/006/008/009/015/017/018/020) |
| `ebpf_regression_test.go` | Port-publishing coverage + CP purpose-label exclusion from `container_map` (INV-B1-017) |
| `mocks/manager_mock.go` | moq-generated `ManagerMock` for CLI tests that drive `controlplane up/down/status` without a real CP |
| `assets/` | **Gitignored.** Holds the pre-compiled Linux binaries produced by `make cp-binary` / `make ebpf-binary` (plain `GOOS=linux` cross-compile after `make ebpf` stages the bpf2go bindings). Never committed. |

## Test seams

Package-level vars in `bootstrap.go` let unit tests stub out side-effecting steps of `ensureRunning`:

```go
var (
    ensureAuthFn    = auth.EnsureAuthMaterial
    ensureCPImageFn = ensureCPImage
    healthzFn       = waitForCPHealthz
    clockSyncFn     = waitForCPClockSync          // host↔CP clock-sync gate
    probeCPTimeFn   = adminclient.ProbeCPTime  // single GetSystemTime probe
    watchSOSFn      = watchSOS                 // boot-time WatchSOS stream watcher
)
```

Tests overwrite these vars, exercise the flow against `dockermocks.FakeClient`, then restore. See `bootstrap_test.go`'s fixture pattern. The fixture stubs `clockSyncFn` (real impl dials the CP's `GetSystemTime`) and counts invocations so both readiness exits assert the gate ran.

### Readiness gate: `awaitCPReady` = SOS watch ⊕ (`/healthz` + clock sync)

Both `ensureRunning` success exits (adopt-existing and freshly-created) return `awaitCPReady(ctx, dc, cfg, log)`, which runs two things concurrently from the moment the CP container is running: the readiness gate `cpReady` (`healthzFn` then `clockSyncFn`) and the SOS watch (`watchSOSFn` → `watchSOS`). The watch spams `WatchSOS` connection attempts — dial, open the stream, block on receive; any failure retries after `sosRetryInterval`, no pre-checks before dialing — for the shared `consts.CPSOSIdleTTL` window (the same clock after which an unwatched CP holding a recoverable failure shuts itself down, so neither side outlives the other). An SOS delivered mid-wait wins and surfaces as `*CPSOSError` carrying the CP's own `Kind` and `Message` — `Kind` (`adminv1.SOSKind`) is what assistance dispatches on, `Message` is the human prose to surface for a kind nothing handles, and the CLI never parses the message to decide; a watch that ends with nothing to deliver (clean end-of-stream, window expired, ctx cancelled) leaves the outcome to the readiness gate alone. **Acting on that error is not `Manager.Start`'s job** — `Start` reports the outcome and stops there. The CP bootstrap verbs (`controlplane up`, `firewall up`, the container-start bootstrap) each catch the `*CPSOSError` at their own call site, hand it to `internal/cmd/controlplane/shared.AssistSOS` (which dispatches on `Kind`), and call `Start` again once the assistance lands (idempotent, so the readiness gate restarts with a fresh budget instead of charging a human's password-typing against it). Streaming calls carry the bearer token via `tokenSource.streamInterceptor` in `adminclient.Dial` — grpc-go's unary and streaming interceptor chains are disjoint, and a dropped stream never reconnects itself (the watch loop re-establishes).

`waitForCPHealthz` is firewall-aware in two ways. (1) When `firewall.enable` (settings.yaml) is true, the wait budget extends from `cpReadyTimeout` by `consts.FirewallStackBringupRPCTimeout` — the CP gates `SetReady` on the firewall stack bringup (the settings-driven gate in `cmd/clawkercp/main.go`), so image pull/build + container create + stack health all happen before `/healthz` turns green. (2) On a transport-level probe failure it inspects the CP container (throttled to once per second, via `cpExitedError`): a terminally exited, not-restarting container — the shape a failed firewall startup gate produces by design (exit code 1) — aborts the wait immediately with a diagnostic error naming the container and pointing at its docker logs, instead of burning the remaining budget on a generic timeout. Inspect errors and mid-restart states keep polling. The clock-sync step polls `adminclient.ProbeCPTime` until the CP clock is no longer behind the host (`!hostTime.After(cpTime)`, i.e. `cpTime >= hostTime` — at or after the host's now, where `hostTime` is sampled *before* the probe so the round-trip latency isn't charged against the CP) or `cpClockSyncTimeout` (30s) — a Docker Desktop VM clock that lagged during host sleep converges once it re-syncs to the host, and CP is not considered "running" until host↔CP clocks align. This is the every-start precondition that lets clawkerd safely exchange its (host-clock-minted) agent assertion: by the time the container starts, the CP clock has been confirmed converged with the host, so a host-domain `iat` is not in the CP's future (which would be a zero-leeway "token used before issued" → poisoned, non-re-mintable bootstrap material). The gate's value is the *wait* — `ensureRunning` returns only `error`, no offset. On non-convergence within `cpClockSyncTimeout` it returns a `cp clock sync deadline exceeded` error.

**Drift observability.** `waitForCPClockSync` takes the `*logger.Logger` and logs the convergence loop under `component=cpboot.clocksync`: `cp_clock_sync` (Info, loop start), `cp_clock_probe` (Info, each retry — carries the compared `hostTime`/`cpTime`), `cp_clock_converged` (Info — `hostTime`/`cpTime`/`cp_sub_delta`), and `cp_clock_sync_timeout` (Error, deadline). The compared timestamps live in the message text, so a lagging VM clock is visible in the file log without raising the level.

**Single gate.** This is the *only* host↔CP clock-sync wait. The CLI's admin-client dial path (`internal/controlplane/adminclient`) no longer re-checks the clock before minting its OAuth token — `Manager.Start` is the precondition for any CP interaction, so the readiness gate here covers it once and the token mint just signs and exchanges.

## Why the split

`cmd/clawkercp/main.go` imports `internal/controlplane` for `NewSubprocessManager`, `NewCPStartupOrchestrator`, `NewAdminServer`, `NewAgentWatcher`, etc. If the host-side embeds lived in the same package, Go would evaluate `//go:embed assets/clawkercp` during stage 6 of `Dockerfile.controlplane` — which is the stage that *builds* `clawkercp`. The asset file doesn't exist yet inside that stage, so the build fails:

```
internal/controlplane/embed_cp.go: pattern assets/clawkercp: no matching files found
```

By moving the embeds + bootstrap + container config + Manager into this leaf subpackage, `cmd/clawkercp` can still import the parent `controlplane` package for daemon-side symbols without pulling in the circular embed directives. This package is imported only by the host-side CLI (`internal/cmdutil`, `internal/cmd/factory`, `internal/cmd/controlplane` + its `shared/`, `internal/cmd/firewall`, `internal/cmd/container/{run,start,restart,shared}`) and by the E2E test harness.

## Package imports

**Uses**: `internal/auth`, `internal/config`, `internal/consts`, `internal/controlplane/adminclient` (for `ProbeCPTime` in the clock-sync gate), `internal/controlplane/firewall` (for `fwcp.DiscoverNetwork` / `fwcp.ComputeStaticIP`), `internal/docker`, `internal/logger`, `pkg/whail`, `github.com/moby/moby/api/types/{container,mount,network}`.

**Used by**: `internal/cmdutil` (Factory field type), `internal/cmd/factory/default.go` (`controlPlaneFunc`), `internal/cmd/controlplane/{up,down,status}.go`, `internal/cmd/firewall/{up,down,status}.go` (`CPRunning` short-circuit in `down`/`status`), `internal/cmd/container/{run,start,restart}` + `internal/cmd/container/shared` (CP lifecycle before container ops), `test/e2e/harness/factory.go`, `test/e2e/controlplane_cli_test.go`.

**Does NOT import** `internal/controlplane` — no circular dependency.

## Host path injection into the CP

The CP runs inside the `clawker-controlplane` container with `CLAWKER_CONFIG_DIR` / `CLAWKER_DATA_DIR` pointing at container-local paths (`/etc/clawker/config`, `/usr/local/share/clawker`). Those paths are bind-mounted from the host XDG dirs — writes from the CP land on the host — but Docker-outside-of-Docker calls that spawn Envoy/CoreDNS siblings require **host-FS** `Mount.Source` values, not container-local ones.

`EnsureOpts.HostDirs` (required, validated in `HostDirs.Validate`) carries the host-resolved `Config` / `Data` / `State` / `Cache` dirs through `BuildCPContainerConfig`. They get serialized onto the CP container's env as `CLAWKER_HOST_{CONFIG,DATA,STATE,CACHE}_DIR`. The `internal/consts/controlplane.go` package then exposes `HostConfigDir` / `HostDataDir` / `HostStateDir` / `HostCacheDir` package vars (plus composed `HostFirewallDataSubdir` / `HostFirewallCertSubdir` / `HostEnvoyConfigPath` / `HostCorefilePath`) for the firewall Stack to read when it builds sibling container specs.

`Manager.Start` resolves `HostDirs` via `consts.{ConfigDir,DataDir,StateDir,CacheDir}()` (host-side) when it invokes `ensureRunning`. Unit tests use the `testHostDirs()` helper in `bootstrap_test.go`; Stack unit tests override the `consts.Host*` package vars directly via `t.Cleanup`-scoped helpers because package init happens before `testenv` sets the env vars.
