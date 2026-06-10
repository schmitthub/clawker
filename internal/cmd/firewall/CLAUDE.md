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
| `refresh.go` | `firewall refresh` — re-read the current project's `clawker.yaml` and sync its egress rules into the store (live apply of yaml edits) |
| `enable.go` | `firewall enable` — re-enroll a container in per-container routing (idempotent; use after `disable`) |
| `disable.go` | `firewall disable` — remove container from per-container routing (eBPF programs remain attached; fast-path exits to bypass on lookup miss) |
| `bypass.go` | `firewall bypass <duration>` — temporary unrestricted egress for a container |
| `rotate_ca.go` | `firewall rotate-ca` — regenerate CA keypair and domain certs |

## Parent Command Pattern

`NewCmdFirewall(f *cmdutil.Factory)` creates the parent command and registers all subcommands via `cmd.AddCommand(...)`. The parent has no `RunE` — it only serves as a grouping command with usage examples.

## Subcommand Table

Every run function now speaks typed gRPC via `f.AdminClient(ctx)` — no in-process firewall manager. `f.AdminClient` is a pure dial with mTLS + OAuth2 and does NOT bootstrap the CP; admin commands fail fast when the CP is down. `firewall up` is one of the explicit bootstrap verbs — its run function calls `f.ControlPlane().EnsureRunning(ctx)` before dialing, mirroring `controlplane up` and the `container start` pre-start phase.

| Command | Constructor | Args | Flags | RPC |
|---------|-------------|------|-------|-----|
| `up` | `NewCmdUp(f, runF)` | none | none | `FirewallInit` |
| `down` | `NewCmdDown(f, runF)` | none | none | `FirewallRemove` |
| `status` | `NewCmdStatus(f, runF)` | none | `--format`, `--json`, `--quiet` | `FirewallStatus` |
| `list` / `ls` | `NewCmdList(f, runF)` | none | `--format`, `--json`, `--quiet` | `FirewallListRules` |
| `add` | `NewCmdAdd(f, runF)` | `<domain>` (required) | `--proto` (default `https`; accepts `http` for plaintext, `ssh`/`tcp`/opaque names), `--port` (dynamic spec: a single port `443` or an inclusive range `9000-9100`; empty = protocol default; validated 1..65535, lo<=hi), `--path` (URL path prefix; Envoy matches it as a prefix at request time), `--action` (`--path` and `--action` are required together; `--action` accepts `allow`/`deny`), `--methods` (CSV, e.g. `GET,HEAD`; narrows the path rule's action to those HTTP verbs; requires `--path`/`--action`; HTTP-family protos only) | `FirewallAddRules` |
| `remove` | `NewCmdRemove(f, runF)` | `<domain>` (required, tab-completable) | `--proto` (default `https`; legacy `tls` translated to `https`), `--port` (dynamic spec: single port or `lo-hi` range; must match the stored rule's port spec), `--path` (lookup is exact-string against the stored `Path`; omit to remove the whole entry) | `FirewallRemoveRule` (+ `FirewallListRules` for completion); with `--path` the call removes a single `PathRule` from the matching rule (`p.Path == path`), otherwise the whole rule; result status enum is `REMOVED` / `PATH_REMOVED` / `NOT_FOUND`. The CLI exits non-zero on `NOT_FOUND` (RPC succeeds, status drives the outcome) so a typo, wrong-proto/port, or unknown path never silently succeeds; the `NOT_FOUND` error message names the missing tuple and tells the user to run `clawker firewall list` |
| `reload` | `NewCmdReload(f, runF)` | none | none | `FirewallReload` |
| `refresh` | `NewCmdRefresh(f, runF)` | none | none | `FirewallAddRules` (re-syncs `cfg.EgressRules()` → `adminv1.EgressRulesToProto`); global (no `--agent`); requires firewall enabled and a resolvable current project; add/update merge only (no prune — delete via `firewall remove`) |
| `enable` | `NewCmdEnable(f, runF)` | none | `--agent` (required) | `FirewallEnable` |
| `disable` | `NewCmdDisable(f, runF)` | none | `--agent` (required) | `FirewallDisable` |
| `bypass` | `NewCmdBypass(f, runF)` | `<duration>` (required unless `--stop`) | `--agent` (required), `--stop`, `--non-interactive` | `FirewallBypass` (+ `FirewallEnable` for Ctrl+C/`--stop`) |
| `rotate-ca` | `NewCmdRotateCA(f, runF)` | none | none | `FirewallRotateCA` |

The hidden `serve` subcommand is intentionally absent — the firewall has no host-side daemon; lifecycle is owned by the CP container.

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
- **Rule mutations** (`add`, `remove`, `refresh`): `FirewallAddRules` / `FirewallRemoveRule` on `AdminClient`; positional `<domain>` + `--proto`/`--port` flags. `add` also takes `--path`/`--action` (required-together) to attach a path-scoped rule to the entry; `remove --path` removes a single path entry by exact-string match without nuking the rule. Both verbs share one underlying merge semantic — yaml input and CLI input are peers. `refresh` is the config-driven sibling of `add`: it re-reads the current project's `clawker.yaml` egress config (`security.firewall.add_domains` + `security.firewall.rules`) and replays the same `FirewallAddRules` merge that runs at container start, live-applying yaml edits without a restart. Like `add` it is add/update only — removing a domain from yaml does not prune it from the store (use `remove`).

## Shell Completion

`remove` provides `ValidArgsFunction` for tab-completing existing firewall domains. The `domainCompletions` helper calls `FirewallListRules` on `AdminClient` and extracts unique `Dst` values. Domains are deduplicated, sorted, and returned as `[]cobra.Completion` with `ShellCompDirectiveNoFileComp`. Silently returns empty on errors (CP unreachable, dial failure).
