# Brainstorm: Claude JSON Persistence in Containers

> **Status:** Active
> **Created:** 2026-02-23
> **Last Updated:** 2026-02-23 20:30

## Problem / Topic
When clawker containers restart, Claude Code inside loses awareness of the last conversation because `~/.claude.json` contains `lastSessionId` keyed by absolute host paths. The session JSONL data survives on config volumes, but the pointer file either gets overwritten or has mismatched path keys. We need a strategy to persist/translate these pointers so conversation continuity survives container restarts.

## Open Items / Questions
- Should we use `--resume <id>` vs path-rewriting vs project-dir symlinking?
- What's the minimal change to `containerfs` to support this?
- Does `--continue` work if we only fix the `.claude.json` entry and project dir?
- Should we persist `.claude.json` on a volume or regenerate it on each restart?
- How do we handle the case where a container changes project paths mid-lifecycle?

## Decisions Made
- (none yet)

## Conclusions / Insights

### Binary Internals (Reverse-Engineered)

**Config Home** (`G9()`):
```
process.env.CLAUDE_CONFIG_DIR ?? path.join(os.homedir(), ".claude")
```
- `CLAUDE_CONFIG_DIR` env var overrides the default `~/.claude` directory

**Global Config File** (`.claude.json`) location:
```
path.join(process.env.CLAUDE_CONFIG_DIR || os.homedir(), `.claude${suffix}.json`)
```
- Production suffix is `""` → file is `~/.claude.json`
- If `CLAUDE_CONFIG_DIR=/foo` → file is `/foo/.claude.json`

**Project Session Directory** (`TC(path)`):
```
path.join(G9(), "projects", nj(path))
```
- `nj(path)` = replace all non-alphanumeric chars with `-`, truncate if too long + hash
- e.g., `/Users/andrew/Code/clawker` → `-Users-andrew-Code-clawker`

**Session Transcript Path**:
```
path.join(TC(E9()), `${sessionId}.jsonl`)
```

**Current Working Dir** (`E9()`): `vR.originalCwd` — set once at startup, immutable

**`--continue` flag**: Looks up `lastSessionId` from project state in `~/.claude.json` keyed by `E9()` (the current working directory)

**`--resume <session-id>`**: Bypasses `lastSessionId` lookup entirely — loads transcript directly by session ID

**`--session-id <uuid>`**: Use a specific session ID for the conversation

### Root Cause Analysis
The problem has **two dimensions**:
1. **`.claude.json` path key mismatch**: Host path `/Users/andrew/Code/clawker` vs container path `/workspace`
2. **Project directory name mismatch**: `~/.claude/projects/-Users-andrew-Code-clawker/` vs `~/.claude/projects/-workspace/`

The session JSONL files survive on the config volume, but Claude Code can't find them because both the pointer (`lastSessionId` key) and the storage directory (`nj()` mangled path) are wrong.

### Possible Approaches
1. **`--resume <id>`**: Extract `lastSessionId` from host config during `containerfs`, pass as CLI arg — cleanest, no file mutation
2. **Path rewriting in `.claude.json`**: During container init, copy host project entry to container path key
3. **Project dir symlink**: Create `~/.claude/projects/-workspace/` → `~/.claude/projects/<host-mangled-path>/`
4. **Dual approach**: Rewrite `.claude.json` entry + symlink project dir — makes both `--continue` and `--resume` work
5. **`CLAUDE_CONFIG_DIR`**: Override env var — doesn't solve path key problem alone


## Gotchas / Risks
- `.claude.json` contains per-project data beyond just `lastSessionId` (allowedTools, mcpServers, onboarding flags, etc.) — modifying it could break other state
- `nj()` truncates long paths and appends a Bun hash — we need to use the same algorithm for consistency
- Container working directory might vary between `run` and `create` commands
- Multiple containers for the same project could race on `.claude.json` writes
- Session files reference `cwd` internally — Claude Code may validate that the JSONL's `cwd` matches `E9()`
- The `--resume` flag shows an interactive picker if no session ID given — need to pass the ID explicitly

## Unknowns
- Does Claude Code validate that transcript `cwd` matches current `E9()`? (If so, `--resume` alone won't work)
- Does `--session-id` reuse an existing session or start fresh with that ID?
- Is there a limit to how many project entries `.claude.json` can hold?

## Next Steps
- Evaluate: `--resume` (approach 1) vs dual path-rewrite+symlink (approach 4)
- Prototype the chosen approach in `containerfs`
- Test: does `--resume <id>` work when the transcript `cwd` doesn't match?
