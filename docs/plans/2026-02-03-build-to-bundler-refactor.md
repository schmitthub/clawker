# Build → Bundler Package Refactor

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rename `internal/build` to `internal/bundler`, extract hostproxy container-side components to `internal/hostproxy/internals/`, and update all references across the codebase.

**Architecture:** `internal/bundler` remains a middle-tier package (Dockerfile rendering, build context assembly, content hashing, version management). Hostproxy container-side scripts/binaries (`ssh-agent-proxy`, `callback-forwarder`, `host-open`, `git-credential-clawker`) move to `internal/hostproxy/internals/` with proper `cmd/` subdirectories for Go binaries. `hostproxy/internals` is a leaf package (stdlib + embed only) that exports embedded byte vars. `bundler` imports `hostproxy/internals` for those assets — dependency arrow is `bundler → hostproxy/internals`, never reverse.

**Tech Stack:** Go (module path rewriting), `go:embed`, shell scripts, `git mv`

---

## Blast Radius Inventory

### Go files importing `internal/build`

| File | Import Style | Symbols Used |
|------|-------------|-------------|
| `internal/docker/builder.go` | `build` | `NewProjectGenerator`, `ContentHash`, `CreateBuildContextFromDir` |
| `internal/docker/builder_test.go` | `build` | `NewProjectGenerator`, `ContentHash` |
| `internal/docker/defaults.go` | `build` | `NewVersionsManager`, `NewDockerfileManager`, `CreateBuildContextFromDir` |
| `internal/cmd/generate/generate.go` | `build` | `NewDockerfileManager`, `LoadVersionsFile`, `SaveVersionsFile`, `NewVersionsManager` |
| `internal/cmd/generate/generate.go` | `registry` | `internal/build/registry` |
| `internal/cmd/init/init.go` | `intbuild` | `DefaultFlavorOptions` |
| `internal/cmd/init/init_test.go` | `intbuild` | `DefaultFlavorOptions` |
| `internal/cmd/project/init/init.go` | `intbuild` | `FlavorToImage`, `DefaultFlavorOptions` |
| `internal/cmd/container/create/create.go` | `intbuild` | `DefaultFlavorOptions` |
| `internal/cmd/container/run/run.go` | `intbuild` | `DefaultFlavorOptions` |

### Internal cross-references (within build package)

| File | Imports |
|------|---------|
| `internal/build/errors.go` | `internal/build/registry` |
| `internal/build/versions.go` | `internal/build/registry`, `internal/build/semver` |
| `internal/build/dockerfile.go` | `internal/build/registry` |
| `internal/build/registry/types.go` | `internal/build/semver` |

### Test harness references (hardcoded paths)

| File | Lines | Reference |
|------|-------|-----------|
| `test/harness/client.go` | 270 | `filepath.Join(projectRoot, "internal", "build", "templates")` |
| `test/harness/hash.go` | 40, 61 | `filepath.Join(rootDir, "internal", "build", "templates")` |
| `test/harness/hash_test.go` | 65, 88, 111, 140 | `filepath.Join(tmpDir, "internal", "build", "templates")` |

### Documentation references

| File | Description |
|------|------------|
| `CLAUDE.md` | Repository structure, key concepts, build commands |
| `.claude/memories/ARCHITECTURE.md` | Package DAG, tier classification, import rules |
| `.claude/memories/DESIGN.md` | Line 255 reference to `internal/build/` |
| `.claude/memories/TESTING-REFERENCE.md` | Lines 348, 601, 626, 635 — `BuildLightImage` docs |
| `.claude/rules/dependency-placement.md` | Line 44 — tier classification |
| `.claude/skills/audit-memory/REFERENCE.md` | Line 12 — package list |
| `internal/build/CLAUDE.md` | Entire file — becomes `internal/bundler/CLAUDE.md` |
| `internal/docker/CLAUDE.md` | Line 125 — dependency note |
| `internal/cmdutil/CLAUDE.md` | Line 7 — build utilities reference |
| `test/CLAUDE.md` | Line 101 — `BuildLightImage` doc |
| `docs/plans/2026-02-02-build-docker-collapse.md` | Historical (no update needed) |
| `docs/plans/2026-02-02-callback-forwarder-go-binary.md` | Historical (no update needed) |

### Embedded assets (current `internal/build/templates/`)

| File | Destination | Reason |
|------|------------|--------|
| `Dockerfile.tmpl` | `internal/bundler/assets/` | Lifecycle — Dockerfile rendering |
| `entrypoint.sh` | `internal/bundler/assets/` | Lifecycle — container init |
| `init-firewall.sh` | `internal/bundler/assets/` | Lifecycle — network security |
| `statusline.sh` | `internal/bundler/assets/` | Claude Code config asset |
| `claude-settings.json` | `internal/bundler/assets/` | Claude Code config asset |
| `ssh-agent-proxy.go` | `internal/hostproxy/internals/cmd/ssh-agent-proxy/main.go` | Hostproxy client |
| `callback-forwarder.go` | `internal/hostproxy/internals/cmd/callback-forwarder/main.go` | Hostproxy client |
| `host-open.sh` | `internal/hostproxy/internals/` | Hostproxy client |
| `git-credential-clawker.sh` | `internal/hostproxy/internals/` | Hostproxy client |

---

## Task 1: Create `internal/hostproxy/internals/` with embedded assets

**Files:**
- Create: `internal/hostproxy/internals/embed.go`
- Create: `internal/hostproxy/internals/cmd/ssh-agent-proxy/main.go` (move from `internal/build/templates/ssh-agent-proxy.go`)
- Create: `internal/hostproxy/internals/cmd/callback-forwarder/main.go` (move from `internal/build/templates/callback-forwarder.go`)
- Move: `internal/build/templates/host-open.sh` → `internal/hostproxy/internals/host-open.sh`
- Move: `internal/build/templates/git-credential-clawker.sh` → `internal/hostproxy/internals/git-credential-clawker.sh`

**Step 1: Create directory structure**

```bash
mkdir -p internal/hostproxy/internals/cmd/ssh-agent-proxy
mkdir -p internal/hostproxy/internals/cmd/callback-forwarder
```

**Step 2: Move Go binaries — remove `//go:build ignore` and keep as `package main`**

Move `internal/build/templates/ssh-agent-proxy.go` → `internal/hostproxy/internals/cmd/ssh-agent-proxy/main.go`:
- Remove the `//go:build ignore` line
- Keep `package main` and all other code as-is

Move `internal/build/templates/callback-forwarder.go` → `internal/hostproxy/internals/cmd/callback-forwarder/main.go`:
- Remove the `//go:build ignore` line
- Keep `package main` and all other code as-is

**Step 3: Move shell scripts**

```bash
git mv internal/build/templates/host-open.sh internal/hostproxy/internals/host-open.sh
git mv internal/build/templates/git-credential-clawker.sh internal/hostproxy/internals/git-credential-clawker.sh
```

**Step 4: Create `internal/hostproxy/internals/embed.go`**

```go
// Package internals provides embedded container-side scripts and source code
// that run inside clawker containers to communicate with the host proxy.
// These are leaf assets (stdlib + embed only) consumed by the bundler package
// when assembling Docker build contexts.
package internals

import _ "embed"

// HostOpenScript is a shell script used as the BROWSER env var inside containers.
// It opens URLs via the host proxy and handles OAuth callback interception.
//
//go:embed host-open.sh
var HostOpenScript string

// GitCredentialScript is a git credential helper that forwards credential
// requests to the host proxy for HTTPS git authentication.
//
//go:embed git-credential-clawker.sh
var GitCredentialScript string

// SSHAgentProxySource is the Go source for the ssh-agent-proxy binary.
// It forwards SSH agent requests from the container to the host proxy.
// Compiled during Docker image build via multi-stage Dockerfile.
//
//go:embed cmd/ssh-agent-proxy/main.go
var SSHAgentProxySource string

// CallbackForwarderSource is the Go source for the callback-forwarder binary.
// It polls the host proxy for captured OAuth callbacks and forwards them
// to the local HTTP server inside the container.
// Compiled during Docker image build via multi-stage Dockerfile.
//
//go:embed cmd/callback-forwarder/main.go
var CallbackForwarderSource string
```

**Step 5: Verify the new package compiles**

Run: `go build ./internal/hostproxy/internals/...`
Expected: Success (no errors)

**Step 6: Verify cmd packages compile independently**

Run: `go build ./internal/hostproxy/internals/cmd/ssh-agent-proxy/ && go build ./internal/hostproxy/internals/cmd/callback-forwarder/`
Expected: Success — both are standalone `package main` with stdlib-only imports

**Step 7: Delete old template files**

```bash
rm internal/build/templates/ssh-agent-proxy.go
rm internal/build/templates/callback-forwarder.go
```

Note: Do NOT delete `host-open.sh` and `git-credential-clawker.sh` — they were `git mv`'d in Step 3.

**Step 8: Commit**

```bash
git add internal/hostproxy/internals/ internal/build/templates/
git commit -m "refactor: extract hostproxy container-side components to internal/hostproxy/internals

Move ssh-agent-proxy, callback-forwarder, host-open, and git-credential-clawker
from internal/build/templates/ to internal/hostproxy/internals/.
Go binaries get proper cmd/ subdirectories without //go:build ignore.
Shell scripts and Go sources are embedded via embed.go and exported as byte vars."
```

---

## Task 2: Rename `internal/build` → `internal/bundler` and move remaining templates to `assets/`

**Files:**
- Rename: `internal/build/` → `internal/bundler/` (all .go files, semver/, registry/)
- Move: `internal/build/templates/` remaining files → `internal/bundler/assets/`
- Update: All `go:embed` paths in `dockerfile.go`
- Update: All internal import paths (`internal/build` → `internal/bundler`, `internal/build/registry` → `internal/bundler/registry`, `internal/build/semver` → `internal/bundler/semver`)

**Step 1: Rename the package directory**

```bash
git mv internal/build internal/bundler
```

**Step 2: Rename templates/ to assets/**

```bash
git mv internal/bundler/templates internal/bundler/assets
```

**Step 3: Update package declaration in all bundler .go files**

In every `.go` file under `internal/bundler/` (not subpackages), change `package build` → `package bundler`:
- `config.go`
- `defaults.go`
- `dockerfile.go`
- `hash.go`
- `versions.go`
- `errors.go`
- `build_test.go`
- `defaults_test.go`
- `hash_test.go`
- `firewall_test.go`

**Step 4: Update `go:embed` paths in `dockerfile.go`**

Replace all `templates/` prefixes with `assets/`:
```
//go:embed assets/Dockerfile.tmpl    (was templates/Dockerfile.tmpl)
//go:embed assets/entrypoint.sh      (was templates/entrypoint.sh)
//go:embed assets/init-firewall.sh   (was templates/init-firewall.sh)
//go:embed assets/statusline.sh      (was templates/statusline.sh)
//go:embed assets/claude-settings.json (was templates/claude-settings.json)
```

Also update the `ReadFile` call:
```go
tmplContent, err := dockerfileFS.ReadFile("assets/Dockerfile.tmpl")  // was "templates/Dockerfile.tmpl"
```

Remove the 4 embed directives for files that moved to hostproxy/internals:
- Remove: `//go:embed templates/host-open.sh` + `var HostOpenScript string`
- Remove: `//go:embed templates/callback-forwarder.go` + `var CallbackForwarderSource string`
- Remove: `//go:embed templates/git-credential-clawker.sh` + `var GitCredentialScript string`
- Remove: `//go:embed templates/ssh-agent-proxy.go` + `var SSHAgentProxySource string`

Add import and alias the hostproxy internals vars:
```go
import (
    "github.com/schmitthub/clawker/internal/hostproxy/internals"
)
```

Then re-export as package-level vars for backward compatibility within the bundler:
```go
var (
    HostOpenScript         = internals.HostOpenScript
    CallbackForwarderSource = internals.CallbackForwarderSource
    GitCredentialScript     = internals.GitCredentialScript
    SSHAgentProxySource     = internals.SSHAgentProxySource
)
```

**Step 5: Update internal import paths within bundler subpackages**

`internal/bundler/errors.go`:
```go
"github.com/schmitthub/clawker/internal/bundler/registry"  // was internal/build/registry
```

`internal/bundler/versions.go`:
```go
"github.com/schmitthub/clawker/internal/bundler/registry"  // was internal/build/registry
"github.com/schmitthub/clawker/internal/bundler/semver"     // was internal/build/semver
```

`internal/bundler/dockerfile.go`:
```go
"github.com/schmitthub/clawker/internal/bundler/registry"  // was internal/build/registry
```

`internal/bundler/registry/types.go`:
```go
"github.com/schmitthub/clawker/internal/bundler/semver"    // was internal/build/semver
```

**Step 6: Update the `go:embed` FS path for Dockerfile.tmpl**

In `dockerfile.go`, update the embed FS directive:
```go
//go:embed assets/Dockerfile.tmpl
var dockerfileFS embed.FS
```

And the ReadFile call:
```go
tmplContent, err := dockerfileFS.ReadFile("assets/Dockerfile.tmpl")
```

**Step 7: Verify bundler compiles**

Run: `go build ./internal/bundler/...`
Expected: Success

**Step 8: Commit**

```bash
git add internal/bundler/ internal/build/
git commit -m "refactor: rename internal/build to internal/bundler

Rename package to better reflect its role: Dockerfile rendering, build context
assembly, and asset embedding. templates/ becomes assets/. Hostproxy container
scripts are now imported from internal/hostproxy/internals."
```

---

## Task 3: Update all external consumers of `internal/build` → `internal/bundler`

**Files:**
- Modify: `internal/docker/builder.go` — change import path + alias
- Modify: `internal/docker/builder_test.go` — change import path + alias
- Modify: `internal/docker/defaults.go` — change import path + alias
- Modify: `internal/cmd/generate/generate.go` — change import paths (both `build` and `registry`)
- Modify: `internal/cmd/init/init.go` — change import path (`intbuild` alias)
- Modify: `internal/cmd/init/init_test.go` — change import path (`intbuild` alias)
- Modify: `internal/cmd/project/init/init.go` — change import path (`intbuild` alias)
- Modify: `internal/cmd/container/create/create.go` — change import path (`intbuild` alias)
- Modify: `internal/cmd/container/run/run.go` — change import path (`intbuild` alias)

**Step 1: Update `internal/docker/builder.go`**

Change:
```go
"github.com/schmitthub/clawker/internal/build"
```
To:
```go
"github.com/schmitthub/clawker/internal/bundler"
```

Update all `build.` references to `bundler.`:
- `build.NewProjectGenerator` → `bundler.NewProjectGenerator`
- `build.ContentHash` → `bundler.ContentHash`
- `build.CreateBuildContextFromDir` → `bundler.CreateBuildContextFromDir`

**Step 2: Update `internal/docker/builder_test.go`**

Same import change. Update all `build.` references to `bundler.`:
- `build.NewProjectGenerator` → `bundler.NewProjectGenerator`
- `build.ContentHash` → `bundler.ContentHash`

**Step 3: Update `internal/docker/defaults.go`**

Same import change. Update:
- `build.NewVersionsManager` → `bundler.NewVersionsManager`
- `build.NewDockerfileManager` → `bundler.NewDockerfileManager`
- `build.CreateBuildContextFromDir` → `bundler.CreateBuildContextFromDir`

**Step 4: Update `internal/cmd/generate/generate.go`**

Change both imports:
```go
"github.com/schmitthub/clawker/internal/build"           → "github.com/schmitthub/clawker/internal/bundler"
"github.com/schmitthub/clawker/internal/build/registry"   → "github.com/schmitthub/clawker/internal/bundler/registry"
```

Update all `build.` references to `bundler.`.

**Step 5: Update commands using `intbuild` alias**

For each of these files, change:
```go
intbuild "github.com/schmitthub/clawker/internal/build"
```
To:
```go
intbuild "github.com/schmitthub/clawker/internal/bundler"
```

No code changes needed — the `intbuild.` prefix stays the same since it's an alias.

Files:
- `internal/cmd/init/init.go`
- `internal/cmd/init/init_test.go`
- `internal/cmd/project/init/init.go`
- `internal/cmd/container/create/create.go`
- `internal/cmd/container/run/run.go`

**Step 6: Verify all consumers compile**

Run: `go build ./internal/...`
Expected: Success

**Step 7: Run unit tests**

Run: `make test`
Expected: All pass

**Step 8: Commit**

```bash
git add internal/docker/ internal/cmd/
git commit -m "refactor: update all imports from internal/build to internal/bundler"
```

---

## Task 4: Update test harness references

**Files:**
- Modify: `test/harness/client.go` — update hardcoded path to templates
- Modify: `test/harness/hash.go` — update hardcoded path to templates
- Modify: `test/harness/hash_test.go` — update hardcoded paths in tests

**Step 1: Update `test/harness/client.go`**

Change line 270:
```go
scriptsDir := filepath.Join(projectRoot, "internal", "build", "templates")
```
To:
```go
scriptsDir := filepath.Join(projectRoot, "internal", "bundler", "assets")
```

Also update the `scriptsDir` to additionally read from `internal/hostproxy/internals/`:
The `BuildLightImage` function needs to collect scripts from TWO directories now:
1. `internal/bundler/assets/` — entrypoint.sh, init-firewall.sh, statusline.sh
2. `internal/hostproxy/internals/` — host-open.sh, git-credential-clawker.sh

And Go sources from:
1. `internal/hostproxy/internals/cmd/ssh-agent-proxy/main.go`
2. `internal/hostproxy/internals/cmd/callback-forwarder/main.go`

The existing logic reads `*.sh` files from a single dir and `*.go` files from the same dir. This needs to be updated to read from both locations. The Go source paths also change from flat files to `cmd/*/main.go`.

**Step 2: Update `test/harness/hash.go`**

Change the templates directory path in both functions (`ComputeTemplateHash` and `ComputeTemplateHashFromDir`):
```go
templatesDir := filepath.Join(rootDir, "internal", "bundler", "assets")
```

Also add hashing of the hostproxy internals directory:
```go
internalsDir := filepath.Join(rootDir, "internal", "hostproxy", "internals")
```

Both directories must be hashed for the content-addressed test image to invalidate when any script changes.

**Step 3: Update `test/harness/hash_test.go`**

Update all hardcoded paths from `internal/build/templates` to `internal/bundler/assets`:
- Line 65: `filepath.Join(tmpDir, "internal", "bundler", "assets")`
- Line 88: same
- Line 111: same
- Line 140: `filepath.Join(root, "internal", "bundler", "assets")`

**Step 4: Run harness tests**

Run: `go test ./test/harness/... -v -count=1`
Expected: All pass

**Step 5: Commit**

```bash
git add test/harness/
git commit -m "refactor: update test harness paths for build→bundler rename

BuildLightImage now reads scripts from both internal/bundler/assets/ and
internal/hostproxy/internals/. Content hash includes both directories."
```

---

## Task 5: Update all documentation

**Files:**
- Modify: `CLAUDE.md` — repository structure, key concepts, package references
- Modify: `.claude/memories/ARCHITECTURE.md` — package DAG, tier classification
- Modify: `.claude/memories/DESIGN.md` — line 255
- Modify: `.claude/memories/TESTING-REFERENCE.md` — BuildLightImage docs
- Modify: `.claude/rules/dependency-placement.md` — tier table
- Modify: `.claude/skills/audit-memory/REFERENCE.md` — package list
- Modify: `internal/bundler/CLAUDE.md` — update package name, add hostproxy/internals import note
- Modify: `internal/docker/CLAUDE.md` — update dependency reference
- Modify: `internal/cmdutil/CLAUDE.md` — update build utilities reference
- Modify: `test/CLAUDE.md` — update BuildLightImage reference
- Create: `internal/hostproxy/internals/CLAUDE.md` — new package docs

**Step 1: Update `CLAUDE.md`**

All references to `internal/build` → `internal/bundler`. Update the repository structure tree:
- `internal/build/` line → `internal/bundler/                 # Dockerfile generation, content hashing, semver, npm registry (leaf — no docker import)`
- Add `internal/hostproxy/internals/` to the hostproxy section
- Update key concepts table: `build/` references → `bundler/`
- Update dependency placement table

**Step 2: Update `.claude/memories/ARCHITECTURE.md`**

- Package DAG: `build/` → `bundler/` in MIDDLE PACKAGES tier
- Add `hostproxy/internals/` note as structurally-leaf subpackage
- Update the import direction examples
- Update "Other Key Components" table

**Step 3: Update `.claude/memories/DESIGN.md`**

Line 255: `internal/build/` → `internal/bundler/`

**Step 4: Update `.claude/memories/TESTING-REFERENCE.md`**

Lines 348, 601, 626, 635: `internal/build/templates/` → `internal/bundler/assets/` and `internal/hostproxy/internals/`

**Step 5: Update `.claude/rules/dependency-placement.md`**

Line 44: `internal/build/` → `internal/bundler/`

**Step 6: Update `.claude/skills/audit-memory/REFERENCE.md`**

Line 12: `internal/build/` → `internal/bundler/`

**Step 7: Update `internal/bundler/CLAUDE.md`**

- Update package name from "Build Package" to "Bundler Package"
- Update description to reflect rename
- Add note about hostproxy/internals import for container-side scripts
- Update subpackage table to replace `templates/` with `assets/`

**Step 8: Update `internal/docker/CLAUDE.md`**

Line 125: `internal/build` → `internal/bundler`

**Step 9: Update `internal/cmdutil/CLAUDE.md`**

Line 7: `internal/build/` → `internal/bundler/`

**Step 10: Update `test/CLAUDE.md`**

Line 101: `internal/build/templates/` → `internal/bundler/assets/` (and mention `internal/hostproxy/internals/`)

**Step 11: Create `internal/hostproxy/internals/CLAUDE.md`**

```markdown
# Hostproxy Internals Package

Container-side scripts and binaries that communicate with the clawker host proxy server. These are embedded at Docker image build time and run inside containers.

## Key Files

| File | Purpose |
|------|---------|
| `embed.go` | `go:embed` directives + exported byte vars |
| `host-open.sh` | BROWSER handler — opens URLs via host proxy, intercepts OAuth callbacks |
| `git-credential-clawker.sh` | Git credential helper — forwards to host proxy `/git/credential` |
| `cmd/ssh-agent-proxy/main.go` | SSH agent forwarding — Unix socket → HTTP to host proxy `/ssh/agent` |
| `cmd/callback-forwarder/main.go` | OAuth callback polling — polls host proxy, forwards to local port |

## Architecture

This is a **leaf package** (stdlib + embed only). It exports embedded content as string vars consumed by the `internal/bundler` package during Docker build context assembly.

The Go binaries under `cmd/` are standalone `package main` programs compiled inside the Docker image during multi-stage builds. They use only stdlib — no imports from the clawker module.

## Dependencies

- Imports: `embed` (stdlib only)
- Imported by: `internal/bundler`
- Does NOT import: `internal/hostproxy` or any other internal package
```

**Step 12: Commit**

```bash
git add CLAUDE.md .claude/ internal/bundler/CLAUDE.md internal/docker/CLAUDE.md internal/cmdutil/CLAUDE.md test/CLAUDE.md internal/hostproxy/internals/CLAUDE.md
git commit -m "docs: update all documentation for build→bundler rename

Update CLAUDE.md, architecture docs, testing reference, dependency rules,
and package-level docs. Add CLAUDE.md for new hostproxy/internals package."
```

---

## Task 6: Final verification

**Step 1: Full build**

Run: `go build ./...`
Expected: Success

**Step 2: Unit tests**

Run: `make test`
Expected: All pass

**Step 3: Verify no stale references**

Run: `grep -r 'internal/build' --include='*.go' --include='*.md' .`

Expected: Only historical references in `docs/plans/2026-02-02-*.md` (old plans — no update needed) and `.claude/prds/` (reference material — no update needed).

**Step 4: Verify dependency direction**

Run: `grep -r '"github.com/schmitthub/clawker/internal/bundler"' internal/hostproxy/`
Expected: No matches (hostproxy must not import bundler)

Run: `grep -r '"github.com/schmitthub/clawker/internal/hostproxy/internals"' internal/bundler/`
Expected: One match in `dockerfile.go` (bundler imports internals — correct direction)

**Step 5: Verify no old package directory remains**

Run: `ls internal/build 2>&1`
Expected: "No such file or directory"
