# internal/cmd/firewall

Cobra commands for the `clawker firewall` command group. Manages the Envoy+CoreDNS egress firewall that controls outbound traffic from agent containers.

## Contents

| File | Purpose |
|------|---------|
| `firewall.go` | Parent command `NewCmdFirewall(f)` — registers all 12 subcommands |
| `up.go` | `firewall up` — start the firewall daemon (blocks) |
| `down.go` | `firewall down` — stop the firewall daemon (SIGTERM) |
| `status.go` | `firewall status` — show firewall health, container IPs, rule count |
| `list.go` | `firewall list` (alias `ls`) — list active egress rules (sorted alphabetically by domain) |
| `add.go` | `firewall add <domain>` — add a domain to the allow list |
| `remove.go` | `firewall remove <domain>` — remove a domain from the allow list |
| `reload.go` | `firewall reload` — force-reload Envoy/CoreDNS config from rule state |
| `enable.go` | `firewall enable` — re-apply iptables DNAT + DNS for a container |
| `disable.go` | `firewall disable` — flush iptables DNAT + restore direct DNS for a container |
| `bypass.go` | `firewall bypass <duration>` — temporary unrestricted egress for a container |
| `rotate_ca.go` | `firewall rotate-ca` — regenerate CA keypair and domain certs |

## Parent Command Pattern

`NewCmdFirewall(f *cmdutil.Factory)` creates the parent command and registers all subcommands via `cmd.AddCommand(...)`. The parent has no `RunE` — it only serves as a grouping command with usage examples.

## Subcommand Table

| Command | Constructor | Args | Flags | Options Fields |
|---------|-------------|------|-------|----------------|
| `up` | `NewCmdUp(f, runF)` | none | none | `IOStreams`, `Config`, `Logger` |
| `down` | `NewCmdDown(f, runF)` | none | none | `IOStreams`, `Config` |
| `status` | `NewCmdStatus(f, runF)` | none | `--format`, `--json`, `--quiet` | `IOStreams`, `Firewall`, `Format` |
| `list` / `ls` | `NewCmdList(f, runF)` | none | `--format`, `--json`, `--quiet` | `IOStreams`, `TUI`, `Firewall`, `Format` |
| `add` | `NewCmdAdd(f, runF)` | `<domain>` (required) | `--proto` (default `tls`), `--port` | `IOStreams`, `Firewall`, `Domain`, `Proto`, `Port` |
| `remove` | `NewCmdRemove(f, runF)` | `<domain>` (required, tab-completable) | `--proto` (default `tls`), `--port` | `IOStreams`, `Firewall`, `Domain`, `Proto`, `Port` |
| `reload` | `NewCmdReload(f, runF)` | none | none | `IOStreams`, `Firewall` |
| `enable` | `NewCmdEnable(f, runF)` | none | `--agent` (required) | `IOStreams`, `Firewall`, `Agent` |
| `disable` | `NewCmdDisable(f, runF)` | none | `--agent` (required) | `IOStreams`, `Firewall`, `Agent` |
| `bypass` | `NewCmdBypass(f, runF)` | `<duration>` (required unless `--stop`) | `--agent` (required), `--stop` | `IOStreams`, `Firewall`, `Agent`, `Duration`, `Stop` |
| `rotate-ca` | `NewCmdRotateCA(f, runF)` | none | none | `IOStreams`, `Config`, `Firewall` |

## Options Pattern

Each subcommand defines a `*Options` struct (e.g. `StatusOptions`, `AddOptions`) populated during command construction. All options structs include `IOStreams`. The `Firewall` field is a lazy Factory closure (`f.Firewall`) used by most subcommands. The `up` and `down` commands use `Config` (and `Logger` for `up`) instead of `Firewall` since they manage the daemon lifecycle directly.

Every constructor follows `NewCmd*(f *cmdutil.Factory, runF func(context.Context, *XOptions) error)` — the `runF` parameter enables test injection.

## Format Flag Support

Only two commands support `--format`/`--json`/`--quiet` (via `cmdutil.AddFormatFlags`):

| Command | Format Support |
|---------|---------------|
| `status` | Yes — `cmdutil.FormatFlags` on `StatusOptions.Format` |
| `list` | Yes — `cmdutil.FormatFlags` on `ListOptions.Format`; also uses `TUI` for table rendering |

All other commands produce action output only (success/error messages via `fmt.Fprintf` to IOStreams).

## Dependency Categories

- **Daemon lifecycle** (`up`, `down`): Use `Config` to resolve paths/settings; `up` also uses `Logger` for daemon logging
- **Infrastructure queries** (`status`, `list`, `reload`, `rotate-ca`): Use `Firewall` Factory noun for firewall manager access
- **Per-container operations** (`enable`, `disable`, `bypass`): Use `Firewall` + `--agent` flag to target a specific container
- **Rule mutations** (`add`, `remove`): Use `Firewall` + positional `<domain>` arg + `--proto`/`--port` flags

## Shell Completion

`remove` provides `ValidArgsFunction` for tab-completing existing firewall domains. The `domainCompletions` helper in `remove.go` calls `FirewallManager.List()` (reads from the rules store, not containers). Domains are deduplicated, sorted, and returned as `[]cobra.Completion` with `ShellCompDirectiveNoFileComp`. Silently returns empty on errors.
