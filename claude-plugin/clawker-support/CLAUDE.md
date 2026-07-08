# Clawker Support Plugin — Development Guide

See `../CLAUDE.md` for general Claude Code skill plugin conventions.

This plugin ships two skills with deliberately opposite documentation
philosophies. Keep their trigger descriptions disjoint:

| Skill | Audience | Philosophy |
|---|---|---|
| `skills/clawker-support` | End users — setup, config, troubleshooting | Minimal concrete details; point at live docs |
| `skills/harness-stack-dev` | Extension authors — harness bundles + stack definitions | Verified concrete reference; every claim checked against clawker source |

## Plugin Structure

```
claude-plugin/clawker-support/
├── .claude-plugin/plugin.json    # Plugin metadata and version
├── README.md                     # User-facing install and usage docs
├── CLAUDE.md                     # This file — development guide
├── skills/clawker-support/
│   ├── SKILL.md                  # Main skill definition and workflow
│   └── reference/
│       ├── project-config.md     # Project config discovery, layering, troubleshooting
│       ├── sample-go.yaml        # Reference config: Go project (clawker's own)
│       ├── sample-node.yaml      # Reference config: Node.js project
│       ├── settings.md           # User settings schema, troubleshooting
│       ├── mcp-recipes.md        # MCP setup methodology, troubleshooting
│       ├── troubleshooting.md    # Entry point routing to domain-specific sections
│       ├── docker-hygiene.md     # Docker disk space diagnosis and cleanup
│       ├── monitoring.md         # OTel + OpenSearch + Prometheus stack, Clawker workspace, telemetry env, troubleshooting
│       ├── firewall-security.md  # Proactive VCS egress lockdown — git credential-exfil defense (HTTPS path-scoping, GitHub write surface, monitoring-discovery loop)
│       ├── claude-code.md        # Claude Code integration — auth section: in-container auth model, managed-config staging, /login troubleshooting
│       └── known-issues.md       # Active bugs and workarounds
└── skills/harness-stack-dev/
    ├── SKILL.md                  # Skill definition: orientation, authoring workflows, iteration loop
    └── reference/
        ├── harness-manifest.md   # harness.yaml field + validation reference
        ├── template-blocks.md    # Block slot semantics, PATH gotcha, cache rules, worked fragments
        ├── stack-authoring.md    # Definition format, placement, self-guarding, collisions
        ├── security-egress.md    # Egress floor design rules + shipped patterns (quoted verbatim)
        └── worked-example.md     # Complete minimal fictional bundle to adapt
```

## clawker-support skill

### Core Principle: Minimal Concrete Details

This skill avoids concrete configuration details where possible. Configs,
packages, APIs, CLI flags, and tooling evolve constantly. Baking field names,
domain lists, or flag syntax into the skill produces stale guidance that agents
treat as authoritative.

**When concrete details DO appear, they are deliberate and load-bearing.**
They represent either stable architectural concepts (e.g., `agent.post_init`
as the build-time vs runtime boundary, and `agent.pre_run` as its once-vs-every-
start counterpart) or curated reference samples that are manually kept current.

#### Reference config samples

`reference/sample-*.yaml` files contain working `.clawker.yaml` configs for
different stacks (Go, Node.js). These are standalone YAML files — not inlined
in markdown — so they only consume context when the agent reads them for a
relevant task. `project-config.md` has a table pointing to each sample.

Samples are manually maintained and NOT drift-checked. When updating, copy
from a known-working source. The docs site
(`https://docs.clawker.dev/configuration`) remains the authoritative schema
reference and should still be fetched for field-level details.

#### What belongs in the skill files

- Decision frameworks and methodology (how to think about a problem)
- Workflow steps and interview questions
- Pointers to live documentation URLs
- Architectural concepts that are stable (discovery rules, layering model)
- Gotchas about common mistakes
- Curated reference config samples (manually maintained, not drift-checked)

#### What does NOT belong in the skill files

- Exhaustive field name lists (point to docs instead)
- CLI flag syntax (point to docs instead) — **exception:** the firewall
  path-scoping flags (`--path`/`--action`, `path_default`/`path_rules`) are
  deliberate and load-bearing security methodology, kept concrete in
  `firewall-security.md`, SKILL.md, and `troubleshooting.md`. The narrowest-
  scope advice is meaningless without them. Treat as a curated reference, not
  drift-prone syntax.
- Domain lists (hardcoded firewall domains, registry URLs)
- Version numbers or base image digests

### Reference File Conventions

Each domain reference file (`project-config.md`, `settings.md`, `mcp-recipes.md`)
follows the same structure:

1. What it is and how it differs from other domains
2. How to get the current schema (always a docs URL)
3. Domain-specific methodology
4. **Troubleshooting** section (consistent heading name across all refs)

`troubleshooting.md` is the entry point — it has a routing table that points
to domain-specific troubleshooting sections and keeps only global/cross-cutting
diagnostics (clawker not found, firewall, credentials, container won't start).

### Dockerfile Templates

The skill does not bundle a copy of clawker's Dockerfile templates. When the
skill needs to map config sections to generated Dockerfile steps, it fetches
the current templates (`Dockerfile.base.tmpl`, `Dockerfile.harness-image.tmpl`)
straight from the repo (see SKILL.md research steps).

Never hardcode template field names into the skill — update methodology and
docs URLs instead.

## harness-stack-dev skill

### Core Principle: Verified Concrete Reference (opposite of clawker-support)

The clawker-support skill deliberately avoids concrete details and points at
live docs. **This skill does the opposite, on purpose.** Its audience is
extension authors, and its value is a verified field dictionary: manifest
vocabulary, validation error meanings, block-slot semantics, and the shipped
bundles' security patterns. Vague methodology would be useless here — an
author needs to know that `seeds[].apply` accepts exactly three tokens and
what each does.

The cost of that choice is a drift obligation:

**Every reference claim must be verifiable against clawker source.** The
ground-truth files are:

| Skill content | Source of truth |
|---|---|
| `harness.yaml` fields + validation errors | `internal/harness/harness.go`, `internal/harness/bundle.go`, `internal/harness/consts.go` |
| Version resolvers | `internal/harness/harness.go` (VersionSpec), `internal/bundler/versions.go` (ResolveHarnessVersion) |
| Block slots, reserved defines | `internal/harness/consts.go` (DeclaredBlocks, isReservedDefine), `internal/harness/compose.go` |
| Master template render order | `internal/bundler/assets/Dockerfile.harness-image.tmpl`, `Dockerfile.base.tmpl` |
| Registry resolution (project `clawker.yaml` lineage), provenance | `internal/bundler/harness.go`, `internal/bundler/stack.go`, `internal/config/schema.go` |
| Name grammar (unified rule) | `internal/consts/name.go` |
| Register/list/remove CLI | `internal/cmd/stack/`, `internal/cmd/harness/`, `internal/cmdutil/registry.go` |
| Stack format + placement | `internal/stack/`, `internal/bundler/stack.go` |
| Egress composition | `internal/bundler/egress.go` |
| Shipped bundle/stack examples | `internal/bundler/assets/harnesses/{claude,codex}/`, `internal/bundler/assets/stacks/*/` |

### Drift Gate

When clawker changes any of the above (new manifest field, new apply token,
renamed block, changed validation message, changed shipped floor), this
skill MUST be updated in the same change. The shipped codex/claude egress
excerpts in `reference/security-egress.md` are quoted verbatim from the
manifests — re-quote them when the manifests change.

### Audience Boundary

This skill is for people BUILDING bundles/stacks. End-user questions
(project config, firewall unblocking, MCP setup, container troubleshooting)
belong to the clawker-support skill — keep the trigger descriptions disjoint.
It is fine for this skill to name clawker source paths: its audience is
working against the clawker contract, sometimes in the clawker repo itself.

## Versioning

Plugin version lives in `.claude-plugin/plugin.json`. **Every change bumps the
patch number (`1.0.Z` → `1.0.Z+1`). No exceptions.**

The version IS the delivery mechanism. This plugin is served from a separate
repo, and the release pipeline only picks up a change when the version
increments (the marketplace caches by version). A change with no bump stays in
this repo and never reaches users — so "is this worth a bump?" is the wrong
question: if you touched the plugin, bump it. A typo fix, a new reference file,
and a workflow rewrite are each just the next patch.

Keep it patch-only. Minor/major are reserved for a deliberate, announced change
in how the plugin works, which effectively never happens here.

## Completion Gate

After making changes to the plugin:

1. clawker-support: check that `known-issues.md` is still accurate — remove
   entries for fixed bugs; verify reference cross-references are consistent
   (troubleshooting routing table, SKILL.md research step references)
2. harness-stack-dev: re-verify changed reference claims against the
   source files in the drift-gate table — never update from memory; check the
   verbatim egress excerpts still match the shipped manifests; verify
   SKILL.md ↔ reference cross-references
3. Bump the patch version in `plugin.json` (see Versioning — the release
   pipeline requires an increment per change)
