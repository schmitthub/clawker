# Stack Contract — Design Model (r10)

**STATUS: IMPLEMENTED (2026-07-08) on `feat/multi-harness-support`.** All 6 tasks of `multi-harness/stack-contract-implementation` are complete (commits e0bfd3f4 → Task 6). §1–§6c below shipped as designed; the only material deviations recorded in the implementation memory: harness registration folded into the pre-existing `harnesses.<name>` map (adds `path:`, not a second node); config-dir materialization DROPPED entirely (shipped resolve from the embedded FS, no fork-convenience copy); no registry `default` flag (built-in default = `claude`); the shared naming validator lives in `internal/consts`. §7 phase-2 items still open (clause-register re-enum, apt-into-stacks fold, monitoring breakout). Docs shipped: `docs/{stacks,harness-bundles,custom-images}.mdx`, `docs/upgrading/v0.13.mdx`, CLI reference, `claude-plugin/clawker-support/skills/{clawker-support,harness-stack-dev}`.

Implementation plan: `multi-harness/stack-contract-implementation` (6-task initiative, 2026-07-08). Companion: `multi-harness/stack-unit-contract-design` (brainstorm log r1–r9 + prior art; its token/provider vocabulary is SUPERSEDED by r10 below — read for rationale history only).

## 0. Why nothing off the shelf fits (user, r9 — still true)

Novel intersection: plugin/extension system FOR building dev containers FOR coding-agent runtimes — plugin engine + build composition + dev-environment variance + security supervision. Prior art each solves one slice: systemd (data plugins, no build), CNB (build composition, but author code at plan time), devcontainer Features (dev variance, no dep semantics/supervision), nix (owns every package — incompatible with degenerate dev envs), vendor sandboxes (no composition). Disciplined line: WHEN code runs is contractual — never in the engine, always in the build, supervised at runtime. Holding all three layers = the moat.

## 1. Terminology (r10 — LOCKED, do not mince)

- **Stack** — robust collection of Dockerfile instruction injections doing complex dev-stack setup conveniently, dedup guaranteed. A collection (nvm+typescript+node; uv+python), not an atom. NEVER replaces existing template injection points — `inject`/`root_run`/`user_run` remain the bespoke hand-roll escape hatch, always available.
- **DEAD vocabulary** (r9 tokens model scrapped by user): "unit", "token", "provides/requires capability", "provider", "binding", "auto-pull", "self-sourced flag", machine-wide default bindings, settings-layer registry. Rationale: capability tokens make clawker arbiter of dep naming — violates no-taxonomy ("node" means different things to different people); conflict resolution would need relationship mappings/prefixes/db.
- Replacement primitive: **named keys + layered store + topology precedence**. Stack name IS the key. Deps are direct stack names. Override = matching key at closer layer, wholesale replace, NEVER merge (selection-not-synthesis survives in stronger form).

## 2. Resolution: layered keyed store, per-lineage scope

**No global namespace. No db. Scope = lineage.** Bundled stacks have no global identity; two harness bundles never conflict because sibling images never share a namespace.

| lineage | lookup chain for key K |
|---|---|
| base | project > shipped |
| base+harness X | project (incl. `harnesses.X` overlay) > X bundle's `stacks/` > shipped |

- Settings layer SCRAPPED from this subsystem (user, r10). One registry home: `clawker.yaml`; store walk-up = the resolution mechanism. Clawker-shipped stacks/harnesses = **virtual base layer of the backing store object** (floor; overridable by matching key, per engine/store design — ties into node-native Store API v2 work).
- Same-layer collision impossible by construction: registry = keyed map; register front-door errors on existing key; `--force` replaces with provenance output (what was shadowed, old vs new path).
- **Names are local bindings chosen by the registrar** (docker-tag model): bundle manifest suggests default, `--name` overrides at register time. Bundle content carries no global identity claim. Want official codex + fork simultaneously → register under distinct names.
- Provenance ALWAYS printed in build output: `harness codex ← ~/tools/codex-bundle (project registry) shadows shipped`.
- Portability caveat (accepted): `harnesses.<name>` resolves against local registry → same clawker.yaml on two machines may resolve differently. Feature (corp drop-in) and footgun; provenance output is the mitigation.

### Placement (RESOLVED r10.1, user 2026-07-08 — option B, cross-stratum dedup KILLED)
- Project-declared stack → base image. Harness-installer stack → that harness image only. **No cross-stratum interaction, ever**: project and harness both declare `node` → base renders project's, harness image ADDITIONALLY renders its lineage-resolved definition on top. Engine NEVER judges whether a base render "satisfies" a harness declaration (name-match-satisfaction = implicit taxonomy, rejected). Satisfaction logic lives INSIDE fragments: self-guards are author-owned (node fragment's "keep existing node ≥ floor" pattern), apt idempotence, PATH shadowing by later layers (Docker physics). Bundle author deals with isolation or documents conflicting-stack combos. IMPL CONSEQUENCE: remove `resolveHarnessStacks`' project-declared skip.
- Shipped stack from a bundle: installer names it WITHOUT bundling → lookup chain bottoms out at shipped (project overlay > bundle stacks/ > shipped). Bundling only means "I want MY definition". Error only when a name resolves nowhere.
- Base harness-agnostic **by construction**: no bundle in base scope, bundled stacks structurally cannot enter base lookup.
- nvm war story (claude ok nvm-node, codex not): project sets `harnesses.codex` stack override — per-harness project overlay. No harness self-source flag needed; nuance lives closest to workload.
- Two harnesses vendor same community stack → each renders in own lineage. Redundancy-over-reconciliation stands (orphaned/duplicate layers = accepted cost, stated in output).

## 3. Registration & distribution

- **CLI command set (new work): `clawker harness register|list|remove`, `clawker stack register|list|remove`.** Registration = deliberate host-side human action, auditable.
- **Path reference (LOCKED r10, user: "it has to be path reference")**: registry entries in clawker.yaml point at bundle/stack dirs on disk. No materialize-into-project copy. Yaml not self-contained across machines — accepted.
- **Harness bundle `installer` section**: declares the bundle's stack deps by name. Bundled stacks live in `stacks/<name>/` inside the bundle, discovered by dir name (dir name IS the name).
- **External stack use by a harness bundle** — two paths, no new vocabulary:
  1. Vendor: copy into bundle's `stacks/rust/` (bundling IS vendoring; author controls version, offline, staleness theirs).
  2. Reference by name: installer names `rust` without bundling; unresolved → build error with guidance (`clawker stack register <path> --name rust`).
- **NO remote fetch** (locked): no auto-clone of stacks at build time — supply-chain surface (unpinned third-party fragments) + engine-sovereignty violation. Engine consumes locally registered data only. Revisit only with full SHA-pinning machinery, separate decision.

## 4. Versioning & parameters (r10)

No version-constraint algebra (locked). A stack cannot carry A version — it's a collection of things (user, 2026-07-08: singular version on a stack is nonsensical). **Parameterization: RESOLVED (user, 2026-07-08): build command args, nothing else.** A stack has NO version — it's a collection of N tools × N versions (python = uv + CPython; node = nvm + node); interiors are unaccountable by nature, no vocabulary can enumerate them. Mechanism: stack author declares Docker `ARG`s per member at their discretion (node stack's `ARG NODE_VERSION=24` is the canonical pattern), user overrides via `clawker build --build-arg` — native Docker, author-documented, zero clawker vocabulary, OUT of config schema. `build.stacks` stays a bare string list. Deeper variance: fork the stack (own every interior choice) or hand-roll via injection points. Clawker composes opaque wholes, never reaches inside (selection-not-synthesis).

## 5. Authority map (r10 updates)

- Egress floor (harness bundle): **immutable, never removable** — locked.
- apt packages: EXTEND surface for **both** harness bundles and project (both user types get install options) — locked.
- Substrate deps: outside stack system; bare minimum for clawker to work + secure-by-default contract (ssh etc.) — locked. Reserved SURFACES still enter contract as validated declarations (r9 §1b holds).
- General precedence law: arbitrary keys, precedence by topology; shipped clawker harnesses+stacks = floor; user overrides with matching key; never merge.
- Unchanged from r9: FIXED clawker invariants (substrate image, clawkerd PID-1, CP enrollment, mTLS, CA trust, reserved surfaces, resolver semantics); FORK = universal escape hatch; engine sovereignty (bundles/stacks pure data, code runs only inside Docker build, no plugin surface into engine); dogfood (shipped = canonical consumers, zero privileged paths); bilateral clause register + conformance suite (§1d of r9 — clause seeds still valid, re-express in r10 vocab during phase 2); ordering contract (now §6c: declaration order only, no topo — root-before-user + base-before-harness stand as Docker physics); prefer UNSAYABLE > VALIDATED > DOCUMENTED.

## 6. Config schema (RESOLVED shape, 2026-07-08)

Registration vs declaration are orthogonal, for both kinds. Registries top-level, declarations under `build:`:

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
      stacks: [bun]              # after bundle's installer stacks
      packages: [libnss3]
      inject: { after_harness_install: [...], before_entrypoint: [...] }
```

- Registered-not-declared = available, inert. Declared-not-registered = front-door error unless bundled/shipped name resolves.
- Paths: relative (from project root) + absolute; no env/~ expansion.
- Every stratum gets the same primitives (packages/stacks/inject); placement = where you declare.
- Registries migrate OUT of settings.yaml (store walk-up finds project entries; shipped = virtual base layer).
- **Manifest minimalism principle (user, 2026-07-08): harness bundle config stays MINIMAL — the Dockerfile fragment is the mechanism, the manifest only declares what the engine must know (stacks, egress, volumes, seeds, version). Do not add abstractions/options for things that can just BE Dockerfile content.** Litmus for any proposed manifest key: could the author write it in the fragment instead? Then no key.

## 6b. Gap list — r10 vs implementation (2026-07-08 audit)

EXISTS, codify only: declaration-order rendering + root/user slots; project-declared skips harness-declared (stacks); fragments render as Go templates w/ DockerfileContext (params ride this); path-based registry entries; filterBasePackages (project vs floor); staleness stamps + materialize-copy-if-missing.

BUILD LIST (dependency order 1→2→3; 4/5/6 parallel after 2):
1. Registry home migration settings.yaml → clawker.yaml (store walk-up = resolution; shipped defs = virtual base layer of backing store, not Ensure*-seeded settings entries; ties into node-native Store API v2 / feat/schema-docs).
2. Override semantics: kill `stackCollisionError` flat-namespace error + `checkBundleShadow` error → matching key closer layer wins wholesale + provenance line in build output (provenance printing doesn't exist at all today). ALSO remove `resolveHarnessStacks`' project-declared skip (cross-stratum dedup killed — placement §2).
3. CLI: `clawker harness|stack register/list/remove` (validate dir + write path entry to clawker.yaml; `--name` local binding; same-layer collision error + `--force`).
4. Bundled `stacks/<name>/` dir discovery (bundles cannot carry stack defs today); lineage lookup gains bundle layer. NO new manifest section — existing flat `stacks:` key is the declaration.
5. Per-harness project overlay surfaces (user, 2026-07-08): `build.harnesses.<name>.{stacks,packages,inject}` — same primitive trio as base, scoped to one lineage. Overlay stacks render AFTER bundle's installer stacks (installer → overlay; project extends bundle's floor). Per-harness inject = harness-image points scoped to ONE harness (today's after_harness_install/before_entrypoint inject hits ALL harness images — new scoping). Overlay packages → apt RUN in that harness image; **NO dedupe vs base list: render as declared, apt idempotence is the whole mechanism** (set-subtraction rejected — buys nothing, adds base-freshness coupling). **Harness BUNDLES get NO packages manifest key (user correction, 2026-07-08): bundles apt-install inside their own Dockerfile fragment blocks — already possible, nothing added.** Manifest stays flat; existing `stacks:` key IS the installer declaration, no `installer:` nesting.
6. Stack parameterization — RESOLVED out of config schema (see §4); nothing to build.
7. Naming convention unification (ValidateHarnessKey exists; stacks separate; one slug rule pending confirm).
8. Contract docs: injection points + stack slots + ordering law as versioned public surface.
9. **BaseContentHash must include base-declared build-arg values** — CRITICAL to this feature, not a follow-up: `--build-arg` per-member ARG override IS the stack parameterization mechanism (§4), and today the base freshness gate silently eats it (skips docker build on hash match; flag never reaches Docker — vanilla BuildKit would honor it). Fix: fold effective values of args the rendered base Dockerfile declares into the hash (harness-side args stay out so they don't force base rebuilds). GH #413 filed then closed 2026-07-08 — belongs in this plan.

## 6c. Ordering (RESOLVED, user 2026-07-08 — replaces r9 topo contract)

**No dep graph, no topo sort.** Stacks = Dockerfile snippets; declared keys in merged config → paths included in build context, rendered **top to bottom in declaration order** (matches existing impl: `resolveProjectStacks` walks build.stacks in yaml order, `splitFragments` preserves it, zero sorting). Extend-another-stack = declare after it; extending author documents the requirement. Stack-to-stack dep machinery: NONE.

## 7. Open items (phase 2)

1. ~~Naming~~ CONFIRMED (user, 2026-07-08): lowercase kebab `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, max 32, dir name IS the name, one rule for stacks + harnesses.
2. ~~Placement~~ RESOLVED (user, 2026-07-08): option B, see §2 — cross-stratum dedup killed, always render, fragment self-guards own satisfaction.
3. ~~Path ergonomics~~ CONFIRMED (user, 2026-07-08): relative (from project root) + absolute; NO env/~ expansion — parsing stays dumb.
4. ~~Schema sketches~~ RESOLVED (2026-07-08): full shape in §6 — registries top-level, declarations under build:, per-harness overlay trio, manifest unchanged.
5. Clause register re-enumeration in r10 vocabulary (from r9 §1d seeds).
6. apt `packages:` fold into stack vocabulary eventually? (carried; low priority)
8. ~~Stack parameterization~~ RESOLVED (user, 2026-07-08): build command args only, like nvm/node stack does today (see §4). SEPARATE general gotcha (not stack-specific): `--build-arg` targeting any ARG in the BASE Dockerfile is silently ignored when BaseContentHash matches (builder skips base build; flag doesn't change rendered bytes). Harness-side ARGs unaffected. NOT normal docker behavior (BuildKit honors --build-arg via arg-value cache keys) — clawker-side bug, user confirmed 2026-07-08. IN the build list (§6b item 9), critical to feature since --build-arg IS the parameterization mechanism; #413 closed as misfiled.
7. **Monitoring stack breakout (major, undecided — own design pass needed).** Harness-specific telemetry artifacts (claude-code index template, ISM policy, index patterns, CC dashboards) hardcoded in monitoring package / opensearch-bootstrap today. Direction: become bundle content (e.g. `monitoring/` dir in bundle), `monitor up` bootstrap discovers from registered bundles. DIRECTION (user, 2026-07-08): explicit `clawker monitor install <bundle>` commands + monitor-owned host-global install ledger with enable/disable/remove — NOT path-aware discovery from project registries (can't un-discover; explicit install gives uninstall + user control). `monitor up` bootstrap iterates ledger's installed+enabled entries instead of hardcoded list. Generic infra indexes (envoy/coredns/cli/clawkercp) stay built into monitoring package. Still undecided: ledger schema/home, dashboard lifecycle on bundle update, install-time vs up-time artifact application.

## 8. Revision log
- v0/r1–r9 (2026-07-07): token/provider capability model drafted from brainstorm; 7 principles locked.
- **r10 (2026-07-07): user teardown of token layer.** Tokens/providers/bindings/auto-pull/settings-registry DEAD → named keys + layered store + per-lineage scope. Path-reference registration locked. No remote fetch locked. No version algebra; stack parameterization = open unheld conversation (flagged 2026-07-08). CLI register command set + installer section + clawker.yaml registry migration = new work items. Principles surviving: dogfood, no-taxonomy (strengthened: position decides, not meaning), engine sovereignty, redundancy>reconciliation, selection-not-synthesis (as wholesale key override), clause register, ordering contract, loud provenance errors.
