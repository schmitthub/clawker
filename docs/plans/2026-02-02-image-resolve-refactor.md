# Image Resolution Refactor — Move UI Out of internal/docker

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remove all user-interaction concerns (IOStreams, Prompter, build triggers) from `internal/docker`, making image resolution a pure Docker client method that returns data for the command layer to act on.

**Architecture:** Inject `*config.Config` into `docker.Client` at construction time via the factory. Convert the free functions `FindProjectImage`, `ResolveImageWithSource`, `ResolveImage` into `Client` methods that use the internal config. Delete `ResolveAndValidateImage`, `ImageValidationDeps`, and `FlavorOption` from the docker package entirely. Move the interactive rebuild-prompt logic into the command layer (`createRun`/`runRun`), where IOStreams and Prompter already live.

**Tech Stack:** Go, internal/docker, internal/config, internal/cmd/container/{run,create}, internal/cmd/factory

---

## Context

### Current Problem

`internal/docker/image_resolve.go` contains `ResolveAndValidateImage` which takes an `ImageValidationDeps` struct packed with:
- `IOStreams` (terminal output)
- `Prompter` (interactive user input)
- `SettingsLoader` (config persistence)
- `DefaultFlavorOptions` / `BuildDefaultImage` (build concerns)

This violates `internal/docker`'s responsibility: it should only know about Docker operations.

### Current Call Chain

```
command (run/create) → docker.ResolveAndValidateImage(deps, client, cfg, settings)
                         ├── docker.ResolveImageWithSource(client, cfg, settings)
                         │     ├── docker.FindProjectImage(client, project)
                         │     └── docker.ResolveDefaultImage(cfg, settings)
                         ├── client.ImageExists(ref)           ← Docker concern ✓
                         ├── deps.Prompter().Select(...)       ← UI concern ✗
                         ├── deps.BuildDefaultImage(flavor)    ← Build concern ✗
                         └── deps.SettingsLoader().Save(...)   ← Config concern ✗
```

### Target Call Chain

```
command (run/create) → client.ResolveImage(ctx)  ← pure Docker + config
                         ├── client.findProjectImage(ctx)
                         └── resolveDefaultImage(cfg, settings)
                       → command checks result.Source == ImageSourceDefault
                       → client.ImageExists(ctx, ref)
                       → if missing: command handles prompt/build/save using its own IOStreams/Prompter
```

### Key Design Decisions

1. `docker.Client` receives `*config.Config` at construction. The factory already has dependency ordering (`clientFunc` runs after `configFunc`).
2. `ResolveImage` becomes a `Client` method — it uses internal config to get project name and settings.
3. `ResolveDefaultImage` stays a **package-level function** (pure config merge, no Docker, useful for tests).
4. `FindProjectImage` becomes an **unexported method** on `Client` (only used internally by `ResolveImage`).
5. `ResolveAndValidateImage`, `ImageValidationDeps`, `FlavorOption` are **deleted** from docker package.
6. The interactive rebuild flow (prompt → build → save settings) moves into a shared helper in `internal/cmd/container/opts/` or is inlined in `createRun`/`runRun`.
7. `NewClient` gains an optional `*config.Config` parameter. When nil (integration tests), resolution methods return nil/empty.
8. `NewFakeClient` gains a `WithConfig` option or similar for unit tests that need resolution.

### Files Changed

| File | Action |
|------|--------|
| `internal/docker/client.go` | Add `cfg *config.Config` field, update `NewClient` signature |
| `internal/docker/image_resolve.go` | Convert to Client methods, delete `ResolveAndValidateImage`/`ImageValidationDeps`/`FlavorOption` |
| `internal/cmd/factory/default.go` | Wire config into `clientFunc(f)` |
| `internal/cmd/container/run/run.go` | Replace `ResolveAndValidateImage` call with `client.ResolveImage` + local prompt logic |
| `internal/cmd/container/create/create.go` | Same as run.go |
| `internal/docker/image_resolve_test.go` | Update unit tests for method-based API |
| `internal/cmd/container/run/run_test.go` | Update `TestImageArg` |
| `internal/docker/dockertest/fake_client.go` | Support config injection |
| `test/harness/factory.go` | Update `NewTestFactory` client construction |
| `test/harness/docker.go` | Update `NewTestClient` |
| `test/internals/image_resolver_test.go` | Update integration tests |
| `test/internals/docker_client_test.go` | Update integration tests |
| Documentation files | Update CLAUDE.md files |

---

## Task 1: Add config field to docker.Client and update constructor

**Files:**
- Modify: `internal/docker/client.go:18-39`
- Modify: `internal/cmd/factory/default.go:72-88`

**Step 1: Update `Client` struct to hold config**

In `internal/docker/client.go`, add a `cfg` field:

```go
type Client struct {
	*whail.Engine
	cfg *config.Config // lazily provides Project() and Settings() for image resolution
}
```

Add import for `"github.com/schmitthub/clawker/internal/config"`.

**Step 2: Update `NewClient` to accept optional config**

```go
// NewClient creates a new clawker Docker client.
// It configures the whail.Engine with clawker's label prefix and conventions.
// The optional config parameter enables image resolution methods.
func NewClient(ctx context.Context, cfg *config.Config) (*Client, error) {
	opts := whail.EngineOptions{
		LabelPrefix:  EngineLabelPrefix,
		ManagedLabel: EngineManagedLabel,
	}

	engine, err := whail.NewWithOptions(ctx, opts)
	if err != nil {
		return nil, err
	}

	return &Client{Engine: engine, cfg: cfg}, nil
}
```

**Step 3: Update all `NewClient` call sites to pass config or nil**

There are 6 call sites that need `nil` added as second arg (these don't need resolution):

- `internal/cmd/factory/default.go:80` — will be updated properly in Step 4
- `test/harness/docker.go:106` — pass `nil`
- `test/harness/factory.go:27` — pass `nil` (for now; Task 6 updates this)
- `test/internals/docker_client_test.go:35,61,81,173` — pass `nil`
- `test/internals/image_resolver_test.go:62` — pass `nil` (for now; Task 5 updates this)

**Step 4: Wire config into factory's `clientFunc`**

In `internal/cmd/factory/default.go`, update `clientFunc` to accept `f` and close over config:

```go
func clientFunc(f *cmdutil.Factory) func(context.Context) (*docker.Client, error) {
	var (
		once      sync.Once
		client    *docker.Client
		clientErr error
	)
	return func(ctx context.Context) (*docker.Client, error) {
		once.Do(func() {
			client, clientErr = docker.NewClient(ctx, f.Config())
			if clientErr == nil {
				docker.WireBuildKit(client)
			}
		})
		return client, clientErr
	}
}
```

Update the call in `New()`:

```go
f.Config = configFunc(f)     // depends on WorkDir
f.Client = clientFunc(f)     // depends on Config
```

**Step 5: Run tests to verify compilation and no regressions**

Run: `go build ./... && make test`
Expected: All pass (no behavior change yet, just plumbing)

**Step 6: Commit**

```bash
git add internal/docker/client.go internal/cmd/factory/default.go test/harness/docker.go test/harness/factory.go test/internals/docker_client_test.go test/internals/image_resolver_test.go
git commit -m "refactor(docker): add config to Client, wire through factory"
```

---

## Task 2: Convert image resolution functions to Client methods

**Files:**
- Modify: `internal/docker/image_resolve.go`
- Modify: `internal/docker/image_resolve_test.go`

**Step 1: Convert `FindProjectImage` to unexported Client method**

Replace the free function with a method. It no longer needs `dockerClient` or `project` params — it gets them from `c.cfg`:

```go
// findProjectImage searches for a clawker-managed image matching the project label
// with the :latest tag. Returns the image reference (name:tag) if found,
// or empty string if not found.
func (c *Client) findProjectImage(ctx context.Context) (string, error) {
	if c.cfg == nil {
		return "", nil
	}

	cfg, err := c.cfg.Project()
	if err != nil || cfg == nil || cfg.Project == "" {
		return "", nil
	}

	f := Filters{}.
		Add("label", LabelManaged+"="+ManagedLabelValue).
		Add("label", LabelProject+"="+cfg.Project)

	result, err := c.ImageList(ctx, ImageListOptions{
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list images: %w", err)
	}

	for _, img := range result.Items {
		for _, tag := range img.RepoTags {
			if strings.HasSuffix(tag, ":latest") {
				return tag, nil
			}
		}
	}

	return "", nil
}
```

**Step 2: Convert `ResolveImageWithSource` to Client method**

```go
// ResolveImageWithSource resolves the image to use for container operations.
// Resolution order:
// 1. Project image with :latest tag (by label lookup)
// 2. Merged default_image from config/settings
//
// Returns nil if no image could be resolved (caller decides what to do).
func (c *Client) ResolveImageWithSource(ctx context.Context) (*ResolvedImage, error) {
	if c.cfg == nil {
		return nil, nil
	}

	// 1. Try to find a project image with :latest tag
	projectImage, err := c.findProjectImage(ctx)
	if err != nil {
		logger.Debug().Err(err).Msg("failed to auto-detect project image")
	} else if projectImage != "" {
		return &ResolvedImage{Reference: projectImage, Source: ImageSourceProject}, nil
	}

	// 2. Try merged default_image from config/settings
	cfg, _ := c.cfg.Project()
	settings, _ := c.cfg.Settings()
	if defaultImage := ResolveDefaultImage(cfg, settings); defaultImage != "" {
		return &ResolvedImage{Reference: defaultImage, Source: ImageSourceDefault}, nil
	}

	return nil, nil
}
```

**Step 3: Convert `ResolveImage` to Client method**

```go
// ResolveImage resolves the image reference to use.
// Returns empty string if no image could be resolved.
func (c *Client) ResolveImage(ctx context.Context) (string, error) {
	result, err := c.ResolveImageWithSource(ctx)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.Reference, nil
}
```

**Step 4: Keep `ResolveDefaultImage` as package-level function (unchanged)**

It's pure config merging with no Docker dependency. Leave it where it is.

**Step 5: Update unit tests in `image_resolve_test.go`**

The tests currently call free functions. Update them to use Client methods:

- `TestResolveDefaultImage` — unchanged (still a package function)
- `TestResolveImage_FallbackToDefault` — needs a Client with config
- `TestResolveImage_NilParameters` — needs a Client with nil config
- `TestFindProjectImage_NilClient` — becomes test of nil-config Client
- `TestFindProjectImage_EmptyProject` — becomes test with empty project in config
- `TestResolveImageWithSource_NoDocker` — update to method calls

For tests that need a Client without a real Docker connection, create a nil-config Client:

```go
// For tests that only exercise config-based resolution (no Docker calls):
client := &Client{} // nil Engine is fine — findProjectImage returns "" when cfg is nil
result, err := client.ResolveImageWithSource(ctx)
```

For tests that need fake Docker responses, use `dockertest.NewFakeClient()`:

```go
fake := dockertest.NewFakeClient()
// Set config on the fake client
fake.Client.SetConfig(cfg) // we'll add this accessor in the same task
result, err := fake.Client.ResolveImageWithSource(ctx)
```

Add a `SetConfig` method to `Client` for test injection:

```go
// SetConfig sets the config gateway on the client. Intended for tests.
func (c *Client) SetConfig(cfg *config.Config) {
	c.cfg = cfg
}
```

**Step 6: Run tests**

Run: `go test ./internal/docker/... -v -run "TestResolve|TestFind"`
Expected: All pass

**Step 7: Commit**

```bash
git add internal/docker/image_resolve.go internal/docker/image_resolve_test.go internal/docker/client.go
git commit -m "refactor(docker): convert image resolution to Client methods"
```

---

## Task 3: Delete ResolveAndValidateImage and related types from docker package

**Files:**
- Modify: `internal/docker/image_resolve.go`

**Step 1: Delete `ImageValidationDeps` struct**

Remove the entire struct definition (currently lines 127-145).

**Step 2: Delete `FlavorOption` struct**

Remove the struct definition (currently lines 30-33).

**Step 3: Delete `ResolveAndValidateImage` function**

Remove the entire function (currently lines 153-269).

**Step 4: Remove unused imports**

After deletions, remove imports that are no longer needed:
- `"github.com/schmitthub/clawker/internal/iostreams"`
- `prompterpkg "github.com/schmitthub/clawker/internal/prompter"`

The file should retain:
- `"context"`, `"fmt"`, `"strings"`
- `"github.com/schmitthub/clawker/internal/config"`
- `"github.com/schmitthub/clawker/internal/logger"`

**Step 5: Verify the docker package compiles**

Run: `go build ./internal/docker/...`
Expected: Compile errors in downstream consumers (run.go, create.go) — that's expected, we fix them in Task 4.

**Step 6: Commit (allow broken downstream)**

```bash
git add internal/docker/image_resolve.go
git commit -m "refactor(docker): delete ResolveAndValidateImage and UI types"
```

---

## Task 4: Move interactive rebuild logic to command layer

**Files:**
- Modify: `internal/cmd/container/run/run.go`
- Modify: `internal/cmd/container/create/create.go`
- Modify: `internal/cmd/container/run/run_test.go`

This is the core behavioral change. The command layer now:
1. Calls `client.ResolveImageWithSource(ctx)` (pure Docker)
2. If result is nil → error "no image"
3. If result.Source is Default → check `client.ImageExists` → if missing, prompt user

**Step 1: Update `runRun` image resolution block**

Replace the `if containerOpts.Image == "@"` block in `run.go` (currently ~lines 160-188) with:

```go
	// Resolve image name
	if containerOpts.Image == "@" {
		resolvedImage, err := client.ResolveImageWithSource(ctx)
		if err != nil {
			return err
		}
		if resolvedImage == nil {
			cmdutil.PrintError(ios, "No image specified and no default image configured")
			cmdutil.PrintNextSteps(ios,
				"Specify an image: clawker container run IMAGE",
				"Set default_image in clawker.yaml",
				"Set default_image in ~/.local/clawker/settings.yaml",
				"Build a project image: clawker build",
			)
			return fmt.Errorf("no image specified")
		}

		// For default images, verify the image exists and offer to rebuild if missing
		if resolvedImage.Source == docker.ImageSourceDefault {
			exists, err := client.ImageExists(ctx, resolvedImage.Reference)
			if err != nil {
				logger.Debug().Err(err).Str("image", resolvedImage.Reference).Msg("failed to check if image exists")
				// Proceed — Docker will error during run if image doesn't exist
			} else if !exists {
				if err := handleMissingDefaultImage(ctx, opts, cfgGateway, resolvedImage.Reference); err != nil {
					return err
				}
			}
		}

		containerOpts.Image = resolvedImage.Reference
	}
```

**Step 2: Add `handleMissingDefaultImage` helper in `run.go`**

This contains the interactive rebuild logic that was in `ResolveAndValidateImage`:

```go
// handleMissingDefaultImage prompts the user to rebuild a missing default image.
// In non-interactive mode, it prints instructions and returns an error.
func handleMissingDefaultImage(ctx context.Context, opts *RunOptions, cfgGateway *config.Config, imageRef string) error {
	ios := opts.IOStreams

	if !ios.IsInteractive() {
		cmdutil.PrintError(ios, "Default image %q not found", imageRef)
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to rebuild the base image",
			"Or specify an image explicitly: clawker run IMAGE",
			"Or build a project image: clawker build",
		)
		return fmt.Errorf("default image %q not found", imageRef)
	}

	// Interactive mode — prompt to rebuild
	p := opts.Prompter()
	options := []prompter.SelectOption{
		{Label: "Yes", Description: "Rebuild the default base image now"},
		{Label: "No", Description: "Cancel and fix manually"},
	}

	idx, err := p.Select(
		fmt.Sprintf("Default image %q not found. Rebuild now?", imageRef),
		options,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to prompt for rebuild: %w", err)
	}

	if idx != 0 {
		cmdutil.PrintNextSteps(ios,
			"Run 'clawker init' to rebuild the base image",
			"Or specify an image explicitly: clawker run IMAGE",
			"Or build a project image: clawker build",
		)
		return fmt.Errorf("default image %q not found", imageRef)
	}

	// Get flavor selection
	flavors := intbuild.DefaultFlavorOptions()
	flavorOptions := make([]prompter.SelectOption, len(flavors))
	for i, f := range flavors {
		flavorOptions[i] = prompter.SelectOption{
			Label:       f.Name,
			Description: f.Description,
		}
	}

	flavorIdx, err := p.Select("Select Linux flavor", flavorOptions, 0)
	if err != nil {
		return fmt.Errorf("failed to select flavor: %w", err)
	}

	selectedFlavor := flavors[flavorIdx].Name
	fmt.Fprintf(ios.ErrOut, "Building %s...\n", intbuild.DefaultImageTag)

	if err := intbuild.BuildDefaultImage(ctx, selectedFlavor); err != nil {
		fmt.Fprintf(ios.ErrOut, "Error: Failed to build image: %v\n", err)
		return fmt.Errorf("failed to rebuild default image: %w", err)
	}

	fmt.Fprintf(ios.ErrOut, "Build complete! Using image: %s\n", intbuild.DefaultImageTag)

	// Persist the default image in settings
	settingsLoader, err := cfgGateway.SettingsLoader()
	if err != nil {
		logger.Warn().Err(err).Msg("failed to load settings loader; default image will not be persisted")
	} else if settingsLoader != nil {
		currentSettings, loadErr := settingsLoader.Load()
		if loadErr != nil {
			logger.Warn().Err(loadErr).Msg("failed to load existing settings; skipping settings update")
		} else {
			currentSettings.DefaultImage = intbuild.DefaultImageTag
			if saveErr := settingsLoader.Save(currentSettings); saveErr != nil {
				logger.Warn().Err(saveErr).Msg("failed to update settings with default image")
			}
		}
	}

	return nil
}
```

**Step 3: Apply identical changes to `createRun` in `create.go`**

The `create.go` command has the same pattern. Extract a similar `handleMissingDefaultImage` helper (or if the signature is identical enough, consider a shared helper in `internal/cmd/container/opts/`).

Since `CreateOptions` and `RunOptions` have different types but both carry `IOStreams`, `Prompter`, and `Config()`, the cleanest approach is to duplicate the helper in each package. The function is ~50 lines and the alternative (shared interface + helper) adds more complexity than it saves for 2 call sites.

The `create.go` version has the same logic but uses `opts.Prompter` and `opts.Config()` from `CreateOptions`.

**Step 4: Remove unused imports from `run.go` and `create.go`**

After removing the `docker.ResolveAndValidateImage` and `docker.ImageValidationDeps` references:
- `run.go`: Remove `docker.FlavorOption` references if any remain. The `docker` import stays (used for labels, naming, etc.).
- `create.go`: Same treatment.

**Step 5: Update `TestImageArg` in `run_test.go`**

The "@ symbol resolution" sub-tests currently call `docker.ResolveImageWithSource(ctx, fake.Client, cfg, settings)` directly. Update them to use the Client method:

```go
// Build a config.Config gateway for the test
testCfg := config.NewConfigForTest(cfg, settings)
fake.Client.SetConfig(testCfg)

// Call the resolution method
result, err := fake.Client.ResolveImageWithSource(ctx)
```

This requires `config.NewConfigForTest` which already exists (from the factory refactor).

**Step 6: Remove the `settings` and `cfg` direct params from the test calls**

The tests no longer pass these explicitly — they're on the Client's config.

**Step 7: Run all unit tests**

Run: `make test`
Expected: All pass

**Step 8: Commit**

```bash
git add internal/cmd/container/run/run.go internal/cmd/container/create/create.go internal/cmd/container/run/run_test.go
git commit -m "refactor: move interactive image rebuild to command layer"
```

---

## Task 5: Update integration tests

**Files:**
- Modify: `test/internals/image_resolver_test.go`
- Modify: `test/internals/docker_client_test.go`

**Step 1: Update `TestFindProjectImage_Integration`**

This test calls `docker.FindProjectImage(ctx, state.dockerClient, projectName)` — a now-unexported method. The test needs to use `client.ResolveImageWithSource(ctx)` instead, or test via the public method that calls it.

Since `findProjectImage` is unexported, rewrite these tests to go through `ResolveImageWithSource`:

```go
// Set config on the test client
testCfg := config.NewConfigForTest(&config.Project{Project: state.projectName}, nil)
state.dockerClient.SetConfig(testCfg)

result, err := state.dockerClient.ResolveImageWithSource(ctx)
```

**Step 2: Update `TestResolveImageWithSource_ProjectImage`**

Same treatment — use Client method instead of free function.

**Step 3: Update `TestFindProjectImage_NoLatestTag`**

Same treatment.

**Step 4: Update `test/internals/docker_client_test.go`**

The `NewClient(ctx)` calls need updated to `NewClient(ctx, nil)`.

**Step 5: Run integration tests**

Run: `go test ./test/internals/... -v -timeout 10m`
Expected: All pass

**Step 6: Commit**

```bash
git add test/internals/image_resolver_test.go test/internals/docker_client_test.go
git commit -m "test: update integration tests for Client method-based image resolution"
```

---

## Task 6: Update test harness and dockertest fake

**Files:**
- Modify: `test/harness/factory.go`
- Modify: `test/harness/docker.go`
- Modify: `internal/docker/dockertest/fake_client.go`

**Step 1: Update `NewTestClient` in `test/harness/docker.go`**

```go
func NewTestClient(t *testing.T) *docker.Client {
	t.Helper()
	RequireDocker(t)

	ctx := context.Background()
	c, err := docker.NewClient(ctx, nil) // no config needed for harness tests
	if err != nil {
		t.Fatalf("failed to create docker client: %v", err)
	}
	// ...
}
```

**Step 2: Update `NewTestFactory` in `test/harness/factory.go`**

Pass config into the client:

```go
func NewTestFactory(t *testing.T, h *Harness) (*cmdutil.Factory, *iostreams.TestIOStreams) {
	t.Helper()
	tio := iostreams.NewTestIOStreams()
	cfg := config.NewConfig(func() string { return h.ProjectDir })
	f := &cmdutil.Factory{
		WorkDir:  func() string { return h.ProjectDir },
		IOStreams: tio.IOStreams,
		Client: func(ctx context.Context) (*docker.Client, error) {
			c, err := docker.NewClient(ctx, cfg)
			return c, err
		},
		Config: func() *config.Config {
			return cfg
		},
		HostProxy: func() *hostproxy.Manager {
			return hostproxy.NewManager()
		},
		Prompter: func() *prompter.Prompter { return nil },
	}
	return f, tio
}
```

**Step 3: Add config support to `NewFakeClient`**

Add an option for injecting config:

```go
// FakeClientOption configures a FakeClient.
type FakeClientOption func(*FakeClient)

// WithConfig sets a config gateway on the fake client for image resolution tests.
func WithConfig(cfg *config.Config) FakeClientOption {
	return func(fc *FakeClient) {
		fc.Client.SetConfig(cfg)
	}
}

func NewFakeClient(opts ...FakeClientOption) *FakeClient {
	// ... existing construction ...

	fc := &FakeClient{
		Client:  client,
		FakeAPI: fakeAPI,
	}

	for _, opt := range opts {
		opt(fc)
	}

	return fc
}
```

**Step 4: Update all `NewFakeClient()` call sites**

Since we added variadic opts, existing callers (`NewFakeClient()`) continue to work with no changes.

**Step 5: Run all tests**

Run: `make test`
Expected: All pass

**Step 6: Commit**

```bash
git add test/harness/factory.go test/harness/docker.go internal/docker/dockertest/fake_client.go
git commit -m "test: update harness and fakes for config-aware Client"
```

---

## Task 7: Update documentation

**Files:**
- Modify: `internal/docker/CLAUDE.md`
- Modify: `internal/cmd/factory/CLAUDE.md`
- Modify: `CLAUDE.md` (root, if needed)
- Modify: `.serena/memories/factory-refactor-plan.md` (mark complete)

**Step 1: Update `internal/docker/CLAUDE.md`**

- Update Client struct documentation to show `cfg *config.Config` field
- Update `NewClient` signature to show `cfg` parameter
- Document `ResolveImage`, `ResolveImageWithSource` as Client methods
- Document `SetConfig` as test helper
- Remove `ImageValidationDeps`, `FlavorOption`, `ResolveAndValidateImage` references
- Note that `FindProjectImage` is now unexported (`findProjectImage`)
- `ResolveDefaultImage` stays as package-level function

**Step 2: Update `internal/cmd/factory/CLAUDE.md`**

- Update `clientFunc` documentation to note it takes `f` and closes over config
- Note dependency: `clientFunc(f)` depends on `f.Config`

**Step 3: Run documentation freshness check**

Run: `bash scripts/check-claude-freshness.sh`

**Step 4: Commit**

```bash
git add internal/docker/CLAUDE.md internal/cmd/factory/CLAUDE.md
git commit -m "docs: update CLAUDE.md files for image resolution refactor"
```

---

## Verification Checklist

After all tasks:

```bash
# Unit tests
make test

# Integration tests (Docker required)
go test ./test/internals/... -v -timeout 10m
go test ./test/commands/... -v -timeout 10m
go test ./test/cli/... -v -timeout 15m

# Build
go build ./...

# Doc freshness
bash scripts/check-claude-freshness.sh
```
