# internal/cmd/firewall

Cobra commands for the `clawker firewall` command group. Manages the Envoy+CoreDNS egress firewall that controls outbound traffic from agent containers.

## Contents

| File | Purpose |
|------|---------|
| `firewall.go` | Parent command `NewCmdFirewall(f)` — registers all 12 subcommands |
| `up.go` | `firewall up` — FirewallInit RPC (idempotent stack-up) |
| `down.go` | `firewall down` — FirewallRemove RPC (global teardown) |
| `status.go` | `firewall status` — show firewall health, container IPs, rule count |
| `list.go` | `firewall list` (alias `ls`) — list active egress rules (sorted alphabetically by domain) |
| `add.go` | `firewall add <domain>` — add a domain to the allow list |
| `remove.go` | `firewall remove <domain>` — remove a domain from the allow list |
| `reload.go` | `firewall reload` — force-reload Envoy/CoreDNS config from rule state |
| `enable.go` | `firewall enable` — re-attach eBPF programs for a container |
| `disable.go` | `firewall disable` — detach eBPF programs + restore direct DNS for a container |
| `bypass.go` | `firewall bypass <duration>` — temporary unrestricted egress for a container |
| `rotate_ca.go` | `firewall rotate-ca` — regenerate CA keypair and domain certs |

## Parent Command Pattern

`NewCmdFirewall(f *cmdutil.Factory)` creates the parent command and registers all subcommands via `cmd.AddCommand(...)`. The parent has no `RunE` — it only serves as a grouping command with usage examples.

## Subcommand Table

Every run function now speaks typed gRPC via `f.AdminClient(ctx)` — no in-process firewall manager. First call to `f.AdminClient` transparently bootstraps the CP container (`controlplane.EnsureRunning`) and dials with mTLS + OAuth2.

| Command | Constructor | Args | Flags | RPC |
|---------|-------------|------|-------|-----|
| `up` | `NewCmdUp(f, runF)` | none | none | `FirewallInit` |
| `down` | `NewCmdDown(f, runF)` | none | none | `FirewallRemove` |
| `status` | `NewCmdStatus(f, runF)` | none | `--format`, `--json`, `--quiet` | `FirewallStatus` |
| `list` / `ls` | `NewCmdList(f, runF)` | none | `--format`, `--json`, `--quiet` | `FirewallListRules` |
| `add` | `NewCmdAdd(f, runF)` | `<domain>` (required) | `--proto` (default `tls`), `--port` | `FirewallAddRules` |
| `remove` | `NewCmdRemove(f, runF)` | `<domain>` (required, tab-completable) | `--proto` (default `tls`), `--port` | `FirewallRemoveRules` (+ `FirewallListRules` for completion) |
| `reload` | `NewCmdReload(f, runF)` | none | none | `FirewallReload` |
| `enable` | `NewCmdEnable(f, runF)` | none | `--agent` (required) | `FirewallEnable` |
| `disable` | `NewCmdDisable(f, runF)` | none | `--agent` (required) | `FirewallDisable` |
| `bypass` | `NewCmdBypass(f, runF)` | `<duration>` (required unless `--stop`) | `--agent` (required), `--stop`, `--non-interactive` | `FirewallBypass` (+ `FirewallEnable` for Ctrl+C/`--stop`) |
| `rotate-ca` | `NewCmdRotateCA(f, runF)` | none | none | `FirewallRotateCA` |

The hidden `serve` subcommand (pre-B2 daemon entrypoint) is deleted — the firewall has no host-side daemon; lifecycle is owned by the CP container.

## Options Pattern

Each subcommand defines a `*Options` struct populated during command construction. All options structs include `IOStreams`. The `AdminClient` field is a lazy Factory closure (`f.AdminClient`) used by every subcommand.

Every constructor follows `NewCmd*(f *cmdutil.Factory, runF func(context.Context, *XOptions) error)` — the `runF` parameter enables test injection.

## Format Flag Support

Only two commands support `--format`/`--json`/`--quiet` (via `cmdutil.AddFormatFlags`):

| Command | Format Support |
|---------|---------------|
| `status` | Yes — `cmdutil.FormatFlags` on `StatusOptions.Format`; reads `FirewallStatus` response |
| `list` | Yes — `cmdutil.FormatFlags` on `ListOptions.Format`; reads `FirewallListRules` response; uses `TUI` for table rendering |

All other commands produce action output only (success/error messages via `fmt.Fprintf` to IOStreams).

## Dependency Categories

- **Stack lifecycle** (`up`, `down`): `FirewallInit` / `FirewallRemove` via `AdminClient`
- **Infrastructure queries** (`status`, `list`, `reload`, `rotate-ca`): read-only RPCs on `AdminClient`
- **Per-container operations** (`enable`, `disable`, `bypass`): `FirewallEnable` / `FirewallDisable` / `FirewallBypass` on `AdminClient`; `--agent` flag identifies the container
- **Rule mutations** (`add`, `remove`): `FirewallAddRules` / `FirewallRemoveRules` on `AdminClient`; positional `<domain>` + `--proto`/`--port` flags

## Shell Completion

`remove` provides `ValidArgsFunction` for tab-completing existing firewall domains. The `domainCompletions` helper calls `FirewallListRules` on `AdminClient` and extracts unique `Dst` values. Domains are deduplicated, sorted, and returned as `[]cobra.Completion` with `ShellCompDirectiveNoFileComp`. Silently returns empty on errors (CP unreachable, dial failure).
