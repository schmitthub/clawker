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

## 2. Image build / Dockerfile — install + default CMD

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
| `config/defaults.go:15-49` | `requiredFirewallRules` hardcodes `api.anthropic.com`, `claude.com`, `.claude.ai`, `mcp-proxy.anthropic.com`, `registry.npmjs.org`, statsig/sentry/datadog | per-harness required-egress set |
| `consts/consts.go:193,198` | `ClaudeDir=".claude"`, `ClaudeProjectsSubdir="projects"` | harness config-dir accessor |
| `consts/monitoring.go:105` | `EnvClaudeCodeEnableTelemetry` | per-harness telemetry env |
| `cmd/root/root.go:32-38` | branding: "Manage Claude Code...", "(claude + docker)", "Start Claude Code in a container" | harness-neutral |
| `cmd/generate/generate.go:42-74` | **entire command** = fetch `@anthropic-ai/claude-code` from npm → Dockerfiles | per-harness version source |
| `cmd/skill/shared/shared.go:16-46` | `clawker skill` shells to `claude` binary, `schmitthub/claude-plugins` marketplace | likely stays Claude-specific (it IS a Claude plugin) |
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

## Next steps

- Draft the `Harness` config schema + Go descriptor design; check against
  `.claude/docs/DESIGN.md` + `ARCHITECTURE.md`.
- Phased migration plan with baked-in presets: claude-code, codex, opencode, pi.
- Decide v1 scope: likely defer plugin/skill generalization and API-key auth may be the
  gating new-infrastructure item for Codex.
