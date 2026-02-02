# Build→Docker Collapse Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Move `Builder`, `Options`, `EnsureImage`, `Build`, `BuildDefaultImage`, and `DefaultImageTag` from `internal/build` into `internal/docker`, making `internal/build` a pure leaf package with zero `docker` imports.

**Architecture:** `internal/build` becomes a leaf (Dockerfile generation, templates, versions, semver). `internal/docker` becomes the domain service that imports build for generation types and provides Builder + Client to commands. Commands are root nodes importing both.

**Tech Stack:** Go, Docker SDK via whail, Cobra CLI

---

### Caller Summary (what each file uses from build)

| File | Symbols Used | Action |
|------|-------------|--------|
| `cmd/image/build/build.go` | `build.NewBuilder`, `build.Options` | Change to `docker.NewBuilder`, `docker.BuilderOptions` |
| `cmd/container/run/run.go` | `intbuild.BuildDefaultImage`, `intbuild.DefaultImageTag`, `intbuild.DefaultFlavorOptions` | `docker.BuildDefaultImage`, `docker.DefaultImageTag`; keep `intbuild.DefaultFlavorOptions` |
| `cmd/container/create/create.go` | `intbuild.BuildDefaultImage`, `intbuild.DefaultImageTag`, `intbuild.DefaultFlavorOptions` | Same as run |
| `cmd/init/init.go` | `intbuild.BuildDefaultImage`, `intbuild.DefaultImageTag`, `intbuild.DefaultFlavorOptions` | Same pattern |
| `cmd/init/init_test.go` | `intbuild.DefaultImageTag`, `intbuild.DefaultFlavorOptions` | `docker.DefaultImageTag`; keep `intbuild.DefaultFlavorOptions` |
| `cmd/project/init/init.go` | `intbuild.FlavorToImage`, `intbuild.DefaultFlavorOptions` | **No change** (both stay in build) |
| `cmd/generate/generate.go` | `build.LoadVersionsFile`, `build.NewVersionsManager`, `build.NewDockerfileManager`, etc. | **No change** (all leaf symbols) |
| `test/harness/docker.go` | `build.NewBuilder`, `build.Options` | Change to `docker.NewBuilder`, `docker.BuilderOptions` |

---

### Task 1: Create `internal/docker/builder.go`

**Files:**
- Create: `internal/docker/builder.go`
- Source: `internal/build/build.go` (lines 1-226, entire file)

**Step 1: Write `internal/docker/builder.go`**

Move all code from `internal/build/build.go` into a new file `internal/docker/builder.go` with these adjustments:

- Package declaration: `package docker`
- Rename `Options` → `BuilderOptions` (avoid collision with other option types in docker package)
- Remove `docker.` prefix from same-package references: `docker.Client` → `Client`, `docker.ImageTagWithHash` → `ImageTagWithHash`, `docker.ImageLabels` → `ImageLabels`, `docker.BuildImageOpts` → `BuildImageOpts`
- Add `build` import for: `build.NewProjectGenerator`, `build.ContentHash`, `build.CreateBuildContextFromDir`
- Remove the `toBuildImageOpts()` method — inline the `BuildImageOpts` struct literal directly (same package now, no mapping needed)
- Keep `mergeTags` as unexported helper

```go
package docker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// Builder handles Docker image building for clawker projects.
type Builder struct {
	client  *Client
	config  *config.Project
	workDir string
}

// BuilderOptions contains options for build operations.
type BuilderOptions struct {
	ForceBuild      bool
	NoCache         bool
	Labels          map[string]string
	Target          string
	Pull            bool
	SuppressOutput  bool
	NetworkMode     string
	BuildArgs       map[string]*string
	Tags            []string
	Dockerfile      []byte
	BuildKitEnabled bool
}

// NewBuilder creates a new Builder instance.
func NewBuilder(cli *Client, cfg *config.Project, workDir string) *Builder {
	return &Builder{
		client:  cli,
		config:  cfg,
		workDir: workDir,
	}
}

// EnsureImage ensures an image is available, building if necessary.
// Uses content-addressed tags to detect whether config actually changed.
func (b *Builder) EnsureImage(ctx context.Context, imageTag string, opts BuilderOptions) error {
	gen := build.NewProjectGenerator(b.config, b.workDir)
	gen.BuildKitEnabled = opts.BuildKitEnabled

	if gen.UseCustomDockerfile() {
		return b.Build(ctx, imageTag, opts)
	}

	dockerfile, err := gen.Generate()
	if err != nil {
		return fmt.Errorf("failed to generate Dockerfile: %w", err)
	}

	hash, err := build.ContentHash(dockerfile, b.config.Agent.Includes, b.workDir)
	if err != nil {
		return fmt.Errorf("failed to compute content hash: %w", err)
	}

	hashTag := ImageTagWithHash(b.config.Project, hash)

	if !opts.ForceBuild {
		exists, err := b.client.ImageExists(ctx, hashTag)
		if err != nil {
			return fmt.Errorf("failed to check image existence for %s: %w", hashTag, err)
		}
		if exists {
			logger.Debug().
				Str("image", hashTag).
				Msg("image up-to-date, skipping build")

			if err := b.client.TagImage(ctx, hashTag, imageTag); err != nil {
				return fmt.Errorf("failed to update :latest alias: %w", err)
			}
			return nil
		}
	}

	tags := make([]string, len(opts.Tags), len(opts.Tags)+1)
	copy(tags, opts.Tags)
	opts.Tags = append(tags, hashTag)
	opts.Dockerfile = dockerfile
	return b.Build(ctx, imageTag, opts)
}

// Build unconditionally builds the Docker image.
func (b *Builder) Build(ctx context.Context, imageTag string, opts BuilderOptions) error {
	gen := build.NewProjectGenerator(b.config, b.workDir)
	gen.BuildKitEnabled = opts.BuildKitEnabled

	opts.Labels = b.mergeImageLabels(opts.Labels)
	tags := mergeTags(imageTag, opts.Tags)

	if gen.UseCustomDockerfile() {
		logger.Info().
			Str("dockerfile", b.config.Build.Dockerfile).
			Msg("building from custom Dockerfile")

		buildCtx, err := build.CreateBuildContextFromDir(
			gen.GetBuildContext(),
			gen.GetCustomDockerfilePath(),
		)
		if err != nil {
			return fmt.Errorf("failed to create build context: %w", err)
		}

		return b.client.BuildImage(ctx, buildCtx, BuildImageOpts{
			Tags:            tags,
			Dockerfile:      filepath.Base(gen.GetCustomDockerfilePath()),
			NoCache:         opts.NoCache,
			Labels:          opts.Labels,
			Target:          opts.Target,
			Pull:            opts.Pull,
			SuppressOutput:  opts.SuppressOutput,
			NetworkMode:     opts.NetworkMode,
			BuildArgs:       opts.BuildArgs,
			BuildKitEnabled: opts.BuildKitEnabled,
			ContextDir:      gen.GetBuildContext(),
		})
	}

	logger.Info().Str("image", imageTag).Msg("building container image")

	var dockerfile []byte
	if len(opts.Dockerfile) > 0 {
		dockerfile = opts.Dockerfile
	} else {
		var err error
		dockerfile, err = gen.Generate()
		if err != nil {
			return fmt.Errorf("failed to generate Dockerfile: %w", err)
		}
	}

	if opts.BuildKitEnabled {
		tempDir, err := os.MkdirTemp("", "clawker-buildctx-*")
		if err != nil {
			return fmt.Errorf("failed to create build context temp dir: %w", err)
		}
		defer os.RemoveAll(tempDir)

		if err := gen.WriteBuildContextToDir(tempDir, dockerfile); err != nil {
			return fmt.Errorf("failed to write build context: %w", err)
		}

		return b.client.BuildImage(ctx, nil, BuildImageOpts{
			Tags:            tags,
			Dockerfile:      "Dockerfile",
			NoCache:         opts.NoCache,
			Labels:          opts.Labels,
			Target:          opts.Target,
			Pull:            opts.Pull,
			SuppressOutput:  opts.SuppressOutput,
			NetworkMode:     opts.NetworkMode,
			BuildArgs:       opts.BuildArgs,
			BuildKitEnabled: opts.BuildKitEnabled,
			ContextDir:      tempDir,
		})
	}

	buildCtx, err := gen.GenerateBuildContextFromDockerfile(dockerfile)
	if err != nil {
		return fmt.Errorf("failed to generate build context: %w", err)
	}

	return b.client.BuildImage(ctx, buildCtx, BuildImageOpts{
		Tags:            tags,
		Dockerfile:      "Dockerfile",
		NoCache:         opts.NoCache,
		Labels:          opts.Labels,
		Target:          opts.Target,
		Pull:            opts.Pull,
		SuppressOutput:  opts.SuppressOutput,
		NetworkMode:     opts.NetworkMode,
		BuildArgs:       opts.BuildArgs,
		BuildKitEnabled: opts.BuildKitEnabled,
		ContextDir:      gen.GetBuildContext(),
	})
}

// mergeImageLabels combines clawker internal labels and user-defined labels.
func (b *Builder) mergeImageLabels(existing map[string]string) map[string]string {
	merged := make(map[string]string)
	for k, v := range existing {
		merged[k] = v
	}
	if b.config.Build.Instructions != nil {
		for k, v := range b.config.Build.Instructions.Labels {
			merged[k] = v
		}
	}
	for k, v := range ImageLabels(b.config.Project, b.config.Version) {
		merged[k] = v
	}
	return merged
}

// mergeTags combines the primary tag with additional tags, avoiding duplicates.
func mergeTags(primary string, additional []string) []string {
	seen := make(map[string]bool)
	result := []string{primary}
	seen[primary] = true
	for _, tag := range additional {
		if !seen[tag] {
			result = append(result, tag)
			seen[tag] = true
		}
	}
	return result
}
```

**Step 2: Verify it compiles (expect failure — old file still exists)**

Run: `go build ./internal/docker/...`
Expected: May get duplicate symbol errors since `build/build.go` still exists. That's fine — next task removes it.

---

### Task 2: Create `internal/docker/defaults.go`

**Files:**
- Create: `internal/docker/defaults.go`
- Source: `internal/build/defaults.go` (lines 13-14, 50-139)

**Step 1: Write `internal/docker/defaults.go`**

Move `DefaultImageTag` constant and `BuildDefaultImage` function. Keep `FlavorOption`, `DefaultFlavorOptions`, `FlavorToImage` in build.

```go
package docker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/schmitthub/clawker/internal/build"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/logger"
)

// DefaultImageTag is the tag used for the user's default base image.
const DefaultImageTag = "clawker-default:latest"

// BuildDefaultImage builds the default clawker base image with the given flavor.
func BuildDefaultImage(ctx context.Context, flavor string) error {
	buildDir, err := config.BuildDir()
	if err != nil {
		return fmt.Errorf("failed to get build directory: %w", err)
	}

	logger.Debug().Msg("resolving latest Claude Code version from npm")
	mgr := build.NewVersionsManager()
	versions, err := mgr.ResolveVersions(ctx, []string{"latest"}, build.ResolveOptions{})
	if err != nil {
		return fmt.Errorf("failed to resolve latest version: %w", err)
	}

	client, err := NewClient(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	defer client.Close()

	WireBuildKit(client)

	buildkitEnabled, bkErr := BuildKitEnabled(ctx, client.APIClient)
	if bkErr != nil {
		logger.Warn().Err(bkErr).Msg("BuildKit detection failed")
	} else if !buildkitEnabled {
		logger.Warn().Msg("BuildKit is not available — cache mount directives will be omitted and builds may be slower")
	}

	logger.Debug().Str("output_dir", buildDir).Msg("generating dockerfiles")
	dfMgr := build.NewDockerfileManager(buildDir, nil)
	dfMgr.BuildKitEnabled = buildkitEnabled
	if err := dfMgr.GenerateDockerfiles(versions); err != nil {
		return fmt.Errorf("failed to generate dockerfiles: %w", err)
	}

	var latestVersion string
	for v := range *versions {
		latestVersion = v
		break
	}
	if latestVersion == "" {
		return fmt.Errorf("no version resolved")
	}

	dockerfileName := fmt.Sprintf("%s-%s.dockerfile", latestVersion, flavor)
	dockerfilesDir := dfMgr.DockerfilesDir()
	dockerfilePath := filepath.Join(dockerfilesDir, dockerfileName)

	logger.Debug().
		Str("dockerfile", dockerfilePath).
		Str("version", latestVersion).
		Str("flavor", flavor).
		Msg("building image")

	buildContext, err := build.CreateBuildContextFromDir(dockerfilesDir, dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}

	err = client.BuildImage(ctx, buildContext, BuildImageOpts{
		Tags:       []string{DefaultImageTag},
		Dockerfile: dockerfileName,
		Labels: map[string]string{
			"com.clawker.managed":    "true",
			"com.clawker.base-image": "true",
			"com.clawker.flavor":     flavor,
		},
		BuildKitEnabled: buildkitEnabled,
		ContextDir:      dockerfilesDir,
	})
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	logger.Info().Str("image", DefaultImageTag).Msg("base image built successfully")
	return nil
}
```

---

### Task 3: Delete `internal/build/build.go` and strip `internal/build/defaults.go`

**Files:**
- Delete: `internal/build/build.go`
- Modify: `internal/build/defaults.go` (remove lines 1-14 `DefaultImageTag` const, lines 50-139 `BuildDefaultImage` func, and `docker`/`config`/`logger` imports)

**Step 1: Delete `internal/build/build.go`**

Run: `rm internal/build/build.go`

**Step 2: Edit `internal/build/defaults.go`**

Remove: `DefaultImageTag` constant, `BuildDefaultImage` function, and all imports except `testing` (none needed for the remaining pure functions). The file should contain only:

```go
package build

// FlavorOption represents a Linux flavor choice for image building.
type FlavorOption struct {
	Name        string
	Description string
}

// DefaultFlavorOptions returns the available Linux flavors for base images.
func DefaultFlavorOptions() []FlavorOption {
	return []FlavorOption{
		{Name: "bookworm", Description: "Debian stable (Recommended)"},
		{Name: "trixie", Description: "Debian testing"},
		{Name: "alpine3.22", Description: "Alpine Linux 3.22"},
		{Name: "alpine3.23", Description: "Alpine Linux 3.23"},
	}
}

// FlavorToImage maps a flavor name to its full Docker image reference.
func FlavorToImage(flavor string) string {
	switch flavor {
	case "bookworm":
		return "buildpack-deps:bookworm-scm"
	case "trixie":
		return "buildpack-deps:trixie-scm"
	case "alpine3.22":
		return "alpine:3.22"
	case "alpine3.23":
		return "alpine:3.23"
	default:
		return flavor
	}
}
```

**Step 3: Verify build package has no docker import**

Run: `grep -r '"github.com/schmitthub/clawker/internal/docker"' internal/build/`
Expected: No output (zero matches)

---

### Task 4: Move tests

**Files:**
- Create: `internal/docker/builder_test.go` (from `internal/build/build_test.go`)
- Delete: `internal/build/build_test.go`
- Modify: `internal/build/defaults_test.go` (remove `TestDefaultImageTag`)

**Step 1: Create `internal/docker/builder_test.go`**

Move all tests from `internal/build/build_test.go`. Key adjustments:
- Package: `docker` (not `docker_test` — tests access unexported `mergeTags`)
- Remove `docker.` prefix from same-package references: `docker.ImageTagWithHash` → `ImageTagWithHash`, `docker.ImageTag` → `ImageTag`, `docker.LabelManaged` → `LabelManaged`, `docker.LabelProject` → `LabelProject`
- Change `build.NewProjectGenerator` → `build.NewProjectGenerator` (import build)
- Change `build.ContentHash` → `build.ContentHash` (import build)
- Change `build.Options` → `BuilderOptions` everywhere
- `dockertest.NewFakeClient()` → `dockertest.NewFakeClient()` (same import)
- Move `TestWriteBuildContextToDir*` tests — these test `build.ProjectGenerator` so they stay in `internal/build/build_test.go` actually. Wait — these are already testing build package types. Let me re-check.

Actually `TestWriteBuildContextToDir` (line 312-361) tests `build.ProjectGenerator.WriteBuildContextToDir` — that stays in build. Only tests for `Builder`, `EnsureImage`, `mergeTags`, `mergeImageLabels` move.

Tests that move to `docker/builder_test.go`:
- `TestMergeTags` (line 21-72)
- `TestMergeImageLabels_InternalLabelsOverrideUser` (line 74-100)
- `TestEnsureImage_CacheHit` (line 117-157)
- `TestEnsureImage_CacheMiss` (line 159-197)
- `TestEnsureImage_ForceBuild` (line 199-223)
- `TestEnsureImage_TagImageFailure` (line 225-253)
- `TestEnsureImage_CustomDockerfileDelegatesToBuild` (line 255-298)
- `TestEnsureImage_ContentHashError` (line 300-310)

Tests that stay (rename file to `internal/build/dockerfile_test.go` or keep in `build_test.go`):
- `TestWriteBuildContextToDir` (line 312-361)
- `TestWriteBuildContextToDir_NoFirewall` (line 363-385)
- `TestWriteBuildContextToDir_WithIncludes` (line 387-416)

**Step 2: Edit `internal/build/defaults_test.go`**

Remove `TestDefaultImageTag` (line 103-109) — it tests the constant that moved to docker. Keep `TestDefaultFlavorOptions` and `TestFlavorToImage`.

**Step 3: Create `internal/docker/defaults_test.go`**

Add `TestDefaultImageTag` here:
```go
package docker

import "testing"

func TestDefaultImageTag(t *testing.T) {
	expected := "clawker-default:latest"
	if DefaultImageTag != expected {
		t.Errorf("DefaultImageTag = %q, want %q", DefaultImageTag, expected)
	}
}
```

**Step 4: Run tests**

Run: `go test ./internal/docker/... ./internal/build/... -count=1`
Expected: All pass (callers not yet updated, but those are in different packages)

---

### Task 5: Update callers — `cmd/image/build/build.go`

**Files:**
- Modify: `internal/cmd/image/build/build.go`

**Step 1: Update imports and references**

- Remove: `"github.com/schmitthub/clawker/internal/build"` import
- `build.NewBuilder(client, cfg, wd)` → `docker.NewBuilder(client, cfg, wd)` (line 187)
- `build.Options{...}` → `docker.BuilderOptions{...}` (line 195)
- Already imports `docker` so no new import needed

**Step 2: Verify compile**

Run: `go build ./internal/cmd/image/build/...`
Expected: Success

---

### Task 6: Update callers — `cmd/container/run/run.go`

**Files:**
- Modify: `internal/cmd/container/run/run.go`

**Step 1: Update imports and references**

- Lines using `intbuild.BuildDefaultImage` → `docker.BuildDefaultImage` (line 545)
- Lines using `intbuild.DefaultImageTag` → `docker.DefaultImageTag` (lines 543, 550, 561)
- Keep `intbuild.DefaultFlavorOptions` (line 528) — stays in build
- If `intbuild` alias is only used for `DefaultFlavorOptions`, keep it. If no build symbols remain, remove the import.

**Step 2: Verify compile**

Run: `go build ./internal/cmd/container/run/...`

---

### Task 7: Update callers — `cmd/container/create/create.go`

**Files:**
- Modify: `internal/cmd/container/create/create.go`

**Step 1: Update imports and references**

Same pattern as run.go:
- `intbuild.BuildDefaultImage` → `docker.BuildDefaultImage` (line 329)
- `intbuild.DefaultImageTag` → `docker.DefaultImageTag` (lines 327, 334, 345)
- Keep `intbuild.DefaultFlavorOptions` (line 312)

---

### Task 8: Update callers — `cmd/init/init.go` and `cmd/init/init_test.go`

**Files:**
- Modify: `internal/cmd/init/init.go`
- Modify: `internal/cmd/init/init_test.go`

**Step 1: Update `init.go`**

- `intbuild.BuildDefaultImage` → `docker.BuildDefaultImage` (line 142)
- `intbuild.DefaultImageTag` → `docker.DefaultImageTag` (lines 160, 175, 178)
- Keep `intbuild.DefaultFlavorOptions` (line 107)
- Add `docker` import if not already present

**Step 2: Update `init_test.go`**

- `intbuild.DefaultImageTag` → `docker.DefaultImageTag` (line 69-70)
- Keep `intbuild.DefaultFlavorOptions` (line 76)
- Add `docker` import, potentially remove `intbuild` alias if `DefaultFlavorOptions` is the only remaining use (then use `build.DefaultFlavorOptions` with unaliased import or keep alias)

---

### Task 9: Update callers — `test/harness/docker.go`

**Files:**
- Modify: `test/harness/docker.go`

**Step 1: Update imports and references**

- `build.NewBuilder` → `docker.NewBuilder` (line 492)
- `build.Options` → `docker.BuilderOptions` (line 503)
- Remove `"github.com/schmitthub/clawker/internal/build"` import if no other build symbols used

**Step 2: Verify compile**

Run: `go build ./test/harness/...`

---

### Task 10: Full compile and test

**Step 1: Full compile**

Run: `go build ./...`
Expected: Success, zero errors

**Step 2: Verify no docker import in build**

Run: `grep -r '"github.com/schmitthub/clawker/internal/docker"' internal/build/`
Expected: No output

**Step 3: Run vet**

Run: `go vet ./...`
Expected: Clean

**Step 4: Run unit tests**

Run: `make test`
Expected: All pass

**Step 5: Commit**

```bash
git add -A
git commit -m "refactor: move Builder and BuildDefaultImage from build to docker

Collapses image building orchestration into the docker domain service.
internal/build is now a pure leaf package (Dockerfile generation, templates,
versions, semver) with zero internal/docker imports.

Package DAG: build (leaf) ← docker (middle) ← commands (root)"
```

---

### Task 11: Update documentation

**Files:**
- Modify: `internal/build/CLAUDE.md`
- Modify: `internal/docker/CLAUDE.md`
- Modify: `CLAUDE.md` (root, if Key Concepts table needs updating)
- Modify: `.serena/memories/` (update any relevant WIP memories)

**Step 1: Update `internal/build/CLAUDE.md`**

Remove Builder, Options, NewBuilder, EnsureImage, Build, BuildDefaultImage, DefaultImageTag documentation. Add note: "Builder and image building orchestration moved to `internal/docker`. This package provides Dockerfile generation, templates, version resolution, and content hashing as a pure leaf."

**Step 2: Update `internal/docker/CLAUDE.md`**

Add Builder section documenting NewBuilder, BuilderOptions, EnsureImage, Build, BuildDefaultImage, DefaultImageTag. Reference build package for Dockerfile generation types.

**Step 3: Commit docs**

```bash
git add internal/build/CLAUDE.md internal/docker/CLAUDE.md CLAUDE.md
git commit -m "docs: update CLAUDE.md files for build→docker move"
```

---

## Verification Checklist

```bash
# 1. build has zero docker imports
grep -r '"github.com/schmitthub/clawker/internal/docker"' internal/build/
# Expected: no output

# 2. docker imports build (correct direction)
grep -r '"github.com/schmitthub/clawker/internal/build"' internal/docker/
# Expected: builder.go and defaults.go

# 3. Compile
go build ./...

# 4. Vet
go vet ./...

# 5. Unit tests
make test

# 6. Full test suite (Docker required)
make test-all
```
