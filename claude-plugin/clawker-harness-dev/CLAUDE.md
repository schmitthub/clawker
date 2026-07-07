# Clawker Harness Dev Plugin — Development Guide

See `../CLAUDE.md` for general Claude Code skill plugin conventions.

## Core Principle: Verified Concrete Reference (opposite of clawker-support)

The `clawker-support` plugin deliberately avoids concrete details and points
at live docs. **This plugin does the opposite, on purpose.** Its audience is
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
| Registry resolution, name grammar, materialization | `internal/bundler/harness.go`, `internal/harness/materialize.go` |
| Toolchain format + placement | `internal/toolchain/`, `internal/bundler/toolchain.go` |
| Egress composition | `internal/bundler/egress.go` |
| Shipped bundle/toolchain examples | `internal/bundler/assets/harnesses/{claude,codex}/`, `internal/bundler/assets/toolchains/*/` |

## Drift Gate

When clawker changes any of the above (new manifest field, new apply token,
renamed block, changed validation message, changed shipped floor), this
plugin MUST be updated in the same change. The shipped codex/claude egress
excerpts in `reference/security-egress.md` are quoted verbatim from the
manifests — re-quote them when the manifests change.

## Plugin Structure

```
claude-plugin/clawker-harness-dev/
├── .claude-plugin/plugin.json    # Plugin metadata and version
├── README.md                     # User-facing install and usage docs
├── CLAUDE.md                     # This file — development guide
└── skills/clawker-harness-dev/
    ├── SKILL.md                  # Skill definition: orientation, authoring workflows, iteration loop
    └── reference/
        ├── harness-manifest.md   # harness.yaml field + validation reference
        ├── template-blocks.md    # Block slot semantics, PATH gotcha, cache rules, worked fragments
        ├── toolchain-authoring.md # Definition format, placement, self-guarding, collisions
        ├── security-egress.md    # Egress floor design rules + shipped patterns (quoted verbatim)
        └── worked-example.md     # Complete minimal fictional bundle to adapt
```

## Audience Boundary

This plugin is for people BUILDING bundles/toolchains. End-user questions
(project config, firewall unblocking, MCP setup, container troubleshooting)
belong to `clawker-support` — keep the trigger descriptions disjoint. It is
fine for this skill to name clawker source paths: its audience is working
against the clawker contract, sometimes in the clawker repo itself.

## Versioning

Plugin version lives in `.claude-plugin/plugin.json`. **Every change bumps
the patch number (`1.0.Z` → `1.0.Z+1`). No exceptions.** The version is the
delivery mechanism — the release pipeline only picks up a change when the
version increments. Keep it patch-only.

## Completion Gate

After making changes to the plugin:

1. Re-verify changed reference claims against the source files in the table
   above — never update from memory.
2. Check the verbatim egress excerpts still match the shipped manifests.
3. Verify cross-references between SKILL.md and reference files are
   consistent.
4. Bump the patch version in `plugin.json`.
