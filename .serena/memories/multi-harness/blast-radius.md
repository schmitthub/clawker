# Schema Migration Blast Radius (main 0.12.x → feat/multi-harness-support)

Assessed 2026-07-06 against actual load paths (`internal/storage/store.go`, `internal/config/migrations.go`, `internal/bundler/{harness,toolchain,dockerfile}.go`, `internal/harness/{bundle,materialize}.go`, `internal/toolchain/toolchain.go`, `internal/cmd/image/build/build.go`, `internal/docker/{image_resolve,builder}.go`).

## Engine ground rules (verified)

- **Unknown keys silently accepted AND preserved on re-save.** `storage.Store.validateKind` (store.go:532-541) allows non-schema paths; `decodeNode` = non-strict yaml.v3. Old keys never error/warn, linger until a migration strips them.
- **JSON Schema validation is editor-only** (strict `additionalProperties: false`, jsonschema.go:33). Nothing validates against it at load. Existing files keep old `$schema` header until next `Write`.
- **harness.yaml / toolchain.yaml decode non-strict** (bundle.go:41, toolchain.go:73) — unknown keys silently dropped; only explicit validators fire.

## Surface table

| # | Surface | Old → New | Behavior on old input | Migration? | Severity |
|---|---------|-----------|----------------------|------------|----------|
| 1 | `build.image` | deleted; base = `bundler.SubstrateImage` + `build.toolchains` | Silently ignored; build "succeeds" on substrate — user's base gone, zero output | none | **SILENT-BREAK** |
| 2 | `build.dockerfile`/`build.context` | deleted; custom-Dockerfile builds removed (`UseCustomDockerfile` gone; orphan comments dockerfile.go:628-630, stale claim builder.go:86) | Silently ignored; clawker's Dockerfile generated instead | none | **SILENT-BREAK** |
| 3 | `agent.claude_code` | deprecated → `harnesses:` map | Works via read shim `Project.HarnessConfigFor` (schema.go:137-148), claude harness only; never warned/rewritten. storeui project editor still exposes ONLY the deprecated path (storeui/project/project.go:27) | shim only | AUTO, silent |
| 4 | `agent.claude_code.use_host_auth` | deleted — whole host-credential-copy feature gone | Silently ignored; fresh containers demand login. Discovery = first run | none | **SILENT-BREAK** (behavior removal) |
| 5 | `build.inject.after_claude_install` | → `after_harness_install` | Works: legacy alias concatenated (dockerfile.go:732); no warning | alias | AUTO |
| 6 | legacy run-list `[{cmd:...}]` | `[]string` | rewritten in place | `migrateRunInstructionsToStrings` | AUTO-MIGRATED |
| 7 | settings missing `harnesses:` | registry required | Load-time seed `harnesses.claude` default+path, auto-saves, silent; `harnesses: {}` respected | `migrateSeedHarnessRegistry` (migrations.go:40) | AUTO-MIGRATED |
| 8 | registry entry empty `path` | explicit path required | shipped: healed at build (`seedShippedEntries` harness.go:225); custom: hard error naming key (harness.go:136-142) | build-time heal | HARD-ERROR / AUTO |
| 9 | multiple `default: true` | one max | hard error listing offenders (harness.go:87-93) | n/a | HARD-ERROR |
| 10 | `default_harness` key | never shipped (git log -S empty) | non-issue | — | — |
| 11 | codex before first build | `EnsureHarnesses` runs only in `clawker build` (build.go:214) | `LoadHarness("codex")` hard-errors; message says hand-edit settings but `clawker build` auto-registers — misleading remedy (harness.go:130-134) | build registers | HARD-ERROR (bad message) |
| 12 | `clawker-<proj>:latest` images | `:<harness>` + `:default` | fallback + stderr warning + rebuild hint (image.go:60, image_resolve.go:96-99); `:latest` never retagged | fallback+warn | WARN |
| 13 | stale materialized bundle `<config>/harnesses/<name>` (branch-track users only; main never materialized) | old staging vocab → copy/mounts/volumes/assets | copy-if-missing never updates; old vocab silently drops → stages nothing, no volumes; mixed old/new can hard-error via validateSeeds/validateStaging | none — no staleness detection | **SILENT-BREAK** / HARD-ERROR |
| 14 | stale materialized toolchain (old single `Dockerfile.toolchain.tmpl`) | root/user fragments | Materialize adds new fragments; user's edited old-name file silently ignored; yaml+no-fragment dir hard-errors. bundler/toolchain.go:23 comment still names old file | none | **SILENT-BREAK** / HARD-ERROR |
| 15 | presets | image lines → toolchains | new projects only | n/a | none |

## Gaps (prioritized; fix on branch)

1. **`migrateRemoveLegacyBuildKeys`** — warn+strip `build.image`/`build.dockerfile`/`build.context` (+ `agent.claude_code.use_host_auth`), precedent `migrateRemoveLegacyMonitoringKeys` (one-shot stderr notice naming values).
2. **use_host_auth removal notice** — message explains new in-container auth model.
3. **Materialized bundle/toolchain staleness detection** — no version/content stamp today; stamp shipped-copy content hash, warn on mismatch at EnsureHarnesses/LoadHarness. Also: `EnsureHarnesses` failure only file-logged (build.go:215) while comment claims a fallback `LoadHarness` doesn't provide once registry non-empty — surface on stderr.
4. **`agent.claude_code` shim silent + storeui exposes only deprecated path** — rewrite migration or stderr deprecation notice + storeui path update to `harnesses.*`.
5. **codex "not registered" error remedy misleading** — point at `clawker build`.
6. **Comment rot:** orphan `UseCustomDockerfile`/`GetCustomDockerfilePath` comments (dockerfile.go:628-630), builder.go:86 stale custom-Dockerfile claim, bundler/toolchain.go:23 old fragment filename, bundler/CLAUDE.md documents deleted `ErrNoBuildImage`/`UseCustomDockerfile`.

## Migration guide must cover

1. build.image/dockerfile/context gone — substrate + `build.toolchains` (node/go/python/rust) + packages/instructions/inject; custom Dockerfiles unsupported.
2. Host credentials no longer copied — in-container auth (browser proxied to host) on first run; token persists in config volume.
3. `agent.claude_code` → `harnesses.claude`; `after_claude_install` → `after_harness_install` (old names still honored).
4. Rebuild required: `clawker build` materializes/registers bundles+toolchains, produces `:<harness>`/`:default` tags; prune `:latest` after.
5. settings gains `harnesses:`/`toolchains:` registries automatically.
6. Branch-track/alpha users: delete `<config>/harnesses/` + `<config>/toolchains/` before rebuild (copy-if-missing never updates).
7. Editor schema headers refresh on next clawker write.
