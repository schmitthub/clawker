# Stack Unit Contract — Design Brainstorm (LIVE DOCUMENT)

Status: **BRAINSTORM — nothing locked except §2. Zero implementation until user signs off.**
Started 2026-07-07. User tears apart iteratively; keep revision notes in §9.

## 1. Problem statement

Shipped unit model (branch `feat/multi-harness-support`) is a flat list of named units with:

- **No unit-to-unit requires.** `typescript` cannot say "needs node" — inexpressible, so the need leaks into another stratum (live specimen 2026-07-07: project `root_run: npm install -g wscat` depends on node stack being declared; broke the moment the key renamed).
- **Dedup = name-string equality.** No version, no capability semantics. Two units both installing "a node" under different names compose silently and fight over PATH/prefix.
- **Conflicts invisible below the name.** Version-manager vs system install, prefix collisions, PATH order — nothing detects them; "self-guarding fragments" are convention, not contract.
- **No validation surface.** Unknown/dead config keys accepted silently (live specimen: `build.toolchains:` after rename → zero warning → base built with no stacks → build died at an unrelated step with `npm: not found`).

User verdict: "very sloppy… going to bite us hard… far from acceptable as is." BLOCKS MERGE.

## 2. Invariants (LOCKED — not up for debate)

1. **Strata separation** (user idiom, verbatim): base images aren't responsible for harness deps; harness deps aren't responsible for user or base-image clawker deps; project configs aren't responsible for the other two — they own bespoke nuanced project needs. Every stratum declares its own needs in ONE enforceable vocabulary. Gaps get fixed in the unit model, never papered over in another stratum's config.
2. **Placement topology**: project-declared → shared base; harness-declared → that harness's image only; earliest-stage-wins dedup. Base stays harness-agnostic by construction (a python harness + node harness + rust harness must NOT fatten one base).
3. Units expose **primitives**, not end-result abstractions (bundle guiding principle).
4. **Dogfood / no privileged paths (user, 2026-07-07):** clawker exposes the capability and consumes it itself for the shipped embedded bundles + stacks — "we never get to cheat, we are consumers of it just like anyone else." Shipped bundles/stacks = best-effort sensible defaults (goal: most users never need anything else) AND the canonical example of proper use of the image builder + packaging feature. If a shipped bundle needs something the public contract can't express, that is a contract gap to fix — never an internal hook.

## 3. Prior art (live-fetched 2026-07-07 — sources cited)

### 3.1 CNB Build Plan (buildpacks/spec buildpack.md via GitHub API; buildpacks.io/docs use-build-plan)

- Detection: each buildpack writes `[[provides]] name=…` and `[[requires]] name=… [requires.metadata]…`. Top-level `requires.version` is **deprecated** — version constraints live in `requires.metadata` by convention (e.g. `version = "10.x"`, `version-source = ".nvmrc"`).
- Resolution rule (normative): "If a required buildpack requires a dependency that is not provided by the same buildpack or a previous buildpack, the trial MUST fail to detect." AND the inverse: an unconsumed provide from a required buildpack also fails the trial. Optional buildpacks with unmet requires/unused provides are just excluded.
- `[[or]]` blocks = alternative plans, tried left-to-right depth-first.
- Build phase receives filtered **Buildpack Plan**: only entries matching what that buildpack provides.
- Worked pattern: node-engine provides `node` AND requires `node` (version from `.nvmrc` in metadata — self-require to pass constraints to itself); npm buildpack requires `node` without providing it.
- **Key takeaway**: matching by name, order-sensitive, version constraints are CONVENTION (metadata) not spec-enforced — the lifecycle never evaluates semver; the PROVIDING buildpack interprets all constraint metadata aimed at its name.

### 3.2 devcontainer Features (containers.dev/implementors/features)

- Manifest `devcontainer-feature.json`: `id`, `version` (semver), `options` (typed: boolean/string, `enum`/`proposals`, defaults; passed to install.sh as env vars), `dependsOn` (hard deps **with options** — "must be satisfied… before the given Feature is installed"; auto-pulls into install set), `installsAfter` (soft ordering — "only influences the installation order of Features that are already set to be installed"), `legacyIds`, `deprecated`.
- Install order: (1) recursive graph build over dependsOn/installsAfter, (2) roundPriority (default 0, override via `overrideFeatureInstallOrder`), (3) round-based commit of Features whose deps are satisfied.
- Equality/dedup: "two Features are identical if their manifest digests are equal, AND the options executed against the Feature are equal (compared value by value)." Local features never equal anything.
- Distribution: OCI registry (`ghcr.io/...`), HTTPS tarball, or local path. OCI Artifact Distribution Spec.
- **Key takeaway**: dependency = concrete feature ID (+ pinned options), NOT abstract capability. No version-constraint solving — you depend on `ghcr.io/x/node:1` and that's that. Dedup only when digest+options identical; two node features with different options = two installs.

### 3.3 nix (nix.dev manual intro)

- Store paths hashed over the full dependency closure: "different versions of a package end up in different paths… so they don't interfere with each other." Conflict-freedom by construction; atomic; rollback.
- **Key takeaway**: the only prior art that ELIMINATES conflicts instead of detecting them — at the cost of abandoning FHS/apt/mutable-prefix UX. Incompatible with clawker's Debian substrate + apt packages + normal dev ergonomics as a wholesale model. Possible partial inspiration: per-unit prefixes + explicit PATH composition (a diluted store) — but that's how version managers already behave and it's where the PATH fights come from. Noted as the honest far pole.

### 3.4 Spectrum summary

| model | dep declaration | version handling | dedup | conflicts |
|---|---|---|---|---|
| shipped clawker | none | none | name string | silent |
| CNB | abstract capability name | metadata convention, provider interprets | name match in ordered group | trial fails, provenance weak |
| devcontainer | concrete artifact ID + options | pinned tag/digest | digest+options equality | ordering solved, semantic conflicts NOT |
| nix | hash closure | exact by construction | store path | impossible by construction |

Clawker's want — abstract capability + REAL version constraints + provenance errors — is stronger than any single one of these. CNB gets closest in shape; devcontainer gets closest in packaging/distribution.

## 4. Design axes (BRAINSTORM — leanings marked, nothing decided)

### A. Dependency vocabulary: capability vs artifact
- CNB-style: `requires: node` (abstract name, any provider satisfies) — enables swapping `corp-node` for `node`.
- devcontainer-style: `requires: stacks/node` (concrete unit) — simpler, no provider ambiguity, weaker.
- Lean: **capability names with a default provider registry** — `provides: [node]` on the node unit; `requires: [node>=22]` from anywhere; resolver picks provider (error if >1 candidate and no explicit choice).

### B. Version constraints: who evaluates?
- CNB: nobody in the lifecycle — provider buildpack reads metadata. Keeps spec small, pushes semver logic into every provider.
- Alternative: resolver evaluates semver ranges centrally against `provides: node@<resolved version>`; needs units to declare the version they will install (they already have version resolvers for pinning — harness VersionSpec precedent in `internal/bundler/versions.go`).
- Lean: **central semver evaluation** — clawker already resolves pinned versions at build time; constraint check is cheap and gives uniform provenance errors ("harness codex requires node>=22; project stack corp-node provides node 18.20.1 (from corp-node/stack.yaml)").

### C. Closure: auto-pull or error?
- devcontainer dependsOn auto-includes. CNB requires an explicit group member to provide.
- Auto-pull question interacts with STRATA: if harness requires node and nothing provides it, auto-adding the node stack to the harness image is arguably correct (harness stratum declaring its own need). If PROJECT unit requires node, auto-add to base.
- Lean: **auto-pull the default provider into the REQUIRING stratum's stage**, surfaced in build output; hard error only on ambiguity or constraint conflict.

### D. Stage assignment under closure
- Rule candidate: a unit lands in the earliest stage that requires it; project→base, harness→harness image; if two harnesses require the same capability and project doesn't, it renders in EACH harness image (base stays lean — invariant §2.2). Earliest-stage-wins already handles project+harness overlap.
- Open: dedup across sibling harness images is a non-goal? (Images are per-harness; duplication across them is layer cost, not correctness cost.)

### E. Conflict semantics
- Same capability, two providers in one resolved graph → error with provenance (unless one is the pulled default and the other explicit — explicit wins?).
- Constraint intersection empty → error with provenance.
- Below-the-name conflicts (PATH/prefix): not solvable by resolver alone. Options: (i) units declare owned paths/prefixes and resolver checks overlap; (ii) convention + docs; (iii) ignore. Open question.

### F. Parameterization
- devcontainer options precedent. One `node` unit with `version` option beats `node18`/`node22` unit forks.
- Interacts with B: option-resolved version becomes the `provides` version fed to constraint check.
- Dedup with differing options (devcontainer: not equal → both install) is EXACTLY our PATH-fight scenario. Lean: **same capability with conflicting requested versions in one stage = error**, not dual-install.

### G. Catalog ownership / distribution (STRATEGIC)
- Option 1: clawker owns embedded catalog (status quo, N units forever, user called this "owning endless dev stacks that are brittle in concert").
- Option 2: adopt/adapt devcontainer Features as the unit format (adapter runs install.sh in build stage) — inherit huge community catalog + OCI distribution; costs: root-context assumptions, options mapping, no capability/version layer on top (we'd add ours), external ecosystem drift.
- Option 3: own the FORMAT (stack.yaml + fragments + new contract fields) but support OCI/git distribution later; ship few, let community author.
- **RESOLVED direction (user gut, 2026-07-07): clawker owns the contract and dogfoods it** (invariant §2.4). Shipped catalog = small, high-bar, canonical. Ecosystem adapters (devcontainer Features as leaf providers, OCI/git distribution) possible LATER but must comply with the contract — never the base model. Pure Features adoption rejected: their no-constraint, options-digest dedup model fights our conflict rules.

### H. Ordering within a stage
- installsAfter-style soft ordering probably needed once requires exist (node before typescript is derivable from requires; soft ordering covers non-dependency preferences). Lean: derive order from requires topology only; add soft ordering only when a real case demands it (no speculative features).

### I. Validation front-door
- Unknown keys, dead unit names, unsatisfiable constraints → error at config write/build front-door (per feedback_validate_at_write_frontdoor_not_cp_generation). Silent-ignore (today's `build.toolchains` specimen) becomes impossible.

## 5. Non-goals (proposed)
- Solving cross-harness-image dedup (per-harness duplication acceptable).
- Full nix-style hermetic store.
- Runtime (container-start) dependency resolution — this is all build-time.

## 6. Open questions for user
1. Capability names vs concrete unit IDs (axis A)?
2. Central semver evaluation acceptable (axis B)? Requires every unit to declare resolved version it installs.
3. Auto-pull default provider vs hard error (axis C)?
4. Below-the-name conflict detection: declare owned prefixes/paths, or convention-only (axis E)?
5. Options on units (axis F) — yes/no, and does same-capability-different-version in one stage hard-error?
6. Catalog strategy fork (axis G): own format vs devcontainer Features adapter vs hybrid.
7. Does `packages:` (apt) fold into the same vocabulary eventually, or stay separate? (Strata idiom says one enforceable vocabulary — apt packages are also "units a stratum needs".)

## 7. Live specimens to test any design against
- typescript needs node (unit→unit requires)
- wscat via npm in project root_run (project need whose dep is stack-level)
- corp-node 18 vs harness needs node>=22 (constraint conflict provenance)
- python harness + node harness + rust harness, one project (base leanness)
- node via version-manager vs system node (below-the-name PATH conflict)
- `build.toolchains` dead key (validation front-door)
- shipped claude bundle declares `stacks: [node]` — concrete name, versionless; the "why node" lives in a YAML comment because the contract can't say it (dogfood gap specimen)
- shipped codex bundle declares NO stacks (standalone musl binary) — contract must cost zero when unused

## 7b. Revision r2 (2026-07-07)
- Axis G resolved by user: own contract + dogfood (§2.4). Open questions Q6 answered; Q1–Q5, Q7 still open.
- New open question Q8: scope of "never cheat" — does clawker's own substrate/base dep set (git, zsh, sudo, supervision deps) become units too, or does the unit contract govern only the three strata's DECLARED needs while substrate stays fixed infrastructure?

## 7c. Revision r3 (2026-07-07) — no canonical taxonomy (user)

**User rejection (verbatim intent):** clawker must NOT get into naming/governing canonical stacks — the variance is exponential (node×{typescript,nvm,nvm-shim,yarn/pnpm/yarn1/yarn2}, python×{venv,pyenv,pipenv,uv}, …) and dev environments are "inherently degenerative, customizable and nonstandard"; teams have varying tooling hygiene. Any taxonomy attempt "will probably kill clawker adoption." Focus = low-level generic capability, zero assumptions.

**Resolved model — opaque tokens + dumb resolver (CNB stance, grounded in spec: lifecycle never interprets names):**
- Capability = opaque user-defined token. Clawker resolves the declared graph, NEVER semantics.
- Shipped units using tokens (claude requires node; node stack provides node) = one author keeping own units coherent (dogfood §2.4), not namespace governance.
- Custom unit declaring `provides: node` takes responsibility for meaning-compatibility. Drop-in works via binding precedence: project binding > settings binding > shipped-default provider > ambiguity = provenance error (mirrors harness registry default-flag precedent).
- **Below-the-name conflicts (PATH/prefix/version-manager fights) formally OUT of scope** — axis E resolved: convention + author responsibility. Q9 resolved: provides promises whatever the unit documents; zero checkable semantic surface.
- Axis A resolved: capability tokens, no meaning registry. Axis G packaging: coupling is bundle→token←stack; existing materialization+registry packaging unchanged, only the vocabulary edge is new.

**Pending user confirmation (asked in r3):**
- Versions optional on BOTH sides; constraint check fires only when requirer constrains AND provider declares version; versionless-provide-meets-versioned-require = error (lean) or warning?
- Axis F demoted: variance handled by cheap unit authoring (fork the shipped unit), not option explosion on shipped units. Options minimal or dead.
- If confirmed, contract core = `provides: [token(@version)?]` / `requires: [token(constraint)?]`; resolver = closure + binding precedence + stage placement by declaring stratum + provenance errors.

## 7d. Revision r4 (2026-07-07) — disconnected-parties matrix (user)

**User framing:** build context has project base + harness base, maintained by DIFFERENT, disconnected parties (harness author = us or community person; project maintainer = wants things in project base AND in the harness base). Goal: "exposing opportunity for all, with precedence, and flexibility" without assumptions. Product stakes: clawker already outclasses Docker's agent sandbox (they're hamstrung by licensing/competitor tools); strong small core userbase; this contract is an adoption lever — get it right.

**Positional-opportunity matrix (proposed, positional not content-prescriptive):**

| position | harness author | project maintainer |
|---|---|---|
| project base | — (never their stratum) | build.stacks + instructions (owns) |
| harness image | manifest requires = the floor | per-harness additions + provider bindings via existing project-config `harnesses.<name>:` entries |

Precedence per harness-image build: (1) bundle manifest requires = author floor; (2) project per-harness ADD units + BIND providers; (3) earliest-stage-wins pulls project-declared shared deps to base; (4) contradictions → provenance error naming both parties.

**Open (asked r4):** can project REMOVE/override an author's requirement, or add+bind only? Lean: add+bind only; escape hatch = fork the bundle (cheap authoring is the product). Full override would destroy author's ability to promise anything about their harness environment.

**Still open from r3:** versionless-provide vs versioned-require (error/warning); options demoted (fork > configure); axis C auto-pull; Q7 apt packages; Q8 substrate scope.

## 7e. Revision r5–r6 (2026-07-07) — composition + lineage overrides (see model doc §4b/§3)

- Composition: one bound provider per token per lineage; central constraint intersection; selection-never-synthesis (only merge in design = constraint math); vendor-privately escape hatch; multi-version provides permitted for version-manager units.
- Per-lineage bindings + self-sourced requires (from real claude/codex nvm war story). **§2 invariant AMENDED: earliest-stage-wins is now DEFAULT, not law** — user signed off. **Redundancy over reconciliation** locked (orphaned base install acceptable; no reconciliation machinery ever).
- Still open: hard pin/reject of specific units in harness floor (vs preference-only); egress-floor removal; apt removal; versionless×versioned; options demotion; auto-pull; apt fold-in.

## 8. Sources
- buildpacks/spec buildpack.md (fetched via GitHub API 2026-07-07)
- buildpacks.io/docs/for-buildpack-authors/how-to/write-buildpacks/use-build-plan/ (2026-07-07)
- containers.dev/implementors/features/ (2026-07-07)
- nix.dev/manual/nix/stable/introduction (2026-07-07)

## 9. Revision log
- r1 (2026-07-07): initial framing, prior art, axes, open questions. Nothing user-approved yet.
