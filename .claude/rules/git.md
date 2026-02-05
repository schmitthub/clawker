# internal/git Package Rules

## Tier 1 (Leaf) Package Constraints

**CRITICAL:** This is a leaf package with strict import restrictions.

### Allowed Imports
- Standard library
- `github.com/go-git/go-git/v6` and subpackages
- `github.com/go-git/go-billy/v6` and subpackages

### FORBIDDEN Imports
- ANY `github.com/schmitthub/clawker/internal/*` packages
- This ensures the git package can be used anywhere without circular dependencies

## Design Principles

1. **Return errors, don't log** — Callers handle logging. Never import logger package.

2. **Pass configuration as parameters** — No config package dependency. Use WorktreeDirProvider interface.

3. **WorktreeDirProvider enables DI** — SetupWorktree and RemoveWorktree take this interface. ListWorktrees takes `[]WorktreeDirEntry` (caller converts from config types).

4. **Slashed branch names supported** — Branch names like `feature/foo` work correctly. The worktree name uses the slugified directory basename, not the branch name.

5. **Facade pattern** — GitManager is the entry point. Access sub-managers via Worktrees().

## Testing Requirements

- Use temp directories for worktree tests (go-git requires real filesystem)
- Test helper: `newTestRepoOnDisk(t)` creates seeded git repo
- Mock `WorktreeDirProvider` with `fakeWorktreeDirProvider` for high-level method tests

## Error Handling

- Wrap go-git errors with context
- Use `ErrNotRepository` sentinel for non-git directory errors
- Support `errors.Is()` for error checking

## API Stability

- `WorktreeDirProvider` interface is the contract with config package
- Adding methods requires coordinated changes to config.Project()
