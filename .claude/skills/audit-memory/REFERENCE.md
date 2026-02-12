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
- `internal/ralph/`
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
