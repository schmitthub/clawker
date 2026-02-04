# Git Worktree Support Initiative

**Branch:** `a/git-worktree-support`
**Parent memory:** None (top-level initiative)
**PRD Reference:** None
**Kickoff Prompt:** "Begin the git worktree support initiative. Read the Serena memory git-worktree-support-initiative and start Task 1: Create internal/git package foundation."
---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Create `internal/git` package foundation | `complete` | Agent 1 |
| Task 2: Add Config.Project() with worktree directory management | `complete` | Agent 2 |
| Task 3: Add GitManager to Factory + --worktree flag | `complete` | Agent 3 |
| Task 4: Implement `clawker worktree` management commands | `complete` | Agent 4 |
| Task 5: Add statusline env vars and mode indicators | `complete` | Agent 5 |
| Task 6: Integration tests and documentation | `pending` | — |

## Key Learnings

### Task 1 Learnings

1. **go-git v6 worktree API limitations**: The `x/plumbing/worktree` experimental API doesn't have `WithBranch` option. Had to implement `AddWithNewBranch` by first creating the worktree, then creating the branch reference and checking it out separately.

2. **Avoid panics in production code**: The initial design used panic for programmer errors in `newWorktreeManager`. Code review correctly flagged this - changed to return `(*WorktreeManager, error)` from `Worktrees()` method. This cascaded through all callers but provides proper error handling.

3. **Silent failures in list operations**: ListWorktrees had two silent failure patterns that were caught by code review:
   - GetWorktreeDir errors were silently skipped - fixed by checking for "not found" vs other errors
   - Open/Head errors were silently discarded - fixed by adding Error field to WorktreeInfo

4. **Cleanup error handling**: When SetupWorktree fails after creating directory, need to include cleanup error in the returned error message if cleanup also fails.

5. **isNotFoundError helper**: Created helper to distinguish "directory doesn't exist" (orphaned metadata, skip silently) vs other errors (permissions, I/O - should surface to user).

### Task 3 Learnings

1. **Factory field naming consistency**: The Factory struct uses `GitManager` (not `Git`) to match the pattern of `HostProxy` - fields are named after the type they return.

2. **Worktree flag parsing security**: The branch name regex `^[a-zA-Z0-9][a-zA-Z0-9._/-]*$` catches most shell injection attempts, making the additional git-specific checks (`.lock`, `..`, `@{`) redundant for security but kept for better error messages.

3. **Test expectation alignment**: When validation has multiple layers (regex + specific checks), tests should expect the error from whichever check fires first. The `@{` test case triggers the regex before the specific `@{` check.

4. **Workspace setup unchanged**: The workspace package doesn't need any changes - it just receives a `WorkDir` path string. Whether that path is a project root or a worktree directory is transparent to workspace setup.

5. **Code review caught CRITICAL nil check issue**: The `gitManagerFunc` was accessing `cfg.Project.RootDir()` without checking if `cfg.Project` was nil first. Added defensive nil check with actionable error message.

6. **Improved isNotFoundError robustness**: Changed from exact string matching to `strings.Contains()` for better compatibility across go-git versions and error message format variations.

7. **Added GitManager to test factory**: The run_test.go testFactory helper now includes a GitManager that returns an error, preventing silent nil pointer panics in tests that might exercise the worktree code path.

8. **Error messages include context**: Worktree setup errors now include branch name and agent name for better debugging: `"setting up worktree %q for agent %q: %w"`.

### Task 4 Learnings

1. **Container status was over-scoped**: The initial implementation tried to show container status for each worktree in `list`, but accessing container state required Docker client dependency and type complexity. Removed this feature to keep the list command simple and focused.

2. **Silent failure patterns in removal**: The `removeSingleWorktree` function had multiple silent failure patterns that were caught by the silent-failure-hunter agent:
   - **CRITICAL (fixed)**: When checking worktree status for uncommitted changes, errors from `wt.Open()`, `wtRepo.Worktree()`, and `status()` were silently ignored, proceeding with removal anyway. Fixed by requiring `--force` flag when status cannot be verified.
   - **HIGH (fixed)**: When `--delete-branch` was explicitly requested but branch deletion failed, the command returned success (exit 0) with just a warning. Fixed by returning an error that clarifies "worktree removed but failed to delete branch".

3. **Error message actionability**: Error messages should include the flag needed to override. "use `--force` to remove anyway" gives users a clear next step rather than just describing the failure.

4. **Batch operation error handling**: When processing multiple worktrees, collect errors and report all at the end rather than failing fast. Users can see which specific operations failed.

5. **Time formatting helper**: Implemented `formatTimeAgo()` for human-readable relative times in list output. Handles edge cases: just now, minutes, hours, days, and falls back to date format for older entries.

### Task 5 Learnings

1. **Empty string check pattern**: When adding optional env vars, always check for empty strings before setting (`if opts.Field != "" { m["VAR"] = opts.Field }`). This prevents spurious empty env vars from appearing in the container environment.

2. **Mode resolution duplication**: The workspace mode resolution logic (`containerOpts.Mode` with fallback to `cfg.Workspace.DefaultMode`) was duplicated in both run.go and create.go. This is intentional - the mode is resolved locally to populate RuntimeEnvOpts before calling workspace.SetupMounts, which does its own resolution internally. Considered refactoring SetupMounts to return the resolved mode, but the duplication is minimal (3 lines) and avoids API changes.

3. **Statusline indicator design**: The `[snap]` indicator only shows for snapshot mode, not `[bind]` for bind mode. This follows the principle that the default state doesn't need indication - only non-default states should be visually flagged.

4. **Pre-existing silent failures**: Code review found a pre-existing issue in statusline.sh where `cd` failures are silently ignored. This could cause incorrect git branch display. Filed for follow-up but not blocking Task 5 since it's not introduced by these changes.

(Agents append here as they complete tasks)

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. **MANDATORY Code Review Gate:**
   - Launch `code-reviewer` agent (pr-review-toolkit:code-reviewer) to review all changes
   - Launch `silent-failure-hunter` agent (pr-review-toolkit:silent-failure-hunter) to check error handling
   - Fix all HIGH/CRITICAL severity feedback
   - Re-run tests after fixes
3. Update the Progress Tracker in this memory
4. Append any key learnings to the Key Learnings section
5. Present the handoff prompt from the task's Wrap Up section to the user
6. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Clawker needs git worktree support to enable isolated branch work. When a user runs `clawker run --worktree feat-42`, clawker should:
1. Create a git worktree for branch `feat-42` (or use existing)
2. Mount the worktree directory as the container's `/workspace`
3. The container remains unaware it's in a worktree — isolation is handled by the host CLI

**Key constraint:** The container starts instantly and hijacks the terminal. No pre-container messages can be shown. Status information flows via environment variables displayed in Claude Code's statusline.

### Third-Party Dependencies

| Dependency | Version | Purpose |
|------------|---------|---------|
| `github.com/go-git/go-git/v6` | v6.x | Git operations (worktree add/list/remove, branch operations) |
| `github.com/go-git/go-billy/v6` | v6.x | Filesystem abstraction for go-git |

**go-git worktree support status:**
- `add` ✅ Supported via `x/plumbing/worktree` (experimental API)
- `list` ✅ Supported
- `remove` ✅ Supported
- `move/lock/prune` ❌ Not supported (not needed for MVP)

**Fallback strategy:** If go-git's experimental API proves insufficient, use `exec.Command("git", args...)` with explicit argument passing (no shell interpolation) to prevent injection attacks.

### Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  TIER 1 (LEAF): internal/git                                    │
│                                                                 │
│  Imports: stdlib + github.com/go-git/* ONLY                     │
│  Does NOT import: ANY internal packages                         │
│                                                                 │
│  Provides:                                                      │
│    - WorktreeDirProvider interface (dependency inversion)       │
│    - GitManager: high-level SetupWorktree/RemoveWorktree        │
│    - WorktreeManager: low-level go-git primitives               │
│    - Returns errors/results — callers handle logging            │
└────────────────────────────┬────────────────────────────────────┘
                             │ interface implemented by
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 2: internal/config                                        │
│                                                                 │
│  Config.Project() implements git.WorktreeDirProvider            │
│                                                                 │
│  Provides:                                                      │
│    - Project.RootDir() - resolve project via os.Getwd()         │
│    - Project.GetOrCreateWorktreeDir() - directory in CLAWKER_HOME│
│    - Registry-based branch→slug mapping                         │
└────────────────────────────┬────────────────────────────────────┘
                             │ used by commands via Factory
                             ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 4 (COMPOSITE): internal/workspace                         │
│                                                                 │
│  Does NOT import internal/git — receives path from commands     │
│                                                                 │
│  Changes:                                                       │
│    - NO changes needed — WorkDir is just a path                 │
│    - NO new strategy — existing bind/snapshot work unchanged    │
└─────────────────────────────────────────────────────────────────┘
```

### Component Design

#### 1. `internal/git` Package (NEW - Tier 1 Leaf)

**Design principle:** Pure utility package with no internal imports. Returns errors and results; callers handle logging. Configuration passed as parameters, not via config package.

**Architecture:** Facade pattern with domain-specific sub-managers. `GitManager` is the entry point, delegating to specialized managers for each git domain (worktrees, and future: remotes, refs, config, etc.).

**Key design:** GitManager defines a `WorktreeDirProvider` interface that Config.Project() implements. This allows high-level orchestration methods without importing config (dependency inversion).

```go
// git.go - Main facade + WorktreeDirProvider interface
package git

import (
  "fmt"

  gogit "github.com/go-git/go-git/v6"
  "github.com/go-git/go-git/v6/plumbing"
  "github.com/go-git/go-git/v6/storage"
  "github.com/go-git/go-git/v6/storage/filesystem"
  "github.com/go-git/go-git/v6/storage/filesystem/dotgit"
  xworktree "github.com/go-git/go-git/v6/x/plumbing/worktree"
  "github.com/go-git/go-billy/v6/osfs"
)

// WorktreeDirProvider is implemented by Config.Project() to manage
// worktree directories in CLAWKER_HOME. Defined here for dependency inversion.
type WorktreeDirProvider interface {
    GetOrCreateWorktreeDir(name string) (string, error)
    GetWorktreeDir(name string) (string, error)
    DeleteWorktreeDir(name string) error
}

// GitManager is the top-level facade owning the repository handle
// and storage backend.
type GitManager struct {
  repo     *gogit.Repository
  repoRoot string
  storer   storage.Storer

  worktrees *WorktreeManager
}

func NewGitManager(repoPath string) (*GitManager, error) {
  repo, err := git.PlainOpen(repoPath)
  if err != nil {
  return nil, fmt.Errorf("opening repository at %s: %w", repoPath, err)
  }

  // We need access to the storer for the worktree sub-package.
  // PlainOpen uses filesystem storage internally, but the repo
  // exposes Storer() in v6.
  return &GitManager{
      repo:   repo,
      storer: repo.Storer,
    }, nil
  }

func (g *GitManager) Worktrees() *WorktreeManager {
    if g.worktrees == nil {
        g.worktrees = newWorktreeManager(g.repo, g.storer)
    }
    return g.worktrees
}

// === High-level orchestration methods (take WorktreeDirProvider) ===

// SetupWorktree creates or gets a worktree for the given branch.
// Orchestrates: directory creation (via provider) + git worktree add.
// Returns the worktree path ready for mounting.
func (g *GitManager) SetupWorktree(dirs WorktreeDirProvider, branch, base string) (string, error) {
    // 1. Get or create worktree directory in CLAWKER_HOME
    wtPath, err := dirs.GetOrCreateWorktreeDir(branch)
    if err != nil {
        return "", fmt.Errorf("creating worktree directory: %w", err)
    }
    // 2. Create git worktree if needed (resolve base, create branch, etc.)
    // 3. Return path ready for mounting
    return wtPath, nil
}

// RemoveWorktree removes a worktree (git metadata + directory).
func (g *GitManager) RemoveWorktree(dirs WorktreeDirProvider, branch string) error {
    wtPath, err := dirs.GetWorktreeDir(branch)
    if err != nil {
        return fmt.Errorf("looking up worktree: %w", err)
    }
    if err := g.Worktrees().Remove(branch); err != nil {
        return fmt.Errorf("removing git worktree: %w", err)
    }
    if err := dirs.DeleteWorktreeDir(branch); err != nil {
        return fmt.Errorf("deleting worktree directory: %w", err)
    }
    return nil
}

// WorktreeManager manages linked worktrees for a repository. - should go into own file worktree.go
type WorktreeManager struct {
  repo *git.Repository
  wt   *xworktree.Worktree
}

func newWorktreeManager(repo *git.Repository, storer storage.Storer) *WorktreeManager {
  wt, err := xworktree.New(storer)
  if err != nil {
    // This only fails if storer is nil or doesn't implement
    // WorktreeStorer — both are programmer errors at this point.
    panic(fmt.Sprintf("creating worktree manager: %v", err))
  }
  return &WorktreeManager{repo: repo, wt: wt}
}

// Add creates a linked worktree at the given path, checking out the
// specified commit. If commit is zero, HEAD is used.
func (w *WorktreeManager) Add(path, name string, commit plumbing.Hash) error {
  wtFS := osfs.New(path)

  var opts []xworktree.Option
  if !commit.IsZero() {
    opts = append(opts, xworktree.WithCommit(commit))
    }

  if err := w.wt.Add(wtFS, name, opts...); err != nil {
  return fmt.Errorf("adding worktree %q at %s: %w", name, path, err)
  }
  return nil
}

// AddDetached creates a linked worktree with a detached HEAD.
func (w *WorktreeManager) AddDetached(path, name string, commit plumbing.Hash) error {
  wtFS := osfs.New(path)

  opts := []xworktree.Option{xworktree.WithDetachedHead()}
  if !commit.IsZero() {
    opts = append(opts, xworktree.WithCommit(commit))
  }

  if err := w.wt.Add(wtFS, name, opts...); err != nil {
    return fmt.Errorf("adding detached worktree %q at %s: %w", name, path, err)
  }
  return nil
}

// List returns the names of all linked worktrees.
func (w *WorktreeManager) List() ([]string, error) {
  names, err := w.wt.List()
  if err != nil {
    return nil, fmt.Errorf("listing worktrees: %w", err)
  }
  return names, nil
}

// Open opens a linked worktree as a full *git.Repository.
func (w *WorktreeManager) Open(path string) (*git.Repository, error) {
  wtFS := osfs.New(path)
  repo, err := w.wt.Open(wtFS)
  if err != nil {
    return nil, fmt.Errorf("opening worktree at %s: %w", path, err)
  }
  return repo, nil
}

// Remove deletes the worktree metadata (does NOT delete the filesystem).
func (w *WorktreeManager) Remove(name string) error {
  if err := w.wt.Remove(name); err != nil {
    return fmt.Errorf("removing worktree %q: %w", name, err)
  }
  return nil
}
```

##### Tier 1 testing
Direct in-memory repos (unit tests). For testing everything that goes through the go-git API directly (the non-worktree parts of your GitManager, and any future sub-managers for refs, remotes, etc.):
```go
func newTestRepo(t *testing.T) *git.Repository {
    t.Helper()
    repo, err := git.Init(memory.NewStorage(), memfs.New())
    if err != nil {
        t.Fatalf("init test repo: %v", err)
    }

    // Seed with an initial commit so HEAD exists
    wt, _ := repo.Worktree()
    f, _ := wt.Filesystem.Create("README.md")
    f.Write([]byte("test"))
    f.Close()
    wt.Add("README.md")
    wt.Commit("initial commit", &git.CommitOptions{
        Author: &object.Signature{
            Name:  "test",
            Email: "test@test.com",
            When:  time.Now(),
        },
    })

    return repo
}
```
##### Tier 2 Testing
Real temp directories (integration tests for worktrees). Here's the catch with the x/plumbing/worktree package specifically: linked worktrees are inherently a filesystem concept. They create .git files in new directories that point back to the main repo's .git/worktrees/<name> metadata. The memory.NewStorage() backend likely doesn't implement WorktreeStorer — the worktree package needs a real filesystem-backed storer to write those metadata dirs.
So for your WorktreeManager tests, you use t.TempDir():

```go
func newTestRepoOnDisk(t *testing.T) (*git.Repository, string) {
    t.Helper()
    dir := t.TempDir()

    repo, err := git.PlainInit(dir, false)
    if err != nil {
        t.Fatalf("init test repo: %v", err)
    }

    // Seed with initial commit
    wt, _ := repo.Worktree()
    readme := filepath.Join(dir, "README.md")
    os.WriteFile(readme, []byte("test"), 0644)
    wt.Add("README.md")
    wt.Commit("initial commit", &git.CommitOptions{
        Author: &object.Signature{
            Name:  "test",
            Email: "test@test.com",
            When:  time.Now(),
        },
    })

    return repo, dir
}

func TestWorktreeManager_Add(t *testing.T) {
    repo, repoDir := newTestRepoOnDisk(t)

    mgr, err := NewGitManager(repoDir)
    if err != nil {
        t.Fatalf("creating git manager: %v", err)
    }

    wtPath := filepath.Join(t.TempDir(), "feature-branch")

    err = mgr.Worktrees().Add(wtPath, "feature-branch", plumbing.ZeroHash)
    if err != nil {
        t.Fatalf("adding worktree: %v", err)
    }

    // Verify it shows up in list
    names, err := mgr.Worktrees().List()
    if err != nil {
        t.Fatalf("listing worktrees: %v", err)
    }

    if !slices.Contains(names, "feature-branch") {
        t.Errorf("expected worktree 'feature-branch' in list, got %v", names)
    }
}

func TestWorktreeManager_Remove(t *testing.T) {
    repo, repoDir := newTestRepoOnDisk(t)
    mgr, _ := NewGitManager(repoDir)

    wtPath := filepath.Join(t.TempDir(), "throwaway")
    mgr.Worktrees().Add(wtPath, "throwaway", plumbing.ZeroHash)

    err := mgr.Worktrees().Remove("throwaway")
    if err != nil {
        t.Fatalf("removing worktree: %v", err)
    }

    names, _ := mgr.Worktrees().List()
    if slices.Contains(names, "throwaway") {
        t.Errorf("worktree 'throwaway' should have been removed")
    }
}
```

##### Mocking the manager in higher layers

```go
// In whatever package needs to consume worktree operations
type WorktreeProvider interface {
    Add(path, name string, commit plumbing.Hash) error
    List() ([]string, error)
    Remove(name string) error
    Open(path string) (*git.Repository, error)
}
```
Hand-rolled Fake for `internal/go/gotest`

```go
type fakeWorktreeProvider struct {
    worktrees map[string]string // name -> path
    addErr    error
    removeErr error
}

func (f *fakeWorktreeProvider) Add(path, name string, _ plumbing.Hash) error {
    if f.addErr != nil {
        return f.addErr
    }
    f.worktrees[name] = path
    return nil
}

func (f *fakeWorktreeProvider) List() ([]string, error) {
    names := make([]string, 0, len(f.worktrees))
    for name := range f.worktrees {
        names = append(names, name)
    }
    return names, nil
}
// ... etc
```

#### 2. Config.Project() — Project Resolution + Worktree Directory Management

**Key architectural changes:**
1. **Remove `--workdir` global flag** from root command
2. **Remove `WorkDir` from Factory** entirely — no longer an option or property
3. **Config.Project() replaces Resolution as public API** — single entry point for project info
4. **Registry-based branch→dir mapping** for robust worktree directory management
5. **GitManager orchestrates worktree setup** — commands make single call to GitManager

**Config.Project() API:**
```go
// internal/config/config.go
type Config struct {
    // ... existing fields ...
    project     *Project
    projectOnce sync.Once
}

func (c *Config) Project() *Project  // Lazily initialized

// internal/config/project.go - NEW FILE
type Project struct {
    entry    *ProjectEntry  // from registry
    registry *Registry
    // internal state for resolution
}

// Core project info
func (p *Project) Name() string                      // project name/slug
func (p *Project) RootDir() (string, error)          // resolve via os.Getwd() + registry, "" if orphaned

// Worktree directory management (registry-based mapping)
func (p *Project) CreateWorktreeDir(name string) (string, error)       // errors if exists
func (p *Project) GetWorktreeDir(name string) (string, error)          // errors if not exists  
func (p *Project) GetOrCreateWorktreeDir(name string) (string, error)  // get or create
func (p *Project) ListWorktreeDirs() ([]WorktreeDirInfo, error)
func (p *Project) DeleteWorktreeDir(name string) error

// WorktreeDirInfo for list results
type WorktreeDirInfo struct {
    Name   string  // original branch name
    Path   string  // filesystem path
    Slug   string  // slugified directory name
}
```

**Resolution logic (internal to RootDir):**
1. Call `os.Getwd()` internally to get current directory
2. Look up project in registry (existing Resolution logic)
3. If project found → return project root
4. If no project found (orphaned) → return "", nil (empty string signals orphaned)
5. Errors only on unexpected failures (registry corruption, fs errors)

**Worktree directory structure + registry mapping:**
```
$CLAWKER_HOME/
└── projects/
    └── <project-slug>/
        └── worktrees/
            ├── feat-42/           # Worktree directory
            └── feature-foo-bar/   # Slugified from feature/foo/bar
```

Registry stores branch→slug mapping for robustness:
```yaml
# ~/.local/clawker/projects.yaml
projects:
  my-app:
    name: "my-app"
    root: "/Users/dev/my-app"
    worktrees:                    # NEW: branch→slug mapping
      "feature/foo": "feature-foo"
      "feature/bar/baz": "feature-bar-baz"
```

**GitManager in Factory:**
```go
// internal/cmdutil/factory.go
type Factory struct {
    // ... existing fields ...
    GitManager func() (*git.GitManager, error)  // NEW: lazy closure (Once captured in closure)
}

// internal/cmd/factory/default.go
func newGitManager(f *cmdutil.Factory) func() (*git.GitManager, error) {
    var once sync.Once  // Once captured in closure, NOT stored in Factory struct
    var mgr *git.GitManager
    var err error
    return func() (*git.GitManager, error) {
        once.Do(func() {
            projectRoot, rootErr := f.Config().Project().RootDir()
            if rootErr != nil {
                err = rootErr
                return
            }
            if projectRoot == "" {
                err = fmt.Errorf("not in a registered project directory")
                return
            }
            mgr, err = git.NewGitManager(projectRoot)
        })
        return mgr, err
    }
}
```

**Command flow (simplified):**
```go
// 1. Get project
project := f.Config().Project()

// 2. Determine workdir based on --worktree flag
var workDir string

if worktreeFlag != "" {
    // Parse flag and set up worktree
    branch, base, err := cmdutil.ParseWorktreeFlag(worktreeFlag)
    if err != nil {
        return err
    }
    gitMgr, err := f.GitManager()
    if err != nil {
        return err
    }
    workDir, err = gitMgr.SetupWorktree(project, branch, base)
    if err != nil {
        return err
    }
} else {
    // Use project root (or cwd if orphaned)
    workDir, err = project.RootDir()
    if err != nil {
        return err
    }
    if workDir == "" {
        workDir, _ = os.Getwd()  // orphaned fallback
    }
}

// 3. Pass to workspace for mounting (just a path - workspace doesn't care if it's a worktree)
setupCfg := workspace.SetupMountsConfig{
    WorkDir: workDir,
    // ...
}
```

**Workspace setup stays minimal:**
```go
// internal/workspace/setup.go - UNCHANGED
type SetupMountsConfig struct {
    ModeOverride string          // unchanged
    Config       *config.Project // unchanged (YAML schema, not new Project type)
    AgentName    string          // unchanged
    WorkDir      string          // path from GitManager.SetupWorktree() or Project().RootDir()
}

// NO new fields needed — WorkDir is just a path, workspace doesn't care if it's a worktree
// NO new WorktreeStrategy — existing strategies work unchanged
```

#### 3. Config Schema — NO CHANGES TO MODE

**Important:** `--worktree` and `--mode` are orthogonal:
- `--mode` = HOW the workspace is mounted (bind, snapshot)
- `--worktree` = WHAT workspace source to mount (project root vs git worktree)

```go
// internal/config/schema.go — NO CHANGES
const (
    ModeBind     Mode = "bind"     // unchanged
    ModeSnapshot Mode = "snapshot" // unchanged
)
// NO ModeWorktree — worktree affects source path, not mount type
```

**Combinations:**
| Command | Source | Mount Type |
|---------|--------|------------|
| `clawker run` | project root | bind |
| `clawker run --mode snapshot` | project root | snapshot |
| `clawker run --worktree feat` | worktree path | bind |
| `clawker run --worktree feat --mode snapshot` | worktree path | snapshot |

#### 4. CLI Flag Design

```bash
# Basic usage
clawker run --worktree              # Auto-generate branch name from HEAD
clawker run --worktree feat-42      # Use/create branch feat-42
clawker run --worktree feat-42:main # Create feat-42 from main

# Compose with mode (worktree is source, mode affects mount type)
clawker run --worktree feat-42 --mode snapshot  # Worktree copied to volume
```

**Parsing logic for `--worktree [branch[:base]]`:**
- No value → auto-generate branch name `clawker-<agent>-<timestamp>`
- `branch` only → use existing or create from HEAD
- `branch:base` → create branch from specified base (error if branch exists)j
- this must parsing should be done by a shared lightweight utility created in cmdutil

#### 5. Worktree Directory Structure

```
$CLAWKER_HOME/
└── projects/
    └── <project-slug>/
        └── worktrees/
            ├── feat-42/           # Worktree directory
            │   ├── .git           # File pointing to main repo
            │   └── (working files)
            └── feat-43/
```

#### 6. Environment Variables for Statusline

| Variable | Value | Purpose |
|----------|-------|---------|
| `CLAWKER_PROJECT` | Project slug | Already in statusline.sh, needs to be SET |
| `CLAWKER_AGENT` | Agent name | Already in statusline.sh, needs to be SET |
| `CLAWKER_WORKSPACE_MODE` | `bind`/`snapshot` | Mount type (unchanged meaning) |
| `CLAWKER_WORKSPACE_SOURCE` | host path | NEW - the mounted host directory path |

**Note:** CLAWKER_PROJECT and CLAWKER_AGENT are referenced in statusline.sh but NOT currently set by the container creation flow. This is a bug that will be fixed as part of Task 4. No separate worktree branch env var is needed — Claude Code's statusline already shows the current git branch, which will be the worktree branch.

### Key Files

**New files to create:**
- `internal/git/git.go` - GitManager facade + WorktreeDirProvider interface + high-level methods
- `internal/git/worktree.go` - WorktreeManager (low-level go-git primitives)
- `internal/git/types.go` - WorktreeInfo struct
- `internal/git/git_test.go` - Unit tests
- `internal/git/CLAUDE.md` - Package documentation
- `internal/config/project.go` - Project struct (implements WorktreeDirProvider)
- `internal/cmdutil/worktree.go` - ParseWorktreeFlag() helper
- `internal/cmd/worktree/worktree.go` - Parent command
- `internal/cmd/worktree/list/list.go` - List subcommand
- `internal/cmd/worktree/remove/remove.go` - Remove subcommand
- `.claude/rules/git.md` - Package-specific rules

**Files to modify:**
- `internal/cmd/root/root.go` - Remove --workdir global flag
- `internal/cmdutil/factory.go` - Remove WorkDir field, add GitManager field
- `internal/cmd/factory/default.go` - Remove workdir init, add GitManager closure
- `internal/config/config.go` - Add Project() accessor method
- `internal/config/registry.go` - Add worktrees map to project entry schema
- `internal/workspace/setup.go` - No changes needed (WorkDir is just a path)
- `internal/docker/env.go` - Add Project/Agent/Mode to RuntimeEnvOpts
- `internal/docker/labels.go` - Add LabelWorkspaceMode, LabelWorkspaceSource
- `internal/cmd/container/run/run.go` - Add --worktree flag, use GitManager.SetupWorktree()
- `internal/cmd/container/create/create.go` - Add --worktree flag
- `internal/cmd/container/opts/opts.go` - Add Worktree fields to ContainerOpts
- `internal/bundler/assets/statusline.sh` - Add mode indicator display
- `internal/clawker/cmd.go` - Register worktree command
- `go.mod` / `go.sum` - Add go-git dependencies

**NOT modified:**
- `internal/config/schema.go` - NO ModeWorktree (worktree is orthogonal to mode)
- `internal/workspace/strategy.go` - NO changes (existing strategies work unchanged)

### Design Patterns

1. **Facade Pattern** (git package) - GitManager with domain-specific sub-managers (Worktree, future: Remotes, Refs)
2. **Facade Pattern** (config package) - Config.Project() hides resolution + worktree dir complexity
3. **Dependency Inversion** - GitManager defines WorktreeDirProvider interface, Config.Project() implements it
4. **Strategy Pattern** (existing, unchanged) - bind/snapshot strategies work as-is
5. **Factory Closure Pattern** - GitManager initialized lazily via factory closure like other deps
6. **Leaf Package Rules** - git package imports only stdlib + go-git, NO internal packages
7. **Registry-based Mapping** - branch→slug mapping stored in projects.yaml for robustness
8. **exec.Command Safety** - If shelling out, use explicit args, never string interpolation
9. **Colon Syntax for Flags** - `--worktree branch:base` follows Docker `image:tag` convention
10. **Orthogonal Flags** - `--worktree` (source) and `--mode` (mount type) are independent

### Testing Strategy

#### Unit Tests (no Docker)
- `internal/git/*_test.go` - GitManager/WorktreeManager with temp git repos
- `internal/config/project_test.go` - Project.RootDir() and worktree dir methods, temp dirs

#### Integration Tests (Docker required)
- `test/commands/worktree_test.go` - Worktree commands against real git repos
- `test/cli/worktree/*.txtar` - Testscript CLI workflows

#### Test Fixtures
- Create test git repositories with pre-existing branches
- Mock go-git interfaces for unit testing git package
- Use `harness.NewHarness` for command tests

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package
- `internal/git` is **Tier 1 (Leaf)** — imports only stdlib + go-git, NO internal packages
- git package returns errors/results — callers (workspace, commands) handle logging
- Never use `exec.Command("sh", "-c", ...)` — always explicit args
- Worktree paths go in `$CLAWKER_HOME/projects/<project>/worktrees/`

---

## Task 1: Create `internal/git` package foundation

**Creates:** `internal/git/` package
**Depends on:** None

### Implementation Phase

1. **Add go-git dependencies to go.mod**
   ```bash
   go get github.com/go-git/go-git/v6
   go get github.com/go-git/go-billy/v6
   ```

2. **Create package structure (facade pattern)**
   - `internal/git/git.go` - GitManager facade + WorktreeDirProvider interface
   - `internal/git/worktree.go` - WorktreeManager (low-level go-git primitives)
   - `internal/git/types.go` - WorktreeInfo struct
   - `internal/git/CLAUDE.md` - Package documentation

3. **Define WorktreeDirProvider interface** (for dependency inversion)
   ```go
   type WorktreeDirProvider interface {
       GetOrCreateWorktreeDir(name string) (string, error)
       GetWorktreeDir(name string) (string, error)
       DeleteWorktreeDir(name string) error
   }
   ```
   Config.Project() will implement this in Task 2.

4. **Implement GitManager facade** (leaf package — no internal imports)
   - `NewGitManager(path)` - Use `git.PlainOpen()` which walks up to find repo root
   - Returns wrapped `git.ErrRepositoryNotExists` if path is not in a git repository
   - `RepoRoot()`, `Repository()` - Core accessors
   - `Worktrees()` - Lazy accessor for low-level worktree sub-manager

5. **Implement high-level orchestration methods** (take WorktreeDirProvider)
   - `SetupWorktree(dirs, branch, base)` - Get/create dir + git worktree add, return path
   - `RemoveWorktree(dirs, branch)` - Remove git worktree + delete directory
   - `ListWorktrees(dirs)` - List all worktrees with info

6. **Implement WorktreeManager sub-manager** (low-level go-git primitives)
   - `Add(path, name, commit)` - Create worktree at path
   - `List()` - List worktree names
   - `Remove(name)` - Remove worktree metadata
   - `Open(path)` - Open worktree as Repository

7. **Write unit tests**
   - Test GitManager creation from various paths within repo
   - Test GitManager returns wrapped `git.ErrRepositoryNotExists` for non-git directories
   - Test WorktreeManager operations with temp git repos (Tier 2 - real filesystem)
   - Test high-level methods with mock WorktreeDirProvider
   - Test error cases (not a git repo, branch already exists, etc.)

8. **Create `.claude/rules/git.md`** with package-specific rules

### Acceptance Criteria

```bash
# Tests pass
go test ./internal/git/... -v

# Package compiles with correct imports
go build ./internal/git/...

# Verify tier compliance - LEAF package, NO internal imports allowed
go list -f '{{.Imports}}' ./internal/git | grep "github.com/schmitthub/clawker/internal" && echo "FAIL: leaf package must not import internal packages" || echo "PASS: imports OK (leaf tier)"
```

### Wrap Up

1. **Run acceptance criteria** — all tests must pass
2. **Code Review Gate** (MANDATORY before completion):
   - Launch `code-reviewer` agent to review all new/modified files
   - Launch `silent-failure-hunter` agent to check error handling
   - Address all HIGH/CRITICAL feedback before proceeding
   - Re-run tests after fixes
3. Update Progress Tracker: Task 1 -> `complete`
4. Append key learnings (go-git API quirks, any fallbacks needed)
5. **STOP.** Do not proceed to Task 2. Present this handoff prompt:

> **Next agent prompt:** "Continue the git worktree support initiative. Read the Serena memory `git-worktree-support-initiative` — Task 1 is complete. Begin Task 2: Add Config.Project() with worktree directory management."

---

## Task 2: Add Config.Project() with worktree directory management

**Creates/modifies:** Config.Project() accessor, project.go, registry schema
**Depends on:** Task 1

### Context

This task creates `Config.Project()` as the public API for project information and worktree directory management. It replaces `Resolution` as the external API. Config.Project() implements `git.WorktreeDirProvider` interface, enabling GitManager to orchestrate worktree setup.

### Implementation Phase

1. **Remove --workdir global flag and Factory.WorkDir**
   - `internal/cmd/root/root.go` - Remove `--workdir` flag
   - `internal/cmdutil/factory.go` - Remove `WorkDir` field entirely
   - `internal/cmd/factory/default.go` - Remove workdir initialization

2. **Create Config.Project() accessor**
   - `internal/config/config.go` - Add `Project() *Project` method (lazy initialized)
   - `internal/config/project.go` - NEW FILE with Project struct

3. **Implement Project struct**
   ```go
   type Project struct {
       entry    *ProjectEntry
       registry *Registry
   }
   
   // Core project info
   func (p *Project) Name() string
   func (p *Project) RootDir() (string, error)  // "" if orphaned, errors on unexpected failures
   
   // Worktree directory management (implements git.WorktreeDirProvider)
   func (p *Project) CreateWorktreeDir(name string) (string, error)
   func (p *Project) GetWorktreeDir(name string) (string, error)
   func (p *Project) GetOrCreateWorktreeDir(name string) (string, error)
   func (p *Project) ListWorktreeDirs() ([]WorktreeDirInfo, error)
   func (p *Project) DeleteWorktreeDir(name string) error
   ```

4. **Add registry-based branch→dir mapping**
   - Update `internal/config/registry.go` to support worktrees map per project
   - Slugify branch names deterministically for filesystem safety
   - Store mapping in `~/.local/clawker/projects.yaml`:
     ```yaml
     projects:
       my-app:
         name: "my-app"
         root: "/path/to/project"
         worktrees:
           "feature/foo": "feature-foo"
     ```

5. **Migrate all callers**
   - Search for `WorkDir()` calls throughout codebase
   - Change to use `f.Config().Project().RootDir()` for project root
   - For orphaned directories, fall back to `os.Getwd()`
   - This affects multiple command files — audit all `internal/cmd/*/` packages

6. **Write tests**
   - Test Project.RootDir() resolves project via os.Getwd() + registry
   - Test Project.RootDir() returns "" for orphaned directories
   - Test worktree dir methods (Create, Get, GetOrCreate, List, Delete)
   - Test registry persistence of branch→slug mapping
   - Test slugification edge cases (slashes, special chars)

### Acceptance Criteria

```bash
# Config tests pass
go test ./internal/config/... -v

# All commands still work (WorkDir migration complete)
go test ./internal/cmd/... -v

# No remaining references to old WorkDir pattern
! grep -r "\.WorkDir()" internal/cmd/ || echo "No old WorkDir calls found"

# Project implements WorktreeDirProvider
go build ./internal/config/...
```

### Wrap Up

1. **Run acceptance criteria** — all tests must pass
2. **Code Review Gate** (MANDATORY before completion):
   - Launch `code-reviewer` agent to review all new/modified files
   - Launch `silent-failure-hunter` agent to check error handling
   - Address all HIGH/CRITICAL feedback before proceeding
   - Re-run tests after fixes
3. Update Progress Tracker: Task 2 -> `complete`
4. Append key learnings
5. **STOP.** Present this handoff prompt:

> **Next agent prompt:** "Continue the git worktree support initiative. Read the Serena memory `git-worktree-support-initiative` — Task 2 is complete. Begin Task 3: Add GitManager to Factory + --worktree flag."

---

## Task 3: Add GitManager to Factory + --worktree flag

**Creates/modifies:** Factory GitManager, container commands, flag parser
**Depends on:** Task 2

### Implementation Phase

1. **Add GitManager to Factory**
   - `internal/cmdutil/factory.go` - Add `GitManager func() (*git.GitManager, error)` field
   - `internal/cmd/factory/default.go` - Add GitManager closure to constructor
   - Uses `f.Config().Project().RootDir()` to get repo path
   - Errors if not in a registered project or not a git repo
   - Initialize lazily using project root

2. **Create cmdutil.ParseWorktreeFlag()**
   - `internal/cmdutil/worktree.go` - Lightweight flag parser
   - Parse `branch[:base]` syntax → returns (branch, base, error)
   - Handle empty value (auto-generate branch name)
   - Validate branch name format (no shell metacharacters)

3. **No changes to SetupMountsConfig needed**
   - `internal/workspace/setup.go` - WorkDir is just a path
   - Workspace setup doesn't care if the path is a worktree or project root
   - CLAWKER_WORKSPACE_SOURCE env var is set to the workDir path directly

4. **Add worktree fields to ContainerOpts**
   - `internal/cmd/container/opts/opts.go` - Add `Worktree string` field

5. **Update container run command**
   - `internal/cmd/container/run/run.go` - Add `--worktree` flag
   - Conditional flow based on flag:
     ```go
     project := f.Config().Project()
     var workDir string
     
     if worktreeFlag != "" {
         branch, base, err := cmdutil.ParseWorktreeFlag(worktreeFlag)
         if err != nil {
             return err
         }
         gitMgr, err := f.GitManager()
         if err != nil {
             return err
         }
         workDir, err = gitMgr.SetupWorktree(project, branch, base)
         if err != nil {
             return err
         }
     } else {
         workDir, err = project.RootDir()
         if err != nil {
             return err
         }
         if workDir == "" {
             workDir, _ = os.Getwd()
         }
     }
     
     setupCfg.WorkDir = workDir  // just a path, workspace doesn't care if it's a worktree
     ```
   - Existing bind/snapshot logic works unchanged

6. **Update container create command**
   - `internal/cmd/container/create/create.go` - Same changes as run

7. **Add workspace labels**
   - `internal/docker/labels.go` - Add `LabelWorkspaceMode`, `LabelWorkspaceSource`
   - Update label creation in run/create commands

8. **Write tests**
    - Factory GitManager creation and error cases
    - Flag parsing tests for `--worktree` syntax
    - Test workDir path flows through to workspace setup
    - Label and env var verification (CLAWKER_WORKSPACE_SOURCE = host path)
    - Test combinations: `--worktree feat --mode snapshot`

### Acceptance Criteria

```bash
# Config resolver tests pass (worktree resolution)
go test ./internal/config/... -v

# Command tests pass
go test ./internal/cmd/container/run/... -v
go test ./internal/cmd/container/create/... -v

# Workspace tests still pass (no strategy changes)
go test ./internal/workspace/... -v

# Factory tests pass (GitManager closure)
go test ./internal/cmd/factory/... -v

# Flag parses correctly
go test -run TestWorktreeFlag ./internal/cmd/container/... -v
```

### Wrap Up

1. **Run acceptance criteria** — all tests must pass
2. **Code Review Gate** (MANDATORY before completion):
   - Launch `code-reviewer` agent to review all new/modified files
   - Launch `silent-failure-hunter` agent to check error handling
   - Address all HIGH/CRITICAL feedback before proceeding
   - Re-run tests after fixes
3. Update Progress Tracker: Task 3 -> `complete`
4. Append key learnings
5. **STOP.** Present this handoff prompt:

> **Next agent prompt:** "Continue the git worktree support initiative. Read the Serena memory `git-worktree-support-initiative` — Task 3 is complete. Begin Task 4: Implement clawker worktree management commands."

---

## Task 4: Implement `clawker worktree` management commands

**Creates:** `internal/cmd/worktree/` command package
**Depends on:** Task 3

### Implementation Phase

1. **Create worktree parent command**
   - `internal/cmd/worktree/worktree.go` - Parent command with subcommands

2. **Implement list subcommand**
   - `internal/cmd/worktree/list/list.go`
   - Shows all worktrees for current project
   - Displays: branch, path, last modified, container status (if any)

3. **Implement remove subcommand**
   - `internal/cmd/worktree/remove/remove.go`
   - Remove worktree by branch name
   - `--force` flag for worktrees with uncommitted changes
   - `--delete-branch` flag to also delete the branch

4. **Register commands**
   - `internal/clawker/cmd.go` - Add worktree command to root

5. **Write tests**
   - Unit tests for each command
   - Test list output formatting
   - Test remove with various flags

### Acceptance Criteria

```bash
# Command tests pass
go test ./internal/cmd/worktree/... -v

# Commands are registered
go build ./cmd/clawker && ./bin/clawker worktree --help
./bin/clawker worktree list --help
./bin/clawker worktree remove --help
```

### Wrap Up

1. **Run acceptance criteria** — all tests must pass
2. **Code Review Gate** (MANDATORY before completion):
   - Launch `code-reviewer` agent to review all new/modified files
   - Launch `silent-failure-hunter` agent to check error handling
   - Address all HIGH/CRITICAL feedback before proceeding
   - Re-run tests after fixes
3. Update Progress Tracker: Task 4 -> `complete`
4. Append key learnings
5. **STOP.** Present this handoff prompt:

> **Next agent prompt:** "Continue the git worktree support initiative. Read the Serena memory `git-worktree-support-initiative` — Task 4 is complete. Begin Task 5: Add statusline env vars and mode indicators."

---

## Task 5: Add statusline env vars and mode indicators

**Creates/modifies:** Env var injection, statusline script
**Depends on:** Task 3

### Implementation Phase

1. **Add fields to RuntimeEnvOpts**
   - `internal/docker/env.go` - Add `Project`, `Agent`, `WorkspaceMode` fields
   - Update `RuntimeEnv()` to set `CLAWKER_PROJECT`, `CLAWKER_AGENT`, `CLAWKER_WORKSPACE_MODE`

2. **Populate env vars in commands**
   - `internal/cmd/container/run/run.go` - Set all fields (Project, Agent, WorkspaceMode, WorkspaceSource)
   - `internal/cmd/container/create/create.go` - Set all fields
   - WorkspaceSource = the actual host path (workDir variable)

3. **Update statusline script**
   - `internal/bundler/assets/statusline.sh`
   - Add mode indicator: `[snap]` for snapshot mode
   - Git branch already shown by Claude Code statusline — indicates worktree branch automatically

4. **Write tests**
   - Verify env vars are set correctly
   - Test statusline output with different modes

### Acceptance Criteria

```bash
# Env var tests pass
go test -run TestRuntimeEnv ./internal/docker/... -v

# Statusline script handles new vars
# (Manual verification with container)
```

### Wrap Up

1. **Run acceptance criteria** — all tests must pass
2. **Code Review Gate** (MANDATORY before completion):
   - Launch `code-reviewer` agent to review all new/modified files
   - Launch `silent-failure-hunter` agent to check error handling
   - Address all HIGH/CRITICAL feedback before proceeding
   - Re-run tests after fixes
3. Update Progress Tracker: Task 5 -> `complete`
4. Append key learnings
5. **STOP.** Present this handoff prompt:

> **Next agent prompt:** "Continue the git worktree support initiative. Read the Serena memory `git-worktree-support-initiative` — Task 5 is complete. Begin Task 6: Integration tests and documentation."

---

## Task 6: Integration tests and documentation

**Creates/modifies:** Integration tests, documentation files
**Depends on:** Tasks 1-5

### Implementation Phase

1. **Create integration tests**
   - `test/commands/worktree_test.go` - Full worktree workflow tests
   - Test: create container with worktree, verify mount, verify env vars

2. **Create CLI workflow tests**
   - `test/cli/worktree/basic.txtar` - Basic worktree operations
   - Test: run with --worktree, list, remove

3. **Update documentation**
   - Update `CLAUDE.md` with git package in structure
   - Update `.claude/memories/ARCHITECTURE.md` with git package in tier diagram
   - Create `internal/git/CLAUDE.md` (if not done in Task 1)
   - Update `internal/workspace/CLAUDE.md` to note worktree paths are just paths (no special strategy)

4. **Run full test suite**
   - `make test` - All unit tests
   - `go test ./test/commands/... -v` - Command integration
   - `go test ./test/cli/... -v` - CLI workflows

### Acceptance Criteria

```bash
# All tests pass
make test
go test ./test/commands/... -v -timeout 10m
go test ./test/cli/... -v -timeout 15m

# Documentation updated
grep -q "internal/git" CLAUDE.md
```

### Wrap Up

1. **Run acceptance criteria** — all tests must pass
2. **Code Review Gate** (MANDATORY before completion):
   - Launch `code-reviewer` agent to review all new/modified files
   - Launch `silent-failure-hunter` agent to check error handling
   - Address all HIGH/CRITICAL feedback before proceeding
   - Re-run tests after fixes
3. Update Progress Tracker: Task 6 -> `complete`
4. Append final key learnings
5. **DONE.** Present completion summary:

> **Initiative complete.** Git worktree support is implemented. Summary:
> - New `internal/git` package (Tier 1 Leaf) for worktree management
> - New `--worktree [branch[:base]]` flag on container run/create (orthogonal to --mode)
> - New `clawker worktree list|remove` commands
> - Statusline shows workspace mode and worktree branch
> - Existing bind/snapshot strategies work unchanged
>
> Next steps: Create PR, run `make test-all`, manual testing with real git repos.

---

## Blast Radius Summary

### New Files (13)
| Path | Lines (est) | Notes |
|------|-------------|-------|
| `internal/git/git.go` | 150 | GitManager facade + WorktreeDirProvider interface + high-level methods |
| `internal/git/worktree.go` | 100 | WorktreeManager (low-level go-git primitives) |
| `internal/git/types.go` | 30 | WorktreeInfo struct |
| `internal/git/git_test.go` | 200 | Unit tests with temp repos + mock provider |
| `internal/git/CLAUDE.md` | 60 | Package docs (Tier 1 Leaf, dependency inversion) |
| `internal/config/project.go` | 150 | Project struct implementing WorktreeDirProvider |
| `internal/cmdutil/worktree.go` | 50 | ParseWorktreeFlag() helper |
| `internal/cmd/worktree/worktree.go` | 40 | Parent command |
| `internal/cmd/worktree/list/list.go` | 100 | List subcommand |
| `internal/cmd/worktree/remove/remove.go` | 100 | Remove subcommand |
| `.claude/rules/git.md` | 30 | Package rules |
| `test/commands/worktree_test.go` | 150 | Integration tests |
| `test/cli/worktree/basic.txtar` | 80 | CLI workflow tests |
| **Total new** | **~1240** |

### Modified Files (14+)
| Path | Changes |
|------|---------|
| `go.mod` | Add go-git deps |
| `go.sum` | Dependency checksums |
| `internal/cmd/root/root.go` | Remove --workdir global flag |
| `internal/cmdutil/factory.go` | Remove WorkDir field, add GitManager field |
| `internal/cmd/factory/default.go` | Remove workdir init, add GitManager closure |
| `internal/config/config.go` | Add Project() accessor method |
| `internal/config/registry.go` | Add worktrees map to project entry |
| `internal/workspace/setup.go` | No changes needed (WorkDir is just a path) |
| `internal/docker/env.go` | Add env fields |
| `internal/docker/labels.go` | Add LabelWorkspaceMode, LabelWorkspaceSource (host path) |
| `internal/cmd/container/run/run.go` | --worktree flag, use GitManager.SetupWorktree() |
| `internal/cmd/container/create/create.go` | --worktree flag |
| `internal/cmd/container/opts/opts.go` | Worktree fields, remove WorkDir reference |
| `internal/bundler/assets/statusline.sh` | Mode indicator |
| `internal/clawker/cmd.go` | Register worktree cmd |
| **All commands using WorkDir** | Migrate to Project().RootDir() |

**WorkDir migration pattern:**
```go
// BEFORE: Factory provides WorkDir directly
wd, err := opts.WorkDir()

// AFTER: Project provides RootDir, GitManager orchestrates worktree
projectRoot, err := f.Config().Project().RootDir()
if projectRoot == "" {
    projectRoot, _ = os.Getwd()  // orphaned fallback
}

// For worktree: GitManager orchestrates
wtPath, err := gitMgr.SetupWorktree(project, branch, base)
```

Note: Search codebase for `WorkDir()` calls to identify all affected files.

### NOT Modified (key exclusions)
| Path | Reason |
|------|--------|
| `internal/config/schema.go` | No ModeWorktree — worktree orthogonal to mode |
| `internal/workspace/strategy.go` | No new strategy — existing bind/snapshot unchanged |

---

## Manual Testing Checklist

After implementation, verify these scenarios work correctly:

### Setup
```bash
# Build clawker
go build -o bin/clawker ./cmd/clawker

# Create a test project
mkdir /tmp/worktree-test && cd /tmp/worktree-test
git init && git commit --allow-empty -m "init"
clawker init
```

### Basic Worktree Operations
```bash
# Run with worktree (should create worktree and container)
clawker run --rm -it --agent test-wt --worktree feat-test @ ls -la
# Verify: /workspace contains worktree files, not main repo

# Start agent with worktree
clawker start -ia --agent test-wt2 --worktree feat-test2

# Verify env vars in container
clawker exec --agent test-wt2 env | grep CLAWKER
# Expected: CLAWKER_PROJECT, CLAWKER_AGENT, CLAWKER_WORKSPACE_MODE=bind, CLAWKER_WORKSPACE_SOURCE=<host path to worktree>

# Stop and remove
clawker stop --agent test-wt2
clawker rm --agent test-wt2
```

### Worktree + Mode Combinations
```bash
# Worktree with bind mode (default)
clawker run --rm -it --agent test-bind --worktree feat-bind @ ls -la

# Worktree with snapshot mode
clawker run --rm -it --agent test-snap --worktree feat-snap --mode snapshot @ ls -la
```

### Worktree Management Commands
```bash
# List worktrees
clawker worktree list
# Expected: shows feat-test, feat-test2, feat-bind, feat-snap

# Remove worktree
clawker worktree remove feat-test

# Verify removal
clawker worktree list
# Expected: feat-test no longer listed
```

### Colon Syntax
```bash
# Create new branch from main
clawker run --rm -it --agent test-from --worktree new-feat:main @ git branch
# Expected: on branch new-feat, created from main
```

### Error Cases
```bash
# Non-git directory
cd /tmp && mkdir not-git && cd not-git
clawker run --worktree test @ ls
# Expected: error about not being in a git repository

# Invalid branch name
clawker run --worktree "feat;rm -rf /" @ ls
# Expected: error about invalid branch name (no injection)
```

### Statusline Verification
```bash
# Start interactive agent
clawker start -ia --agent test-status --worktree feat-status

# Inside container, verify statusline shows:
# - Project name
# - Agent name
# - [snap] indicator if snapshot mode (omitted for bind)
# - Git branch shown by Claude Code — should be feat-status (indicates worktree)
```
