# Multi-Harness: Design

Decided architecture for making clawker support any coding agent harness
(claude-code, codex, opencode, pi, + user-custom), not just Claude Code. This file
is the source of truth for **decisions**; `claude-coupling-inventory.md` is the
coupling **evidence**.

## Goal

A harness is selectable per project build. Major harnesses ship baked in; the design
stays open so users can add their own. The harness is one orthogonal dimension inside
a project — not a separate project, not a separate config file.

## Image model: per-harness image, build-time selection

- **One image per (project, harness).** No fat multi-harness image — a single image
  with multiple harnesses installed causes config-dir seed collisions, an ambiguous
  entrypoint CMD, and install bloat.
- `clawker build --harness <name>` selects the harness; default from settings
  `default_harness`.
- `@` image resolution becomes **(project, harness)-keyed** — N project images, one
  per built harness.
- A single-harness image seeds exactly one config-dir and has one unambiguous CMD.

## Harness identity is the build→runtime join key

- **Build stamps the harness identity into the image** (baked label/env, e.g. the
  harness-name label) and into the container label on create.
- **Build = selection.** No hooks run at build.
- **Runtime (create/start) reads the baked identity** to resolve which harness's config,
  hooks, staging, egress, and CMD apply.
- The lifecycle code that runs hooks (CP dispatch, `BootstrapServicesPreStart`, the
  marker-gated init plan) executes long after build, so the identity MUST persist in the
  image + container label — that label is how runtime resolves the harness.

## Lifecycle timing (build vs runtime — do not conflate)

| phase | when | what |
|---|---|---|
| build | `clawker build --harness X` | bake image + stamp identity. NO hooks. |
| create | container create (once) | post_init script injected; config volume seeded via harness stager |
| first start | first run/start | post_init executed once (marker-gated), via CP dispatch |
| every start | run/start/restart | pre_run delivered + executed |

All hook selection + config lookup at create/start is keyed by the baked harness identity.

## Config model: harness-keyed map in project config

Project config (`clawker.yaml`) gains a harness-keyed map. Keys are harness names
(dynamic — open to custom harnesses):

```yaml
build:  { image, packages }          # shared, harness-agnostic base
agent:  { env, from_env }            # shared base
harnesses:
  claude_code:
    agent: { post_init: <claude mcp setup>, pre_run: ... }
    build: { inject: { after_harness_install: [...] } }
  codex:
    agent: { post_init: <codex setup> }
    build: { ... }
```

- **Effective config** for `--harness codex` = base `build`/`agent` ⊕ `harnesses.codex` ⊕
  the codex harness-definition's own fragments.
- **Namespacing solves arbitrary user content.** A user's Claude MCP commands under
  `harnesses.claude_code.agent.post_init` are read only when the baked identity is
  `claude_code`; a codex container reads `harnesses.codex.*` and never sees them.
- **Storage supports the dynamic keyed map.** The merge engine already handles
  `map[string]T` keyed maps via a custom `KindFunc` (the registry's
  `map[string]WorktreeEntry` / `KindWorktreeMap` is the precedent); it merges by key
  across config layers. Parallel config *files* break the merge engine — keyed maps in
  one file do not.

## Harness = Go interface + registry (code, not just data)

A harness needs code, not only values — bespoke logic (e.g. Claude's plugin-registry
JSON-key rewriting in `internal/containerfs`) cannot be expressed as pure data.

- **`Harness` is a Go interface** — methods for install, staging, CMD, egress, init plan,
  config-dir, credentials, etc.
- **Registry: `map[string]Harness`**, name→impl. `--harness <name>` resolves the name to
  an impl; the project's `harnesses.<name>` config is passed *into* the impl's methods.
  This registry is how a dynamic string key reaches real code logic.
- **The selected harness contributes fragments** composed with the project's generic config:
  - build fragment — install step, inject defaults, required packages
  - agent fragment — its own init/MCP hooks (Claude MCP belongs to the claude harness, not
    project `post_init`)
  - egress fragment — its required floor + security path-rules
  - staging — a `HarnessStager` impl for the bespoke host-config staging

## Extensibility tiers

| tier | defined by | covers |
|---|---|---|
| baked-in | Go interface impl (full power) | claude_code, codex, opencode, pi — bespoke install, JSON rewrites, custom egress |
| declarative + scripts | yaml descriptor + hook scripts (`install.sh`, `stage.sh`, `seed.sh`) interpreted by one generic impl | user-custom harnesses that fit data + shell |
| full custom Go | compiled-in / plugin | anything the script tier can't express |

The big-4 are Go impls. Open extensibility is the declarative+script tier (a `stage.sh`
can `jq`-rewrite, an `install.sh` can do anything). Full-custom-Go is a later plugin story.

## What the harness interface/descriptor must carry (superset)

From the inventory, across all subsystems:

1. binary/command — default CMD, spawn routeArgs prepend
2. install — command/URL or package + version resolver (npm vs GitHub-release vs binary)
3. config-dir — env var name + default dir name
4. credential source — keyring service | API-key env | file fallback
5. config staging — manifest (files/subdirs, settings-key allowlist) + bespoke fixups (`HarnessStager`)
6. seed/onboarding — seed files + dest, onboarding-bypass file, merge strategy
7. prompt/statusline assets + destination paths
8. required egress — domain/path allowlist + security path-rules
9. telemetry — env var name + mapping (or none)
10. host-state mount — projects-equivalent dir, copy-vs-live-bind

## Where Claude-specific logic moves

- required egress floor (`defaults.go requiredFirewallRules`) → claude harness egress fragment
- containerfs `.claude` staging pipeline → claude `HarnessStager` impl
- Claude MCP setup → claude harness agent fragment (out of project `post_init` / defaults)
- Dockerfile install/seed/env → claude harness build fragment + assets
- spawn `routeArgs "claude"`, `CMD ["claude"]` → claude harness CMD
- CP `configSeedScript`/`postInitScript` → parameterized by harness descriptor

## Open questions (undecided)

- **Container naming shape** — `clawker.project.harness.agent` (4-seg) vs harness in the leaf
  vs label-only; how global-scope (`clawker.agent`, no project) absorbs the harness segment.
  Touches the load-bearing naming convention (labels authoritative for filtering).
- **Descriptor form** — `type:`-references-builtin + overrides vs each entry self-contained.
- **v1 openness** — ship baked-in-only first, or include the declarative+script tier in v1.
- **API-key auth** — greenfield: clawker has only OAuth-blob plumbing; an API-key path (codex /
  OpenAI key) is net-new infrastructure and likely the gating item for codex.
- **Plugin/skill generalization** — deeply Claude-specific (`stagePlugins` JSON rewrite,
  `clawker skill` IS a Claude plugin); likely out of v1 scope.

## Status

Mechanic-level coupling mapped: containerfs staging, firewall floor, agent/build coupling,
build.go. Remaining recon + the unlocated MCP setup tracked at the end of
`claude-coupling-inventory.md`.
