# internal/cmd/firewall

Cobra commands for the `clawker firewall` command group. Manages the Envoy+CoreDNS egress firewall that controls outbound traffic from agent containers.

## Layout

One subpackage per subcommand (the `internal/cmd/<noun>/<verb>` pattern); cross-command helpers live in `shared/`.

| Package | Contents |
|---------|----------|
| (root) `firewall.go` | Parent command `NewCmdFirewall(f)` — registers the 13 subcommand constructors; no `RunE` of its own. `firewall_test.go` pins the registration list |
| `up/` | `firewall up` — CP bootstrap (`Manager.Start` via `f.ControlPlane(ctx)`, with `cpshared.AssistSOS` + one retry on a `*CPSOSError`) then `shared.BringUpStack`. One of the explicit CP-bootstrap verbs; all other firewall admin commands fail fast when the CP is down |
| `down/` | `firewall down` — `FirewallRemove` RPC (global teardown) |
| `status/` | `firewall status` — health snapshot; `--format`/`--json`/`--quiet` via `cmdutil.AddFormatFlags` |
| `list/` | `firewall list` (alias `ls`) — rule dump sorted by (domain, proto, port); format flags like `status` |
| `add/` | `firewall add <domain>` — `--proto`, `--port` (spec validated CLI-side via `shared.ValidatePortFlag`), `--path`+`--action` (required together), `--methods` |
| `remove/` | `firewall remove <domain>` — key removal, `--path` for single PathRule; domain tab-completion (`domainCompletions`); NOT_FOUND exits non-zero |
| `reload/` | `firewall reload` — `FirewallReload` with the bringup RPC deadline |
| `refresh/` | `firewall refresh` — `shared.ComposeProjectRules` → `shared.SyncRules` (live-apply yaml edits; add/update only) |
| `prune/` | `firewall prune` — one `FirewallRemoveRule{all: true}` wipe, then (unless `--all`) refresh's exact re-sync; `-a/--all`, `-y/--yes`; keep set composed BEFORE the wipe; confirmation default-no |
| `enable/` `disable/` | Per-container enroll/unenroll; `--agent` required |
| `bypass/` | `firewall bypass <duration>` — timed unrestricted egress; `--stop`, `--non-interactive`; the live countdown dashboard lives in `bypass_dash.go` |
| `rotateca/` | `firewall rotate-ca` — regenerate MITM CA + domain certs |
| `shared/` | The helpers subcommands have in common — see below |

## shared/ package

Imported by every subcommand package (and by `internal/cmd/controlplane` for `BringUpStack`); imports none of them.

| Symbol | Purpose |
|--------|---------|
| `CallWithSpinner` / `CallWithSpinnerTimeout` | Spinner-wrapped AdminService RPC with the quick deadline (`rpcTimeout`) / an explicit one (`consts.FirewallStackBringupRPCTimeout` for FirewallInit/FirewallReload) |
| `WrapRPCError` | gRPC error → header + per-sentinel remediation lines (from `errdetails.ErrorInfo` Reasons) |
| `WarnStackDownExposure` | Loud stderr security warning on failed stack bringup |
| `PrintStackRestartedNote` | "takes effect on next `firewall up`" note when `stack_restarted=false` |
| `BringUpStack` | Idempotent FirewallInit + summary + exposure warning — the bringup UX shared by `firewall up` and `controlplane up` (the CP daemon has its own in-process pre-`SetReady` gate; this CLI path covers the CP-already-running case) |
| `ComposeProjectRules` | Firewall-enabled + current-project gates, then `bundler.EgressRules(cfg, "")` → wire rules — the container-start sync set |
| `SyncRules` / `RuleSyncResult` | `FirewallAddRules` push + per-status tally (`Added`/`Modified`/`Unchanged`/`StackRestarted`) |
| `ValidatePortFlag` | `--port` dynamic-spec validation (`config.ParsePortSpec`) at the CLI boundary |

Callers returning `shared.WrapRPCError(...)` / propagating `ComposeProjectRules`/`SyncRules` errors carry `//nolint:wrapcheck` — the callee already wraps with header + remediation; re-wrapping would double the user-visible prefix.

## Options + constructor pattern

Every subcommand keeps the `NewCmd<X>(f *cmdutil.Factory, runF func(context.Context, *XOptions) error)` shape: an `XOptions` struct of Factory closures (+ flag fields), `runF` as the test trapdoor, and an unexported `<x>Run(ctx, opts)` holding the logic. RPCs go through `f.AdminClient(ctx)` — a pure dial; no in-process firewall manager, no CP bootstrap except in `up`.

## Test conventions (every subpackage)

In-package test files (`//nolint:testpackage` — they exercise the unexported run function directly) with two table layers:

1. **Constructor table** — `NewCmd<X>(f, runF)` with a capture-runF; asserts flag/arg parsing populates Options (and that validation failures never invoke runF).
2. **Run-function table** — drives `<x>Run` against `adminv1mocks.AdminServiceClientMock` (+ `configmocks`/`projectmocks` where config gates exist), asserting request payloads via moq recorded `Calls()[i].In` accessors (never copy proto structs by value — vet lock-copy) and rendered output on `iostreams.Test()` buffers.

Factories in tests are `&cmdutil.Factory{...}` struct literals — never `factory.New()`. Sparse fixtures/mocks carry `//nolint:exhaustruct` with a reason (repo idiom). Note `.golangci.yml` runs `new-from-merge-base: main` — new files get the full linter surface.

## RPC table

| Command | RPC | Notes |
|---------|-----|-------|
| `up` | `FirewallInit` | via `shared.BringUpStack`; bringup deadline |
| `down` | `FirewallRemove` | global teardown; rules store preserved |
| `status` | `FirewallStatus` | read-only |
| `list` | `FirewallListRules` | read-only |
| `add` | `FirewallAddRules` | merge semantics server-side (`MergeRule`) |
| `remove` | `FirewallRemoveRule` | keyed `(dst, proto, port)` + optional `path`; `REMOVED`/`PATH_REMOVED`/`NOT_FOUND` status enum |
| `refresh` | `FirewallAddRules` | config-driven sibling of `add` |
| `prune` | `FirewallRemoveRule{all:true}` then `FirewallAddRules` | `all` mutually exclusive with dst/proto/port/path (InvalidArgument); empty store → NOT_FOUND, re-sync still runs |
| `reload` | `FirewallReload` | bringup deadline |
| `enable`/`disable` | `FirewallEnable`/`FirewallDisable` | `--agent` → container ref |
| `bypass` | `FirewallBypass` (+ `FirewallEnable` on `--stop`/Ctrl+C) | dead-man timer CP-side |
| `rotate-ca` | `FirewallRotateCA` | quick deadline |

The hidden `serve` subcommand is intentionally absent — the firewall has no host-side daemon; lifecycle is owned by the CP container (`firewall_test.go` pins this).
