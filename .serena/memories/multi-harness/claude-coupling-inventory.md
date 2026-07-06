# Multi-Harness: Claude-Coupling Inventory

Planning artifact for making clawker support any coding agent harness (Claude Code,
Codex, OpenCode, pi, ...) instead of being hardwired to Claude Code.

Produced by tracing every code path from the `run` / `create` / `start` entrypoints
(`internal/cmd/container/{run,create,start}`). Six discovery passes covered: image
build, container create, in-container init/dispatch, config schema, auth/credentials,
CLI surface + project init.

## Big picture

- There is **no harness abstraction**. The word "agent" everywhere means *the clawker
  container*, never *the coding tool*. Claude Code is assumed to be THE harness.
- Coupling is **concentrated, not smeared**: config schema, firewall/CP/clawkerd
  plumbing, workspace, and language presets are already harness-neutral.
- A new `Harness` descriptor must be born in the config layer and flow outward into the
  bundler (Dockerfile), containerfs (host-config staging), CP init scripts, clawkerd
  spawn routing, required-egress defaults, and telemetry env.

---

## 1. Config schema — the missing abstraction point

Where a `harness` descriptor must be born.

| file:line | thing | fix |
|---|---|---|
| `internal/config/schema.go:72-82` | `ClaudeCodeConfig` block (`agent.claude_code`: `config.strategy`, `use_host_auth`, `mount_projects`) — the ONLY harness binding | replace w/ harness selector (`agent.harness: claude-code\|codex\|opencode`) + generic harness config block |
| `schema.go:91,97-120` | `AgentConfig.ClaudeCode` field + 4 accessors (`UseHostAuthEnabled`/`MountProjectsEnabled`/`ConfigStrategy`) | generic `HostAuthEnabled()`/`MountStateEnabled()`/`ConfigStrategy()` on harness |
| `schema.go:68` | `InjectConfig.AfterClaudeInstall` (`after_claude_install`) Dockerfile inject point | `after_harness_install` |
| `schema.go:93-94` | `post_init`/`pre_run` descs encode "before Claude Code launches" / "default: claude" | field agnostic; default-CMD must derive from harness |
| `schema.go:20` | default aliases `go`/`wt` pass `--dangerously-skip-permissions` (Claude flag) | harness-aware default aliases |
| `schema.go:439-445` | `TelemetryConfig` shape maps 1:1 to Claude OTEL env | per-harness telemetry mapping |

**No `harness`/`agent-type`/`codex`/`opencode` anywhere in config or consts.** Built from scratch.

### CRITICAL: `build` and `agent` blocks are INTRINSICALLY harness-coupled (not extractable)

The `build` and `agent` blocks are intrinsically harness-coupled — the harness is woven into both
and cannot be cleanly lifted out:

- **`build` installs the harness.** The generated Dockerfile's install step (`curl claude.ai/install.sh`)
  is the harness install; `InjectConfig.AfterClaudeInstall` (schema.go:68) is a claude-named inject
  point ("add MCP servers, install plugins"); `AfterUserSetup`/`AfterUserSwitch` descs say "claude user".
  Build is harness-shaped at its core, not just in a sub-block.
- **`agent` hooks are harness setup.** `agent.post_init` (schema.go:93) desc = "install MCP servers …
  seeding claude code config"; `pre_run` (schema.go:94) = "before the CMD (default: claude)". The
  claude MCP setup users put in `post_init` is *harness setup misfiled as project config* — it will
  fire claude commands inside a codex container and fail every start.

**Model implication:** the selected harness must **contribute fragments** that compose with the
project's generic config at build time:
- build frag — install step, inject defaults, required packages
- agent frag — its own init/MCP hooks (claude MCP belongs to the claude harness, NOT project post_init)
- egress frag — its required floor + security path-rules (below)

The refactor = **extract claude-specific bits OUT of project config + `defaults.go` INTO the claude
harness definition**, then compose `selected-harness fragments ⊕ project generic build/agent`. Project
config keeps only genuinely harness-agnostic parts (base image, extra packages, project-specific env,
non-harness post_init). This is why harness defs can't simply move to settings and leave project
"clean" — project's build/agent must still receive the harness's contributed fragments.

### Required firewall floor — hardcoded Claude, always-merged, non-removable (MECHANIC-LEVEL)

`config/defaults.go:15-47` — `requiredFirewallRules []EgressRule`. `Config.EgressRules()` composes
**baseline ⊕ project `security.firewall` rules ⊕ `add_domains`** (config CLAUDE.md). The baseline is
the egress **floor**: always present, NOT removable from project config. All 11 entries are Claude:
- `api.anthropic.com`, `claude.com`, `platform.claude.com` (API + OAuth token exchange)
- `.claude.ai` — host-allow for login, BUT carries **bespoke security PathRules**: deny `/public/` +
  `/share/` so a prompt-injected agent can't pivot into fetching attacker-authored UGC from a trusted
  origin. `PathDefault` empty → `EffectivePathDefault` = allow (denylist mode) to keep OAuth/login intact.
- `mcp-proxy.anthropic.com` (MCP proxy)
- `registry.npmjs.org` (npm — only because claude is npm-installed + Node baked in)
- `sentry.io`, `statsig.anthropic.com`, `statsig.com`, `.datadoghq.com`, `.datadoghq.eu` (telemetry/flags)

**Harness-fatal:** build a codex image and the floor STILL forces anthropic egress AND omits codex's
real required egress (OpenAI endpoints) → codex can't reach its API. The floor must become the
**selected harness's required egress** (a per-harness fragment), including that harness's own
security path-rules. The `.claude.ai` UGC-deny is security knowledge that lives WITH the claude
harness, not in a shared default. (`requiredFirewallDomains` at :52-59 is a derived back-compat list.)

## 2. Image build / Dockerfile — install + default CMD

> RECON COMPLETE (2026-07-03, full template read at HEAD). Mechanics:
> - **Install**: `ARG CLAUDE_CODE_VERSION={{.ClaudeVersion}}` sits DIRECTLY above its
>   consumer RUN — BuildKit invalidates at ARG DECLARATION line; hoisting re-runs every
>   layer below (comment documents this). Install = `curl claude.ai/install.sh | bash -s
>   ${VER}` + optional BuildKit cache mount `~/.npm` (cache-mount path is harness-specific).
> - **Seeds**: 3 COPYs (statusline.sh, claude-settings.json→settings.json,
>   claude-config.json→.config.json) → `~/.claude-init/`, deliberately in USER scope
>   BEFORE `USER root` so after_claude_install/before_entrypoint injects + user Copy can
>   reference them.
> - **Env**: `CLAUDE_CONFIG_DIR` + telemetry block (`CLAUDE_CODE_ENABLE_TELEMETRY`,
>   `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA`, OTEL_* fed by template params) = claude;
>   PATH/.local/bin, BROWSER=host-open, TERM/LANG = generic.
> - **managed-settings.json** heredoc in EARLY root scope — claude enterprise mechanism
>   (PATH into CC shell snapshot); position load-bearing for build-time `claude` calls
>   in inject points.
> - **Prompt asset**: `COPY clawker-agent-prompt.md /etc/claude-code/CLAUDE.md` in late
>   root block (clawker-release cache block). `CMD ["claude"]` last; ENTRYPOINT clawkerd
>   generic.
> - **KEY STRUCTURAL FINDING**: template is cache-locality-ARCHITECTED (early-root /
>   user-scope / late-root blocks, ARG adjacency, seed placement). Harness content must
>   fill FIXED SLOTS in the master template (managed-config slot, env slot, install
>   ARG+RUN slot, seed slot, prompt-asset slot, CMD) — NOT free-form fragment
>   concatenation. Descriptor supplies slot contents; template owns ordering + cache
>   strategy. Naive per-harness template assembly would destroy the cache design.

| file:line | thing | fix |
|---|---|---|
| `internal/bundler/assets/Dockerfile.tmpl:638` | **`CMD ["claude"]`** — single most load-bearing default | harness-defined entrypoint command |
| `Dockerfile.tmpl:544-552` | `ARG CLAUDE_CODE_VERSION` + `curl claude.ai/install.sh \| bash` install | harness install command/URL |
| `Dockerfile.tmpl:447,453-483` | `ENV CLAUDE_CONFIG_DIR`, `CLAUDE_CODE_ENABLE_TELEMETRY`, OTEL block | harness config-dir env + telemetry env |
| `Dockerfile.tmpl:390-413` | `/etc/claude-code/managed-settings.json` PATH-injection heredoc | harness managed-config step |
| `Dockerfile.tmpl:558-560,606` | `COPY` claude seeds → `~/.claude-init/*` + prompt → `/etc/claude-code/CLAUDE.md` | harness seed-file set + dest paths |
| `bundler/versions.go:17` | `ClaudeCodePackage = "@anthropic-ai/claude-code"` | harness package/resolver — **npm-shaped resolver assumes npm distribution; Codex/OpenCode use GitHub releases/binaries → need new `Fetcher` impls** |
| `bundler/dockerfile.go:60,91,256` | `DefaultClaudeCodeVersion`, `ClaudeVersion` threaded end-to-end → `BuilderOptions.ClaudeCodeVersion` (`docker/builder.go:39-44,83`) → template | rename to `HarnessVersion` |
| `bundler/assets/statusline.sh` | entire file = Claude statusline (parses Claude JSON, hits `api.anthropic.com/oauth/usage`) | harness-provided asset or omit |
| `bundler/assets/claude-config.json` | `{"hasCompletedOnboarding":true}` — onboarding bypass payload | harness onboarding-bypass file |

Base images (`buildpack-deps`/`alpine`) are **harness-neutral** — no change. Node baked in
*because* Claude hooks shell to `node`.

### `internal/cmd/image/build/build.go` — the build command (build-time harness-selection injection point)

This is where the **decided model** puts harness selection: `clawker build --harness <name>`
(default from `settings.default_harness`). Today it is single-harness/Claude-only.

| file:line | thing | fix |
|---|---|---|
| `build.go:217` | `imageTag := docker.ImageTag(projectName)` — image tag is **project-keyed only** | becomes (project, harness)-keyed — N project images, one per built harness; harness name into tag/labels |
| `build.go:228` | `docker.NewBuilder(client, cfg, wd, projectName)` — no harness dimension | thread selected harness descriptor into builder |
| `build.go:237` | `claudeCodeVersion := bundler.DefaultClaudeCodeVersion` | per-harness version default from descriptor |
| `build.go:238-250` | resolves Claude Code "latest" via npm; warning "Could not resolve latest Claude Code version", debug field `claude_code_version` | per-harness version resolver (npm vs github-release vs binary); only runs for npm-distributed harnesses |
| `build.go:261-275` | `BuilderOptions{ ... ClaudeCodeVersion: claudeCodeVersion }` | harness-agnostic version field + descriptor |
| `build.go:305-307` | progress display `Title: "Building "+projectName`, `Subtitle: imageTag` | surface harness in build progress |
| `build.go` (whole) | **no `--harness` flag exists** | add `--harness` flag (default = settings `default_harness`); resolve descriptor here, drives Dockerfile gen + image identity |

Note: `auth.EnsureAuthMaterial()` (build.go:131) is clawker CP/firewall CA material — **harness-agnostic**, no change. BuildKit detection, label/build-arg parsing, iidfile, progress wiring all harness-neutral.

## 3. Container create — host-config staging (MECHANIC-LEVEL, verified against code)

`internal/containerfs/` is a bespoke staging pipeline with **Claude-plugin-registry-internal
knowledge baked in**. Not "copy a config dir" — it filters JSON keys, rewrites specific JSON
fields host→container, and skips named cache files. This is the **reference spec** the harness
descriptor must generalize from. Every line below verified against `containerfs/containerfs.go`
+ `consts.go` (read 2026-06-23).

### `ResolveHostConfigDir` (containerfs.go:31-58)
- `$CLAUDE_CONFIG_DIR` if set → `filepath.Abs` normalize, must exist + be dir, else hard error.
- else `~/.claude` (`consts.ClaudeDir`) if exists+dir.
- else error "claude config dir not found on host".

### `PrepareClaudeConfig` (containerfs.go:85-140) — stages into `<tmp>/.claude/`
1. **settings.json** — `stageSettings` (253-287): read host `settings.json`, JSON-parse, extract
   **ONLY the `enabledPlugins` key**, write `{enabledPlugins: ...}` filtered. Skip if file/key absent.
   (Claude settings can hold secrets/host-specific junk → deliberate allowlist of one key.)
2. **agents/ skills/ commands/** — `stageDirectory` (291-306): full recursive copy each, `EvalSymlinks`
   on the source dir, skip-if-missing.
3. **CLAUDE.md** — direct `copyFile` if present (user-level instructions). *(codex/opencode use a
   different filename — `AGENTS.md` etc — and different location.)*
4. **plugins/** — `stagePlugins` (308-379): WalkDir-copy the whole `plugins/` tree INCLUDING `cache/`,
   but **skip top-level `install-counts-cache.json`**. Then rewrite two registry JSONs:
   - `known_marketplaces.json`: keys `installPath` + `installLocation` → **prefix-swap**
     `<host>/.claude/plugins` → `<containerHome>/.claude/plugins`.
   - `installed_plugins.json`: `installPath` → **prefix-swap** (same); `projectPath` → **full replace**
     with `containerWorkDir`.
   - broken symlinks: warn + skip (Claude leaves dangling cache symlinks after plugin updates).
   - rewrite engine = `pathRewriteRule{key, hostPrefix, containerPath}` + recursive `rewriteJSONPaths`
     (382-441): `hostPrefix != ""` = prefix-swap-if-matches; `hostPrefix == ""` = replace whole value.

### `PrepareCredentials` (containerfs.go:142-197) — stages `<tmp>/.claude/.credentials.json` (mode 0600)
1. keyring `GetClaudeCodeCredentialsRaw()` → write blob **VERBATIM, never via typed struct** (a
   round-trip fabricates a zero `organizationUuid` the refresh endpoint rejects, and drops unmodeled keys).
2. fallback file `<hostConfigDir>/.credentials.json` byte-for-byte.
3. neither → error "authenticate on the host first or set `agent.claude_code.use_host_auth: false`".

### projects/ — NOT staged here
Live **bind mount** in `internal/workspace` (`GetClaudeProjectsMount`, strategy.go:82-114), overlays
the per-agent config volume at `/home/claude/.claude/projects` so auto-memory + session jsonls persist
across runs. Distinct mechanism from the copied config volume (copy vs live-bind).

### What this means for the descriptor (KEY FINDING)
Per-harness staging needs **code, not just declarative config**. The JSON-key path-rewriting
(`installPath`/`installLocation`/`projectPath` in named registry files) is bespoke Claude-plugin
knowledge — no generic data schema expresses it cleanly. So the harness abstraction is likely a
**Go interface (`HarnessStager`) with per-harness implementations**, NOT a pure-yaml descriptor.
Declarative fields (config-dir env, dirs-to-copy, settings-key allowlist, cred source, instruction
filename, copy-vs-bind) cover the *common* shape; an interface method covers the *bespoke* per-harness
fixups. This directly tempers the "fully config-driven" goal — some harness logic must be baked Go.

### Wiring + the rest
| file:line | thing | fix |
|---|---|---|
| `containerfs/consts.go:7-28` | the layout contract: `CLAUDE_CONFIG_DIR`, `.credentials.json`, `settings.json`, `CLAUDE.md`, `enabledPlugins`, `agents/skills/commands/plugins`, `known_marketplaces.json`/`installed_plugins.json`/`install-counts-cache.json` | per-harness layout descriptor (data) + stager impl (code) |
| `cmd/container/shared/containerfs.go:40-112` | `InitConfigOpts.ClaudeCode`, `ConfigStrategy()=="copy"` gate, dest `~/.claude` | harness config block + selected stager + target |
| `cmd/container/shared/container_create.go:~1599` | passes `ClaudeCode: projectCfg.Agent.ClaudeCode` into init | pass harness-selected descriptor |
| `workspace/strategy.go:82-114`, `setup.go:202-220` | config-volume + projects bind land at `~/.claude/...`; gated on `MountProjectsEnabled()` | harness mount targets + copy-vs-bind list |
| `docker/env.go:143-148` | injects `CLAUDE_CODE_ENABLE_TELEMETRY=0` when monitoring down | harness telemetry env |

## 4. Auth / credentials — OAuth blob only, NO API-key path

**Critical: zero `ANTHROPIC_API_KEY` plumbing anywhere.** Auth is purely OAuth-credential-blob
(Claude.ai subscription). A harness authing via API key (Codex/OpenAI key) has **zero existing
infrastructure** — net-new work.

| file:line | thing | fix |
|---|---|---|
| `internal/keyring/claude_code.go:7` | `claudeCodeCredentialsService = "Claude Code-credentials"` keyring service | per-harness keyring service (or none) |
| `keyring/claude_code.go:11-61` | `ClaudeAiOauth` typed schema + `GetClaudeCodeCredentialsRaw` | per-harness raw blob fetch |
| `containerfs.go:169,195` | keyring → `.credentials.json` fallback; error names `agent.claude_code.use_host_auth` | harness credential source |
| `cmd/auth/` + `internal/auth/` | **OUT OF SCOPE** — that's CP mTLS/cert auth, not harness auth | — |

## 5. In-container init / dispatch (CP → clawkerd)

> PATH CORRECTION + recon COMPLETE (2026-07-03): package is `controlplane/agent/`
> (top-level, not `internal/controlplane/agent/`). Step audit at HEAD: init plan =
> config/git/git-credentials/ssh/post_init/agent-initialized; boot plan =
> docker-socket/pre_run/agent-ready. ALL generic except `ConfigSeedScript` body
> (`~/.claude-init`→`~/.claude`, statusline+.config.json+settings jq-merge) and
> post-init marker `$HOME/.claude/post-initialized`. Scripts are self-gating by
> design (CP feature-flag-free). Resolution in migration.md 3e: build-time seeds +
> generic apply script + marker→DotClawkerDir + routeArgs cmd from image Config.Cmd.

| file:line | thing | fix |
|---|---|---|
| `controlplane/agent/exec.go:58-75` | `configSeedScript` — copies `~/.claude-init/{statusline.sh,.config.json,settings.json}` → `~/.claude/`, jq-merges settings | harness seed descriptor `{seedSrc,configDir,files,merge}` |
| `exec.go:139-148` | `postInitScript` marker `~/.claude/post-initialized` | derive from harness configDir (other marker `/var/lib/clawker/agent-initialized` already generic) |
| `clawkerd/spawn.go:92-122` | **`routeArgs` prepends literal `"claude"`** when argv[0] is flag/not-on-PATH (docker `--help`-routing) | thread harness default-command string |
| `clawkerd/spawn_unix.go:252-260` | log `event=spawn_argv_routed_to_claude` | neutral event name |
| `clawkerd/progress.go:205-214` | step labels — **already neutral** ("Seeding agent config", "Running agent command") | no change |

## 6. Required egress + CLI surface

| file:line | thing | fix |
|---|---|---|
| `config/defaults.go:15-47` | `requiredFirewallRules` hardcoded Claude egress floor — **see section 1 "Required firewall floor" for the mechanic-level treatment** (always-merged via `EgressRules()`, non-removable, `.claude.ai` UGC-deny, codex-fatal) | per-harness required-egress fragment |
| `consts/consts.go:193,198` | `ClaudeDir=".claude"`, `ClaudeProjectsSubdir="projects"` | harness config-dir accessor |
| `consts/monitoring.go:105` | `EnvClaudeCodeEnableTelemetry` | per-harness telemetry env |
| `cmd/root/root.go:32-38` | branding: "Manage Claude Code...", "(claude + docker)", "Start Claude Code in a container" | harness-neutral |
| `cmd/generate/generate.go:42-74` | **entire command** = fetch `@anthropic-ai/claude-code` from npm → Dockerfiles | per-harness version source |
| `cmd/skill/*` | `clawker skill` is fully claude-hardwired — **see "clawker skill command" block below** | generic agent skill + per-harness selector flag |
| `cmd/image/build/build.go:230-249`, `cmd/monitor/monitor.go:19`, run/create examples | Claude version-resolve messaging, "telemetry for Claude Code", `--dangerously-skip-permissions` examples | harness-neutral |

`clawker init` + presets (Python/Go/Rust YAML) are **harness-agnostic** — coupling is in
schema defaults + the template they feed, not init logic.

---

## What a harness descriptor must carry

Distilled across all 6 areas, one `Harness` abstraction needs:

1. **binary/command** — default CMD (`claude`), routeArgs prepend
2. **install** — command/URL or package + version resolver (npm vs GitHub-release vs binary)
3. **config-dir** — env var name (`CLAUDE_CONFIG_DIR`), default dir name (`.claude`)
4. **credential source** — keyring service name + fallback file, **OR API-key env (new)**
5. **config staging manifest** — files/subdirs to copy, settings-key allowlist, plugin handling
6. **seed/onboarding** — seed files + dest, onboarding-bypass file, jq-merge strategy
7. **prompt/statusline assets** + dest paths (`/etc/claude-code/CLAUDE.md`)
8. **required egress** — domain/path allowlist
9. **telemetry** — env var name + mapping (or none)
10. **host-state mount** — projects-equivalent dir (or none)

## Sharp edges for planning

- **API-key auth = greenfield.** Codex needs an OpenAI key path; clawker has only OAuth-blob
  plumbing today.
- **npm resolver is load-bearing.** `versions.go` / `generate` assume npm. Non-npm harnesses
  need new `Fetcher` impls behind the existing interface.
- **`CMD ["claude"]` <-> `routeArgs "claude"` are coupled** (spawn.go comment says so) — must
  change together with the Dockerfile.
- **Plugin/skill system is deeply Claude-specific** (`stagePlugins` rewrites Claude plugin
  registry JSON; `clawker skill` IS a Claude plugin) — likely scoped out of v1 generalization.
- **Container username `claude`** (`consts.go:424`) is just a username, not a code path — low
  priority, cosmetic.

### `clawker skill` command — fully claude-hardwired (MECHANIC-LEVEL)

The clawker-support skill = a Claude Code plugin that gives the harness hands-on knowledge of
clawker internals. The command wraps the `claude plugin` CLI end-to-end:

- `skill.go:14-20` — parent command Short/Long: "Manage the clawker Claude Code skill plugin".
- `shared/shared.go:16-17` — `MarketplaceSource = "schmitthub/claude-plugins"`,
  `PluginName = "clawker-support@schmitthub-plugins"`.
- `shared/shared.go:20-29` — `ValidScopes = {user, project, local}` — the scopes the **Claude CLI**
  accepts for plugin ops.
- `shared/shared.go:31-41` — `CheckClaudeCLI` = `exec.LookPath("claude")`, error points to
  `docs.anthropic.com/.../claude-code`.
- `shared/shared.go:43-63` — `RunClaude` = `exec.CommandContext(ctx, "claude", args...)` — every
  install/show/remove subcommand shells out to the `claude` binary.
- subcommands `install/` `show/` `remove/` add the marketplace + install/uninstall the plugin via
  `claude plugin marketplace add` / `claude plugin install` / `remove`.

**Direction (user):** make this a **generic agent-skill** command + a **per-harness selector flag**
(e.g. `--claude`, later `--codex`). The harness owns its skill/extension-install mechanism — its CLI
binary, marketplace/source format, scope vocabulary, and install commands — because each harness's
extension system differs (Claude plugins vs whatever codex/opencode use; some have none). So
"skill/extension install" is another **per-harness fragment** on the `Harness` interface; the command
resolves the selector flag to a harness and calls its installer. The clawker-support skill content
itself may need per-harness packaging.

### Note on `post_init` / MCP — no clawker-owned MCP setup exists

clawker does NOT ship or own any MCP setup. `agent.post_init` is **arbitrary user content** that
*may* contain MCP setup (or anything else). That is exactly why it must be namespaced under the
harness-keyed config map (`harnesses.<name>.agent.post_init`, see `design.md`): arbitrary
harness-specific user commands only run in that harness's container. There is no clawker MCP code to
generalize — the coupling is structural (where arbitrary hooks live), not a baked script.

---

The descriptor superset + sharp edges above feed the harness abstraction. The decided
architecture (config model, build→runtime split, interface/registry/tiers) lives in
`design.md` — the source of truth for decisions; this file is the coupling evidence.

## Remaining recon (still file:line + summary depth, not mechanic)

- **Section 2** — Dockerfile install/seed steps: the real RUN/COPY/ENV mechanics.
- **Section 5** — CP init: actual `configSeedScript` / `postInitScript` shell bodies; spawn `routeArgs`.
