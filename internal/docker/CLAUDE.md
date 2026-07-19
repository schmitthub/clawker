# Docker Client Package

Clawker-specific Docker middleware wrapping `pkg/whail.Engine` with labels, naming conventions, and image building orchestration.

## PTYHandler (`pty.go`)

Full terminal session lifecycle for interactive container sessions. `NewPTYHandler() *PTYHandler`.

| Method | Purpose |
|--------|---------|
| `Setup()` | Enable raw mode on stdin |
| `Restore()` | Reset visual state (ANSI) + restore termios. Unconditionally disables the input/visual modes an in-container TUI enables but can't undo on an abrupt end (Ctrl-P+Q detach / kill): mouse tracking (`?1000/1002/1003/1006l`), bracketed paste (`?2004l`), focus reporting (`?1004l`), show cursor, SGR/charset reset — all idempotent, no side effects. The alt-screen leave (`?1049l`) is the lone exception: gated on `containerInAltScreen` because its DECRC cursor-restore squashes primary-screen output when emitted blind. `restoreSequence(inAlt)` is the pure decision (unit-tested); the scanner tracks alt-screen enter/leave in the output copy. |
| `Stream(ctx, hijacked)` | Bidirectional I/O (stdin→conn, conn→stdout) |
| `StreamWithResize(ctx, hijacked, resizeFunc)` | Stream + resize propagation |
| `GetSize()` | Returns (width, height, err) |
| `IsTerminal()` | TTY detection |

**Dependencies**: `internal/term` (RawMode), `internal/signals` (ResizeHandler). **Consumers**: container `run`, `start`, `attach`, `exec`.

## Naming Convention

- **3-segment** (project-scoped agent): `clawker.project.agent` — **2-segment** (global-scope agent, no project namespace): `clawker.agent`
- **Volumes**: infrastructure volumes `clawker.project.agent-purpose` (workspace, history); harness-scoped volumes `clawker.project.agent-harness.name` (bundle-declared persisted dirs + the clawker lifecycle volume) — the harness segment is the harness's exact selection spelling (bare name, or the qualified `namespace.bundle.component` address for an installed-bundle harness) and keeps two harnesses that declare the same volume name (both shipped harnesses declare `config`) from ever landing on one volume
- **Network**: from `config.Config.ClawkerNetwork()` (no constant in this package)

Functions: `ValidateResourceName(name) error`, `ContainerName(project, agent) (string, error)`, `VolumeName(project, agent, purpose) (string, error)`, `HarnessVolumeName(project, agent, harness, volume) (string, error)`, `ContainerNamesFromAgents(project, agents) ([]string, error)`, `ContainerNamePrefix`, `ImageTag`, `GenerateRandomName`. Constants: `NamePrefix = "clawker"`.

**Validation**: `ValidateResourceName` validates user-sourced inputs (agent, project names) against Docker's container name rules: `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`. No length cap is enforced (Docker imposes none at the engine level). Built into `ContainerName` and `VolumeName` — callers cannot bypass validation. Internal `purpose` strings (`"history"`, `"workspace"`) are not validated. `HarnessVolumeName` validates the harness segment against `consts.ValidateHarnessRef` (bare OR qualified selection spelling) and the volume segment against `consts.ValidateName`, joining them via `consts.JoinIdentity`. That pairing keeps the composition injective **for a fixed (project, agent) pair**: every token is dot-free, so the joined purpose has exactly one dot (bare harness) or three (qualified), and splitting recovers the pair. The proof does not extend across agents — agents join the harness with `-` and both allow interior hyphens, so agent `dev` + harness `my-fork` aliases agent `dev-my` + harness `fork`; that cross-agent case necessarily carries different harness labels and is refused by `EnsureHarnessVolume`'s ownership check (same ambiguity existed under the flat scheme).

## Labels

All label keys come from `config.Config` interface methods (`LabelManaged()`, `LabelProject()`, etc.). No label constants are exported from this package — callers use `(*Client)` methods which read keys from `c.cfg`.

**Client methods** (all on `*Client`): `ContainerLabels(project, agent, version, image, workdir)`, `AgentVolumeLabels(project, agent)`, `HarnessVolumeLabels(project, agent, harness)`, `ImageLabels(project, version)`, `NetworkLabels()`. `AgentVolumeLabels` always sets `purpose=PurposeAgent`; the per-volume role lives in the volume name suffix, not the label. `HarnessVolumeLabels` is the agent volume labels plus `consts.LabelHarness` — used for harness-scoped volumes (bundle-declared dirs + clawker lifecycle volume) so label-based agent cleanup still finds them.

**Filters** (all on `*Client`): `ClawkerFilter()`, `ProjectFilter(project)`, `AgentFilter(project, agent)` — return `whail.Filters`.

## Client (`client.go`)

```go
func NewClient(ctx context.Context, cfg config.Config, log *logger.Logger, opts ...ClientOption) (*Client, error)
func NewClientFromEngine(engine *whail.Engine, cfg config.Config, log *logger.Logger) *Client  // test constructor
type ClientOption func(*clientOptions)    // WithLabels(whail.LabelConfig)
```

`Client` embeds `*whail.Engine`. Fields: `cfg config.Config` (interface, always set), `ChownImage string`.

**Image methods**: `Close()`, `ResolveImageWithSource(ctx, projectName)`, `BuildImage(ctx, reader, opts)`, `ImageExists(ctx, ref)`.

### Container type

```go
type Container struct {
    ID, Name, Project, Agent, Image, Workdir, Status string; Created int64
}
```

### Container query/management methods (all on `*Client`)

| Method | Signature |
|--------|-----------|
| `IsMonitoringActive` | `(ctx context.Context) bool` — checks for otel-collector on the clawker network |
| `ListContainers` | `(ctx context.Context, includeAll bool) ([]Container, error)` — all managed containers |
| `ListContainersByProject` | `(ctx context.Context, project string, includeAll bool) ([]Container, error)` — project-scoped |
| `FindContainerByAgent` | `(ctx context.Context, project, agent string) (string, *container.Summary, error)` — returns (name, summary, err); not-found = `(name, nil, nil)` |
| `RemoveContainerWithVolumes` | `(ctx context.Context, containerID string, force bool) error` — stops + removes container + associated volumes |

**Image resolution**: `ImageSource` enum (`Project`/`Global`). `ResolvedImage` struct (Reference + Source). `ResolveImageWithSource(ctx, projectName)` is scope-keyed: project scope (non-empty `projectName`) looks up Docker images matching the project label with `:latest` tag → `ImageSourceProject`; global scope (empty `projectName`) looks up the clawker-managed global image (`ImageTag("")`, managed filter + reference match — global images intentionally carry no project label) → `ImageSourceGlobal`. Returns `nil, nil` when no built image exists for the scope. Scopes do not ladder (a project with no built image never resolves the global image), and there is deliberately no fallback to `cfg.Project().Build.Image` — that is a bare base image, never runnable as an agent. `projectName` is the resolved project identity (from `project.ProjectManager.CurrentProject(ctx).Name()` at the command layer); empty string means no registered project.

## Builder (`builder.go`)

`NewBuilder(cli *Client, cfg *config.Project, workDir, projectName string)`. `Build(ctx, tag, opts)` is **two-phase**: it first ensures the per-project shared base image (`BaseImageTag(project)` = `clawker-<project>:base`) exists and is fresh — comparing `bundler.BaseContentHash` against the image's `consts.LabelBaseContentHash` label, rebuilding on miss/drift or `--no-cache` — then builds the harness image `FROM` it. Base failure aborts before the harness build. `--pull` applies to the base build only (the harness parent is the local-only `:base` tag). `OnComplete` fires only for the harness build (`--iidfile` = runnable image). Base labels: `ImageLabels` + content hash + `LabelPurpose=PurposeBaseImage`, never user labels or `LabelHarness`; the harness image also records the base content hash. Legacy-stream progress events from the base build are namespaced via `phaseProgress` (`base:` StepID prefix, `[base]` StepName prefix; `[internal]` steps left intact for downstream filtering). In-image layer cache invalidation stays delegated to the daemon-side builder (BuildKit layer cache or classic `probeCache`). `BuilderOptions`: `NoCache/Pull/SuppressOutput/BuildKitEnabled`, `Labels/Target/NetworkMode/BuildArgs/Tags/OnProgress/OnComplete/HarnessVersion/HarnessName`.

## Test Labels (`defaults.go`)

`TestLabelConfig(cfg config.Config, testName ...string) whail.LabelConfig` — test label set for `WithLabels` in test code so `CleanupTestResources` can find test-created resources.

## BuildKit (`buildkit.go`)

`Pinger` (type alias), `BuildKitEnabled(ctx, Pinger)`, `WireBuildKit(c *Client)`. Both `Pinger` and `BuildKitEnabled` deprecated — prefer `whail.*` directly.

## Environment (`env.go`)

`RuntimeEnv(opts RuntimeEnvOpts) ([]string, error)` — builds container env vars. Precedence: base → terminal → agent env → instruction env. Sorted by key. `Worktree: true` (linked-worktree workspace) adds `GOFLAGS=-buildvcs=false` — Go cannot stamp linked worktrees (its VCS walk skips the `.git` file and lands on the mounted main `.git`); user env overrides.

## Volume Utilities (`volume.go`)

`EnsureVolume(...)`, `EnsureHarnessVolume(...)`, `CopyToVolume(...)`, `LoadIgnorePatterns(path)`, `FindIgnoredDirs(hostPath, patterns)`. `EnsureHarnessVolume` is the ownership failsafe for harness-scoped volumes: an existing MANAGED volume whose `consts.LabelHarness` label names a different harness is refused with `*HarnessVolumeOwnershipError` (use `errors.As`); same-harness re-entry (container recreation, repeated run) adopts silently, and so does an unlabeled managed occupant — that population is hand-placed (e.g. backup/restore; clawker always labels harness-scoped volumes, flat pre-harness names are uncomposable here), refusing it would not stop deliberate placement (the label is forgeable by whoever creates the volume), and Docker cannot retro-label a local volume. Volumes lacking the managed label are invisible to whail's label-scoped inspect and outside the check. CopyToVolume uses two-phase ownership fix: tar headers with UID/GID 1001 + post-copy chown via `Client.ChownImage` (defaults to a busybox image when unset); set `Client.ChownImage` to override.

Ignore matching uses **.gitignore semantics** via `go-git`'s `plumbing/format/gitignore` (`compileIgnorePatterns`): anchoring (leading/middle `/` pins to the workspace root; unanchored patterns match at any depth, so `build/` also matches `internal/build`), directory-only trailing `/`, negation (`!pattern`), and `**` globs. Malformed globs never error — like git, they just don't match.

`FindIgnoredDirs` walks a host directory and returns relative paths of directories matching ignore patterns. Used by bind mode to generate tmpfs overlay mounts. Key differences from the snapshot copy path: only returns directories, force-keeps `.git/` even if a pattern would match it (bind mode needs git for live development), and skips recursion into matched directories — so, as in gitignore, a path under an ignored directory cannot be re-included by a negation.

**Snapshot `.git` handling**: the snapshot copy path honors `.clawkerignore` patterns verbatim and has **no** hardcoded `.git` skip — `.git` is copied into the ephemeral volume by default (snapshot's isolation comes from the copy-to-volume direction, not from withholding git history), and is excluded only if a pattern explicitly matches it.

`BindOverlayDirsFromPatterns(patterns) []string` — derives directory overlay targets from ignore patterns for bind mode. Only returns deterministic directory paths, skips file-glob patterns; a leading `**/` is stripped first (the workspace-root instance is deterministic, and must be masked even before it exists on the host so container-created dirs don't write through the bind mount), and candidates are re-checked against the full pattern list so a negation removes them.

## Opts Types (`opts.go`)

`MemBytes`, `MemSwapBytes`, `NanoCPUs` (pflag.Value). Container options: `UlimitOpt`, `WeightDeviceOpt`, `ThrottleDeviceOpt`, `GpuOpts`, `MountOpt`, `DeviceOpt`. Constructors: `NewUlimitOpt`, `NewWeightDeviceOpt`, `NewThrottleDeviceOpt`, `NewGpuOpts`, `NewMountOpt`, `NewDeviceOpt`. `ParseCPUs(value) (int64, error)`.

## Type Re-exports (`types.go`)

Re-exports ~37 Docker types from whail. Key groups: container/exec options, image options/results, volume/network options, copy options, resource management, wait conditions. Also re-exports the `ErrNotManaged` sentinel (managed-label jail refusal; a NotFound during the managed check collapses to it) so commands can `errors.Is`-match without importing whail.

## Testing (`mocks/`)

`NewFakeClient(cfg config.Config, opts ...FakeClientOption)` — function-field fake backed by `whailtest.FakeAPIClient`. Config is required as first param (used for label keys and engine options). `FakeClient.Cfg` field stores the config for test assertions.

Standalone fixture functions (`ContainerFixture`, `RunningContainerFixture`) use a package-level `defaultCfg = configmocks.NewBlankConfig()` to avoid cascading cfg params to every caller.

**Fixtures**: `ContainerFixture()`, `RunningContainerFixture()`, `ImageSummaryFixture()`, `MinimalCreateOpts()`, `MinimalStartOpts()`, `BuildKitBuildOpts()`

**Assertions**: `AssertCalled(t, method)`, `AssertNotCalled(t, method)`, `AssertCalledN(t, method, n)`, `Reset()`

**Setup helpers** (all on `*FakeClient`):
- **Container lifecycle**: `SetupContainerCreate/Start/Stop/Kill/Pause/Unpause/Rename/Restart/Update/Remove`
- **Container I/O**: `SetupContainerResize/Attach/Wait(exitCode)/Inspect(id, summary)/InspectReapState(autoRemove, running)/Logs(logs)/Top(titles, processes)/Stats(json)`
- **Exec**: `SetupExecCreate(execID)/ExecStart/ExecAttach/ExecAttachWithOutput(data)/ExecInspect`
- **Copy**: `SetupCopyToContainer/CopyFromContainer`
- **Volumes/Networks**: `SetupVolumeExists/VolumeCreate/NetworkExists/NetworkCreate`
- **BuildKit**: `SetupBuildKit/BuildKitWithProgress(events)/BuildKitWithRecordedProgress(events)/PingBuildKit/LegacyBuild/LegacyBuildError`
- **Query**: `SetupFindContainer/ImageExists/ImageList/SetupContainerListError`

## Gotchas

- **`cfg` is unexported** — `Client.cfg` is a private field. Production code uses `NewClient(ctx, cfg, log, opts...)`. Test code in other packages uses `NewClientFromEngine(engine, cfg, log)` or `mocks.NewFakeClient(cfg)`.
- **No label constants exported** — all label keys come from `config.Config` methods. External packages that need label keys must hold a `config.Config` reference.
- **`parseContainers` is a Client method** — it needs `c.cfg` for label keys when parsing container summaries.
- **LSP false positives** — gopls reports false "no field or method" errors on `config.Config` interface and false "copylocks" warnings. These are stale LSP cache issues — the real compiler (`go build`) is authoritative.
- **`NewFakeClient` requires cfg** — Signature is `NewFakeClient(cfg config.Config, opts ...FakeClientOption)`. All callers pass `configmocks.NewBlankConfig()` as first arg (`import configmocks "github.com/schmitthub/clawker/internal/config/mocks"`). There is no `WithConfig` option.

