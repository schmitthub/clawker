---
name: harness-toolchain-dev
description: >
  Use when the user is authoring, extending, or debugging a clawker harness
  bundle or toolchain definition — adding a new coding-agent CLI to clawker,
  writing or editing harness.yaml, Dockerfile.harness.tmpl, toolchain.yaml,
  Dockerfile.toolchain-root.tmpl / Dockerfile.toolchain-user.tmpl, designing
  an egress floor, declaring volumes/seeds/staging, filling template block
  slots, or registering a bundle in the settings harnesses/toolchains
  registries. Distinct from the clawker-support skill (end-user config and
  troubleshooting): this skill is for extension AUTHORS building the bundles
  themselves.
license: MIT
compatibility: >
  Requires the clawker CLI installed on the host. Works on materialized
  bundle directories under the clawker config dir or on custom bundle
  directories anywhere on disk.
allowed-tools: Bash(clawker *), Bash(which clawker), Bash(ls *), Bash(cat *), Read, Write, Edit, Glob, Grep, WebFetch, WebSearch
---

# Clawker Harness & Toolchain Development

You are an expert in clawker's extension model. You help developers author
**harness bundles** (packaging a coding-agent CLI so clawker can build, run,
and firewall it) and **toolchain definitions** (reusable language-toolchain
install fragments). You know the manifest vocabulary, the template block
contract, the validation front doors, and the security posture a bundle must
uphold.

**This skill is deliberately concrete.** Unlike end-user support guidance,
the reference files here are a verified field dictionary — every field name,
enum token, and validation rule is checked against clawker's source. Treat
them as authoritative. When something is not covered, read the shipped
bundles (they are the living examples) instead of guessing.

## Orientation

### What a harness is

A harness = one coding-agent CLI (Claude Code, Codex, your own) packaged as
a **bundle directory**:

```
<bundle>/
├── harness.yaml              # manifest: data the Go engine consumes
├── Dockerfile.harness.tmpl   # {{define}} overrides for the master template's block slots
├── assets/                   # optional: every file the bundle adds to the build context
└── toolchains/<name>/        # optional: bundle-embedded toolchain definitions
```

- `harness.yaml` holds everything consumed *outside* template rendering:
  version resolver, toolchain declarations, persisted volumes, first-boot
  seeds, create-time host staging, and the firewall egress floor. Full field
  reference: `reference/harness-manifest.md`.
- `Dockerfile.harness.tmpl` holds ALL build surface (env, install steps,
  CMD) as `{{define}}` bodies for the six declared block slots. The master
  template owns instruction ordering and cache architecture; a bundle can
  only fill the declared slots. Reference: `reference/template-blocks.md`.
- `assets/` is staged **verbatim** into the docker build context under the
  same `assets/` prefix. The template's COPY instructions and the manifest's
  `seeds[].file` entries reference `assets/`-prefixed paths — those
  directives alone decide what lands in the image. Nothing is copied by
  naming convention.

### Registry and materialization

- Bundles are registered in **settings.yaml** (user settings, not project
  config) under `harnesses:` — a map of name → `{path: <bundle dir>,
  default: <bool>}`. Every entry carries an explicit `path`; resolution is
  registry-only. At most one entry may set `default: true`.
- The harness **name is the image tag**, so it must match the docker tag
  grammar (`[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}`) and must not be one of the
  reserved aliases `default`, `latest`, `base`.
- Shipped bundles (`claude`, `codex`) are embedded in the binary and
  **materialized copy-if-missing** to `<config-dir>/harnesses/<name>/`
  (config dir: `$CLAWKER_CONFIG_DIR`, else `$XDG_CONFIG_HOME/clawker`, else
  the platform default). Materialized copies are user-owned and editable in
  place — upgrades only add files the user does not have; existing files are
  never overwritten. **Editing a materialized shipped bundle is the fastest
  way to learn the format and a legitimate customization path.**
- Toolchain definitions have the parallel registry `toolchains:` (name →
  `{path}`) and materialize to `<config-dir>/toolchains/<name>/`. Shipped:
  `go`, `node`, `python`, `rust`. Reference:
  `reference/toolchain-authoring.md`.

### Image identity and the two-image split

Every project builds **two images**: a harness-agnostic shared base
(`clawker-<project>:base` — substrate + system packages + user setup +
project-declared toolchains + project instructions) and a thin **harness
image** built FROM it (harness-declared toolchains + bundle blocks + seeds +
clawker runtime assets). The harness image is tagged with the harness name
(`clawker-<project>:<harness>`); the default harness's build also stamps a
`:default` alias. Containers and images carry the `dev.clawker.harness`
label (authoritative for filtering).

### Runtime model a bundle must respect

- clawkerd (clawker's supervisor) is PID 1 and spawns the image's CMD —
  your harness CLI — via **direct exec with a privilege drop to the
  unprivileged container user (`claude`)**. There is no login shell in that
  spawn path, so the CLI binary must be reachable on the image's static
  `ENV PATH` (`~/.local/bin` and `/usr/local/bin` are on it). See the PATH
  gotcha in `reference/template-blocks.md`.
- Declared volumes are named docker volumes
  (`clawker.<project>.<agent>-<name>`) mounted over the image paths at run
  time — anything the image bakes into a volume path is shadowed. Seeds
  exist to solve this: they stage into `~/.clawker/seed/` and a generic CP
  init step applies them into the volume on first boot.
- **No credential staging.** Host credentials are never copied into
  containers. The user authenticates once inside the container (browser
  OAuth is proxied to the host browser); the token persists in the harness
  config volume. Do not add credential files to `seeds:` or `staging:`.
  See `reference/security-egress.md`.

## Workflow: author a new harness end-to-end

1. **Research the CLI you are packaging.** How is it installed (npm,
   standalone installer, GitHub release binary)? Where does it keep state
   (config dir env var? hardcoded `~/.<tool>`)? What domains does it need at
   runtime? Prefer install methods with integrity verification.

2. **Create the bundle directory.** Anywhere on disk works; the
   conventional home is `<config-dir>/harnesses/<name>/`. Start from
   `reference/worked-example.md` (complete minimal fictional bundle) and the
   shipped bundles: read the materialized `claude/` and `codex/` bundle
   dirs — codex is the compact template, claude the full-featured one
   (seeds, staging filters, telemetry env).

3. **Write `harness.yaml`.** Work through the sections in order — version
   resolver, volumes (declare every persisted dir; nothing is assumed),
   seeds, staging, toolchains, egress floor. Field-by-field rules and
   validation error meanings: `reference/harness-manifest.md`. Egress design
   rules: `reference/security-egress.md`.

4. **Write `Dockerfile.harness.tmpl`.** Fill only the blocks you need —
   most harnesses need exactly three: `block_3` (env), `block_4` (install),
   `block_6` (CMD). Block semantics, shell/user context per block, the
   `{{.HarnessVersion}}` ARG-adjacency cache rule, and the PATH gotcha:
   `reference/template-blocks.md`.

5. **Register it.** Add to settings.yaml (NOT project config):

   ```yaml
   # In: <config-dir>/settings.yaml (user settings)
   harnesses:
     myharness:
       path: /absolute/path/to/bundle
   ```

   Add `default: true` only if it should win bare `@` refs and untagged
   builds — and remember at most one entry may carry the flag.

6. **Build and run.**

   ```bash
   clawker build -t myharness          # -t selects the registered harness
   clawker run @:myharness             # @ = default harness; @:<name> selects
   ```

7. **Verify runtime behavior.** Inside the container: the CLI resolves from
   PATH (`which <cli>`), state lands in the declared volume (`docker volume`
   naming: `clawker.<project>.<agent>-<volume-name>`), seeds applied on
   first boot, auth flow completes (browser OAuth proxies to host), egress
   floor suffices (watch for blocked-connection errors; widen only from
   observed traffic — `reference/security-egress.md`).

## Workflow: author a new toolchain

1. Create `<config-dir>/toolchains/<name>/` with `toolchain.yaml`
   (description only) plus `Dockerfile.toolchain-root.tmpl` and/or
   `Dockerfile.toolchain-user.tmpl` — at least one, each non-empty.
2. Make every fragment **self-guarding**: skip the install when the tool is
   already present, with a `clawker toolchain <name>: ... — skipping
   install` echo. This is what lets project and harness declarations
   coexist. Idiom and placement semantics:
   `reference/toolchain-authoring.md`.
3. Register in settings.yaml under `toolchains: {<name>: {path: ...}}` —
   or embed under a harness bundle's `toolchains/<name>/` subdir if it is
   bespoke to that harness (then prefix the name; the namespace is flat and
   collisions are errors).
4. Declare it: project `build.toolchains: [<name>]` (renders in the base
   image) or harness manifest `toolchains: [<name>]` (renders in the
   harness image unless the project also declared it).

## Iteration loop and where errors surface

Edit → `clawker build -t <name>` → `clawker run @:<name>`. Bundle changes
need a rebuild; manifest-only changes to `staging:`/`egress:` take effect at
container **create** time (staging copies and firewall composition run
there), but rebuild anyway to keep image-side state (volumes, seeds,
blocks) in sync.

| Failure | When it surfaces | Looks like |
|---|---|---|
| Manifest field/vocabulary errors (bad volume name, seed outside assets/, dest not under a volume, unknown apply/rewrite token, duplicate toolchain decl) | Bundle load — first command that loads the bundle (build, create, firewall sync) | `harness "<name>": <specific rule>` — see the validation table in `reference/harness-manifest.md` |
| Template defines an unknown or reserved name | Compose (build) | `defines unknown block ... declared blocks: [block_1 ... block_6]` / `defines reserved name` |
| Toolchain name unresolvable or collides | Dockerfile generation (build) | `unknown toolchain` / `defined both by harness bundle ... and by ...` |
| Version resolution failure (registry unreachable, tag missing prefix) | Build — **warning, not fatal** | Build proceeds with the floating `latest` default |
| Registered path has no bundle | Any bundle load | `no bundle at registered path ... fix harnesses.<name>.path in settings or rebuild to re-materialize` |
| Install RUN failures | Docker build | Normal build error; build-time network is the host daemon's — NOT the firewall (never add egress rules to fix a build) |
| CLI not found at container start | Runtime | CMD exec fails / container exits — almost always the PATH gotcha (`reference/template-blocks.md`) |

## Reference files

| File | Read when |
|---|---|
| `reference/harness-manifest.md` | Writing or debugging any `harness.yaml` field — full verified field + validation reference |
| `reference/template-blocks.md` | Writing `Dockerfile.harness.tmpl` — block slot semantics, shell/user context, cache rules, PATH gotcha, worked examples |
| `reference/toolchain-authoring.md` | Writing a toolchain definition — format, placement, self-guarding, collisions, cache implications |
| `reference/security-egress.md` | Designing a bundle's `egress:` floor — minimal-floor rules, path scoping, UGC-sink denial, MITM/SNI notes |
| `reference/worked-example.md` | Starting a new bundle — complete minimal fictional harness to adapt |

## Gotchas

- **The workspace is never staged.** It arrives via bind mount or snapshot.
  `staging.copy` sources inside the project workspace are rejected at stage
  time; seeds/staging are for state OUTSIDE the workspace only.
- **Volumes shadow image content.** Anything block_4 installs into a
  declared volume path disappears at runtime under the volume mount. Put
  payloads outside volume paths (see the codex install's `CODEX_HOME`
  redirect) and use `seeds:` for files that must appear inside a volume.
- **Build-time network ≠ runtime network.** Install steps run on the host
  daemon's network; the egress floor governs only the running container.
  Never add install-time domains to `egress:`.
- **Blocks 1–3 run under `/bin/sh`, blocks 4–6 under zsh.** And ARGs do not
  survive FROM — the master re-declares `USERNAME` and `ZSH_ENV`; declare
  any ARG you consume in your own block, adjacent to its consumer.
- **A registered name is forever an image tag.** Renaming a harness means
  re-registering and rebuilding; old images keep the old tag.
- **Materialization never overwrites.** Fixing a shipped bundle upstream
  does not propagate to a user's edited copy — their file wins. To reset a
  file to the shipped version, delete it and rebuild (re-materialization
  fills the gap). A freshly materialized shipped copy carries a
  `.clawker-shipped-hash` stamp file used for staleness detection —
  bookkeeping only; loaders and build-context staging never read it, and a
  custom bundle needs no stamp.
