---
name: audit-memory
description: Audit all Claude memory and documentation files for staleness, symbol accuracy, and context budget.
disable-model-invocation: true
allowed-tools: Bash, Read, Glob, Grep
---

# Audit Memory

Perform a comprehensive documentation health audit across all Claude context files.

## Steps

### 1. Inventory

Glob all documentation files and build a summary table:

- `CLAUDE.md` (root)
- `cmd/*/CLAUDE.md`, `internal/*/CLAUDE.md`, `test/*/CLAUDE.md`, and `pkg/*/CLAUDE.md`
- `.claude/rules/*.md`
- `.claude/docs/*.md`
- `.claude/memories/*.md`
- `.serena/memories/*.md`

For each file, report: **path**, **line count** (`wc -l`), **estimated tokens** (lines × 4).

Group into categories:
- **Always-loaded**: root `CLAUDE.md`, `.claude/rules/*.md`
- **Lazy-loaded**: `cmd/*/CLAUDE.md`, `internal/*/CLAUDE.md`, `test/*/CLAUDE.md`, `pkg/*/CLAUDE.md`
- **On-demand**: `.claude/docs/*.md`, `.claude/memories/*.md`
- **WIP tracking**: `.serena/memories/*.md`

### 2. Freshness Check

Run the freshness script and include its output:

```bash
bash scripts/check-claude-freshness.sh --no-color
```

### 3. Symbol Accuracy

For each `cmd/*/CLAUDE.md`, `internal/*/CLAUDE.md`, `test/*/CLAUDE.md`, and `pkg/*/CLAUDE.md`:

1. Read the file
2. Extract all backtick-wrapped Go identifiers (pattern: single backtick-wrapped words matching `[A-Z][A-Za-z0-9]*` — exported symbols)
3. For each identifier, grep for it in `*.go` files in the same directory
4. Report:
   - **Missing**: identifiers documented but not found in Go source (renamed or deleted)
   - **Undocumented**: exported Go symbols (`^func [A-Z]`, `^type [A-Z]`, `^var [A-Z]`, `^const [A-Z]`) in the directory not mentioned in the CLAUDE.md

### 4. Rules Path Validation

For each `.claude/rules/*.md` file with a `paths:` frontmatter field:

1. Parse the glob patterns from `paths: ["glob1", "glob2"]`
2. Run `git ls-files '<glob>'` for each pattern
3. Flag rules where no files match any glob (dead rule)

### 5. Serena Memory Staleness

Read each `.serena/memories/*.md` file and flag:

- Files containing "COMPLETE", "Status: Complete", "Status: Done", or "DONE" — these track finished work and should be cleaned up
- Files with no updates in >30 days (check `git log --format=%at -1`)

### 6. Architecture and Design Accuracy

1. Identify changes in architecture, design, CLI commands, test harnesses, or test doubles from the freshness check output, git statuses, or commit messages
2. For each `.claude/docs/*.md` files:
   - Check for mentions of outdated components, patterns, or practices
   - Flag files that likely need updates based on the nature of the changes (e.g., if a major refactor is detected, all architecture/design docs may need review)

### 7. Context Budget

Report totals against budgets:

| Category | Budget | Actual |
|----------|--------|--------|
| Root CLAUDE.md + all rules | < 1000 lines | ? |
| Each individual CLAUDE.md | < 200 lines | ? |
| Total always-loaded context | < 1500 lines | ? |

Flag any files exceeding their budget.

### 8. Recommendations

Output a prioritized action list using these categories:

- **DELETE**: Completed WIP memories in `.serena/memories/`
- **UPDATE**: Stale docs (from freshness check) or docs with missing/wrong symbols
- **TRIM**: Files exceeding context budget
- **ADD**: Packages in `internal/` or `pkg/` with Go files but no `CLAUDE.md`

Format each recommendation as:
```
[ACTION] path/to/file — reason
```

Sort by priority: DELETE > UPDATE > TRIM > ADD.
