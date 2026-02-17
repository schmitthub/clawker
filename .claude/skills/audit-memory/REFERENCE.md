# Audit Memory Reference

## Expected CLAUDE.md Locations

Packages that **should** have a CLAUDE.md (contain significant public API):

- `internal/docker/`
- `internal/containerfs/`
- `internal/config/`
- `internal/cmdutil/`
- `internal/iostreams/`
- `internal/workspace/`
- `internal/bundler/`
- `internal/cmd/loop/shared/`
- `internal/socketbridge/`
- `internal/tui/`
- `internal/term/`
- `internal/hostproxy/`
- `pkg/whail/`

Packages that **don't need** a CLAUDE.md (thin wiring or test-only):

- `internal/cmd/*/` — Covered by `.claude/rules/container-commands.md` and similar rules
- `test/harness/` — Covered by `.claude/rules/testing.md`
- `internal/logger/` — Simple setup, covered in code-style rule
- `internal/prompter/` — Small, self-documenting

## Go Symbol Extraction Patterns

Exported identifiers in CLAUDE.md (backtick-wrapped):

```
grep -oE '`[A-Z][A-Za-z0-9]*`' file.md | tr -d '`' | sort -u
```

Exported symbols in Go source:

```
grep -E '^(func|type|var|const) [A-Z]' *.go | sed 's/^.*:\(func\|type\|var\|const\) \([A-Za-z0-9]*\).*/\2/' | sort -u
```

For methods on types:

```
grep -E '^\s*func \([^)]+\) [A-Z]' *.go | sed 's/.*) \([A-Za-z0-9]*\).*/\1/' | sort -u
```

## Mintlify Documentation Files

### Directory Structure

```
docs/
├── docs.json              # Mintlify config (theme, nav, colors, integrations)
├── custom.css             # Dark terminal theme overrides
├── favicon.svg            # >_ terminal prompt icon (amber on dark)
├── index.mdx              # Homepage
├── quickstart.mdx         # Hand-authored guide
├── installation.mdx       # Hand-authored guide
├── configuration.mdx      # Hand-authored guide
├── architecture.md        # Developer docs (with Mintlify frontmatter)
├── design.md              # Developer docs (with Mintlify frontmatter)
├── testing.md             # Developer docs (with Mintlify frontmatter)
├── assets/                # Image assets
└── cli-reference/         # Auto-generated (82 files, NEVER edit directly)
    ├── clawker.md
    ├── clawker_container.md
    └── ...
```

### Auto-generated vs Hand-authored

| Type | Extension | Editable | Source |
|------|-----------|----------|--------|
| CLI reference | `.md` | No — regenerate via Makefile, checked in, freshness verified in CI | `internal/docs/markdown.go` + `cmd/gen-docs/main.go` |
| Hand-authored pages | `.mdx` | Yes | Direct edit |
| Developer docs | `.md` | Yes | Direct edit (have Mintlify frontmatter) |
| Site config | `.json` | Yes | Direct edit |
| Theme CSS | `.css` | Yes | Direct edit |

### Regeneration Command

```bash
go run ./cmd/gen-docs --doc-path docs --markdown --website
```

### Navigation Validation

Compare `docs.json` nav entries against actual files:

```bash
# Extract page refs from docs.json
grep -oE '"cli-reference/[^"]+' docs/docs.json | sed 's/"//' | sort > /tmp/nav-pages.txt

# List actual CLI reference files (without extension)
ls docs/cli-reference/*.md | sed 's|docs/||;s|\.md$||' | sort > /tmp/actual-pages.txt

# Diff
diff /tmp/nav-pages.txt /tmp/actual-pages.txt
```

## Context Budget Guidelines

From the memory consolidation plan:

| Scope                       | Budget | Notes |
|-----------------------------|--------|-------|
| Root CLAUDE.md              | ~200 lines | Always loaded |
| Each `.claude/rules/*.md`   | ~50-100 lines | Always loaded for matching paths |
| Total always-loaded         | < 1500 lines | Root + all rules |
| Each `internal/*/CLAUDE.md` | < 200 lines | Lazy-loaded per file access |
| `.claude/memories/*.md`     | No hard limit | On-demand only |
| `.claude/docs/*.md`         | No hard limit | On-demand only |

## Staleness Thresholds

| Age | Severity | Action |
|-----|----------|--------|
| 0-30 days | OK | No action needed |
| 31-60 days | Warning | Review for accuracy |
| 61-90 days | Stale | Update or verify still correct |
| 90+ days | Critical | Likely outdated, prioritize update |

## Rules Path Frontmatter Format

```yaml
---
description: Brief description
paths: ["internal/docker/**", "pkg/whail/**"]
---
```

Glob patterns use Git's pathspec format. Validate with:

```bash
git ls-files 'pattern'
```
