# Bundler Package

Leaf package: Dockerfile generation, version management, content hashing, and build configuration for clawker container images. Imports `internal/hostproxy/internals` for container-side scripts (embed-only leaf). **No `internal/docker` import** — building orchestration (`Builder`, `EnsureImage`, `Build`) lives in `internal/docker`.

## Key Files

| File | Purpose |
|------|---------|
| `hash.go` | Content-addressed hashing for Dockerfile + includes |
| `defaults.go` | Flavor selection (`FlavorOption`, `DefaultFlavorOptions`, `FlavorToImage`) |
| `dockerfile.go` | Dockerfile templates, context generation, project scaffolding |
| `config.go` | Variant configuration (Debian/Alpine) |
| `versions.go` | Claude Code version resolution via npm registry |
| `errors.go` | Error types (`NetworkError`, `RegistryError`, `ErrVersionNotFound`, etc.) |

## Subpackages

| Package | Purpose |
|---------|---------|
| `semver/` | Semantic version parsing, comparison, sorting, matching |
| `registry/` | npm registry client, version info types, fetcher interface |
| `assets/` | Dockerfile template, entrypoint, firewall, statusline scripts |

## Content Hashing (`hash.go`)

```go
func ContentHash(dockerfile []byte, includes []string, workDir string, embeddedScripts []string) (string, error)
func EmbeddedScripts() []string  // Returns all embedded script contents for hashing
```

SHA-256 of rendered Dockerfile + sorted include file contents + embedded scripts. Returns 12-char hex prefix. Images tagged `clawker-<project>:sha-<hash>` with `:latest` aliased.

`EmbeddedScripts()` dynamically discovers all embedded assets via `embed.FS` (bundler/assets/) plus `internals.AllScripts()` (hostproxy scripts). Scripts are sorted for deterministic hashing. New scripts added to either location are automatically included without manual list maintenance.

**Stability guarantee:** Dockerfile only contains structural instructions (FROM, RUN, COPY, USER, WORKDIR, ARG). Config-dependent values injected at container creation time or via Docker build API.

**BuildKit vs Legacy:** `BuildKitEnabled=true` emits `--mount=type=cache` directives. Different builders produce different hashes (correct behavior). The flag flows through `DockerfileContext`, `ProjectGenerator`, and `DockerfileManager`.

## Flavor Utilities (`defaults.go`)

```go
type FlavorOption struct { Name, Description string }
func DefaultFlavorOptions() []FlavorOption  // bookworm, trixie, alpine3.22, alpine3.23
func FlavorToImage(flavor string) string    // Maps flavor to base image; unknown pass through
```

Note: `DefaultImageTag` constant and `BuildDefaultImage` function have moved to `internal/docker/defaults.go`.

## Dockerfile Generation (`dockerfile.go`)

### DockerfileManager -- multi-version/variant matrix builds

```go
type DockerFileManagerOptions struct { OutputDir string; VariantCfg *VariantConfig }
func NewDockerfileManager(cfg config.Config, opts *DockerFileManagerOptions) *DockerfileManager
func (m *DockerfileManager) GenerateDockerfiles(versions *registry.VersionsFile) error
func (m *DockerfileManager) DockerfilesDir() string  // outputDir/dockerfiles
```

`cfg config.Config` (interface) provides `DockerfilesSubdir()`, `MonitoringConfig()`, `ContainerUID()`, `ContainerGID()`. `BuildKitEnabled` field controls cache mount emission. Writes scripts once, renders Dockerfile per version/variant.

### ProjectGenerator -- single project builds from clawker.yaml

```go
func NewProjectGenerator(cfg config.Config, workDir string) *ProjectGenerator
func (g *ProjectGenerator) Generate() ([]byte, error)                                  // Render Dockerfile
func (g *ProjectGenerator) GenerateBuildContext() (io.Reader, error)                   // Tar archive (legacy)
func (g *ProjectGenerator) GenerateBuildContextFromDockerfile(dockerfile []byte) (io.Reader, error)
func (g *ProjectGenerator) WriteBuildContextToDir(dir string, dockerfile []byte) error // Filesystem (BuildKit)
func (g *ProjectGenerator) UseCustomDockerfile() bool
func (g *ProjectGenerator) GetCustomDockerfilePath() string
func (g *ProjectGenerator) GetBuildContext() string
func CreateBuildContextFromDir(dir, dockerfilePath string) (io.Reader, error)  // Tar from directory
```

`cfg config.Config` (interface) — replaces old `*config.Config` struct pointer. `BuildKitEnabled` field mirrors DockerfileManager. `WriteBuildContextToDir` for BuildKit's fsutil mount; `GenerateBuildContextFromDockerfile` for legacy tar stream.

### DockerfileContext -- template data

```go
type DockerfileContext struct {
    BaseImage, Username, Shell, WorkspacePath, ClaudeVersion string
    Packages []string; UID, GID int; IsAlpine, BuildKitEnabled bool
    Instructions *DockerfileInstructions; Inject *DockerfileInject
    // OTEL telemetry — from config.MonitoringConfig
    OtelMetricsEndpoint, OtelLogsEndpoint string
    OtelLogsExportInterval, OtelMetricExportInterval int
    OtelLogToolDetails, OtelLogUserPrompts, OtelIncludeAccountUUID, OtelIncludeSessionID bool
}
```

### Dockerfile Instruction Types

```go
type DockerfileInstructions struct { Copy []CopyInstruction; Args []ArgInstruction; UserRun, RootRun []RunInstruction }
type DockerfileInject struct { AfterFrom, AfterPackages, AfterUserSetup, AfterUserSwitch, AfterClaudeInstall, BeforeEntrypoint []string }
type CopyInstruction struct { Src, Dest, Chown, Chmod string }
type ArgInstruction struct { Name, Default string }
type RunInstruction struct { Cmd, Alpine, Debian string }  // OS-variant aware RUN
```

### Helpers

```go
func otelBaseEndpoint(mon config.MonitoringConfig) string  // Constructs OTEL base URL from config components
```

Falls back from `mon.OtelCollectorEndpoint` (explicit override) to `http://<OtelCollectorInternal>:<OtelCollectorPort>`.

### Constants and Embedded Assets

```go
const DefaultClaudeCodeVersion, DefaultUsername, DefaultShell = "latest", "claude", "/bin/zsh"
```

UID/GID come from `cfg.ContainerUID()` / `cfg.ContainerGID()` (no bundler-local constants).

Embedded: `DockerfileTemplate`, `EntrypointScript`, `FirewallScript`, `StatuslineScript`, `SettingsFile`, `HostOpenScript`, `CallbackForwarderSource`, `GitCredentialScript`, `SocketForwarderSource`.

## Version Management (`versions.go`)

```go
const ClaudeCodePackage = "@anthropic-ai/claude-code"
type ResolveOptions struct { Debug bool }
func NewVersionsManager() *VersionsManager
func NewVersionsManagerWithFetcher(fetcher registry.Fetcher, cfg *VariantConfig) *VersionsManager
func (m *VersionsManager) ResolveVersions(ctx, patterns, opts) (*registry.VersionsFile, error)
func LoadVersionsFile(path) (*registry.VersionsFile, error)
func SaveVersionsFile(path, vf) error
```

Patterns: `"latest"`, `"stable"`, `"next"` via dist-tags. `"2.1"` partial-matches highest `2.1.x`. `"2.1.2"` exact.

## Variant Configuration (`config.go`)

```go
type VariantConfig struct { DebianDefault, AlpineDefault string; Variants map[string][]string; Arches []string }
func DefaultVariantConfig() *VariantConfig   // trixie/alpine3.23, amd64+arm64v8
func (c *VariantConfig) IsAlpine(variant string) bool
func (c *VariantConfig) VariantNames() []string
```

## Subpackage: `semver/`

```go
type Version struct { Major, Minor, Patch int; Prerelease, Build, Original string }  // Minor/Patch=-1 if unset
func Parse(s string) (*Version, error)          // Partial: "2", "2.1", "2.1.3-beta+build"
func MustParse(s string) *Version               // Panics on error
func IsValid(s string) bool
func Compare(a, b *Version) int                 // -1/0/1; prereleases < releases
func Sort(vs []*Version); SortDesc(vs []*Version)
func SortStrings(vs []string) []string; SortStringsDesc(vs []string) []string  // Filter invalid
func Match(versions []string, target string) (string, bool)  // Best match for partial pattern
func FilterValid(versions []string) []string
```

`Version` methods: `HasMinor()`, `HasPatch()`, `HasPrerelease()`, `String()`.

## Subpackage: `registry/`

```go
type Fetcher interface { FetchVersions(ctx, pkg) ([]string, error); FetchDistTags(ctx, pkg) (DistTags, error) }
func NewNPMClient(opts ...Option) *NPMClient  // implements Fetcher
func WithHTTPClient(*http.Client) Option; WithBaseURL(string) Option; WithTimeout(time.Duration) Option
type DistTags map[string]string                         // "latest" -> "2.1.3"
type VersionInfo struct { FullVersion, DebianDefault, AlpineDefault string; Major, Minor, Patch int; ... }
type VersionsFile map[string]*VersionInfo               // Keys(), SortedKeys(), MarshalJSON()
type NPMPackageInfo struct { Name string; DistTags; Versions map[string]struct{} }
func NewVersionInfo(v *semver.Version, debianDefault, alpineDefault string, variants) *VersionInfo
```

## Error Types (`errors.go`)

```go
var ErrVersionNotFound, ErrInvalidVersion, ErrNoVersions error  // Re-exported from registry/
type NetworkError = registry.NetworkError   // { URL, Message, Err } -- Unwrap() supported
type RegistryError = registry.RegistryError // { Package, StatusCode, Message } -- IsNotFound() bool
```

## Dependencies

Imports: `internal/config`, `internal/bundler/registry`, `internal/bundler/semver`, `internal/hostproxy/internals` (embed-only). **Does NOT import `internal/docker`** — this is a leaf package.

## Tests

Unit tests: `dockerfile_test.go`, `build_test.go`, `hash_test.go`, `defaults_test.go`, `firewall_test.go`. Subpackage: `registry/npm_test.go`, `semver/semver_test.go`. Docker integration: `test/whail/`.

Test helper: `testConfig(t, yaml) config.Config` wraps `config.ReadFromString(yaml)` — preferred test double for bundler tests. All test configs use YAML fixtures rather than mock/fake constructors.
