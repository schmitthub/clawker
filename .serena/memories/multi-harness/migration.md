# Multi-Harness: Surgical Migration Spec

Execution plan for extracting Claude Code coupling into yaml-backed **harness
profiles**. Decisions below are LOCKED (user-confirmed 2026-07-03); evidence in
`claude-coupling-inventory.md`; architecture background in `design.md` (this file
supersedes design.md where they conflict — notably extensibility tiers).

## Locked decisions

1. **Terminology: harness.** Config key `harnesses:`, package `internal/harness`.
   ("Profile" reserved for prose: "harness profile".) NO `--harness` flag —
   run/create select via image ref `@:tag`; build selects via docker-canon
   `-t/--tag <harness>` (decision 4).
2. **Image identity: docker tags.** Repo = project, tag = harness:
   `clawker-<project>:<harness>` (e.g. `clawker-myproj:codex`). Idiomatic
   variant-tagging (`python:3.12-slim` pattern). Replaces today's
   `clawker-<project>:latest` (`docker.ImageTag`, `internal/docker/names.go:207`).
   Building the project's DEFAULT harness also stamps alias tag `:default`
   (multi-tag, same image ID — `python:latest`/`python:3.13` pattern).
   `:latest` treated as legacy claude-code during resolution.
   Edge: `default_harness` settings change → stale `:default` alias; CLI retags
   on settings change or next build (docker tag = metadata op, no rebuild).
3. **Container naming unchanged** — 3-segment `clawker.project.agent`. Harness in
   `dev.clawker.harness` label (labels authoritative, design decision #3) — the
   SINGLE runtime join key. NO baked harness env vars (user-rejected as redundant):
   descriptor interpretation happens exclusively host-side (CLI + CP); the container
   only ever receives concrete values (CMD via image, script bodies + spawn cmd via
   CP dispatch payload).
4. **Harness selection = docker canon, no bespoke flag.**
   - `run @` / `create @` → `:default` tag; `@:codex` → `:<codex>` (image-ref
     tag syntax, `docker run python:3.12` shape). Everything after the ref =
     container-argv passthrough, untouched.
   - `build [-t|--tag <harness>]` → docker-build-canon flag (build's positional
     is context path, not a ref); value validated against harness registry;
     default = registry entry flag (`harnesses.<name>.default: true`; was `default_harness`, superseded 2026-07-04).
   - Missing tag → error listing built harness tags. `start` reads container
     label — never ambiguous.
   - **Strict tag=harness validation:** `-t X` / `@:X` must match a registry
     harness key (shipped + user descriptors); unknown → error listing valid
     names; validated at the build front-door. Reserved tags: `default`
     (alias), `latest` (legacy). Harness keys constrained to docker-tag-safe
     charset — one name across config key, image tag, label value. Hand-retag
     drift: label is truth; create errors on label↔tag mismatch. Loosening
     later is non-breaking (label already authoritative).
   - **Completions + dynamic help (ships with Phase 2 selection UX).**
     Precedent: `init.go` RegisterFlagCompletionFunc, `firewall/remove.go`
     domainCompletions (dynamic via Factory closure). `build -t` completes
     REGISTRY keys (buildable); `run/create @:` ValidArgsFunction completes
     BUILT tags (docker image list on `clawker-<project>`), annotated with
     harness descriptions. Long/Example composed at command construction from
     registry. gen-docs bakes shipped set + note that user descriptors appear
     dynamically.
5. **Harness #2 = codex.** NOT greenfield: auth = `OPENAI_API_KEY` via existing
   `agent.from_env`/`env` plumbing, or copy `~/.codex/auth.json` (same
   file-fallback pattern as claude staging). Verify codex specifics (install
   method, config layout, egress list) against real codex during its phase — do
   not trust training data.
6. **File-backed harness bundles from the START. No hardcoded-Go harness tier.**
   Harness = DIRECTORY bundle: **slim harness.yaml + Dockerfile.harness.tmpl +
   asset files**. Build surface = the tmpl, via Go `{{block}}`/`{{define}}`
   composition — master Dockerfile.tmpl declares empty-default blocks at its
   slot positions (managed_config, env, install, root_assets, cmd); harness
   tmpl's defines override them; master keeps ordering + cache architecture.
   Yaml keeps only Go-consumed data (version resolver, context_files, seeds
   apply-manifest, staging, egress, unattended_flag, skill_install). NOT
   embedded: shipped bundles in bundler assets/ materialized into
   `<user-config>/harnesses/<name>/` (user-owned, editable); registry =
   settings.yaml root key `harnesses` (slug → bundle path) feeding validation
   + completions. Custom harness = bundle dir + registry entry. THAT is the
   feature. Full format spec: `descriptor-schema.md` rev 2.
   (Supersedes design.md "big-4 are Go impls" tier table.)
7. **No version framing.** This is a feature landing on 0.12.x alpha with live
   users — no "v1/v2" language in code, docs, or plans.

## Feasibility: every coupling point is data-expressible

Proof that killed the Go-impl tier: the ONE spot inventory flagged as
"bespoke code" — claude plugin-registry rewriting in `internal/containerfs` — is
already internally data-shaped: `pathRewriteRule{key, hostPrefix, containerPath}`
+ generic recursive `rewriteJSONPaths`. It's a yaml schema hiding in a Go file.

| descriptor field | expressible | engine primitive needed |
|---|---|---|
| binary/CMD + routeArgs prepend | string | — |
| install | command/URL + resolver enum (npm\|github-release\|none) | one Fetcher per resolver type |
| config-dir | env var name + dir name | — |
| credentials | enum keyring(service)\|file(path)\|env(vars) | verbatim-blob rule (no typed round-trip — fabricated `organizationUuid` bug) |
| staging manifest | dirs, settings-key allowlist, skip-files, json_rewrites | generic stager interpreting manifest |
| seeds/onboarding | files + dest + merge strategy enum | jq-merge primitive |
| assets (prompt/statusline) | paths; embedded for shipped, user paths for custom | — |
| required egress | `EgressRule` list — already yaml-shaped in project config | — |
| telemetry | env mapping | — |
| host-state mount | dir + copy-vs-bind | — |

**Extensibility contract (honest boundary):** one-commit-harness holds only while
the new harness composes existing primitives. A novel auth scheme or installer
type = Go work to add the primitive, then yaml again. Optional escape-hatch hook
scripts (`stage.sh` etc.) deliberately deferred — add only if a real harness
breaks the mold.

**Descriptor validation:** reuse #409 config JSON Schema infra — descriptor yaml
gets `$schema` header + generated schema. That IS the user-authoring UX.

## Phases (strangler; each phase lands green, claude behavior byte-identical)

### Phase 0 — recon completion + descriptor schema draft
- Remaining mechanic-level recon: Dockerfile install/seed RUN/COPY/ENV bodies.
  (CP script bodies + routeArgs recon DONE 2026-07-03 — see inventory §5:
  plans generic, only ConfigSeedScript + marker path are claude.)
- Draft `harness.yaml` descriptor schema covering the 10 fields; write
  claude-code.yaml + codex.yaml drafts on paper to stress the schema BEFORE
  building the engine (two descriptors up front = schema pressure-test).

### Phase 1 — engine + embedded claude-code.yaml, zero behavior change
- `internal/harness`: descriptor types, loader (embedded FS + user dir),
  registry `map[string]Descriptor`, generic engine funcs consumed by
  subsystems.
- claude-code.yaml expresses EVERYTHING currently hardcoded: install step,
  CMD, config-dir, credential source (keyring `Claude Code-credentials` +
  `.credentials.json` fallback), staging manifest incl. `enabledPlugins`
  allowlist + plugin json_rewrites + `install-counts-cache.json` skip, seeds,
  assets (statusline.sh, claude-config.json onboarding bypass, CLAUDE.md dest),
  egress floor incl. `.claude.ai` `/public/`+`/share/` deny path-rules,
  OTEL telemetry mapping, projects/ live-bind mount.
- Gate: golden Dockerfile tests byte-identical; full unit suite green; nothing
  else references the descriptor yet.

### Phase 2 — identity plumbing
- `clawker build [-t <harness>]` (default: settings/project `default_harness`,
  fallback claude-code). Image tag `clawker-<project>:<harness>` + `:default`
  alias when building the default harness; bake `dev.clawker.harness` label.
  NO harness env vars.
- Create stamps container label from image label. `@` → `:default` tag;
  `@:X` → `:<X>` (decision 4).
- Runtime resolution is host-side only, and minimal: CP needs NO descriptor
  access (see 3e — seeds baked at build, generic apply script, routeArgs cmd
  from image `Config.Cmd[0]`). clawkerd receives concrete values, never
  resolves a harness. Label serves CLI `@` resolution + filtering.
- Legacy `:latest` images resolve as claude-code.

### Phase 3 — subsystem extraction (one PR each, golden-guarded)
Order chosen so each step consumes the descriptor where Phase 2 left identity:
- **3a config schema:** `harnesses:` keyed map in clawker.yaml (merge engine
  KindFunc precedent: `KindWorktreeMap`); `agent.claude_code` →
  `harnesses.claude_code` (deprecation shim reads old key + warns);
  `after_claude_install` → `after_harness_install`; `default_harness` in
  settings + project. Effective config = base build/agent ⊕ harnesses.<name> ⊕
  descriptor fragments.
- **3b egress floor:** `requiredFirewallRules` (`config/defaults.go`) deleted;
  `EgressRules()` composes selected-harness descriptor egress ⊕ project rules.
  `.claude.ai` UGC-deny moves into claude-code.yaml (security knowledge lives
  WITH the harness).
- **3c bundler:** Dockerfile.tmpl sections (install ARG+RUN, config-dir ENV,
  harness env block, managed-files step, seed COPYs, CMD) driven by descriptor
  slots. `ClaudeCodeVersion`→`HarnessVersion` threading; version resolver
  behind Fetcher enum (npm fetcher = existing code). Telemetry: NO descriptor
  block — full OTEL_* set lives in claude-code.yaml `env:` (values
  template-rendered w/ settings params, omit-empty for conditionals);
  `docker/env.go` monitoring-down injection → standard
  `OTEL_*_EXPORTER=none` (harness-blind; kills `EnvClaudeCodeEnableTelemetry`
  coupling; VERIFY claude honors it).
- **3d containerfs:** staging pipeline becomes manifest interpreter;
  `ResolveHostConfigDir` uses descriptor config-dir env+name; credential
  staging uses descriptor credential enum. Claude behavior reproduced from
  claude-code.yaml manifest.
- **3e CP/clawkerd:** init/boot plans are ALREADY generic (verified at HEAD,
  `controlplane/agent/{init,boot}_steps.go`) — only claude bits are
  `ConfigSeedScript` body + post-init marker path. Fix by extending the
  existing self-gating-script philosophy (exec.go comment: CP stays
  feature-flag-free):
  - seeds become BUILD-time: bundler writes descriptor seeds to generic
    `~/.clawker/seed/` + manifest (dest + copy-if-missing|jq-merge per file);
    CP dispatches ONE generic apply script for all harnesses — config step
    needs zero runtime harness knowledge, not even the label
  - post-init marker `$HOME/.claude/post-initialized` → `DotClawkerDir`
    (wart-fix, independent)
  - `routeArgs` default cmd: CP reads image `Config.Cmd[0]` via docker
    inspect → spawn-dispatch payload; no descriptor lookup needed
  - neutral log event names (`spawn_argv_routed_to_claude` → generic)
  Net: CP/clawkerd need NO descriptor access at all; label used only by
  CLI-side resolution + filtering.
- **3f CLI surface:** root branding harness-neutral; `generate` per-harness
  version source; run/create examples; harness-aware default aliases (drop
  hardcoded `--dangerously-skip-permissions` for non-claude); `clawker skill`
  → generic command + per-harness selector (descriptor field for
  skill/extension install, or explicit "none").
- **3g workspace mounts:** projects-equivalent live-bind from descriptor
  host-state field; `MountProjectsEnabled` generalized.

### Phase 4 — additional harnesses, strict order: codex → opencode → pi
Each lands as its own ~yaml-only commit; each is a fresh abstraction proof.
Codex first (below), then opencode, then pi — same acceptance shape.

#### codex.yaml
- Embedded codex descriptor: install (verify real distribution), config-dir
  `~/.codex`, credentials env `OPENAI_API_KEY` + file `auth.json`, AGENTS.md
  instruction dest, egress (api.openai.com, auth endpoints — verify live),
  no telemetry mapping, no plugin staging.
- Acceptance: `clawker build --harness codex && clawker run @ --harness codex`
  boots codex against real API through firewall. e2e per-harness smoke.
- This commit should be ~yaml-only. Every line of Go it needs = an engine gap
  to fix in-place, not to hack around.

### Phase 5 — user-custom harness UX
- User descriptor dir (settings-adjacent), JSON Schema published, docs page
  "add your own harness", `clawker harness list/validate` (or similar)
  discoverability. clawker-support skill docs updated.

## Risk register
- **Schema freeze pressure:** descriptor schema is public contract from Phase 5;
  Phase 0 two-descriptor pressure-test is the mitigation.
- **Plugin/skill system:** claude-specific (plugins staging stays as manifest
  json_rewrites; `clawker skill` selector). Other harnesses declare none.
- **statusline.sh / prompt assets:** claude assets move behind descriptor asset
  refs; embedded-vs-user-path asset resolution needed.
- **Telemetry:** only claude has OTEL mapping today; descriptor allows none.
- **Test matrix:** golden files grow per (template × harness); keep harness
  dimension in golden seed scenarios.
- **Back-compat:** existing `:latest` images + `agent.claude_code` config keys —
  both get shims + deprecation warnings, alpha tolerance for eventual removal.

## Status

**2026-07-04 (post-MVP, pre-UAT): per-harness agent runtime + codex bundle doc-verified.**
- User: "take agent and convert it to per-harness schema" → AskUserQuestion resolved to **shared base + per-harness layer** (agent: stays harness-agnostic; the deprecated-shim and kill-agent options were rejected).
- `HarnessConfig` gained `env_file/from_env/env/post_init/pre_run` (schema.go). Composition: env = agent spec then harness spec, harness wins on collision, each spec internally env_file<from_env<env (`shared.ResolveAgentEnv(agent, harnessCfg, harnessName, projectDir, log)` + `applyEnvSpec` w/ scope-prefixed diagnostics `harnesses.<name>.from_env`); hooks = concat base→harness via `Project.PostInitFor/PreRunFor` (+ unexported nil-tolerant `postInit/preRun` accessors, `composeHookScript`).
- Wiring: `buildCreateTimeEnv` + `injectPostInitIfConfigured` (container_create.go, both resolve via `bundler.ResolveHarnessName(cfg,"")`); `BootstrapServicesPreStart` pre_run → `PreRunFor` (container_start.go — start-time re-resolution same as EgressRules; per-container `dev.clawker.harness` label fixes this properly in Phase 2).
- Tests: `agentenv_test.go` NEW (shared_test pkg, envAgentCfg/envHarnessCfg exhaustive-literal builders), `TestPostInitFor/TestPreRunFor` in schema_harness_test.go (incl. legacy claude_code composition + no-leak), `TestCreateContainer_HarnessPostInit` (harness-only hook injects). `testHarnessCfg` helper replaced 9 HarnessConfig literals in shared tests (exhaustruct). gen-docs schemas regenerated. Gate: make test 5431 green, `golangci-lint run ./...` 0 issues.
- Codex bundle (.codex-bundle-experiment/) corrected after developers.openai.com/codex review: egress = api.openai.com/auth.openai.com/chatgpt.com ONLY (registry.npmjs.org removed — npm install is build-time on host daemon network); unattended_flag = `--dangerously-bypass-approvals-and-sandbox` (--full-auto deprecated); staging = AGENTS.md + `dirs: [prompts]` + auth.json credential (config.toml NOT copied: host-path-keyed `[projects]` trust table + mcp command paths, and containerfs filter vocab is JSON-only — a TOML harness wanting key-filtering needs a future `toml_keys` verb); seeds block deleted (policy: seeds = managed config + credential copying only, never user-config defaults — auto-memory feedback_seeds_managed_config_and_creds_only).
Spec written 2026-07-03. Phase 0 DONE. **Phase 1 POC IMPLEMENTED 2026-07-03 (uncommitted, feat/multi-harness) — awaiting user review.**

### Extraction phases 3b/3c-partial/3d/3e/3g IMPLEMENTED 2026-07-04 (uncommitted, all green: 5407 tests, lint 0)
- **3b egress floor:** `requiredFirewallRules`/`RequiredFirewallRules()`/`RequiredFirewallDomains()`/`GetFirewallDomains` DELETED from config. Floor lives in harness.yaml `egress:` (claude's carries the .claude.ai UGC-deny + SNI/docker-registry comments). `bundler.EgressRules(cfg, name)` (bundler/egress.go) composes floor ⊕ `cfg.ProjectEgressRules()` (renamed from EgressRules — rename forces compile break so no caller silently loses the floor). Conversion verbatim field-for-field; firewall.NormalizeRule fills proto/port/action server-side so store keys stay stable. Consumers: firewall refresh.go + container_start.go. Floor semantic guards moved to bundler/egress_test.go (TestEgressRules_ClaudeFloor etc. incl. external-bundle + multi-default-error cases). Tests isolate CLAWKER_CONFIG_DIR to temp so materialized bundles can't interfere.
- **3e seeds:** master Dockerfile.tmpl gained generic seed-staging section after block_4 (COPY per seed → `/home/${USERNAME}/.clawker/seed/<dest>` + heredoc `seed-manifest`: `config_dir=<rel>` header + `<apply> <dest>` lines). claude block_4 seed COPYs deleted; context_files trimmed to clawker-agent-prompt.md (Load auto-unions seed files into ContextFiles + validates file/dest plain-name + apply enum — `harness.SeedApply*` consts). CP `ConfigSeedScript` rewritten as generic manifest interpreter (consts.SeedSubdir="seed", SeedManifestFile="seed-manifest"; case on apply token; json-merge = existing wins). Post-init marker → `~/.clawker/post-initialized` (consts.PostInitMarkerFile; recreate now re-runs post_init — container-lifecycle semantics, was config-volume-persisted). dockerfile_test composes paths from consts so template↔script drift fails. Goldens regenerated + reviewed. **Old images (`.claude-init`, no manifest) no-op the new seed step — rebuild required; alpha tolerance.**
- **3e routeArgs:** `AgentReady` proto gained `default_cmd` (CP dialer resolves image `Config.Cmd[0]` via `imageDefaultCmd` — container inspect → image inspect, degrades to "" w/ `event=image_default_cmd_unavailable`); `BootPlan(defaultCmd)`; clawkerd `spawnEntry func(string) error`, `routeArgs(argv, lookPath, defaultCmd)` — empty default disables routing; event renamed `spawn_argv_routed_to_default_cmd`.
- **3d containerfs:** full manifest interpreter. `ResolveHostConfigDir(harness.ConfigDirSpec)` (env override + home-relative default), `ResolveHostMountSource(spec, subdir)` (replaces ResolveHostProjectsDir), `PrepareConfig(log, staging, ...)` (files w/ json_keys allowlist via filterJSONKeys, dirs, trees w/ skip + json_rewrites — `harness.RewritePrefixSwap`/`RewriteReplaceWithWorkdir`), `PrepareCredentials(log, staging, hostDir)` (ordered chain; keyring via NEW `keyring.GetRawByService(service)`; file; env type = "not stageable" error until needed). containerfs/consts.go DELETED (layout is manifest data now). harness.Load gained validateStaging (rewrite + credential-type vocab). Old test suite passes against claude-shaped fixture = parity proof.
- **3g workspace:** `GetConfigVolumeMounts(project, agent, configDirPath)` (volume target from manifest; empty path = no config volume), `GetHostStateMount(hostDir, configDirPath, destSubdir)` (replaces GetClaudeProjectsMount/ClaudeProjectsTargetPath), SetupMountsConfig gained `Harness harness.Staging`; mounts loop over staging.mounts, still gated on MountProjectsEnabled (3a renames the key). shared/container_create.go `loadSelectedHarness()` feeds SetupMounts + InitContainerConfig (InitConfigOpts gained `Staging`).
- **3c telemetry:** docker/env.go monitoring-down now sets `OTEL_{METRICS,LOGS,TRACES}_EXPORTER=none` (consts.EnvOTel*Exporter/OTelExporterNone); `EnvClaudeCodeEnableTelemetry` deleted.

### EXTERNAL UGC EXPERIMENT (DONE-criterion gate) — BUILD PATH PROVEN LIVE 2026-07-04
Compiled `bin/clawker`, zero recompile: authored codex bundle at scratchpad `harness-exp/bundles/codex/` (harness.yaml: npm resolver @openai/codex, CODEX_HOME/.codex staging, auth.json file credential, openai egress, config.toml seed; tmpl: block_1 apt node, block_3 ENV CODEX_HOME, block_4 npm install to user prefix, block_6 CMD codex) + settings.yaml registry `codex: {default: true, path: ...}`. `clawker project init --yes && clawker build` → **`clawker-codexp:latest` built**: resolver fetched real 0.142.5, CMD ["codex"], codex-cli 0.142.5 runs at ~/.local/bin, seed-manifest `config_dir=.codex`, NO claude anywhere. Bundle iteration loop proven (first build failed: `npm install -g` as user hit /usr/local EACCES exit 243 → fixed in BUNDLE ONLY: `npm config set prefix ~/.local`). Bundle is DRAFT — egress/install verified against training data only.
**Run path NOT live-proven** (in-container session cannot run clawker run — would touch host CP). Host-side UAT needed: registry entry + `clawker run` with codex default; verify CP seed apply, staging skip (no ~/.codex on host = config copy skipped), egress floor sync (codex floor, no anthropic), codex CMD spawn.

### 3a/3f MVP IMPLEMENTED 2026-07-04 (later same session; 5412 tests, lint 0)
- `ClaudeCodeConfig`→`HarnessConfig` (+`HarnessConfigOptions`, `ConfigStrategyCopy`/`ConfigStrategyFresh` consts); project root gained `harnesses: map[string]HarnessConfig` (KindStructMap); `Project.HarnessConfigFor(name)`: map entry → legacy `agent.claude_code` (built-in default harness ONLY — never leaks onto codex; TestHarnessConfigFor pins) → nil=defaults. Legacy key kept, desc marked Deprecated; no file migration (reading shim only).
- `after_harness_install` inject added; `after_claude_install` deprecated alias — bundler merges legacy-first into `DockerfileInject.AfterHarnessInstall`, renders at same position (goldens: comment-only diff, reviewed).
- Threading: `InitConfigOpts.Harness *config.HarnessConfig` + `SetupMountsConfig.HarnessConfig`, both resolved via `HarnessConfigFor(bundle.Name)` in container_create; workspace no longer reads project.Agent directly.
- Version naming: `DefaultClaudeCodeVersion`→`DefaultHarnessVersion`, `BuilderOptions/ProjectGenerator.ClaudeCodeVersion`→`HarnessVersion`, `ResolveLatestClaudeCodeVersion`→`ResolveLatestHarnessVersion`. root.go branding harness-neutral. Error texts generic. Schemas/docs regenerated.
- **Formatting gotcha hit again:** `.golangci.yml` has `new-from-merge-base: main` — `golangci-lint fmt` on schema.go realigned ALL struct-tag columns → every snake_case yaml tag line became "changed" → tagliatelle wave; converged by trailing `//nolint:tagliatelle` on every snake-tag line in schema.go (harness.go precedent). nolint:golines is USELESS (formatters ignore nolint; nolintlint flags it unused).
- Still deferred from 3f: alias `--dangerously-skip-permissions` → unattended_flag consumption; `clawker skill` per-harness selector; `generate` cmd claude-only (`ClaudeCodePackage` kept — genuinely claude); CLAUDE.md docs sweep.
- Host-side live UAT: claude image rebuild + boot (new seed mechanism), codex run path.
- Docs sweep: bundler/CLAUDE.md (very stale), containerfs/workspace/shared CLAUDE.mds, clawkerd/CLAUDE.md spawnEntry signature, Mintlify.
- Then: bake codex in as shipped bundle (after real-codex verification), block naming (user), Phase 2 identity.

### POC state (what exists in the working tree)
- Master `internal/bundler/assets/Dockerfile.tmpl`: **6** `{{block "block_N" .}}{{end}}` slots (GENERIC placeholder names block_1..block_6 — final names still TBD by user), `{{.HarnessConfigDir}}` param in runtime-dirs RUN, `{{.HarnessVersion}}` in header.
- **Node moved out of master (2026-07-04, user correction: node is claude's dep, not clawker's — clawkerd is static Go).** New block_1 = root scope post-packages/docker-CLI pre-user-context (heavy-toolchain cache zone, exactly where node sat) — claude's block_1 define carries NODE_USE_SYSTEM_CA + full root node install (ARG NODE_VERSION + arch matrix) verbatim. nvm RUN moved to claude block_4 head (above version ARG; .zshenv touch + SHELL zsh stay master). `-p nvm` dropped from master's debian zsh-in-docker (nvm still loads via installer's PROFILE→.zshenv). Blocks renumbered: old 1..5 → 2..6. Multiset-diff of goldens = pure moves + comment rewording only.
- OPEN from node move: (a) alpine master package list still carries node-source-build toolchain (g++/python3/linux-headers etc., comment says "nvm source-builds Node on musl") — node-motivated, candidate to move to claude bundle's apk step; (b) after_user_switch inject + root_run no longer see node (arrives block_1 for root scope — root_run at ~317 runs AFTER block_1 so root_run DOES see node; but after_user_switch inject runs before block_4's nvm); (c) codex bundle will need its own node for npm install (or use GH-release native bins) — per-harness toolchain duplication is the accepted cost.
- `internal/bundler/assets/harnesses/claude_code/`: harness.yaml (full slim schema incl. seeds/staging/egress/unattended_flag/skill_install — only version+context_files+staging.config_dir consumed so far) + Dockerfile.harness.tmpl (5 verbatim-lift defines) + statusline.sh/claude-settings.json/claude-config.json/clawker-agent-prompt.md (git-mv'd).
- `internal/harness` pkg: Manifest types, Bundle+Load (fs.FS), Compose (master+fragment parse-set; validates defines ⊆ declared blocks, rejects reserved inject-key names), Materialize (copy-if-missing, user edits win), FileMode. Tests: compose override/reject + materialize no-clobber.
- `internal/bundler/harness.go`: `//go:embed all:assets/harnesses`, DefaultHarnessName=claude_code, `ResolveHarnessName(cfg, explicit) (string, error)` (explicit → single registry entry with `default: true` → builtin; ERRORS when >1 entry marked default, sorted names in message — user-required validation 2026-07-04), HarnessBundleDir (registry entry `.path` → `<config>/harnesses/<name>`), DONE CRITERION (user 2026-07-04): POC done only when claude is FULLY extracted (build AND runtime — 3a-3g) AND a second harness works as pure external UGC bundle (registry path + bundle dir on a compiled binary, no recompile); proven external candidates then get baked in as shipped bundles. Custom bundle today: builds correctly end-to-end via registry path + default:true (only selector until Phase 2), but runtime prep (containerfs seeds, keyring credentials, workspace init, egress floor) still runs claude-hardwired paths. Phase 2 must include: validate registry keys (user-generated) against docker tag grammar `[A-Za-z0-9_][A-Za-z0-9._-]{0,127}` at resolution/registry-read with error naming settings.yaml — decided 2026-07-04: no validation in POC (tag not yet derived from name), do NOT rely on docker's late/cryptic "invalid reference format". HARNESS SLUG = **`claude`** (renamed from claude_code 2026-07-04 — user: underscores invalid style for image tags, registry key IS the tag; "claude" chosen for symmetry w/ codex/opencode/pi when user didn't answer claude vs claude-code — one-const swap to change: `consts.DefaultHarnessName` + `git mv assets/harnesses/claude`; bundler.DefaultHarnessName aliases consts). Settings migration `migrateSeedHarnessRegistry` (config/migrations.go, appended to SettingsMigrations): seeds `harnesses: {claude: {default: true}}` into settings.yaml lacking the key at ANY store load; populated or `harnesses: {}` untouched; TestMigrateSeedHarnessRegistry 4 subtests incl. byte-stable reload; monitoring no-op fixture gained `harnesses: {}` to stay isolated. `EnsureHarnesses` (renamed from MaterializeHarnesses 2026-07-04, user directive "registry is the customization surface — ensure files AND registry at build time"): materializes bundles + `ensureHarnessRegistry`/`seedShippedEntries` seeds a settings entry per shipped harness (entry-granular copy-if-missing — existing entries NEVER touched; built-in default gets `default: true` only when created fresh and no other entry holds flag; no-op → no write). LoadHarness (materialized dir wins; embedded fallback keeps tests hermetic). Callers: build.go warn-on-fail (wraps resolve as "resolving harness"), dockerfile.go ×2. Zero-default resolution keeps builtin fallback as safety net for hermetic/no-write contexts. Tests: TestEnsureHarnesses_SeedsRegistryAndBundles (file-backed NewIsolatedTestConfig, byte-stable idempotency) + TestEnsureHarnesses_NeverClobbersUserEntries.
- Settings schema (2026-07-04): `harnesses: map[string]HarnessSettings` — struct entry `{default bool, path string}`, `default_harness` key REMOVED (default = per-entry flag). Backed by NEW native `storage.KindStructMap` (map[string]struct): field.go const+String+normalizeStruct case, store.go kindAccepts (accepts reflect.Map) + isOpaqueField (opaque like StructSlice), defaults panic on default-tag, storeui classifyAndFormat ("N entries") + BrowserStructSlice editor + alias, configdoc "object map", jsonschema `additionalProperties: <struct schema>`. Native case wins over KindFunc (fallback-only unchanged); KindFunc tests flipped to map[string][]string as the unsupported exemplar. Tests: bundler/harness_test.go (resolution table incl. multi-default error + bundle-dir), storage TestNormalizeFields_StructMap, storeui TestWalkFields_StructMap_ClassifiedNatively. JSON schemas + configuration.mdx regenerated.
- DockerfileContext: ClaudeVersion→HarnessVersion + new HarnessConfigDir. ProjectGenerator: `Harness` field + lazy bundle; Generate/GenerateDockerfiles compose via harness.Compose; all 3 context stagers loop bundle.Manifest.ContextFiles instead of hardcoded embeds (StatuslineScript/SettingsFile/ConfigFile/AgentPromptFile embed vars DELETED).
- `ResolveHarnessVersion(ctx, client, bundle)` per manifest resolver (npm|none; github-release errors); ResolveOptions gained Package. build.go: MaterializeHarnesses + LoadHarness + ResolveHarnessVersion replace hardcoded ResolveLatestClaudeCodeVersion call (old func kept).
- **Gate**: new `internal/bundler/golden_test.go` (5 scenarios, GOLDEN_UPDATE=1). Output byte-identical to pre-refactor EXCEPT one reviewed 4-line move: CLAUDE_CONFIG_DIR comment+ENV now renders after LANG (block_2 contiguity: master's TERM/COLORTERM/LANG previously interleaved). Verified identical shape across all 5 scenarios.
- **Proven**: full `docker build` of generated context succeeded (1.85GB, ENTRYPOINT clawkerd, CMD ["claude"] from block_5; claude 2.1.200, seeds, managed-settings, agent prompt, OTEL env all verified in-image). `make test` 5379 green; `golangci-lint run ./...` 0 issues.

### POC gaps (known, deliberate)
- Phase 2 identity untouched: image tag still `:latest`, no @:tag selection, no completions, no dev.clawker.harness label.
- harness.yaml seeds/staging/egress/credentials/skill_install parsed but UNCONSUMED (containerfs/CP/egress extraction = phases 3b-3e).
- Version resolution still funnels through DefaultClaudeCodeVersion const naming; generate cmd (versions.json flow) still claude-package-only.
- Comment neutralization not done (master comments still say Claude Code) — separate comments-only commit per plan.
- bundler CLAUDE.md + user docs not updated.
