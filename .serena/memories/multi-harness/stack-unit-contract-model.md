# Stack Unit Contract — Design Model (v0 draft for teardown)

Companion: `multi-harness/stack-unit-contract-design` (brainstorm log + prior art + open questions). This doc = the abstraction: separation of concerns, topology, user stories. Nothing implemented; user iterating.

## 0. Why nothing off the shelf fits (user, r9)

Novel intersection: a plugin/extension system FOR building development containers FOR coding agent runtimes — "a cluster fuck of everything": plugin engine + build composition + dev-environment variance + security supervision. Each prior art solves one slice: systemd (data plugins, no build/untrusted authors), CNB (build composition, but author code at plan time + no security boundary), devcontainer Features (dev variance, no dep semantics/supervision), nix (perfect builds by owning every package — incompatible with inherently degenerate dev envs), vendor agent sandboxes ("lackluster temu versions" — sandbox without composition). Clawker = the intersection: data-plugin engine at plan time (sovereign, auditable) → author code at build time (sandboxed into image layers, three disconnected parties) → security supervision at runtime (nobody else attempts). The disciplined line: WHEN code runs is contractual — never in the engine, always in the build, supervised at runtime. Nobody wants to own this intersection because all three layers must be held to get any one right — that's the moat.

## 1. Roles (separation of concerns)

| role | owns | declares | never touches |
|---|---|---|---|
| **Contract owner** (clawker core) | vocabulary, resolver, placement rules, error surfaces, substrate image | — | token MEANINGS (no taxonomy, no assumptions about dev environments) |
| **Stack author** (clawker-shipped, community, corp platform team, or the project maintainer wearing another hat) | one unit: manifest + root/user fragments; documents what its tokens mean | `provides: [token(@version)?]`, `requires: [token(constraint)?]` | other strata's configs; the resolver |
| **Harness author** (us or community; disconnected from projects) | one bundle: harness runtime, egress floor, volumes, blocks | `requires: [token(constraint)?]` — the FLOOR for its image | installing stack-level deps directly; project config |
| **Project maintainer** | project's bespoke needs | base units (`build.stacks`), per-harness additions/bindings (`harnesses.<name>:`), bespoke instructions | editing bundles/units they don't own (escape hatch = fork; authoring is cheap) |
| **Machine operator** (settings; often same human, different hat) | what's registered/available on this machine | unit + bundle registry entries, machine-wide default bindings | project semantics |

Dogfood invariant: clawker-as-stack-author and clawker-as-harness-author have ZERO privileged paths — shipped units/bundles are canonical consumers of the same contract.

## 1b. Governance: who wins (scoped sovereignty, not a rank ladder)

**Engine sovereignty (r8, user): clawker CLI/source is THE authority.** Bundles / stacks / project configs are pure DATA (YAML declarations + inert template fragments) influencing behavior only through hooks the engine exposes. They can never control or extend validation, graph resolution, binding, placement, ordering, or error handling — no plugin surface into the resolver, no author-defined validation, no author merge policies. Fragments execute only inside the Docker build (sandboxed into the image), never in the clawker process, never at plan time. Engine owns the schema; data cannot extend the vocabulary it's written in. Prior-art divergence, deliberate: CNB's detect phase RUNS author code; ours is static declarations — authors get less power, users get a resolver fully determined by clawker source + visible data (auditable, reproducible). The party scopes below are DATA-AUTHORITY scopes — whose declarations the engine listens to for which decision; the engine is above the table.

**Extension model (r8, user, verbatim intent): "hooks through conventions (file naming, directory structure, schema definitions) that clawker honors to load, validate, and inject."** Every hook is convention-shaped data at a well-known position; engine lifecycle per hook is uniform: load → validate → inject at a defined position. Authors never register code. Block slots, stack fragments, seeds, egress floors, requires — one hook species at different positions ([[feedback_first_principles_strata_not_literal_mapping]]: positional opportunities, never content-prescriptive). User: "very much a classical plugin/extension system" — closest classical prior art = systemd units (declarative files at well-known paths, engine-owned load/validate/order, Requires=/After=, drop-in override.conf overlays ≈ per-lineage additions, enable symlinks ≈ bindings, no plugin code in PID 1). Lesson inherited: the unit schema becomes the product surface, versioned + documented forever. Boundary to defend when pressure comes for a "dynamic stack": data forever, execution only inside the Docker build.

Three parties with variable shared interest (user, 2026-07-07: "sometimes they are on the same page, sometimes they couldn't disagree more"). Clawker core's own supervision needs also ride on the build. Each party is absolute in its own scope; scopes differ in KIND:

1. **Clawker core = invariant scope.** Supervision contract (clawkerd PID-1, firewall CA trust, socketbridge, volumes/labels, env bootstrap, user model). Wins absolutely — but ONLY on a minimal, enumerated, machine-validated reserved set. Generalize the `isReservedDefine` precedent: reserved surfaces (paths, env vars, tokens, volume names) are DECLARED, validator rejects collisions with provenance. Outside the set: zero say (no-taxonomy rule). Q8 answered: substrate deps stay out of the unit graph; substrate's reserved SURFACES enter the contract as validated declarations (today they're implicit — a fragment clobbering .zshenv breaks silently at runtime; that's the disconnected-parties failure mode).
2. **Harness author = floor scope.** `requires:` = functional floor of their image ("my agent boots"). Project may add+bind, never remove. Loses only to invariants.
3. **Project dev = discretionary scope.** Closest to workload → last word on everything left: bindings, provider picks, additions. project binding > settings binding > shipped default.

**Conflict taxonomy — every disagreement gets exactly one outcome:**

| conflict | outcome |
|---|---|
| different positions (base vs harness image) | compatible by construction |
| same token, incompatible constraints/providers | provenance error; humans decide, build never guesses |
| project rejects harness author's floor | exit: fork the bundle (cheap authoring = pressure valve) |
| anyone rejects a clawker invariant | no — or a deliberate operator-level toggle clawker chose to expose (e.g. firewall enable); never expressible in unit/bundle vocabulary |

## 1c. Authority map (fixed vs extend vs override — v0)

Mutability classes + litmus tests (future surfaces classify themselves):
- **FIXED** — breaks supervision/security if violated → no vocabulary touches it. "This is just how it's gotta be."
- **EXTEND** — another party's promise/floor → add, never subtract.
- **OVERRIDE** — choice among functional equivalents → precedence chain, closest-to-workload wins.
- **OWN** — bespoke to one party → sole discretion.
- **FORK** — universal escape hatch for rejecting someone's EXTEND/OWN surface (materialized user-owned copies already enable it).

| surface | class | holder | others may |
|---|---|---|---|
| substrate image, clawkerd/PID-1, CP enrollment, mTLS | FIXED | clawker | — |
| firewall CA trust in image | FIXED | clawker | operator toggle firewall.enable (deliberate exposed valve) |
| reserved surfaces (env bootstrap, user model, volume topology, labels, reserved blocks) | FIXED | clawker | validator rejects collisions w/ provenance |
| resolver semantics, placement rules, error format | FIXED | clawker | — |
| token meanings | — | nobody | per-unit documentation (out of contract forever) |
| base apt packages | EXTEND | clawker seeds minimum (tiny, justified per entry) | project adds; removal OPEN (lean: no) |
| harness `requires` floor | EXTEND | harness author | project add+bind, never remove; FORK to reject |
| bundle egress floor | EXTEND | harness author | project adds; removal OPEN (lean: host firewall ops only; too-wide floor → fork?) |
| capability→provider binding | OVERRIDE | project > settings > shipped default | ambiguity = error |
| project base stacks, instructions, per-harness adds, post_init/pre_run | OWN | project | — |
| harness image blocks/runtime | OWN | harness author | project extends via per-harness surfaces; FORK for guts |
| shipped units/bundles content | OWN (clawker-as-author), materialized user-owned | clawker | fork = one cp; staleness stamps warn never clobber |

OPEN rows flagged for user: egress-floor removal (deny-override in build vocabulary vs host-op/fork only); apt package removal.

## 1d. Bilateral clause register (r7 — sacred surfaces, contractually enforced)

Every party gets rights AND prohibitions per surface; clawker core makes promises too. Enforcement split: clauses binding authors/projects → validator front-door w/ provenance; clauses binding clawker core → conformance suite (contract in executable form, CI); shipped bundles pass the same validator as community ones (dogfood).

Seed clauses (user examples verbatim, to be enumerated exhaustively in design phase 2):

| surface | clause | promisor | enforcement |
|---|---|---|---|
| named volumes | harness bundles can't declare volumes matching clawker-reserved names/paths | harness author | validator |
| named volumes | clawker core will never mount volumes other than its enumerated set | clawker | conformance test |
| named volumes | project configs can't create named volumes, period | project | vocabulary absence |
| build seeding | seed/stage dests fall under bundle's own declared volumes (exists today) | harness author | validator |
| build seeding | seeds = managed config + creds only ([[feedback_seeds_managed_config_and_creds_only]]) | harness author + clawker | validator where checkable |
| runtime fs copy | config staging touches only bundle's declared volume paths | harness author | validator |
| env | harness env / project env disjoint; clawker-reserved env (ZSH_ENV, CLAWKER_*) untouchable | all three | validator |
| base instructions | project may add root_run/user_run/inject | project right | schema |
| base instructions | harness bundles cannot inject into base — base harness-agnostic by construction | harness author | vocabulary absence |
| config spaces | harness manifests cannot set project config values; per-harness overlays are project-owned | both | schema separation |

**Ordering contract (binding/render guardrails):**
1. Within a stage: topo order by requires — the ONLY semantically guaranteed order.
2. Ties: declaration order — stable, documented, golden-testable.
3. Root fragments before user fragments; base layers before harness layers (FROM-chain physics).
4. Guardrail: a unit depending on ordering the contract doesn't guarantee = author bug by definition. Need to run after X? Require X.

**Design principle (r7): prefer UNSAYABLE over VALIDATED over DOCUMENTED.** Several prohibitions are enforced by vocabulary absence (no key/slot exists to violate) — the cheapest, strongest guardrail. Reach for validator rules second, documentation last.

## 2. Vocabulary (the entire contract surface)

- **Unit manifest**: `provides: [token(@version)?]`, `requires: [token(constraint)?]` + root/user fragments. Tokens = opaque strings; version optional both sides.
- **Harness manifest**: `requires: [token(constraint)?]`.
- **Project config**: `build.stacks: [unit-ref]` (declaring a unit ADDS it to the graph — its provides satisfy others' requires; no separate bind needed in the common case), `harnesses.<name>.stacks/bindings` for per-harness adds + provider bindings.
- **Settings**: registry of units/bundles + machine default bindings.

That's it. Meaning, hygiene, PATH discipline = author documentation, out of contract.

## 3. Topology

```
substrate (pinned debian + clawker infra)              contract owner
    │
project base ◄── project-declared units render here    project maintainer
    │            (their provides visible to ALL harness images)
    ├──────────► harness image A (clawker-<proj>:claude)
    │              bundle A requires (floor)            harness author A
    │              + project per-harness adds/binds     project maintainer
    ├──────────► harness image B (clawker-<proj>:codex)
    │              bundle B requires nothing → zero contract cost
```

Placement rules: project-declared → base; harness-declared/auto-pulled-for-harness → that harness image only; base harness-agnostic by construction (polyglot harnesses never fatten base). **Earliest-stage-wins = DEFAULT satisfaction, not law (user signed off 2026-07-07):** harness lineage may self-source a token, shadowing the base install. **Redundancy over reconciliation (user, verbatim intent): "redundancy is the cost of flexibility… better off with a project base dep install that gets orphaned in favor of a separate additional harness level install than trying to manage/reconcile both."** No reconciliation machinery, ever; orphaned layers = accepted cost, stated in resolver output.

## 4. Resolution pipeline (contract owner's machine)

1. **Collect**: project base units + per-harness adds + harness requires + transitive unit requires.
2. **Closure**: each unsatisfied `require token` → candidates = units already in graph first; else registry lookup via binding precedence: project binding > settings binding > shipped-default provider. No provider → error. Ambiguity (>1 candidate, no binding) → error.
3. **Constraint check**: per token, intersect all requires-constraints; check against provider's declared version. Fires only when both sides declare. (OPEN: versionless provide × versioned require = error or warning.)
4. **Placement**: unit → earliest stage needing it, bounded by declaring stratum (auto-pulled deps of a harness-only require land in that harness image, not base).
5. **Order**: topo-sort by requires within each stage.
6. **Render + validate**: fragments render; front-door rejects unknown keys, unregistered unit refs, unsatisfiable graphs. Silent-ignore impossible.

Every error names its provenance: "harness codex requires node>=22 (codex/harness.yaml); bound provider corp-node provides node@18.20.1 (project binding, .clawker.yaml)".

## 4b. Composition rules (same-token peers — r5)

Prior-art poles (both grounded): CNB = one provider per name, provider privately merges all requirers' metadata (opaque per-provider policy); nix = double-install by hash-isolated store paths (needs non-FHS world; rejected wholesale).

Ours:
1. **One bound provider per token per image lineage.** All requires of a token bind to the same provider unit; renders ONCE at earliest needing stage; later stages inherit layers. Double install impossible by construction.
2. **Version conflict = central constraint intersection.** Empty intersection → provenance error naming all requirers + files. We don't reconcile incompatibles; we make them loud + cheap to route around (rebind / fork / drop). Unlike CNB: central + uniform, no per-provider merge policy.
3. **Peer escape hatch: don't participate in the token.** Author who needs their own version vendors it in their own prefix, doesn't require the token — self-containment invisible to the graph (nix's trick, locally, at author discretion).
4. **Advanced provider (permitted, never required):** one unit may provide multiple versions of a token (`provides: [node@18, node@22]`) — truthful for version-manager-flavored units; resolver satisfies conflicting requirers from it; runtime selection is below-the-name (author-documented).
5. **Sibling harness images = separate lineages.** Per-image resolution; cross-image duplication is layer cost only (non-goal, locked).
6. **Per-lineage bindings + self-sourced requires (r6 — from user's real nvm war story: claude tolerated nvm-node, codex choked, zero vocabulary for nuance).** Binding = (lineage, token) → provider unit; lineage = image ancestry (base, base+claude, base+codex). Cross-stage satisfaction (base provides → harness require satisfied) is the DEFAULT, not a law — a harness require may be marked self-sourced ("bring my own regardless of base"), rendering that lineage's bound provider in the harness stage. Docker physics: base layers immutable from harness stage → override = SHADOWING (harness-stage install + PATH precedence, harness fragments render later), stated loudly in resolver output with provenance. Harness-author opinion has two strengths: preference (project per-harness binding may override) vs hard pin/reject of SPECIFIC units (floor-strength; project recourse = fork; never abstract flavor names — taxonomy stays dead). OPEN (user call): is hard pin allowed at all, given it dents corp drop-in for that harness? **CONSEQUENCE signed off (user, 2026-07-07): earliest-stage-wins demoted to default; redundancy-over-reconciliation accepted (see §3).**
7. **Reconciliation = SELECTION, never synthesis (r5, user challenge: rival nvm-flavored node units).** Two providers of one token: resolver binds one, loser's fragments never render (zero partial application). Units are atomic opaque installers; the contract composes graphs of WHOLE units, never unit interiors. The only merge anywhere = constraint intersection (math on declared version strings — narrows or errors, cannot produce nonsense). Grounding: CNB likewise merges REQUESTS not installations (provider picks 1 artifact from N metadata wishes via its own priority policy — surprising winners possible, no content blending exists); nix links via absolute store paths + RPATH/shebang rewriting + profile symlinks, possible only because nix builds every package itself — structurally unavailable to us (opaque author fragments).

## 5. User stories

| # | actor | story | contract exercise |
|---|---|---|---|
| 1 | stack author | ship `typescript` unit that needs node without knowing which node | unit requires `node`; resolver closes over whatever provides it (was: inexpressible → strata violation) |
| 2 | project maintainer | wscat CLI available in images | author 5-line project-local unit (provides wscat, requires node) instead of `root_run: npm install -g wscat` hack that breaks when node vanishes |
| 3 | corp platform team | company-blessed node everywhere, shipped bundles untouched | corp-node provides `node`; project declares it in base; claude's require satisfied by earliest-stage-wins |
| 4 | project maintainer | catch corp-node too old for a harness | constraint check: corp-node@18 vs claude requires node>=X → provenance error at build, not npm-not-found mid-build |
| 5 | polyglot team | python harness + node harness + rust harness, one project | each harness image self-contains its runtime; base stays lean |
| 6 | harness author (codex) | static musl binary, no deps | empty requires; contract costs zero |
| 7 | community harness author | new agent needs bun>=1, no shipped bun stack | build error "no provider for token bun" → any party registers a bun unit; no waiting on clawker to bless bun |
| 8 | anyone | typo/dead key (`build.toolchains`) | validation front-door error, never silent ignore |
| 9 | project maintainer | claude image additionally needs playwright deps | per-harness add under `harnesses.claude:` — lands in claude image only |
| 10 | machine operator | prefer nvm-flavored node on this machine for all projects | settings default binding `node → my-nvm-node`; project binding still outranks |

## 6. Open questions (carried from brainstorm log r3/r4)

1. Project override power: add+bind only (lean) vs remove/override author's floor.
2. Versionless provide × versioned require: error (lean) vs warning.
3. Options on units: demoted to near-zero — fork > configure (lean; variance is exponential).
4. Auto-pull default provider silently (surfaced in build output) vs always error when require unsatisfied by declared graph.
5. Do apt `packages:` eventually fold into the same vocabulary (a unit that provides via apt)?
6. Substrate scope: clawker's own base deps stay outside the unit system (lean) or become units.

## 7. Revision log
- v0 (2026-07-07): roles/vocabulary/topology/pipeline/stories drafted from brainstorm r1–r4. Awaiting teardown.
