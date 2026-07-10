# Bundle Commands Package

`clawker bundle` — the FINAL verb set for the bundle-distribution model, replacing
the register-era `stack`/`harness`/`monitor units` commands (all deleted). A
bundle is the single distribution unit; its components (harnesses, stacks,
monitoring extensions) are peers, listed together with resolution provenance.

## Structure

```text
internal/cmd/bundle/
├── bundle.go          # Parent command; registers subcommands
├── install/           # install.go (arg/flag → source, write mechanics) + target.go (layer resolution)
├── list/              # list.go — merged component table over Resolver.List
├── remove/            # remove.go — purge a cached bundle
├── update/            # update.go — refetch on version change (stubbed)
└── validate/          # validate.go — local manifest/convention validation
```

## Subcommands

| Command | Purpose |
|---------|---------|
| `bundle list` / `ls` | Every resolvable component (all three types) across floor/loose/bundle tiers: `ADDRESS/TYPE/VERSION/SOURCE/PROVENANCE`, `!`-marked shadow rows, `--json`. This is where monitoring-extension provenance is listed (the register-era `monitor units list` is gone). |
| `bundle install [source]` | Declare a source (`bundles:` entry) + prefetch. Source = git URL, `owner/repo` shorthand (expands to a URL), or local dir. Flags `--ref/--sha/--subdir/--auto-update` + target `--user` (default, config-dir), `--project`, `--local`. Fetch is stubbed (`ErrNotWired`); the yaml write is real. |
| `bundle remove <namespace.name>` / `rm` | Purge a cached bundle (all versions + metadata); warns when still declared. |
| `bundle update [namespace.name]` | Refetch on version change; stubbed (`ErrNotWired`) until the fetch subsystem lands. |
| `bundle validate <dir> [--strict]` | Local validation: hard-fail on malformed manifest / missing fields / reserved namespace; advisory warnings (typo suggestions, empty dirs) that `--strict` promotes to failures. |

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
