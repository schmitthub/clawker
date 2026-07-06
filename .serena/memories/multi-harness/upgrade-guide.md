# Multi-Harness Upgrade Guide (user-facing migration notes)

Living doc — collects breaking changes users hit when upgrading across the `feat/multi-harness` merge. Feeds an eventual docs page / CHANGELOG entry. Add entries as UAT surfaces them. Keep lean (alpha policy: no heavy migration investment — see auto-memory feedback_alpha_no_user_migration_docs).

## Required upgrade steps

1. **Rebuild all project images with `--no-cache`.**
   Old images lack the generic image-baked seed dir (`~/.clawker/seed` + `seed-manifest`). Cached layers silently no-op the new seed step — container comes up without harness seeds and nothing errors.

2. **Recreate containers on the new image with FRESH volumes.**
   Old config volumes carry state keyed to pre-extraction paths. Delete old volumes; do not carry them forward.

3. **Restart the control plane.**
   `default_cmd` routing (CP image-inspect → AgentReady → clawkerd routeArgs) requires the new CP binary. Old CP + new containers = harness CMD not routed.

4. **Re-init the monitoring stack (if running).**
   UAT 2026-07-05: new CP + pre-upgrade monitoring stack degraded netlogger (x509 verify failure dialing collector :4319 — trusted lane down, enforcement unaffected). Full cycle fixed it: stop CP → `clawker monitor down --volumes` → `monitor init --force` → `monitor up` → CP up. Netlogger came back `ready`. Root cause on the old pairing unprovable post-mortem; re-init is the durable remedy. (Also: OpenSearch data volume is dropped — telemetry history resets.)

## Behavior on OLD containers touched by NEW CP (if user skips step 2)

- Post-init marker moved `~/.claude/post-initialized` → `~/.clawker/post-initialized` (marker extracted out of claude's config dir, harness-blind). New CP sees no marker at new path → **re-runs seed apply + post_init once**, then writes new marker.
- Seeds are merge/skip-existing — benign. `post_init` re-fire is benign only if the user's hook is idempotent.
- DECIDED 2026-07-05 (UAT finding #1): accepted as one-time artifact; NO legacy-path shim in CP boot script (would re-introduce claude-hardcoded path into the harness-blind script). Remedy = fresh volumes (step 2).

## Automatic (no user action)

- settings.yaml gains `harnesses:` registry via load-time migration (`migrateSeedHarnessRegistry`); existing `harnesses: {}` respected as user choice.

## Config conversion (breaking: legacy keys lose precedence)

- `agent.claude_code` → `harnesses.claude`; `agent.post_init`/`pre_run` containing harness-specific commands (claude mcp add, etc.) → `harnesses.claude.post_init`/`pre_run`. Legacy `agent.claude_code` shim covers the builtin default harness ONLY — once a `harnesses:` map entry exists at ANY config layer, it out-ranks the legacy key at every layer.
- **Sweep ALL config layers**, not just project `.clawker.yaml`: local overlay (`.clawker.local.yaml`) and the global config (`<config>/clawker.yaml`) each merge independently. UAT 2026-07-06: converted project file only → global layer's `agent.post_init` still injected claude commands into a codex container (`command not found: claude`); an overlay's `agent.claude_code.use_host_auth: false` was silently out-ranked by the project's new `harnesses.claude` entry until the overlay was converted too.
- Hooks inject at container CREATE — recreate containers after config conversion.

## Image tags + registry (2026-07-06)

- Images now tag `clawker-<project>:<harness>` (registry key = tag); registry default also gets `:default` alias. `:latest` no longer produced — resolution accepts it as legacy fallback with a rebuild warning. `run/create @` resolves `:default`→`:latest`; `@:tag` selects a harness image exactly. `build -t NAME` selects the harness (bare registered name, or full ref whose tag part names one — strict).
- EVERY settings `harnesses:` entry now requires explicit `path:` to its bundle dir — shipped included, no name-keyed fallback. Migration/build-time ensure backfills shipped entries; custom entries without path hard-error.
- Containers + images carry `dev.clawker.harness` label; start-time pre_run/egress compose against the container's label, not the registry default.

## Volumes + config_dir removal (2026-07-06, second wave)

- `staging.config_dir` DELETED. Bundles now declare persisted dirs explicitly in a top-level `volumes:` list — `{name, path}` per entry; each becomes a named volume `clawker.<proj>.<agent>-<name>` mounted at `~/<path>`. Multiple volumes fully supported. Names `history`/`workspace` reserved. Claude/codex bundles use `name: config` so pre-existing `-config` volumes still line up.
- `${config_dir}` token DELETED. Host-side srcs use shell-style env defaults instead: `src: ${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json` (`~`, `$VAR`, `${VAR}`, `${VAR:-fallback}`; relative results absolutize against cwd). Dests are plain container-home-relative paths (`dest: .claude/settings.json`) and MUST fall under a declared volume — load-time error otherwise (copies land in volumes; a non-persisted dest is a config error, no ephemeral lane by design).
- Seeds' `dest` is now home-relative too (`.claude/settings.json`) and must sit under a volume; the image seed-manifest lost its `config_dir=` header (CP seed script reads `<apply> <dest>` lines, mkdir -p per dest). Rebuild + new CP required together.
- Missing host state is a soft skip everywhere (fresh machine ≠ error); a genuinely unreadable source file is still a hard create error.

## Bundle schema breaking changes (harness.yaml authors)

- 2026-07-06: staging vocabulary replaced. `files:`/`dirs:`/`trees:` (implicit config-dir-relative names) → single `copy:` list of explicit directives: `src` (host path or doublestar glob; `~`, `$VAR`, `${config_dir}` = RESOLVED host config dir) + `dest` (container-home-relative; `${config_dir}` = container config dir name); filter verbs `json_keys`/`skip`/`json_rewrites` per entry. `credentials[].path` → `src` (token-capable); `mounts[].host_subdir/dest_subdir` → `src`/`dest`. Dest must resolve under `${config_dir}` (only volume-backed target today) — validated at bundle load. Sources inside the project workspace rejected at stage time (workspace is mounted, never staged). `unattended_flag` REMOVED (clawker is a dumb sandbox — harness flags go through user args or aliases only).

## Credential copying removed (2026-07-06)

- Host credentials are NO LONGER copied into containers. `staging.credentials` (keyring/file) deleted from the bundle schema; `use_host_auth` deleted from project config (all layers — remove the key or it's dead yaml); `internal/keyring` package deleted. Rationale: copied OAuth tokens race the host's refresh-token rotation lineage — the host token gets invalidated and everyone re-auths anyway. New model: **authenticate once inside the container** on first run; the token family persists in the config volume across restarts/recreates that reuse the volume.

## Bundle layout: assets/ (2026-07-06)

- `context_files` manifest key DELETED. Bundles now put every build-context file under an `assets/` subdirectory — the whole tree is staged verbatim into the docker build context (paths keep the `assets/` prefix). What lands in the image and where is decided ONLY by the bundle template's `COPY assets/...` instructions and by `seeds[].file` entries, which must now be `assets/`-relative paths (validated at load: under `assets/`, file exists). A bundle with no assets/ dir is valid.
- `skill_install` manifest key DELETED (was dead data; `clawker skill` remains a user-invoked command).

## TODO as UAT progresses

- Verify whether anything else in old config volumes misbehaves under new paths (history volume OK? claude config volume reused?)
- CHANGELOG entry + Mintlify upgrade note once branch nears merge.
