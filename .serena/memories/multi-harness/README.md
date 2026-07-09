# Multi-Harness — Current State (branch feat/multi-harness-support)

The multi-harness feature and the stack/harness composition contract are
implemented on this branch. Code and shipped docs are the source of truth:

- **Design model**: serena `multi-harness/stack-unit-contract-model` (this
  folder) — the contract's design and current state.
- **Public contract surface**: `docs/stack-contract.mdx` — engine guarantees
  (E1–E22), author obligations (A1–A11), permanent citation anchors, backed by a
  `// Conformance: <ID>` badge suite.
- **User docs**: `docs/{harness-bundles,stacks,custom-images}.mdx`,
  `docs/upgrading/v0.13.mdx`, CHANGELOG `[0.13.0]`.
- **Author skill**: `claude-plugin/clawker-support/skills/harness-stack-dev/`
  (bundle/stack authoring reference within the clawker-support plugin).
- **Code**: `internal/bundler/` (bundle/stack/harness types, loaders
  `LoadBundle`/`LoadStackDefinition`/`LoadHarness`, resolution, egress floor,
  basehash, embedded `assets/harnesses/{claude,codex}` + `assets/stacks/{go,node,python,rust}`),
  `internal/config/` (manifest schemas `Manifest`/`StackManifest`/`EgressRule`,
  project `stacks:`/`harnesses:`/`build.harnesses:` registries, front-door
  validation), `internal/consts/name.go` (unified naming rule),
  `internal/cmd/{stack,harness}/` (register/list/remove front-door). The former
  `internal/harness` and `internal/stack` packages folded into `internal/bundler`.

## Model in one paragraph

A harness is a file-backed bundle (harness.yaml + Dockerfile fragment + assets/)
embedded for claude + codex, resolving straight from the binary (no
materialization). Custom bundles register per-project in `clawker.yaml`
`harnesses:` (name → path); the built-in default is claude (no registry default
flag). Images: pinned Debian substrate → shared base (packages + project stacks +
instructions/inject) → per-harness image. Language stacks are file-backed
(stack.yaml + root/user fragments), declared never installed, registered
per-project in `clawker.yaml` `stacks:`; per-lineage lookup (project registry →
bundle `stacks/` → shipped), closer layer wins wholesale, provenance printed.
Registration is a path reference (no copy, no remote fetch). Parameterization is
Docker `--build-arg` against author-declared ARGs, folded into the base freshness
hash. Egress floor composes from the selected bundle's `egress:` and is
immutable. There is no capability-token algebra, no version-constraint algebra,
and no cross-stratum dedup.

## Files in this folder

| file | purpose |
|---|---|
| `stack-unit-contract-model.md` | THE design model: engine sovereignty, per-lineage resolution, placement, registration, config schema, authority map, ordering, and open (phase-2) work. |
| `harness-research-opencode-pi.md` | Live-verified recon for the next two harness bundles (opencode, pi). Authoring them via the harness-stack-dev skill is the planned UAT exercise. Items marked UNVERIFIED need firewall-live confirmation. |

## Open work (also tracked in the model doc §7)

- Monitoring stack breakout — own design pass (explicit `clawker monitor install
  <bundle>` + host-global ledger).
- Host UAT of the full register → build → run flow (not drivable in-container).
- Harness template block names still placeholder `block_1..6`
  (`bundler.DeclaredBlocks`) — final event-centric names undecided.
- Skills-plugin multi-agent packaging — merge blocker for the branch.
- opencode/pi bundles via the harness-stack-dev skill (research memo ready).
