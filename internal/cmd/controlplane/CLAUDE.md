# internal/cmd/controlplane

Cobra commands for the `clawker controlplane` command group. Break-glass
lifecycle control for the clawker control plane container.

## Why this exists

Day-to-day, operators do not invoke these verbs. `f.AdminClient` transparently
calls `controlplane.EnsureRunning` on first use, so the first `clawker firewall
status` (or any admin RPC) auto-boots the CP. This package exposes that
lifecycle explicitly for debugging, upgrades, and recovery paths.

## Contents

| File | Purpose |
|------|---------|
| `controlplane.go` | Parent command `NewCmdControlPlane(f)` — registers `up`/`down`/`status` |
| `up.go` | `controlplane up` — wraps `Manager.EnsureRunning` (idempotent) |
| `down.go` | `controlplane down` — `Manager.Stop` (CP container only); warns about orphan Envoy/CoreDNS |
| `status.go` | `controlplane status` — `Manager.IsRunning` + `Manager.ProbeHealthz` + best-effort `FirewallStatus` RPC |
| `up_test.go` / `down_test.go` / `status_test.go` | Unit tests driving the run functions through `mocks.ManagerMock` |

## Subcommand Table

| Command | Constructor | Args | Flags | Manager methods |
|---------|-------------|------|-------|-----------------|
| `up` | `NewCmdUp(f, runF)` | none | none | `EnsureRunning` |
| `down` | `NewCmdDown(f, runF)` | none | none | `IsRunning`, then `Stop` on the running path |
| `status` | `NewCmdStatus(f, runF)` | none | `--format`, `--json`, `--quiet` | `IsRunning`, `ProbeHealthz`; plus best-effort `FirewallStatus` via `f.AdminClient` |

## Factory dependency

Every verb here reaches the CP lifecycle through one noun: `f.ControlPlane()
controlplane.Manager`. The `Manager` interface lives in `internal/controlplane/
manager.go` and is wired in `internal/cmd/factory/default.go` via
`controlPlaneFunc(f)` (a `sync.Once`-cached closure that calls
`controlplane.NewManager(f.Client, f.Config, f.Logger)`).

No package-level seams. Tests inject a `*mocks.ManagerMock` by overriding
`tb.F.ControlPlane` on the per-test `testBed`; each test programs only the
methods it exercises, so an unexpected call to an unprogrammed method
panics — that's the assertion for paths that should short-circuit.

## `controlplane down` and firewall orphans (INV-B2-008)

The CP owns Envoy and CoreDNS container lifecycle, but `controlplane down`
only removes the CP container itself. Envoy and CoreDNS keep running on
`clawker-net` until the next `controlplane up` adopts them, or until the
operator runs `clawker firewall down` first to route `FirewallRemove`
through the CP.

`down` therefore:
1. Short-circuits with an `InfoIcon` message if `Manager.IsRunning` returns
   false — avoids spinning up a CP just to turn it back off.
2. On successful `Manager.Stop`, writes a `WarningIcon` line to **stderr**
   telling the operator to run `clawker firewall down` first next time.
   The warning is routed to stderr, not stdout, so scripted callers that
   parse `down`'s stdout see only the success confirmation.

## `controlplane status` tolerance model

`status` short-circuits before touching `f.AdminClient` when
`Manager.IsRunning` reports false, which keeps the absent-CP branch from
triggering `AdminClient`'s lazy bootstrap. When the CP is present, the
command:

1. Calls `Manager.ProbeHealthz` — a dedicated 2-second-budget point-in-time
   probe (separate from the `EnsureRunning` polling path). Transport errors
   land on `row.HealthzError` and are rendered alongside the icon.
2. Best-effort queries `FirewallStatus` via `f.AdminClient`. Both the
   AdminClient dial error and the RPC error are tolerated and surfaced on
   `row.FirewallError` — `status` is a diagnostic tool, not a gate.

Residual race: if the CP dies between the `IsRunning` check and the
`AdminClient` dial, the `AdminClient` closure may re-bootstrap it. Accept:
the operator's explicit request was "show me status", and the best response
to a dying CP is to reconcile it. The tolerance model keeps the command
exit zero regardless.

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

| Field | `up` | `down` | `status` |
|-------|:----:|:------:|:--------:|
| `IOStreams` | ✓ | ✓ | ✓ |
| `ControlPlane` | ✓ | ✓ | ✓ |
| `AdminClient` | ­ | ­ | ✓ (best-effort) |

## Format flag support

Only `status` accepts `--format`/`--json`/`--quiet` via `cmdutil.AddFormatFlags`.
`up` and `down` are action verbs with fixed textual output. Semantic color
methods (`cs.Success` / `cs.Error` / `cs.Info`, plus `cs.*Icon()`) are used
throughout — no raw `cs.Red` / `cs.Green`.

## Import boundary

The commands import `internal/controlplane` (for the `Manager` interface
type) but never import `pkg/whail` — the `*docker.Client` abstraction is
held entirely behind `Manager`. All lifecycle side effects are reached
through Manager methods, which is what makes the moq mock a complete
substitute.

## Testing

- `newTestBed(t)` returns a `*testBed` with a fresh `mocks.ManagerMock` on
  `f.ControlPlane` and the stdout/stderr capture buffers from
  `iostreams.Test()`.
- Each test programs only the Manager methods it exercises; unprogrammed
  methods panic, which is the failure signal for paths that shouldn't
  call them.
- `status_test.go` adds a `statusHarness` that layers an
  `AdminServiceClientMock` on top for the firewall-RPC assertions.
- E2E coverage lives in `test/e2e/controlplane_cli_test.go` — walks
  `up → up (idempotent) → status → down → status` and the no-op `down`
  path on an absent CP. Authored in Task 7, deferred to the final
  host-side review per the initiative E2E policy.
