# Bundler Package

Leaf package: Dockerfile generation, version management, and build configuration for clawker container images. Imports `internal/hostproxy/internals` for container-side scripts (embed-only leaf). **No `internal/docker` import** — building orchestration (`Builder`, `Build`) lives in `internal/docker`.

## Key Files

| File | Purpose |
|------|---------|
| `defaults.go` | Flavor selection (`FlavorOption`, `DefaultFlavorOptions`, `FlavorToImage`) |
| `dockerfile.go` | Dockerfile templates, context generation, project scaffolding |
| `config.go` | Variant configuration (Debian/Alpine) |
| `versions.go` | Claude Code version resolution via npm registry |
| `errors.go` | Error types (`NetworkError`, `RegistryError`, `ErrVersionNotFound`, etc.) |

## Subpackages

| Package | Purpose |
|---------|---------|
| `registry/` | npm registry client, version info types, fetcher interface |
| `assets/` | Dockerfile template, statusline script, claude config seeds, agent prompt (the clawkerd binary is imported from `clawkerd/embed`, not stored here) |

## Build Cache Strategy

Cache invalidation is delegated entirely to the builder (BuildKit's layer cache, or the classic builder's `probeCache` for legacy daemons) — both hash RUN/COPY inputs and skip identical steps automatically. The Dockerfile template's layer ordering (`internal/bundler/assets/Dockerfile.tmpl`) and ARG vs ENV choices control which layers invalidate when, but the cache mechanism itself is Docker's, not clawker's.

**Host-UID is baked into the rendered Dockerfile (Linux only).** `consts.ContainerUID()` / `ContainerGID()` resolve to the CLI invoker's `os.Getuid()` / `Getgid()` on Linux, so `useradd --uid {{.UID}}` in `Dockerfile.tmpl` varies per host user. On macOS/Windows, `consts.resolveProcessID` returns `fallbackContainerUID`/`GID` (1001) — Docker Desktop's virtiofs / gRPC-FUSE share masks UID/GID at the boundary, so baking the host UID would offer no access benefit and risks `groupadd --gid` collisions with base-image groups (e.g. macOS staff=20 vs Debian dialout=20). Required for the Linux `~/.claude/projects` bind-mount writability contract.

**BuildKit vs Legacy:** `BuildKitEnabled=true` emits `--mount=type=cache` directives in the rendered Dockerfile; legacy builder silently ignores them. The flag flows through `DockerfileContext`, `ProjectGenerator`, and `DockerfileManager`.

## Flavor Utilities (`defaults.go`)

```go
type FlavorOption struct { Name, Description string }
func DefaultFlavorOptions() []FlavorOption  // bookworm, trixie, alpine3.22, alpine3.23
func FlavorToImage(flavor string) string    // Maps flavor to base image; unknown pass through
```

`DefaultImageTag` constant and `BuildDefaultImage` function live in `internal/docker/defaults.go`, not here.

## Dockerfile Generation (`dockerfile.go`)

### DockerfileManager -- multi-version/variant matrix builds

```go
type DockerFileManagerOptions struct { VariantCfg *VariantConfig }
func NewDockerfileManager(cfg config.Config, opts *DockerFileManagerOptions) *DockerfileManager
func (m *DockerfileManager) GenerateDockerfiles(versions *registry.VersionsFile) error
func (m *DockerfileManager) DockerfilesDir() (string, error)  // delegates to cfg.DockerfilesSubdir()
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

`cfg config.Config` (interface). `BuildKitEnabled` field mirrors DockerfileManager. `WriteBuildContextToDir` for BuildKit's fsutil mount; `GenerateBuildContextFromDockerfile` for legacy tar stream.

**ProjectGenerator is a pure renderer** — it does not perform any network I/O. The Claude Code version that gets baked into the rendered `ARG CLAUDE_CODE_VERSION=<value>` default comes from the `ClaudeCodeVersion string` field on the generator. Callers (the build command) resolve via the command-layer factory and set the field before calling `Generate()`. Empty falls back to the literal `DefaultClaudeCodeVersion`:

```go
gen := bundler.NewProjectGenerator(cfg, workDir)
gen.ClaudeCodeVersion = "2.1.5"  // resolved by caller via bundler.ResolveLatestClaudeCodeVersion
df, _ := gen.Generate()
```

This separation keeps bundler hermetic in tests (no HTTP traffic on `Generate()`) and aligns with the repo's factory-DI pattern: HTTP-using dependencies live on the Factory (`f.HttpClient`), not buried inside leaf packages.

### Claude Code Version Resolution

```go
func ResolveLatestClaudeCodeVersion(ctx context.Context, httpClient *http.Client) (string, error)
```

Wraps `NewVersionsManagerWithFetcher` + `registry.NewNPMClient(registry.WithHTTPClient(httpClient))` + `ResolveVersions("latest")`. On resolution failure returns `(DefaultClaudeCodeVersion, err)` so callers can warn the user while still producing a usable rendered Dockerfile (the install RUN downloads npm-latest at build time when given the `"latest"` literal).

**Production wiring:** `internal/cmd/image/build/build.go` calls this once per build, passing `f.HttpClient()` from the Factory.

**Test wiring:** bundler tests use a package-local `stubRoundTripper` (implements `http.RoundTripper`) passed to `&http.Client{Transport: stubRT}`. `http.RoundTripper` is the stdlib mock seam; no project-defined interface required. Command-layer tests (in `internal/cmd/image/build/`) wire the same pattern through `cmdutil.Factory{HttpClient: ...}`.

### Claude Code Version Pinning (build-arg passthrough)

`Dockerfile.tmpl` declares `ARG CLAUDE_CODE_VERSION=<resolved-version>` — **not** `ENV`. Three properties this gives:

1. **ARG-cache mechanic:** the `ARG CLAUDE_CODE_VERSION` declaration is placed in the template **directly above its only consumer** (the Claude install RUN), NOT near the top of the final stage. Under BuildKit (Docker 23+ default) a changed ARG default busts the cache at the ARG's **declaration line**, not at first use — verified empirically, and contrary to the classic builder's documented "first usage, not definition" rule. So adjacency is load-bearing: a CC release rolls the rendered default and invalidates only the install layer + the late root block, leaving apt/apk + Node + git-delta + zsh-in-docker cached above. Hoisting the declaration upward would re-run that whole expensive chain on every CC release (which can be several a week). This applies to incremental cache reuse; `clawker build --no-cache` invalidates everything regardless of ARG positioning.
2. **Runtime invisibility:** Claude Code does not read `CLAUDE_CODE_VERSION` at runtime (verified against the official env-var list at code.claude.com/docs/en/settings). ARG is build-only, so the env var is naturally absent from the running container.
3. **User override:** `clawker build --build-arg CLAUDE_CODE_VERSION=2.1.4` pins the install to an explicit version, bypassing the npm resolution. Already wired through `internal/cmd/image/build/build.go`.

### DockerfileContext -- template data

```go
type DockerfileContext struct {
    BaseImage, Username, Shell, WorkspacePath, ClaudeVersion string
    Packages []string; UID, GID int; IsAlpine, BuildKitEnabled bool
    Instructions *DockerfileInstructions; Inject *DockerfileInject
    // OTEL telemetry — from config.MonitoringConfig
    OtelEndpoint string  // base URL only; SDK appends /v1/{metrics,logs,traces}. Traces ride the same base via OTEL_TRACES_EXPORTER=otlp + CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1, both hard-coded in Dockerfile.tmpl (not context fields).
    OtelLogsExportInterval, OtelMetricExportInterval int
    OtelLogToolDetails, OtelLogUserPrompts, OtelIncludeAccountUUID, OtelIncludeSessionID bool
    HasFirewallCA bool; GoBuilderImage string
}
```

`GoBuilderImage` is the Go toolchain image for builder stages, pinned to exact patch version + SHA digest (default: `DefaultGoBuilderImage`). Tracks `go.mod`.

### Baked-in Node.js Runtime (root `/usr/local` + user nvm)

Node + npm are baked into every generated image (Claude Code hooks `UserPromptSubmit`/`SessionStart` shell out to `node`). One Node install plus the nvm tool:

- **Root node on `/usr/local`** (the `Install Node.js` block, before the `root_run` render): lands `node`/`npm` on `/usr/local/bin`, overriding the base image's older Node. Serves clawkerd-as-PID-1, `root_run`/system steps, and the agent's Bash tool — all resolve `node` from the inherited `${PATH}` floor.
- **nvm** (installed after user creation): the version-switch tool. The user or `post_init` runs `nvm install`/`nvm use` to move onto another Node at runtime. On Alpine that source-compiles (no musl prebuilt), so the apk block bakes in the build toolchain (`g++`, `python3`, `linux-headers`, …).

- **Channel, not exact pin.** `ARG NODE_VERSION` (default `24`) names the LTS *line*, not a patch. The root install resolves the latest patch of that line per-build from `nodejs.org/dist/index.json` (`jq --arg ver "$NODE_VERSION"` — single-quoted `$NODE_VERSION` would NOT expand and silently resolve nothing), floating onto security patches on rebuild. Rationale (justified exception to clawker's pin-everything posture) lives in `docs/threat-model.mdx`. See [[feedback_pinning_policy_scope_is_clawker_artifacts_not_user_dockerfile]].
- **Own integrity check on Node, every path.** Not delegated to TLS alone. Debian: prebuilt tarball from nodejs.org/dist, GPG-verified via `SHASUMS256.txt.asc`. Alpine x86_64: musl prebuilt from unofficial-builds.nodejs.org, sha256-verified against that mirror's `SHASUMS256.txt` (unsigned upstream — that mirror's integrity level; do NOT hardcode a per-version checksum). Alpine other arches: source build, GPG-verified against nodejs.org `SHASUMS256.txt`. nvm itself is installed from its canonical installer and floated, not pinned — a `curl`-fetched tool is invisible to Dependabot (cf. CVE-2026-10796, nvm ≤0.40.4 RCE; a pre-fix pin would have stranded every image); nvm checksums the Node tarballs it downloads.
- **Managed-settings PATH is `.local/bin` + `${PATH}`.** `.local/bin` holds the `claude` binary; `${PATH}`'s `/usr/local/bin` carries the root Node floor. clawker does NOT set `NVM_SYMLINK_CURRENT` or route PATH through `$NVM_DIR/current`. The old `.npmrc` `prefix=${HOME}/.npm-global` mechanism is gone.

`ENV NODE_USE_SYSTEM_CA=1` set near the top of the final stage (before USER switch) so root and unprivileged `${USERNAME}` both trust `/etc/ssl/certs/ca-certificates.crt`. When `HasFirewallCA` is set, the firewall MITM cert is merged into that bundle via `update-ca-certificates`, transparently trusting the interception cert for `fetch()`/TLS.

### Clawker-Assets Placement (cache-locality + inject-lifetime invariants)

Clawker-managed assets are split across THREE positions in the final stage, dictated by USER scope, build-time read dependencies, and inject-point lifetime contracts:

**1. Early root scope (before `USER ${USERNAME}`):**
- `mkdir /etc/claude-code` + `managed-settings.json` heredoc

`managed-settings.json` is the Linux managed-settings path — highest-precedence Claude Code env override, the only documented enterprise mechanism that injects `PATH` into Claude Code's Bash-tool shell snapshot (built at session start, AFTER zsh init files, so `.zshenv` is insufficient on its own). Its PATH is `.local/bin` + the inherited `${PATH}` (whose `/usr/local/bin` carries the root Node as the floor); the agent's node otherwise comes from Claude Code's own `$NVM_DIR/versions/node/*` enumeration, so clawker does NOT add a `$NVM_DIR/current/bin` entry (pre-creating `current` as a symlink collides with Claude Code's `current/<ver>` bookkeeping → self-referential loop). Any `claude` invocation in `after_claude_install` / `before_entrypoint` inject points reads this at session start and depends on `.local/bin` to find the `claude` binary (and on `${PATH}` for `node` + the global-npm bin, e.g. `claude mcp add <package>`). Must exist BEFORE any potential build-time claude session, so it can't sit in the late block. The heredoc body is structural (template-author-edited only), so locking it early has negligible cache cost.

**2. User-scope (right after `RUN curl ... claude.ai/install.sh`, while `USER ${USERNAME}` is in effect):**
- `statusline.sh`, `claude-settings.json`, `claude-config.json` seeds → `/home/${USERNAME}/.claude-init/`

These stay in the user-scope section because `after_claude_install` / `before_entrypoint` inject points and user `Instructions.Copy` may reference `~/.claude-init/` contents at injection time. Burying them under the trailing `USER root` block would silently break that contract.

**3. Late root scope (between trailing `USER root` and `ENTRYPOINT`):**
1. Agent prompt: `COPY clawker-agent-prompt.md` → `/etc/claude-code/CLAUDE.md` (Claude reads this at session start as an additional system message; not load-bearing for command execution, so safe to defer)
2. `{{if .HasFirewallCA}}` block: CA cert COPY + `update-ca-certificates` + `SSL_CERT_FILE` / `CURL_CA_BUNDLE` ENVs (runtime traffic only; `docker build` itself goes via host network, not through the in-container firewall)
3. Host-proxy + socket-forwarder binaries (`host-open`, `git-credential-clawker`, `callback-forwarder`, `clawker-socket-server`) + single batched `chmod +x` (one layer, not four)
4. `COPY clawkerd` (every CLI release rolls this — last so its layer's invalidation tail is just `ENTRYPOINT`)

**Why this works for cache:** a clawker bump that only touches late-block assets (the common case — agent prompt edit, host-proxy script edit, clawkerd binary bump) invalidates ONLY the late block. Everything above — apt/apk, Node, git-delta, zsh-in-docker, Claude Code install, the user-scope seeds, user `Instructions.Copy`, every `Inject.*` point, the early-root managed-settings heredoc — stays cached. A bump that touches user-scope seeds (rare — those files change occasionally with clawker releases) invalidates from the seed COPYs downward, still cheap.

**Test invariants** (`TestBuildContext_LateClawkerBlock`):
- managed-settings.json appears BEFORE the first `USER ${USERNAME}` switch (early root scope)
- Claude config seeds appear BEFORE the trailing `USER root` switch (user scope)
- Agent prompt + firewall CA + host-proxy/socket binaries + clawkerd appear AFTER the trailing `USER root` (late root scope)
- clawkerd's COPY is the last asset before `ENTRYPOINT`

`TestBuildContext_CollapsedChmod` separately pins the single-chmod batching for the late root block's four `/usr/local/bin/*` binaries. Regressions that scatter the block, bury the seeds under USER root, or move managed-settings.json out of early root scope fail these tests.

### Dockerfile Instruction Types

```go
type DockerfileInstructions struct { Copy []CopyInstruction; Args []ArgInstruction; UserRun, RootRun []RunInstruction }
type DockerfileInject struct { AfterFrom, AfterPackages, AfterUserSetup, AfterUserSwitch, AfterClaudeInstall, BeforeEntrypoint []string }
type CopyInstruction struct { Src, Dest, Chown, Chmod string }
type ArgInstruction struct { Name, Default string }
type RunInstruction struct { Cmd, Alpine, Debian string }  // OS-variant aware RUN
```

### OTEL Endpoint Composition

Bundler does not compose OTEL URLs itself. `DockerfileContext.OtelEndpoint` is populated by callers from `cfg.OtelCollectorURL()` (see `internal/config/consts.go`) and wired into the container as `OTEL_EXPORTER_OTLP_ENDPOINT`. The OTel SDK appends `/v1/metrics`, `/v1/logs`, and `/v1/traces` per signal, matching the collector's OTLP HTTP receiver. Single base covers every signal — no per-signal endpoint vars. Defaults to the otel-collector so Prometheus retains metric metadata for OpenSearch Dashboards (Prometheus' `/api/v1/metadata` excludes OTLP-ingested series). Direct OTLP push to Prometheus' native receiver remains supported as an alternate endpoint (saves a hop, trades metadata). Never hand-concatenate host + port + path in bundler code — add the accessor to config and read it.

### Constants and Embedded Assets

```go
const DefaultClaudeCodeVersion, DefaultUsername, DefaultShell = "latest", "claude", "/bin/zsh"
```

UID/GID come from `cfg.ContainerUID()` / `cfg.ContainerGID()` (no bundler-local constants).

Embedded: `DockerfileTemplate`, `StatuslineScript`, `SettingsFile`, `ConfigFile`, `AgentPromptFile`, `HostOpenScript`, `CallbackForwarderSource`, `GitCredentialScript`, `SocketForwarderSource`. The pre-compiled clawkerd binary (`clawkerdembed.Binary`, from `clawkerd/embed`) flows through `COPY clawkerd` as the last layer in the late root block — a clawkerd version bump invalidates only that layer via BuildKit/legacy content-keyed cache.

## Version Management (`versions.go`)

```go
const ClaudeCodePackage = "@anthropic-ai/claude-code"
type ResolveOptions struct { Debug bool; Output io.Writer }
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

Semver parsing/comparison uses `github.com/Masterminds/semver/v3` directly (`semver.NewConstraint` + `Constraint.Check` for partial-match resolution in `versions.go`; `semver.NewVersion` / `*semver.Version` accessors elsewhere).

## Subpackage: `registry/`

```go
type Fetcher interface { FetchVersions(ctx, pkg) ([]string, error); FetchDistTags(ctx, pkg) (DistTags, error) }
func NewNPMClient(opts ...Option) *NPMClient  // implements Fetcher
func WithHTTPClient(*http.Client) Option; WithBaseURL(string) Option; WithTimeout(time.Duration) Option
type DistTags map[string]string                         // "latest" -> "2.1.3"
type VersionInfo struct { FullVersion, DebianDefault, AlpineDefault string; Major, Minor, Patch int; ... }
type VersionsFile map[string]*VersionInfo               // Keys(), SortedKeys()
type NPMPackageInfo struct { Name string; DistTags; Versions map[string]struct{} }
func NewVersionInfo(v *semver.Version, debianDefault, alpineDefault string, variants) *VersionInfo
```

## Error Types (`errors.go`)

```go
var ErrNoBuildImage error                                        // No build.image configured — returned by ProjectGenerator.buildContext()
var ErrVersionNotFound, ErrInvalidVersion, ErrNoVersions error  // Re-exported from registry/
type NetworkError = registry.NetworkError   // { URL, Message, Err } -- Unwrap() supported
type RegistryError = registry.RegistryError // { Package, StatusCode, Message } -- IsNotFound() bool
type ParseError = registry.ParseError       // { URL, Snippet, Err } -- Unwrap() supported; HTTP 200 decode failure
```

## Dependencies

Imports: `internal/config`, `internal/bundler/registry`, `github.com/Masterminds/semver/v3`, `internal/hostproxy/internals` (embed-only), `clawkerd/embed` (embed-only — `clawkerdembed.Binary`). **Does NOT import `internal/docker`** — this is a leaf package.

## Tests

Unit tests: `dockerfile_test.go`, `build_test.go`, `defaults_test.go`, `versions_test.go`. Subpackage: `registry/npm_test.go`. Docker integration: `test/whail/`.

Test helper: `testConfig(t, projectYAML) config.Config` wraps `configmocks.NewFromString(cleanedProject, settingsYAML)` with default monitoring settings — preferred test double for bundler tests. All test configs use YAML fixtures rather than mock/fake constructors.
