# Bundle Commands Package

`clawker bundle` — the FINAL verb set for the bundle-distribution model, replacing
the register-era `stack`/`harness`/`monitor units` commands (all deleted). A
bundle is the single distribution unit; its components (harnesses, stacks,
monitoring extensions) are peers, inventoried by the per-type read-only listing
commands (`clawker harness list`, `clawker stack list`,
`clawker monitor extensions` — built on `cmdutil.NewInventoryListCommand` over
`Manager.Inventory`), while `bundle list` shows the bundles themselves.

## Structure

```text
internal/cmd/bundle/
├── bundle.go          # Parent command; registers subcommands
├── install/           # install.go (arg/flag → source, write mechanics) + target.go (layer resolution)
├── list/              # list.go — per-identity bundle table over Manager.Statuses
├── prune/             # prune.go — roots-based cache GC sweep over Manager.Prune
├── remove/            # remove.go — purge a cached bundle
├── update/            # update.go — declaration-driven refetch on version change
└── validate/          # validate.go — local manifest/convention validation
```

## Subcommands

| Command | Purpose |
|---------|---------|
| `bundle list` / `ls` | Bundles ONLY — one honest per-identity row (`BUNDLE/VERSION/SOURCE/STATUS` via `Manager.Statuses`): resolving (installed/in-place), declared-but-not-installed, cached-but-not-declared, and hand-placed (unmanaged) — the actionable states repeat as stderr hints in every output mode; `--json`/`--quiet` (quiet emits the identity, or the canonical source for a never-fetched entry). Components are the per-type inventory commands' territory (`harness list`/`stack list`/`monitor extensions`), each row naming its owning bundle. |
| `bundle install [source]` | Declare a source (`bundles:` entry) + prefetch into the host cache. Source = git URL, `owner/repo` shorthand (expands to a URL), or local dir. Flags `--ref/--sha/--subdir/--auto-update` + target `--user` (default, config-dir), `--project`, `--local`. No-arg form fetches every declared-but-uncached bundle. A prefetch failure leaves the yaml write in place (reported, not rolled back). |
| `bundle prune` | Roots-based cache GC: removes every cache entry whose exact source value no declaration addresses (current config layers + user layer + every registered project incl. worktrees — nested layers included, via `Manager.Prune`), plus a rooted key's superseded twin left by an upstream identity rename, plus entries with an unreadable (but present) fetch receipt when condemned. Also reclaims abandoned `.tmp` staging (restoring a retired entry whose replacement never committed). Reports drops on stdout and sweep warnings on stderr; hand-placed (receipt-less) entries never collected; warns when one identity is cached from ≥2 distinct repositories (mirror-attack anomaly surface), naming each repo + declaring files. The install/update verbs run the same reconciliation identity-scoped via `Manager.AutoGC`. |
| `bundle remove <namespace.name>` / `rm` | Purge a cached bundle (every cache entry of the identity); warns when still declared. |
| `bundle update [namespace.name]` | Declaration-driven refetch on version change: each declared ref/unpinned source compares its own value-keyed entry's receipt against the remote tip and refetches in place on drift; sha-pinned skipped, never-installed skipped with an install hint, a failed refetch keeps the cached version. Prints one line per source considered. |
| `bundle validate <dir> [--strict]` | Local validation: hard-fail on malformed manifest / missing fields / reserved namespace / invalid component (`Manager.Validate` runs each component through the manager's `ComponentValidator` — the same check the install prefetch applies); advisory warnings (typo suggestions, empty dirs) that `--strict` promotes to failures. |

## Access pattern

All subcommands take the `BundleManager func() (*bundle.Manager, error)` Factory
noun; `install` additionally takes `Config` for the store write (decode the
target layer's own `bundles` seq via `store.Layers()`, append idempotently,
`WriteTo(targetPath)` — never `Set` the union). Validation of a new source runs
at the write front door via `config.ValidateBundleSource`.

## Testing

External `_test` packages drive the real run functions through `NewCmdX(f, nil)` +
`Execute`, with a `bundle.Manager` built from `configmocks.NewBlankConfig()` (or a
fresh `config.NewConfig()` for install's write round-trip) and `testenv.New(t)`
for isolated floor/loose/cache dirs. `install/install_internal_test.go` unit-tests
the pure source-classification and target-derivation helpers.
