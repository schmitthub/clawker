# Stack Contract — Design Model

The design and current state of the stack/harness composition contract on
`feat/multi-harness-support`. The versioned public surface is
`docs/stack-contract.mdx` (engine guarantees E1–E22, author obligations A1–A11,
permanent citation anchors) with a conformance suite of `// Conformance: <ID>`
badges on covering tests.

## Why nothing off the shelf fits

Novel intersection: a plugin/extension system FOR building dev containers FOR
coding-agent runtimes — plugin engine + build composition + dev-environment
variance + security supervision. Prior art each solves one slice: systemd (data
plugins, no build), CNB (build composition but author code runs at plan time),
devcontainer Features (dev variance, no dep semantics or supervision), nix (owns
every package — incompatible with degenerate dev envs), vendor sandboxes (no
composition). The disciplined line that holds all three layers is the moat: WHEN
code runs is contractual — never in the engine, always in the build, supervised
at runtime.

## Terminology

- **Stack** — a robust collection of Dockerfile instruction injections that
  perform complex dev-stack setup conveniently, with dedup guaranteed. A
  collection (nvm + typescript + node; uv + python), not an atom. Stacks NEVER
  replace the existing template injection points — `inject` / `root_run` /
  `user_run` remain the bespoke hand-roll escape hatch, always available.
- The composition primitive is **named keys + layered store + topology
  precedence**. The stack name IS the key. Deps are direct stack names. Override
  = matching key at a closer layer, wholesale replace, NEVER merge.

There is no capability-token / provides-requires / provider-binding / auto-pull
algebra, and no settings-layer registry. Capability tokens would make clawker
the arbiter of dependency naming, which violates no-taxonomy ("node" means
different things to different people); conflict resolution under that model would
need relationship mappings / prefixes / a database. The contract deliberately
carries none of that.

## Resolution: layered keyed store, per-lineage scope

No global namespace, no database. Scope is the lineage. Bundled stacks have no
global identity; two harness bundles never conflict because sibling images never
share a namespace.

| lineage | lookup chain for key K |
|---|---|
| base | project > shipped |
| base+harness X | project (incl. `build.harnesses.X` overlay) > X bundle's `stacks/` > shipped |

- One registry home: `clawker.yaml`; the store walk-up IS the resolution
  mechanism. Clawker-shipped stacks/harnesses are the **virtual base layer of
  the backing store** (floor; overridable by a matching key). There is no
  settings-layer registry and no config-dir materialization: shipped definitions
  resolve straight from the embedded FS.
- Same-layer collision is impossible by construction: the registry is a keyed
  map; the register front-door errors on an existing key, and `--force` replaces
  with provenance output (what was shadowed, old vs new path).
- **Names are local bindings chosen by the registrar** (docker-tag model): the
  bundle manifest suggests a default name, `--name` overrides at register time.
  Bundle content carries no global identity claim. To run the official codex and
  a fork simultaneously, register under distinct names.
- Provenance is ALWAYS printed in build output when a closer layer shadows a
  farther one, e.g. `harness codex ← ~/tools/codex-bundle (project registry)
  shadows built`. The arrow-label for the embedded layer is `built`.
- Portability caveat (accepted): `harnesses.<name>` resolves against the local
  registry, so the same `clawker.yaml` on two machines may resolve differently.
  This is both a feature (corp drop-in) and a footgun; provenance output is the
  mitigation.
- There is no registry `default` flag. The built-in default harness is `claude`;
  a project-selectable default would be a new design decision.

### Placement (cross-stratum dedup does not exist)

- A project-declared stack renders in the base image. A harness-installer stack
  renders in that harness's image only. There is NO cross-stratum interaction:
  if project and harness both declare `node`, the base renders the project's and
  the harness image ADDITIONALLY renders its lineage-resolved definition on top.
  The engine never judges whether a base render "satisfies" a harness
  declaration (name-match-satisfaction would be an implicit taxonomy).
  Satisfaction logic lives INSIDE fragments: author-owned self-guards (a node
  fragment's "keep existing node ≥ floor" pattern), apt idempotence, and PATH
  shadowing by later layers (Docker physics). A bundle author deals with
  isolation or documents conflicting-stack combos.
- A shipped stack referenced from a bundle: the installer names it without
  bundling → the lookup chain bottoms out at shipped (project overlay > bundle
  `stacks/` > shipped). Bundling only means "I want MY definition". A name that
  resolves nowhere is the only error.
- The base is harness-agnostic **by construction**: no bundle enters base scope,
  and bundled stacks structurally cannot enter the base lookup.
- Per-harness nuance lives closest to the workload: e.g. claude works with an
  nvm-node stack but codex does not → the project sets a `harnesses.codex` stack
  override (per-harness project overlay). No harness self-source flag is needed.
- Two harnesses vendoring the same community stack each render it in their own
  lineage. Redundancy-over-reconciliation stands: orphaned/duplicate layers are
  an accepted cost, stated in build output. There is no reconciliation machinery.

## Registration & distribution

- CLI command set: `clawker harness register|list|remove`, `clawker stack
  register|list|remove`. Registration is a deliberate host-side human action,
  auditable.
- **Registration is by path reference**: registry entries in `clawker.yaml`
  point at bundle/stack dirs on disk. No materialize-into-project copy. The yaml
  is not self-contained across machines — accepted.
- A harness bundle's manifest declares its stack deps by name; bundled stacks
  live in `stacks/<name>/` inside the bundle, discovered by dir name (dir name
  IS the name).
- External stack use by a harness bundle, two paths, no new vocabulary:
  1. Vendor — copy into the bundle's `stacks/<name>/` (bundling IS vendoring;
     the author controls version, offline behavior, and staleness).
  2. Reference by name — the installer names `rust` without bundling; unresolved
     → a build error with guidance (`clawker stack register <path> --name rust`).
- **No remote fetch**: the engine never auto-clones stacks at build time. That
  would add a supply-chain surface (unpinned third-party fragments) and violate
  engine sovereignty. The engine consumes only locally registered data. Revisit
  only with full SHA-pinning machinery as a separate decision.

## Versioning & parameters

There is no version-constraint algebra. A stack cannot carry A version — it is a
collection of N tools × N versions (python = uv + CPython; node = nvm + node);
the interiors are unaccountable by nature and no vocabulary can enumerate them.
Parameterization is Docker build args and nothing else: a stack author declares
Docker `ARG`s per member at their discretion (the node stack's `ARG
NODE_VERSION=24` is the canonical pattern), and the user overrides via `clawker
build --build-arg` — native Docker, author-documented, zero clawker vocabulary,
out of the config schema. `build.stacks` stays a bare string list. Deeper
variance: fork the stack (own every interior choice) or hand-roll via injection
points. Clawker composes opaque wholes and never reaches inside
(selection-not-synthesis).

Note: `--build-arg` targeting an ARG the rendered base Dockerfile declares is
folded into `BaseContentHash`, so the base freshness gate honors it (harness-side
ARGs stay out of the base hash so they don't force base rebuilds). Without the
fold the builder would skip the base build on a hash match and silently eat the
flag.

## Authority map

- **Egress floor** (harness bundle `egress:`) — immutable, never removable. The
  floor decodes directly as `config.EgressRule` (shared with project
  `security.firewall.rules`). E7 (a floor rule cannot request TLS-skip) is
  VALIDATED, not structural: `bundler.LoadBundle` rejects a floor rule with
  `insecure_skip_tls_verify: true` at both register and build front doors, and
  `harness register` additionally runs `firewall.ValidateRule` over floors; CP
  ingestion validation is the second net.
- **apt packages** — an extend surface for BOTH harness bundles and projects.
  Bundles apt-install inside their own Dockerfile fragment blocks (nothing added
  to the manifest). Projects get `build.packages` (base image) and
  `build.harnesses.<name>.packages` (per-harness image) because project configs
  are YAML-only.
- **Substrate deps** — outside the stack system: the bare minimum for clawker to
  work plus the secure-by-default contract (ssh etc.). Reserved surfaces still
  enter the contract as validated declarations.
- General precedence law: arbitrary keys, precedence by topology; shipped clawker
  harnesses+stacks = floor; the user overrides with a matching key; never merge.
- Fixed clawker invariants: substrate image, clawkerd PID-1, CP enrollment,
  mTLS, CA trust, reserved surfaces, resolver semantics. FORK is the universal
  escape hatch. Engine sovereignty: bundles/stacks are pure data, code runs only
  inside the Docker build, no plugin surface reaches into the engine. Dogfood:
  shipped bundles/stacks are canonical consumers with zero privileged paths.
- Design preference order for expressing a constraint: UNSAYABLE > VALIDATED >
  DOCUMENTED.

## Config schema

Registration and declaration are orthogonal for both kinds. Registries are
top-level; declarations live under `build:`:

```yaml
# clawker.yaml
stacks:                          # STACK REGISTRY: name → path (availability; CLI register writes here)
  my-rust: { path: ./stacks/my-rust }
harnesses:                       # HARNESS REGISTRY: name → path (separate concern from build config)
  codex: { path: ./tools/codex-bundle }
build:
  packages: [...]                # base apt
  stacks: [go, my-rust]          # base declaration, renders in this order
  harnesses:                     # per-harness overlay: same primitive trio, one lineage
    claude:
      stacks: [bun]              # after the bundle's installer stacks
      packages: [libnss3]
      inject: { after_harness_install: [...], before_entrypoint: [...] }
```

- Registered-not-declared = available, inert. Declared-not-registered = a
  front-door error unless a bundled/shipped name resolves.
- Paths: relative (from project root) or absolute; no env/`~` expansion —
  parsing stays dumb.
- Every stratum gets the same primitives (packages/stacks/inject); placement is
  where you declare.
- Per-harness overlay stacks render AFTER the bundle's installer stacks
  (installer → overlay; the project extends the bundle's floor). Per-harness
  inject is scoped to that one harness's image and appends after any global
  project inject at the same points. Overlay packages become an apt RUN in that
  harness image with NO dedupe vs the base list — render as declared, apt
  idempotence is the mechanism (set-subtraction was rejected: it buys nothing
  and adds base-freshness coupling).
- **Manifest minimalism**: harness bundle config stays minimal — the Dockerfile
  fragment is the mechanism, the manifest only declares what the engine must
  know (stacks, egress, volumes, seeds, version). Litmus for any proposed
  manifest key: could the author write it in the fragment instead? Then no key.

Manifest schema types are owned by `internal/config` (`config.Manifest`,
`config.StackManifest`, `config.EgressRule`); loaders and resolution live in
`internal/bundler` (`LoadBundle`, `LoadStackDefinition`, `LoadHarness`). The
`internal/harness` and `internal/stack` packages no longer exist — both folded
into `internal/bundler`. The shared naming validator lives in `internal/consts`
(`ValidateName` / `ValidateHarnessName`, `NameMaxLength = 32`, rule
`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, one rule for stacks + harnesses, dir name IS
the name).

## Ordering

No dep graph, no topo sort. Stacks are Dockerfile snippets; declared keys in the
merged config become paths in the build context, rendered top to bottom in
declaration order. To extend another stack, declare after it; the extending
author documents the requirement. There is no stack-to-stack dep machinery.
Root-before-user and base-before-harness hold as Docker physics.

## Current-state gotchas

- `validateProjectRegistries` runs at load, not on `Set`/`Write`. The register
  commands self-validate before writing; a future non-command write path would
  be unguarded until the next load.
- `ProjectRoot()` is registry-anchored: it is `""` for a project that has a
  `clawker.yaml` on disk but was never registered (`clawker init` /
  `project register` not run). When empty, register writes ABSOLUTE paths and
  `store.Write()` lands in the USER config-dir `clawker.yaml`, not the local
  project file. Register prints `Written to <path>` so the destination is never
  invisible. Registration is meant to run inside an initialized project.

## Open items (phase 2)

1. **Monitoring stack breakout** (major, own design pass needed). Harness-specific
   telemetry artifacts (claude-code index template, ISM policy, index patterns,
   CC dashboards) are hardcoded in the monitoring package / opensearch-bootstrap
   today. Direction: they become bundle content (e.g. a `monitoring/` dir in the
   bundle), applied via explicit `clawker monitor install <bundle>` commands plus
   a monitor-owned host-global install ledger with enable/disable/remove — NOT
   path-aware discovery from project registries (explicit install gives an
   uninstall path and user control). `monitor up` bootstrap iterates the ledger's
   installed+enabled entries instead of a hardcoded list; generic infra indexes
   (envoy/coredns/cli/clawkercp) stay built into the monitoring package. Still
   undecided: ledger schema/home, dashboard lifecycle on bundle update,
   install-time vs up-time artifact application.
2. **Host UAT of the full register → build → run flow** — cannot be driven
   in-container.
3. **Harness template block names are still placeholders** (`block_1..6`,
   `bundler.DeclaredBlocks`) — final event-centric names are undecided; renaming
   ripples docs + skill + bundles.
4. **Skills-plugin multi-agent packaging** (merge blocker for the branch) — the
   skills plugin goes multi-agent, no longer Claude Code-only packaging; adopt an
   established all-in-one multi-agent harness plugin approach. User docs already
   say "agent skills plugin"; dev CLAUDE.md files still describe the Claude Code
   plugin spec and get updated when the packaging lands.
5. **opencode/pi bundles** authored via the harness-stack-dev skill — the planned
   UAT exercise for that skill (research memo: `harness-research-opencode-pi`).
