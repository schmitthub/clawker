# Guide: Testing Git Worktree Operations with In-Memory Storage

This guide shows how to create a testable `GitManager` for worktree operations using go-git's in-memory storage pattern, designed for Factory-based DI approaches.

## Core Concept: Real Implementation, Different Storage

go-git doesn't require mocking. Both production and test use **real go-git code** - the difference is the storage backend:

- **Production**: Filesystem storage → persists to disk
- **Tests**: Filesystem storage over memfs → real git operations, in-memory speed

The key insight:

```
┌─────────────────────────────────────────────────────────┐
│  filesystem.NewStorage(memfs.New(), cache)              │
├─────────────────────────────────────────────────────────┤
│  ✓ Filesystem storage semantics                         │
│    - Supports .git/worktrees/ directory structure       │
│    - Required for xworktree.Worktree operations         │
│                                                         │
│  ✓ In-memory performance                                │
│    - No disk I/O                                        │
│    - Fast test execution                                │
│                                                         │
│  ✓ Test isolation                                       │
│    - Each test gets fresh memory                        │
│    - No cleanup needed                                  │
│    - Parallel-safe                                      │
└─────────────────────────────────────────────────────────┘
```

This is different from `memory.NewStorage()` which doesn't support the directory structure needed for linked worktrees.

---

## DI Pattern Overview

```go
// Your existing pattern
type Factory struct {
    GitManager  GitManager
    Logger      Logger
    // ... other dependencies
}

// Production setup
func NewProductionFactory() *Factory {
    return &Factory{
        GitManager: NewGoGitManager("/path/to/repo"),
        // ...
    }
}

// Test setup
func NewTestFactory() *Factory {
    return &Factory{
        GitManager: NewInMemoryGitManager(),
        // ...
    }
}

// Caller doesn't know or care which implementation
func NewWorktreeService(f *Factory) *WorktreeService {
    return &WorktreeService{git: f.GitManager}
}
```

---

## GitManager Interface

Define an interface matching your worktree operations:

```go
// git/manager.go
package git

import "github.com/go-git/go-billy/v6"

// GitManager handles git worktree operations
type GitManager interface {
    // Worktree management
    AddWorktree(name string, opts WorktreeOptions) (WorktreeHandle, error)
    RemoveWorktree(name string) error
    ListWorktrees() ([]string, error)

    // Access worktrees
    OpenWorktree(name string) (WorktreeHandle, error)

    // Repository info
    GetCurrentCommit() (string, error)
    GetBranch() (string, error)
}

type WorktreeOptions struct {
    Commit   string // Commit hash to checkout
    Branch   string // Branch name (creates if doesn't exist)
    Detached bool   // Create with detached HEAD
}

type WorktreeHandle interface {
    // Filesystem access for the worktree
    Filesystem() billy.Filesystem

    // Git operations within this worktree
    Commit(message string, author, email string) (string, error)
    Status() (WorktreeStatus, error)
    Checkout(ref string) error
}

type WorktreeStatus struct {
    IsClean   bool
    Modified  []string
    Untracked []string
}
```

---

## Production Implementation

```go
// git/manager_gogit.go
package git

import (
    "path/filepath"

    "github.com/go-git/go-billy/v6"
    "github.com/go-git/go-billy/v6/osfs"
    gogit "github.com/go-git/go-git/v6"
    "github.com/go-git/go-git/v6/plumbing"
    "github.com/go-git/go-git/v6/plumbing/cache"
    "github.com/go-git/go-git/v6/plumbing/object"
    "github.com/go-git/go-git/v6/storage/filesystem"
    xworktree "github.com/go-git/go-git/v6/x/plumbing/worktree"
)

type GoGitManager struct {
    repo            *gogit.Repository
    worktreeMgr     *xworktree.Worktree
    worktreeBaseDir string
    worktrees       map[string]*goGitWorktreeHandle
}

func NewGoGitManager(repoPath, worktreeBaseDir string) (*GoGitManager, error) {
    dotGitPath := filepath.Join(repoPath, ".git")
    dotGitFS := osfs.New(dotGitPath)
    storer := filesystem.NewStorage(dotGitFS, cache.NewObjectLRUDefault())

    repo, err := gogit.Open(storer, osfs.New(repoPath))
    if err != nil {
        return nil, WrapError(err)
    }

    wtMgr, err := xworktree.New(storer)
    if err != nil {
        return nil, WrapError(err)
    }

    return &GoGitManager{
        repo:            repo,
        worktreeMgr:     wtMgr,
        worktreeBaseDir: worktreeBaseDir,
        worktrees:       make(map[string]*goGitWorktreeHandle),
    }, nil
}

func (g *GoGitManager) AddWorktree(name string, opts WorktreeOptions) (WorktreeHandle, error) {
    wtPath := filepath.Join(g.worktreeBaseDir, name)
    wtFS := osfs.New(wtPath)

    xopts := []xworktree.Option{}
    if opts.Commit != "" {
        xopts = append(xopts, xworktree.WithCommit(plumbing.NewHash(opts.Commit)))
    }
    if opts.Detached {
        xopts = append(xopts, xworktree.WithDetachedHead())
    }

    if err := g.worktreeMgr.Add(wtFS, name, xopts...); err != nil {
        return nil, WrapError(err)
    }

    wtRepo, err := g.worktreeMgr.Open(wtFS)
    if err != nil {
        return nil, WrapError(err)
    }

    handle := &goGitWorktreeHandle{fs: wtFS, repo: wtRepo}
    g.worktrees[name] = handle
    return handle, nil
}

func (g *GoGitManager) RemoveWorktree(name string) error {
    delete(g.worktrees, name)
    return WrapError(g.worktreeMgr.Remove(name))
}

func (g *GoGitManager) ListWorktrees() ([]string, error) {
    names, err := g.worktreeMgr.List()
    return names, WrapError(err)
}

func (g *GoGitManager) OpenWorktree(name string) (WorktreeHandle, error) {
    if h, ok := g.worktrees[name]; ok {
        return h, nil
    }
    // Would need to reconstruct from metadata - simplified here
    return nil, ErrWorktreeNotFound
}

func (g *GoGitManager) GetCurrentCommit() (string, error) {
    head, err := g.repo.Head()
    if err != nil {
        return "", WrapError(err)
    }
    return head.Hash().String(), nil
}

func (g *GoGitManager) GetBranch() (string, error) {
    head, err := g.repo.Head()
    if err != nil {
        return "", WrapError(err)
    }
    return head.Name().Short(), nil
}

// WorktreeHandle implementation
type goGitWorktreeHandle struct {
    fs   billy.Filesystem
    repo *gogit.Repository
}

func (h *goGitWorktreeHandle) Filesystem() billy.Filesystem {
    return h.fs
}

func (h *goGitWorktreeHandle) Commit(message, author, email string) (string, error) {
    wt, err := h.repo.Worktree()
    if err != nil {
        return "", WrapError(err)
    }
    hash, err := wt.Commit(message, &gogit.CommitOptions{
        Author: &object.Signature{Name: author, Email: email},
    })
    return hash.String(), WrapError(err)
}

func (h *goGitWorktreeHandle) Status() (WorktreeStatus, error) {
    wt, err := h.repo.Worktree()
    if err != nil {
        return WorktreeStatus{}, WrapError(err)
    }
    status, err := wt.Status()
    if err != nil {
        return WorktreeStatus{}, WrapError(err)
    }

    result := WorktreeStatus{IsClean: status.IsClean()}
    for path, s := range status {
        if s.Worktree == gogit.Untracked {
            result.Untracked = append(result.Untracked, path)
        } else if s.Worktree != gogit.Unmodified {
            result.Modified = append(result.Modified, path)
        }
    }
    return result, nil
}

func (h *goGitWorktreeHandle) Checkout(ref string) error {
    wt, err := h.repo.Worktree()
    if err != nil {
        return WrapError(err)
    }
    return WrapError(wt.Checkout(&gogit.CheckoutOptions{
        Branch: plumbing.NewBranchReferenceName(ref),
    }))
}
```

---

## Test Implementation (In-Memory)

The key insight: use `filesystem.NewStorage(memfs.New(), cache)` to get filesystem storage semantics over an in-memory filesystem.

```go
// git/manager_inmemory.go
package git

import (
    "github.com/go-git/go-billy/v6"
    "github.com/go-git/go-billy/v6/memfs"
    gogit "github.com/go-git/go-git/v6"
    "github.com/go-git/go-git/v6/plumbing"
    "github.com/go-git/go-git/v6/plumbing/cache"
    "github.com/go-git/go-git/v6/plumbing/object"
    "github.com/go-git/go-git/v6/storage/filesystem"
    xworktree "github.com/go-git/go-git/v6/x/plumbing/worktree"
)

// InMemoryGitManager implements GitManager with in-memory storage
// Use this in test Factory instead of production GoGitManager
type InMemoryGitManager struct {
    repo        *gogit.Repository
    worktreeMgr *xworktree.Worktree
    dotGitFS    billy.Filesystem  // .git directory (in memory)
    mainWtFS    billy.Filesystem  // Main worktree (in memory)
    worktrees   map[string]*inMemoryWorktreeHandle
}

func NewInMemoryGitManager() (*InMemoryGitManager, error) {
    dotGitFS := memfs.New()
    mainWtFS := memfs.New()

    // filesystem.NewStorage wrapping memfs = filesystem semantics, memory speed
    storer := filesystem.NewStorage(dotGitFS, cache.NewObjectLRUDefault())

    repo, err := gogit.Init(storer, gogit.WithWorkTree(mainWtFS))
    if err != nil {
        return nil, err
    }

    wtMgr, err := xworktree.New(storer)
    if err != nil {
        return nil, err
    }

    return &InMemoryGitManager{
        repo:        repo,
        worktreeMgr: wtMgr,
        dotGitFS:    dotGitFS,
        mainWtFS:    mainWtFS,
        worktrees:   make(map[string]*inMemoryWorktreeHandle),
    }, nil
}

func (m *InMemoryGitManager) AddWorktree(name string, opts WorktreeOptions) (WorktreeHandle, error) {
    wtFS := memfs.New()  // Each worktree gets its own in-memory FS

    xopts := []xworktree.Option{}
    if opts.Commit != "" {
        xopts = append(xopts, xworktree.WithCommit(plumbing.NewHash(opts.Commit)))
    }
    if opts.Detached {
        xopts = append(xopts, xworktree.WithDetachedHead())
    }

    if err := m.worktreeMgr.Add(wtFS, name, xopts...); err != nil {
        return nil, WrapError(err)
    }

    wtRepo, err := m.worktreeMgr.Open(wtFS)
    if err != nil {
        return nil, WrapError(err)
    }

    handle := &inMemoryWorktreeHandle{fs: wtFS, repo: wtRepo}
    m.worktrees[name] = handle
    return handle, nil
}

func (m *InMemoryGitManager) RemoveWorktree(name string) error {
    delete(m.worktrees, name)
    return WrapError(m.worktreeMgr.Remove(name))
}

func (m *InMemoryGitManager) ListWorktrees() ([]string, error) {
    return m.worktreeMgr.List()
}

func (m *InMemoryGitManager) OpenWorktree(name string) (WorktreeHandle, error) {
    if h, ok := m.worktrees[name]; ok {
        return h, nil
    }
    return nil, ErrWorktreeNotFound
}

func (m *InMemoryGitManager) GetCurrentCommit() (string, error) {
    head, err := m.repo.Head()
    if err != nil {
        return "", WrapError(err)
    }
    return head.Hash().String(), nil
}

func (m *InMemoryGitManager) GetBranch() (string, error) {
    head, err := m.repo.Head()
    if err != nil {
        return "", WrapError(err)
    }
    return head.Name().Short(), nil
}

// ── Test Helpers ─────────────────────────────────────────────────────────────

// CreateInitialCommit sets up a commit so worktrees have something to checkout
func (m *InMemoryGitManager) CreateInitialCommit(filename, content string) (string, error) {
    wt, err := m.repo.Worktree()
    if err != nil {
        return "", err
    }

    f, err := m.mainWtFS.Create(filename)
    if err != nil {
        return "", err
    }
    f.Write([]byte(content))
    f.Close()

    if _, err := wt.Add(filename); err != nil {
        return "", err
    }

    hash, err := wt.Commit("initial commit", &gogit.CommitOptions{
        Author: &object.Signature{Name: "Test", Email: "test@test.com"},
    })
    return hash.String(), err
}

// MainWorktreeFS returns the main repo's worktree filesystem for test setup
func (m *InMemoryGitManager) MainWorktreeFS() billy.Filesystem {
    return m.mainWtFS
}

// GetWorktreeFS returns a worktree's filesystem for test assertions
func (m *InMemoryGitManager) GetWorktreeFS(name string) billy.Filesystem {
    if h, ok := m.worktrees[name]; ok {
        return h.fs
    }
    return nil
}

// ── Worktree Handle ──────────────────────────────────────────────────────────

type inMemoryWorktreeHandle struct {
    fs   billy.Filesystem
    repo *gogit.Repository
}

func (h *inMemoryWorktreeHandle) Filesystem() billy.Filesystem {
    return h.fs
}

func (h *inMemoryWorktreeHandle) Commit(message, author, email string) (string, error) {
    wt, err := h.repo.Worktree()
    if err != nil {
        return "", WrapError(err)
    }
    hash, err := wt.Commit(message, &gogit.CommitOptions{
        Author: &object.Signature{Name: author, Email: email},
    })
    return hash.String(), WrapError(err)
}

func (h *inMemoryWorktreeHandle) Status() (WorktreeStatus, error) {
    wt, err := h.repo.Worktree()
    if err != nil {
        return WorktreeStatus{}, WrapError(err)
    }
    status, err := wt.Status()
    if err != nil {
        return WorktreeStatus{}, WrapError(err)
    }

    result := WorktreeStatus{IsClean: status.IsClean()}
    for path, s := range status {
        if s.Worktree == gogit.Untracked {
            result.Untracked = append(result.Untracked, path)
        } else if s.Worktree != gogit.Unmodified {
            result.Modified = append(result.Modified, path)
        }
    }
    return result, nil
}

func (h *inMemoryWorktreeHandle) Checkout(ref string) error {
    wt, err := h.repo.Worktree()
    if err != nil {
        return WrapError(err)
    }
    return WrapError(wt.Checkout(&gogit.CheckoutOptions{
        Branch: plumbing.NewBranchReferenceName(ref),
    }))
}
```

---

## Domain Errors

```go
// git/errors.go
package git

import (
    "errors"
    "fmt"
    gogit "github.com/go-git/go-git/v6"
    "github.com/go-git/go-git/v6/plumbing"
)

var (
    ErrNotARepository      = errors.New("not a git repository")
    ErrWorktreeNotFound    = errors.New("worktree not found")
    ErrWorktreeExists      = errors.New("worktree already exists")
    ErrDirtyWorktree       = errors.New("worktree has uncommitted changes")
    ErrBranchNotFound      = errors.New("branch not found")
    ErrInvalidWorktreeName = errors.New("invalid worktree name")
)

func WrapError(err error) error {
    if err == nil {
        return nil
    }
    switch {
    case errors.Is(err, gogit.ErrRepositoryNotExists):
        return fmt.Errorf("%w: %v", ErrNotARepository, err)
    case errors.Is(err, gogit.ErrWorktreeNotClean):
        return fmt.Errorf("%w: %v", ErrDirtyWorktree, err)
    case errors.Is(err, gogit.ErrBranchNotFound),
         errors.Is(err, plumbing.ErrReferenceNotFound):
        return fmt.Errorf("%w: %v", ErrBranchNotFound, err)
    default:
        return err
    }
}
```

---

## Using in Your Factory

```go
// factory/factory.go
package factory

import "yourproject/git"

type Factory struct {
    GitManager git.GitManager
    // ... other dependencies
}

// Production factory
func NewFactory(repoPath, worktreeDir string) (*Factory, error) {
    gitMgr, err := git.NewGoGitManager(repoPath, worktreeDir)
    if err != nil {
        return nil, err
    }
    return &Factory{
        GitManager: gitMgr,
    }, nil
}
```

```go
// factory/factory_test.go
package factory

import "yourproject/git"

// TestFactory for use in tests
func NewTestFactory() (*Factory, *git.InMemoryGitManager) {
    gitMgr, _ := git.NewInMemoryGitManager()
    return &Factory{
        GitManager: gitMgr,
    }, gitMgr  // Return manager for test setup/assertions
}
```

---

## Test Examples

```go
// service/builder_test.go
package service

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "yourproject/factory"
    "yourproject/git"
)

func TestBuilder_CreatesWorktreeForBuild(t *testing.T) {
    f, gitMgr := factory.NewTestFactory()

    // Setup: create initial commit
    commitHash, err := gitMgr.CreateInitialCommit("main.go", "package main")
    require.NoError(t, err)

    // Create service with test factory
    builder := NewBuilder(f)

    // Action
    err = builder.StartBuild("build-123", commitHash)
    require.NoError(t, err)

    // Assert: worktree was created
    names, _ := f.GitManager.ListWorktrees()
    assert.Contains(t, names, "build-123")

    // Assert: worktree has correct files
    wtFS := gitMgr.GetWorktreeFS("build-123")
    _, err = wtFS.Stat("main.go")
    assert.NoError(t, err)
}

func TestBuilder_CleansUpWorktreeAfterBuild(t *testing.T) {
    f, gitMgr := factory.NewTestFactory()
    commitHash, _ := gitMgr.CreateInitialCommit("main.go", "package main")

    builder := NewBuilder(f)
    builder.StartBuild("build-456", commitHash)

    // Action
    err := builder.FinishBuild("build-456")
    require.NoError(t, err)

    // Assert: worktree was removed
    names, _ := f.GitManager.ListWorktrees()
    assert.NotContains(t, names, "build-456")
}

func TestBuilder_FailsOnDirtyWorktree(t *testing.T) {
    f, gitMgr := factory.NewTestFactory()
    commitHash, _ := gitMgr.CreateInitialCommit("main.go", "package main")

    builder := NewBuilder(f)
    wt, _ := builder.StartBuild("build-789", commitHash)

    // Add uncommitted changes
    wtFS := wt.Filesystem()
    file, _ := wtFS.Create("dirty.txt")
    file.Write([]byte("uncommitted"))
    file.Close()

    // Action: try to finish with dirty worktree
    err := builder.FinishBuild("build-789")

    // Assert
    assert.ErrorIs(t, err, git.ErrDirtyWorktree)
}

func TestBuilder_WorktreesAreIsolated(t *testing.T) {
    f, gitMgr := factory.NewTestFactory()
    commitHash, _ := gitMgr.CreateInitialCommit("main.go", "package main")

    builder := NewBuilder(f)

    // Create two worktrees
    wt1, _ := builder.StartBuild("build-a", commitHash)
    wt2, _ := builder.StartBuild("build-b", commitHash)

    // Modify one worktree
    file, _ := wt1.Filesystem().Create("only-in-a.txt")
    file.Write([]byte("a"))
    file.Close()

    // Assert: other worktree doesn't have the file
    _, err := wt2.Filesystem().Stat("only-in-a.txt")
    assert.True(t, errors.Is(err, os.ErrNotExist))
}
```

---

## Key Files in go-git for Reference

local fs  location: `/Users/andrew/Code/vendor/go-git/go-git`

| File | Purpose |
|------|---------|
| `x/plumbing/worktree/worktree.go` | Linked worktree manager |
| `x/plumbing/worktree/worktree_options.go` | WithCommit, WithDetachedHead options |
| `x/plumbing/worktree/worktree_test.go` | Test patterns with memfs |
| `x/storage/worktree_storer.go` | WorktreeStorer interface |
| `storage/filesystem/storage.go` | Filesystem storage implementation |
| `repository.go:40-82` | Repository error definitions |
| `worktree.go:37-52` | Worktree error definitions |
