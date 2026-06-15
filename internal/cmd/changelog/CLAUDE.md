# Changelog Command Package

`clawker changelog` — render the curated, user-facing changelog entries to
stdout with colored, emoji-tagged headers.

Entries come from the curated `CHANGELOG.md`, **fetched over the network at
runtime** by the `f.Changelog` loader (`internal/changelog`), not from an embed.
The command **force-refreshes** (always tries the network), falling back to the
on-disk cache when offline; a load failure degrades to a brief stderr note and a
zero exit, so a network blip never fails the command.

This is the explicit, on-demand surface that the show-once upgrade teaser (in
`internal/clawker/Main`) points users to. The teaser itself lives there, not
here. (The GitHub release-notes header is produced by the release workflow's
`awk` extraction, not by this command — there is no `--format markdown` path.)

## Files

| File | Purpose |
|------|---------|
| `changelog.go` | `NewCmdChangelog(f, runF)`, `ChangelogOptions`, `validateFlags`, run + render helpers |
| `consts.go` | Tag-badge emoji prefixes (`tagEmoji*`) |

## Key Symbols

```go
func NewCmdChangelog(f *cmdutil.Factory, runF func(context.Context, *ChangelogOptions) error) *cobra.Command
type ChangelogOptions struct { IO *iostreams.IOStreams; Loader *changelog.Loader; Version string; All bool; Since string; Flag string }
```

`Version` is injected from `f.Version` (the running `build.Version`); the
`--version` flag (`opts.Flag`) overrides it to select a specific release. The
`*changelog.Loader` is resolved from `f.Changelog()` in `RunE` and stored on
`opts.Loader`; the `runF` injection seam takes `context.Context` (from
`cmd.Context()`) so tests pass a loader-backed Options without a context struct
field.

## Flag Surface

- no args → the running version's entry (`changelog.ForVersion` over loaded entries)
- `--version vX` → a specific version's entry (overrides the running version; accepts a leading `v`)
- `--all` → the full curated history (all loaded entries)
- `--since vX` → entries with `vX < version <= current` (`changelog.Between`, lo-exclusive)

`validateFlags` enforces: `--all` and `--since` are mutually exclusive.
`cobra.NoArgs` rejects positional arguments.

## Rendering

Entries are loaded once via `opts.Loader.Load(ctx, true)` (force-refresh), then
`selectEntries` maps the flags to a query over the in-memory slice. Each entry
goes to `ios.Out`: a `cs.Bold` header of a colored, emoji-prefixed tag badge +
`vX.Y.Z` + muted ` - date`, then the verbatim markdown body, then an optional
muted `Docs:` line. `tagBadge` maps each `changelog.Tag*` to an emoji + semantic
color (feature→Success, fix→Info, breaking→Error, perf→Warning, changed→Primary;
unknown→Muted).

- When no entry matches the selection, an info line goes to `ios.ErrOut` and
  stdout stays empty.
- When the load fails (network + cache both unavailable), a `cs.WarningIcon()`
  "could not load changelog" note goes to `ios.ErrOut` and the command exits 0.

On a DEV build (`build.Version == "DEV"`) the no-arg and `--since` queries return
nothing — there is no released semver to anchor against. `--all` still works.

## Testing

`changelog_test.go` — `NewCmdChangelog` + `iostreams.Test()`, no Docker. A test
helper wires `f.Changelog` to a `changelog.NewLoader` backed by an `httptest`
server serving a self-contained fixture CHANGELOG.md (nil state → always fetch,
temp-dir cache). Covers no-arg / `--version` / `--all` / `--since` (incl.
lo-exclusive bound), the `--all`+`--since` mutual-exclusion error, the
positional-arg error, the unknown-version info path, the load-error→stderr-note
degradation, `runF` injection (incl. the `--version` override), and the
tag-badge emoji map.
