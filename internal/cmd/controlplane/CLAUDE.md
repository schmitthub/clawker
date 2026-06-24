# internal/cmd/controlplane

Cobra commands for the `clawker controlplane` command group. Break-glass
lifecycle control for the clawker control plane container.

## Why this exists

`f.AdminClient` is a pure dial — it does NOT bootstrap the CP, so admin
commands fail fast when the CP is down. CP lifecycle is owned by a small
set of explicit bootstrap verbs: `container start` (pre-start phase),
`firewall up` (before `FirewallInit`), and the `controlplane up/down/status`
verbs in this package. Day-to-day operators rarely invoke this package —
it exists for debugging, upgrades, and recovery paths where the operator
wants to control the CP lifecycle directly.

## Naming — "domain" is data-layer design talk, never a runtime label

"Domain" (DDD bounded context) is a way to *think* about the data-layer design, not a
thing in the running system. The implementation names sub-components for what they are —
a **Store**, a **Storage Repository** inside a package. Call wiring what it is — the agent
package, the dialer, the watcher, the executor — never "the agent domain", "domain
handlers", or `*Domain` symbols. No numbered `// Phase N` comment scaffolding.

## Contents

| File | Purpose |
|------|---------|
| `controlplane.go` | Parent command `NewCmdControlPlane(f)` — registers `up`/`down`/`status`/`agents` |
| `up.go` | `controlplane up` — wraps `Manager.EnsureRunning` (idempotent); when `firewall.enable` (settings.yaml) is true, also brings the firewall stack up via `firewall.BringUpStack` (idempotent `FirewallInit`) |
| `down.go` | `controlplane down` — `Manager.Stop` (CP container only); no orphan warning — CP drains its own firewall stack on SIGTERM |
| `status.go` | `controlplane status` — `Manager.IsRunning` + `Manager.ProbeHealthz` + best-effort `FirewallStatus` RPC |
| `agents.go` | `controlplane agents` — `AdminClient.ListAgents` snapshot of the agent registry |
| `up_test.go` / `down_test.go` / `status_test.go` / `agents_test.go` | Unit tests driving the run functions through `mocks.ManagerMock` |

## Subcommand Table

| Command | Constructor | Args | Flags | Manager methods |
|---------|-------------|------|-------|-----------------|
| `up` | `NewCmdUp(f, runF)` | none | none | `EnsureRunning`; then, when `firewall.enable` (settings.yaml) is true, `FirewallInit` via `f.AdminClient` (`firewall.BringUpStack`) |
| `down` | `NewCmdDown(f, runF)` | none | none | `IsRunning`, then `Stop` on the running path |
| `status` | `NewCmdStatus(f, runF)` | none | `--format`, `--json`, `--quiet` | `IsRunning`, `ProbeHealthz`; plus best-effort `FirewallStatus` via `f.AdminClient` |
| `agents` | `NewCmdAgents(f, runF)` | none | `--format`, `--json`, `--quiet` | none (uses `f.AdminClient` → `ListAgents`) |

## Factory dependency

Every verb here reaches the CP lifecycle through one noun: `f.ControlPlane()
cpboot.Manager`. The `Manager` interface lives in `internal/controlplane/
cpboot/manager.go` and is wired in `internal/cmd/factory/default.go` via
`controlPlaneFunc(f)` (a `sync.Once`-cached closure that calls
`cpboot.NewManager(f.Client, f.Config, f.Logger)`).

No package-level seams. Tests inject test doubles by overriding Factory
closures on the per-test `testBed`: a `*mocks.ManagerMock` on
`tb.F.ControlPlane` (always), plus — for the `up` firewall paths — a
`ConfigMock` on `tb.F.Config` (`withSettings`), an
`AdminServiceClientMock` on `tb.F.AdminClient` (`withAdminMock`), and a
`dockermocks.FakeClient` on `tb.F.Client` (`withDockerFake`). Each test
programs only the methods it exercises, so an unexpected call to an
unprogrammed method panics — that's the assertion for paths that should
short-circuit.

## `controlplane up` and firewall bringup

`firewall.enable` (settings.yaml) means the firewall stack should be up
whenever the CP is. Two cooperating mechanisms deliver that:

1. **CP-side (every boot, startup gate)**: the CP daemon reads settings
   at startup and, when enabled, runs the in-process `FirewallInit`
   synchronously BEFORE `SetReady` (the settings-driven firewall
   bringup gate in `cmd/clawkercp/main.go`).
   A bringup failure fails CP startup (exit code 1) — fail-closed and
   loud, never silently unenforced. `/healthz` green therefore implies
   the stack is up when the firewall is enabled, and the host-side
   `cpboot` healthz wait extends its budget accordingly (and fail-fasts
   with a diagnostic error if the CP container terminally exits). Covers
   CP boots no CLI observes (restart policy, container-start bootstrap).
2. **CLI-side (idempotent path)**: `upRun` loads config after
   `EnsureRunning` and, when enabled, dials `f.AdminClient` and calls
   `firewall.BringUpStack` — the same spinner + shared-deadline +
   exposure-warning UX as `firewall up`. This covers the case where the
   CP was already running with the stack down (e.g. after `firewall
   down`); on a fresh boot it is a fast idempotent no-op because the
   startup gate already brought the stack up.

When `firewall.enable` is false the verb never starts the stack —
bringing up a stack the user disabled would be a policy violation. It
does, however, check (via `f.Client`, advisory — lookup failures warn,
never fail the verb) whether a previously-started Envoy/CoreDNS sibling
is still running, and prints a stderr warning pointing at
`clawker firewall down` when settings say off but the stack is still
enforcing. A failed stack bringup returns an error (and prints the
stack-down exposure warning) even though the CP itself is up.

## `controlplane down` and firewall teardown (INV-B2-008, reworked)

The CP owns Envoy and CoreDNS lifecycle end-to-end. `controlplane down`
does a single thing — `docker stop clawker-controlplane` — which sends
SIGTERM to PID 1 inside the CP (`cmd/clawkercp/main.go`). The CP's
SIGTERM handler converges on the same `drainCallback` as the
drain-to-zero path via `sync.Once`, so the teardown runs exactly once
regardless of which trigger fires:

1. `actionQueue.Close` drains accepted submissions.
2. `grpcServer.GracefulStop` retires in-flight RPCs.
3. `handler.CancelAllBypassTimers` stops dead-man timers.
4. `stack.Stop` removes the Envoy + CoreDNS containers from the clawker network.
5. `ebpfMgr.FlushAll` wipes `container_map` + `bypass_map` so a
   subsequent `controlplane up` starts with a clean per-container state.

`down` itself is therefore minimal:
1. Short-circuits with an `InfoIcon` message if `Manager.IsRunning`
   returns false — avoids spinning a CP up just to turn it off.
2. On successful `Manager.Stop`, writes a success line to stdout. There
   is deliberately no orphan-firewall warning — the CP cleans up after
   itself, and any residual warning text would be a bug indicator, not
   a feature.

## `controlplane status` tolerance model

`status` short-circuits before touching `f.AdminClient` when
`Manager.IsRunning` reports false — no point dialing a CP that isn't
there. When the CP is present, the command:

1. Calls `Manager.ProbeHealthz` — a dedicated 2-second-budget point-in-time
   probe (separate from the `EnsureRunning` polling path). Transport errors
   land on `row.HealthzError` and are rendered alongside the icon.
2. Best-effort queries `FirewallStatus` via `f.AdminClient`. Both the
   AdminClient dial error and the RPC error are tolerated and surfaced on
   `row.FirewallError` — `status` is a diagnostic tool, not a gate.

Residual race: if the CP dies between the `IsRunning` check and the
`AdminClient` dial, the dial fails fast and the transport error lands on
`row.FirewallError`. Exit code stays zero — `status` is diagnostic, not a
gate — but the output reflects the dying CP truthfully.

### Output shape (JSON/template)

| Field | Meaning |
|-------|---------|
| `container_running` | `Manager.IsRunning` snapshot |
| `healthz_ok` | `/healthz` returned HTTP 200 |
| `healthz_status` | Last observed HTTP status (0 on transport failure) |
| `healthz_error` | Transport error string (empty on HTTP response) |
| `firewall_running` | `FirewallStatus.Running` |
| `firewall_ready` | `EnvoyHealth && CorednsHealth` |
| `firewall_rule_count` | Active rule count |
| `firewall_error` | AdminClient dial error OR RPC error (empty on success) |

Field names and `json` tags are a contract with the E2E test's mirrored
`cpStatusRow` in `test/e2e/controlplane_cli_test.go` — a rename here
silently breaks JSON unmarshaling on that side.

## Factory dependencies per verb

| Field | `up` | `down` | `status` | `agents` |
|-------|:----:|:------:|:--------:|:--------:|
| `IOStreams` | ✓ | ✓ | ✓ | ✓ |
| `TUI` | ­ | ­ | ­ | ✓ |
| `Logger` | ­ | ­ | ­ | ✓ |
| `Config` | ✓ | ­ | ­ | ­ |
| `Client` | ✓ (firewall-disabled advisory check) | ­ | ­ | ­ |
| `ControlPlane` | ✓ | ✓ | ✓ | ­ |
| `AdminClient` | ✓ (firewall-enabled only) | ­ | ✓ (best-effort) | ✓ |

## Format flag support

`status` and `agents` accept `--format`/`--json`/`--quiet` via `cmdutil.AddFormatFlags`.
`up` and `down` are action verbs with fixed textual output. Semantic color
methods (`cs.Success` / `cs.Error` / `cs.Info`, plus `cs.*Icon()`) are used
throughout — no raw `cs.Red` / `cs.Green`.

## Import boundary

`up`, `down`, and `status` import `internal/controlplane/cpboot` (for the `Manager` interface
type) but never import `pkg/whail`. CP lifecycle side effects are reached
through Manager or AdminClient methods, which is what makes the moq mocks
complete substitutes. `agents` imports only `api/admin/v1` for the
`AdminServiceClient` surface. `up` additionally imports the sibling
command package `internal/cmd/firewall` for the exported
`BringUpStack` helper so both verbs share one bringup UX (spinner,
shared RPC deadline, exposure warning, remediation hints) instead of
duplicating it, and `internal/docker` (via `f.Client` +
a label-filtered `ContainerList` on the firewall purpose label) for the
read-only firewall-disabled advisory check — the one Docker touch in this package, deliberately not
routed through Manager because it is not a CP lifecycle operation.

## Testing

- `newTestBed(t)` returns a `*testBed` with a fresh `mocks.ManagerMock` on
  `f.ControlPlane` and the stdout/stderr capture buffers from
  `iostreams.Test()`.
- Each test programs only the Manager methods it exercises; unprogrammed
  methods panic, which is the failure signal for paths that shouldn't
  call them.
- `status_test.go` adds a `statusHarness` that layers an
  `AdminServiceClientMock` (from `api/admin/v1/mocks`) on top for the firewall-RPC assertions.
- `agents_test.go` adds an `agentsHarness` that wires an `AdminServiceClientMock`
  directly as `f.AdminClient` (no ControlPlane mock needed — the verb uses only
  the AdminService gRPC surface).
- E2E coverage lives in `test/e2e/controlplane_cli_test.go` — walks
  `up → up (idempotent) → status → down → status` and the no-op `down`
  path on an absent CP.
