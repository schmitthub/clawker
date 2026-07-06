# Multi-Harness: Bundle Format & Schema (Phase 0, rev 2)

Final Phase 0 format decision (2026-07-03, supersedes rev 1's yaml-only model):
harness = directory bundle of **slim harness.yaml + Dockerfile.harness.tmpl +
asset files**. Build surface lives in the template; yaml keeps only data that
Go code consumes outside template rendering.

## Bundle layout + registry (LOCKED)

- NOTHING embedded-authoritative. Shipped pre-configured bundles live in
  bundler `assets/harnesses/<name>/`, materialized into
  `<user-config>/harnesses/<name>/` beforehand (init/upgrade). User-owned,
  editable in place; custom harnesses = same shape.
- **Registry = settings.yaml root key `harnesses`**: slug → bundle dir path.
  THE enum for command validation + completions (no dir scanning). Settings
  store typed mutation + #409 JSON Schema for free. Path indirection allows
  repo-local bundles.
  ```yaml
  harnesses:
    claude_code: { path: <config-dir>/harnesses/claude_code }
    codex:       { path: <config-dir>/harnesses/codex }
  default_harness: claude_code
  ```
- Naming: settings `harnesses` = registry; project clawker.yaml `harnesses` =
  per-harness overlay map (same slugs, different layer).
- Upgrade staleness policy (user edited materialized bundle): re-copy missing,
  never clobber; exact policy at Phase 1.

```
<user-config>/harnesses/claude_code/
  harness.yaml               # slim data (below)
  Dockerfile.harness.tmpl    # ALL build surface — blocks fill master slots
  statusline.sh  settings.json  config.json  agent-prompt.md
```

## Format: Go template block/define composition (LOCKED)

Go text/template has no Jinja `extends` but `{{block}}`/`{{define}}` gives
equivalent composition: master Dockerfile.tmpl declares named blocks (empty
defaults) at its slot positions; parsing the harness tmpl into the same
template set overrides them. Blocks = the fixed slots from inventory §2 —
engine-enforced: a harness fills declared block names only, cannot disturb
master ordering/cache architecture. Native Dockerfile content (heredocs,
ARG placement, cache mounts) — no yaml string-escaping layer.

Master slots — names are EVENT-CENTRIC positional opportunities (what
precedes/follows), NEVER content-prescriptive, no `harness_` prefix:
| block | master position |
|---|---|
| `BLOCK_1_TBD` | root scope, before `USER ${USERNAME}` |
| `BLOCK_2_TBD` | after the static-env section that follows the user switch |
| `BLOCK_3_TBD` | after user-mode RUNs — version-ARG declaration-adjacency cache zone |
| `BLOCK_4_TBD` | after trailing `USER root`, before clawker asset COPYs |
| `cmd` | final instruction |

(claude uses them for: managed-settings heredoc / config-dir+OTEL env /
install+seeds / prompt copy / `CMD ["claude"]` — but that's claude's choice,
not the slot's meaning. Line-by-line disposition: `master-template-refactor.md`.)

Inject point `after_harness_install` (renamed from `after_claude_install`)
sits after the install block in the master. Template params available to
harness blocks: `.HarnessVersion`, `.BuildKitEnabled`, `.OtelEndpoint` + other
settings-driven params, `${USERNAME}` etc.

## Slim harness.yaml (only non-template data)

```yaml
version:                       # feeds {{.HarnessVersion}} pre-render
  resolver: npm | github-release | none
  package: <npm name | owner/repo>
context_files: [<bundle-relative>]   # staged into build context (explicit list,
                                     # no COPY-directive scanning)
seeds:                         # runtime apply manifest for the generic init
  - { file: <seed name>, dest: <config-dir-relative>, apply: copy-if-missing | copy-if-missing-or-empty | json-merge }
staging:                       # create-time host→container config copy (containerfs engine)
  config_dir: { env: <VAR>, path: <dir> }   # host-side resolution (env override, default dir)
  files: [{ name: <f>, json_keys: [<allowlist>] }]
  dirs: [<recursive copy>]
  trees:
    - dir: <dir>
      skip: [<files>]
      json_rewrites: [{ file: <f>, key: <k>, rewrite: prefix-swap | replace-with-workdir }]
  credentials:                 # ordered fallback chain
    - { type: keyring, service: <svc>, dest: <rel>, mode: 0600, verbatim: true }
    - { type: file, path: <rel>, dest: <rel>, mode: 0600 }
    - { type: env, vars: [<VARS>] }        # existing agent.from_env plumbing
  mounts: [{ host_subdir: <d>, dest_subdir: <d> }]   # live-bind vs copy
egress: [<EgressRule — existing yaml shape>]
unattended_flag: <flag>        # generated aliases (go/wt)
skill_install: {...}           # optional; claude-only
```

DEAD fields (moved to template or derived): `binary` (CMD in `cmd`
block; routeArgs = CP reads image Config.Cmd[0]), `install.run`/`cache_mounts`
(`BLOCK_3_TBD` block), `env`/`managed_files`/`instructions` (blocks),
`display_name` (registry/settings if needed).

## claude_code bundle (expresses 100% of current hardcode)

harness.yaml:
```yaml
version: { resolver: npm, package: "@anthropic-ai/claude-code" }
context_files: [statusline.sh, settings.json, config.json, agent-prompt.md]
seeds:
  - { file: statusline.sh, dest: statusline.sh, apply: copy-if-missing }
  - { file: config.json,   dest: .config.json,  apply: copy-if-missing-or-empty }
  - { file: settings.json, dest: settings.json, apply: json-merge }
staging:
  config_dir: { env: CLAUDE_CONFIG_DIR, path: .claude }
  files: [{ name: settings.json, json_keys: [enabledPlugins] }, { name: CLAUDE.md }]
  dirs: [agents, skills, commands]
  trees:
    - dir: plugins
      skip: [install-counts-cache.json]
      json_rewrites:
        - { file: known_marketplaces.json, key: installPath,     rewrite: prefix-swap }
        - { file: known_marketplaces.json, key: installLocation, rewrite: prefix-swap }
        - { file: installed_plugins.json,  key: installPath,     rewrite: prefix-swap }
        - { file: installed_plugins.json,  key: projectPath,     rewrite: replace-with-workdir }
  credentials:
    - { type: keyring, service: "Claude Code-credentials", dest: .credentials.json, mode: 0600, verbatim: true }
    - { type: file, path: .credentials.json, dest: .credentials.json, mode: 0600 }
  mounts: [{ host_subdir: projects, dest_subdir: projects }]
egress:  # current requiredFirewallRules verbatim incl. .claude.ai UGC path-denies
  - { dst: api.anthropic.com }
  - { dst: claude.com }
  - { dst: platform.claude.com }
  - dst: .claude.ai
    path_rules: [{ path: /public/, action: deny }, { path: /share/, action: deny }]
  - { dst: mcp-proxy.anthropic.com }
  - { dst: registry.npmjs.org }
  - { dst: sentry.io }
  - { dst: statsig.anthropic.com }
  - { dst: statsig.com }
  - { dst: .datadoghq.com }
  - { dst: .datadoghq.eu }
unattended_flag: --dangerously-skip-permissions
skill_install: { cli: claude, marketplace: schmitthub/claude-plugins, plugin: clawker-support@schmitthub-plugins, scopes: [user, project, local] }
```

Dockerfile.harness.tmpl (sketch — bodies lifted verbatim from current master):
```
{{define "BLOCK_1_TBD"}}
RUN mkdir -p /etc/claude-code && cat > /etc/claude-code/managed-settings.json <<EOF
{ "env": { "PATH": "/home/${USERNAME}/.local/bin:${PATH}" } }
EOF
{{end}}

{{define "BLOCK_2_TBD"}}
ENV CLAUDE_CONFIG_DIR=/home/${USERNAME}/.claude
ENV CLAUDE_CODE_ENABLE_TELEMETRY=1
ENV CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1
ENV OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
... (full OTEL block w/ {{.OtelEndpoint}}, conditionals via {{if .OtelInclude*}})
{{end}}

{{define "BLOCK_3_TBD"}}
ARG HARNESS_VERSION={{.HarnessVersion}}
{{- if .BuildKitEnabled}}
RUN --mount=type=cache,target=/home/${USERNAME}/.npm \
    curl -fsSL "https://claude.ai/install.sh" | bash -s ${HARNESS_VERSION}
{{- else}}
RUN curl -fsSL "https://claude.ai/install.sh" | bash -s ${HARNESS_VERSION}
{{- end}}
COPY --chown=${USERNAME}:${USERNAME} statusline.sh /home/${USERNAME}/.clawker/seed/statusline.sh
COPY --chown=${USERNAME}:${USERNAME} settings.json /home/${USERNAME}/.clawker/seed/settings.json
COPY --chown=${USERNAME}:${USERNAME} config.json /home/${USERNAME}/.clawker/seed/config.json
{{end}}

{{define "BLOCK_4_TBD"}}
COPY agent-prompt.md /etc/claude-code/CLAUDE.md
{{end}}

{{define "BLOCK_5_TBD"}}
CMD ["claude"]
{{end}}
```

## codex bundle (pressure test #2 — deepwiki-verified 2026-07-03)

harness.yaml:
```yaml
version: { resolver: npm, package: "@openai/codex" }   # github-release also viable (musl bins)
context_files: []              # VERIFY: config.toml seed for onboarding bypass?
seeds: []
staging:
  config_dir: { env: CODEX_HOME, path: .codex }
  files: [{ name: config.toml }, { name: AGENTS.md }]  # VERIFY user-level AGENTS.md semantics
  dirs: []
  trees: []
  credentials:
    - { type: file, path: auth.json, dest: auth.json, mode: 0600 }   # ChatGPT OAuth blob
    - { type: env, vars: [OPENAI_API_KEY, CODEX_API_KEY] }
  mounts: []                   # VERIFY: history.jsonl / log/ persistence
egress:
  - { dst: api.openai.com }
  - { dst: auth.openai.com }
  - { dst: registry.npmjs.org }
  # VERIFY full set at impl
unattended_flag: --dangerously-bypass-approvals-and-sandbox   # VERIFY exact flag
```

Dockerfile.harness.tmpl:
```
{{define "BLOCK_2_TBD"}}
ENV CODEX_HOME=/home/${USERNAME}/.codex
{{end}}
{{define "BLOCK_3_TBD"}}
ARG HARNESS_VERSION={{.HarnessVersion}}
RUN npm install -g @openai/codex@${HARNESS_VERSION}
{{end}}
{{define "BLOCK_5_TBD"}}
CMD ["codex"]
{{end}}
```
(unfilled blocks fall through to master's empty defaults)

## Pressure-test verdict

Both express fully; codex strictly smaller (3 of 5 blocks, near-empty yaml).
Engine primitives: version Fetcher (npm|github-release), template composer
(master + harness tmpl parse-set), context stager (context_files), seed baker
+ ONE generic apply script (CP descriptor-free), staging engine
(files/dirs/trees/json_rewrites), credential chain, egress composer.

## Open items

- codex impl-time VERIFYs above (onboarding seed, AGENTS.md, unattended flag,
  full egress, history mount).
- Template safety: user-authored tmpl can only fill declared blocks, but block
  CONTENT is arbitrary Dockerfile — same trust level as existing
  build.instructions/inject (user-owned machine, images run locally; no new
  trust surface).
- docker/env.go monitoring-down override → standard OTEL_*_EXPORTER=none
  (harness-blind; VERIFY claude honors).
- Golden tests: per-harness golden Dockerfiles; claude_code golden must stay
  byte-identical to current output (Phase 1 gate).
