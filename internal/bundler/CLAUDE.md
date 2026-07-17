# Bundler Package

Image-generation package: Dockerfile generation, harness bundle + stack/monitoring-unit loading + validation, template composition, egress composition, and harness version management for clawker container images. Component **resolution** (which floor/loose/installed tier a name comes from) lives in `internal/bundle` — this package composes and renders what the resolver hands back through `bundle.NewResolver(cfg).Resolve`. Manifest/schema types live in `internal/config`. Imports `internal/hostproxy/internals` for container-side scripts (embed-only). **No `internal/docker` import** — building orchestration (`Builder`, `Build`) lives in `internal/docker`.

## Key Files

| File | Purpose |
|------|---------|
| `dockerfile.go` | Dockerfile rendering (`ProjectGenerator`), build-context generation, embedded templates/scripts |
| `basehash.go` | Base-image freshness hash (`BaseContentHash`) |
| `bundle.go` | Bundle loading + validation (`LoadBundle`, staging/volume/seed/egress-floor validators, `validateStackDecls` for the harness `stacks:` dependency list), `Bundle` type + accessors (`WalkAssets`), harness-format filename consts (`HarnessManifestFile`, `HarnessTemplateFile`, `AssetsDir`). Monitoring is a bundle **peer component** enumerated by `internal/bundle`, never declared in `harness.yaml` — the harness manifest carries no `monitoring:` field. |
| `compose.go` | Master-template composition (`Compose`, `DeclaredBlocks`), block-slot + reserved-define validation |
| `stack_load.go` | Stack definition loading (`LoadStackDefinition`, `StackDefinition`, `ValidateStackName` — accepts bare or qualified addresses via `consts.ValidateComponentRef`), stack-format filename consts (`StackManifestFile`, `StackRootFragmentFile`, `StackUserFragmentFile`) |
| `harness.go` | Harness selection + loading through the one resolution algorithm (`internal/bundle` resolver: bare = loose > floor, qualified = installed), selector validation, provenance (`LoadHarness`, `ResolveHarnessName`, `ValidateHarnessSelector`, `ShippedHarnessNames`, `KnownHarnessNames`, `IsKnownHarness`) |
| `stack.go` | Stack resolution through the one algorithm (`resolveStack` over the `internal/bundle` resolver), fragment rendering + provenance line composition |
| `egress.go` | Effective egress rule composition (harness floor + project rules) |
| `config.go` | Variant configuration |
| `versions.go` | Harness version resolution (npm dist-tags / GitHub releases) |
| `errors.go` | Error types (`NetworkError`, `RegistryError`, `ErrVersionNotFound`, etc.) |

## Subpackages

| Package | Purpose |
|---------|---------|
| `registry/` | npm registry client (`NPMClient`), GitHub releases client (`GitHubReleaseClient`), version info types, fetcher interface |
| `assets/` | Master Dockerfile templates (`Dockerfile.base.tmpl`, `Dockerfile.harness-image.tmpl`) plus the managed agent prompt (`clawker-agent-prompt.md`, embedded as `AgentPromptContent`) — the harness-agnostic agent-context briefing copied at build time to a harness's `managed_prompt.dest`; it deliberately lives with the master machinery, NOT in any harness's assets. The shipped floor — harnesses (`claude`, `codex`), stacks (`go`, `node`, `python`, `rust`), and the `claude-code` monitoring extension — relocated to `internal/bundle/assets/{harnesses,stacks,monitoring}/` and is loaded through `bundle.FloorNames`/`bundle.FloorFS`; `ShippedHarnessNames`/`ShippedStackNames` shim onto that floor API. The `claude-code` monitoring extension is loaded by `internal/monitor` (which owns the monitoring-unit loader now); the bundler package no longer touches monitoring. The clawkerd binary is imported from `clawkerd/embed`, not stored here. |

## Build Cache Strategy

Cache invalidation is delegated entirely to the builder (BuildKit's layer cache, or the classic builder's `probeCache` for legacy daemons) — both hash RUN/COPY inputs and skip identical steps automatically. The templates' layer ordering (`assets/Dockerfile.base.tmpl`, `assets/Dockerfile.harness-image.tmpl`) and ARG vs ENV choices control which layers invalidate when, but the cache mechanism itself is Docker's, not clawker's.

**Host-UID is baked into the rendered Dockerfile (Linux only).** `consts.ContainerUID()` / `ContainerGID()` resolve to the CLI invoker's `os.Getuid()` / `Getgid()` on Linux, so `useradd --uid {{.UID}}` in the base template varies per host user. On macOS/Windows, `consts.resolveProcessID` returns `fallbackContainerUID`/`GID` (1001) — Docker Desktop's virtiofs / gRPC-FUSE share masks UID/GID at the boundary, so baking the host UID would offer no access benefit and risks `groupadd --gid` collisions with base-image groups (e.g. macOS staff=20 vs Debian dialout=20). Required for the Linux writability contract of the harness host-state binds (`staging.mounts`, e.g. the claude bundle's `~/.claude/projects`).

**BuildKit vs Legacy:** `BuildKitEnabled=true` emits `--mount=type=cache` directives in the rendered Dockerfile; legacy builder silently ignores them. The flag flows through `DockerfileContext` and `ProjectGenerator`.

## Dockerfile Generation (`dockerfile.go`)

### ProjectGenerator -- single project builds from clawker.yaml

Renders the **two-image split**: a per-project shared base image
(`clawker-<project>:base`, harness-agnostic layers — packages, user setup,
project `instructions`, zsh tooling, HEALTHCHECK) and a thin harness image
that builds `FROM` it (template blocks, harness volume dirs, config seeds,
clawker root assets, ENTRYPOINT/CMD). Templates:
`assets/Dockerfile.base.tmpl` (no block slots, plain `template.Parse`) and
`assets/Dockerfile.harness-image.tmpl` (block slots, composed with the
bundle fragment via `Compose` — `compose.go`). Keep shared sections of the
two templates in sync.

```go
func NewProjectGenerator(cfg config.Config, workDir string) *ProjectGenerator
func (g *ProjectGenerator) GenerateBase() ([]byte, error)                              // Render base-image Dockerfile
func (g *ProjectGenerator) GenerateHarness() ([]byte, error)                           // Render harness-image Dockerfile (needs BaseImageRef)
func (g *ProjectGenerator) BaseContentHash(baseDockerfile []byte, buildArgs map[string]*string) (string, error) // Freshness key (basehash.go)
func (g *ProjectGenerator) GenerateBaseBuildContext(dockerfile []byte) (io.Reader, error)      // Tar: project ctx + Dockerfile under BaseDockerfileName (legacy)
func (g *ProjectGenerator) GenerateHarnessBuildContext(dockerfile []byte) (io.Reader, error)   // Tar: bundle assets + CA + clawker binaries (legacy)
func (g *ProjectGenerator) WriteHarnessBuildContextToDir(dir string, dockerfile []byte) error  // Filesystem (BuildKit)
func (g *ProjectGenerator) GetBuildContext() string
```

Fields: `BuildKitEnabled`, `HarnessVersion` (resolved npm version for the
harness ARG), `Harness` (bundle selector; empty = the configured default harness via `ResolveHarnessName`),
`BaseImageRef` (FROM ref for the harness image, set by the docker Builder —
bundler never derives project names; `GenerateHarness` errors
`ErrNoBaseImageRef` without it).

**Context ownership:** the base image's build context is the PROJECT
build-context directory (`GetBuildContext()`) because user
`instructions.copy` srcs live there and render base-side; the harness
context stages only bundle assets + firewall CA + clawker-owned
scripts/binaries. `BaseDockerfileName` (`Dockerfile.clawker-base`) is the
reserved tar entry name so a user's own `Dockerfile` is never clobbered.

**Freshness (`basehash.go`):** `BaseContentHash` = SHA-256 of the rendered
base Dockerfile bytes + everything the base build reads from the project
context: contents **and permission bits** of files — and mode records for
directories, whose bits COPY preserves too — matched by `instructions.copy`
srcs (sorted; `.git` pruned wherever it appears in a walked path, so a
dereferenced link into a sibling checkout never hashes that repo's git
state; missing srcs hash a stable marker; symlinks hash a link record of
their target string, and a src that is itself a symlink additionally hashes
its dereferenced content — an unresolvable link of any kind hashes the
missing marker rather than erroring, keeping the gate's never-blocks-a-build
contract), the
context's `.dockerignore` content (it gates what COPY can see — hashed only
when copy instructions exist), plus the effective values of any
`--build-arg` entries the base build honors: args the rendered base
Dockerfile declares via `ARG` lines, and Docker's predefined proxy args
(`HTTP_PROXY` et al., upper/lowercase), which need no declaration (a nil
value = `--build-arg NAME` pass-through resolves to `os.Getenv(NAME)`). The
docker Builder passes `BuilderOptions.BuildArgs` in and compares the result
against the `:base` image's `consts.LabelBaseContentHash` label to decide
base rebuilds. Folding base-relevant args in keeps clawker faithful to
Docker — the base skip would otherwise silently eat a `--build-arg` that
changes what the base build produces. Args the base neither declares nor
Docker predefines (harness-only or unknown) are excluded, so they never
force a base rebuild: with no base-relevant args the hash equals the
Dockerfile+context-inputs hash exactly (no arg bytes appended) — a base's
identity depends only on its rendered inputs, independent of the arg-folding
path. Deliberately NOT a whole-context hash —
source edits outside copy srcs never rebuild the base. Glob semantics are
Go's, not Docker's; imprecision worst-cases as a spurious rebuild, never a
wrong image.

**Substrate base:** every base Dockerfile renders `FROM` the single pinned
`SubstrateImage` digest (Debian bookworm-slim). There is no user-selectable
base image and no custom-Dockerfile path — project customization happens via
`build.packages`, `build.stacks`, `instructions`, and `inject`.

**ProjectGenerator is a pure renderer** — it does not perform any network
I/O. The harness version baked into the rendered version ARG comes from
the `HarnessVersion` field, resolved at the command layer via
`bundler.ResolveHarnessVersion` (Factory `f.HttpClient`); empty falls back
to `DefaultHarnessVersion`. This keeps bundler hermetic in tests and
aligns with the repo's factory-DI pattern.

**FROM-boundary invariants (harness-image template):** ARGs don't survive
FROM — the final stage re-declares `ARG USERNAME` and `ARG ZSH_ENV`; SHELL
carries over via image config, so the template resets `SHELL ["/bin/sh"]`
after FROM (`root_after_stacks` and `user_after_stacks` run under sh) and
restores zsh before `user_after_shell_switch`. `root_after_stacks` is the first root
step of the harness image and runs AFTER user creation (which lives in the
base image).

**Block slots** (`DeclaredBlocks`, compose.go): `root_after_stacks`,
`user_after_stacks`, `user_after_shell_switch`, `root_before_entrypoint`, `cmd` — named for
the permission scope + the template event they render relative to, never for
content (they are positional opportunities; a harness may put anything in
any of them).

### Harness Version Resolution

```go
func ResolveHarnessVersion(ctx context.Context, httpClient *http.Client, b *Bundle) (string, error)
```

`ResolveHarnessVersion` dispatches on the bundle manifest's version spec: `npm` (package's "latest" dist-tag), `github-release` (latest release tag via `registry.GitHubReleaseClient`, manifest tag prefix stripped), or `none` (the `DefaultHarnessVersion` literal). On resolution failure returns `(DefaultHarnessVersion, err)` so callers can warn the user while still producing a usable rendered Dockerfile (the install RUN downloads whatever "latest" is at build time).

**Production wiring:** `internal/cmd/image/build/build.go` calls `ResolveHarnessVersion` once per build, passing `f.HttpClient()` from the Factory.

**Test wiring:** bundler tests use a package-local `stubRoundTripper` (implements `http.RoundTripper`) passed to `&http.Client{Transport: stubRT}`. `http.RoundTripper` is the stdlib mock seam; no project-defined interface required. Command-layer tests (in `internal/cmd/image/build/`) wire the same pattern through `cmdutil.Factory{HttpClient: ...}`.

### Harness version build-arg (claude bundle pattern)

The claude bundle's fragment declares `ARG CLAUDE_CODE_VERSION={{.HarnessVersion}}` — **not** `ENV`. Three properties this gives:

1. **ARG-cache mechanic:** the ARG declaration sits **directly above its only consumer** (the install RUN in the claude fragment's user_after_shell_switch block), NOT near the top of the stage. Under BuildKit (Docker 23+ default) a changed ARG default busts the cache at the ARG's **declaration line**, not at first use — verified empirically, and contrary to the classic builder's documented "first usage, not definition" rule. So adjacency is load-bearing: a harness release rolls the rendered default and invalidates only the install layer + everything below it, leaving the stack fragments and the blocks above user_after_shell_switch cached (the shared base image is a separate image and never invalidates). `clawker build --no-cache` invalidates everything regardless of ARG positioning.
2. **Runtime invisibility:** ARG is build-only, so the version var is naturally absent from the running container (Claude Code does not read `CLAUDE_CODE_VERSION` at runtime).
3. **User override:** `clawker build --build-arg CLAUDE_CODE_VERSION=2.1.4` pins the install to an explicit version, bypassing the npm resolution. Wired through `internal/cmd/image/build/build.go`.

### DockerfileContext -- template data

```go
type DockerfileContext struct {
    BaseImage, Username, Shell, WorkspacePath, HarnessVersion, HarnessBaseImage string
    Packages, HarnessVolumeDirs, StackRootSteps, StackUserSteps []string
    HarnessPackages []string  // per-harness overlay apt packages (build.harnesses.<name>.packages); harness image only, no dedupe vs Packages
    HarnessSeeds []config.Seed; UID, GID int; BuildKitEnabled bool
    Instructions *DockerfileInstructions; Inject *DockerfileInject
    // OTEL telemetry — from config.MonitoringConfig
    OtelEndpoint string  // base URL only; SDK appends /v1/{metrics,logs,traces}. Traces ride the same base via OTEL_TRACES_EXPORTER=otlp + CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1, both hard-coded in the claude bundle fragment (not context fields).
    OtelLogsExportInterval, OtelMetricExportInterval int
    OtelLogToolDetails, OtelLogUserPrompts, OtelIncludeAccountUUID, OtelIncludeSessionID bool
    HasFirewallCA bool; GoBuilderImage string
}
```

`GoBuilderImage` is the Go stack image for builder stages, pinned to exact patch version + SHA digest (default: `DefaultGoBuilderImage`). Tracks `go.mod`.

### Harness Bundles (`harness.go`)

```go
const DefaultHarnessName = "claude"
func ShippedHarnessNames() []string                                    // floor harness names (claude, codex) via bundle.FloorNames
func ResolveHarnessName(cfg config.Config, explicit string) (string, error) // explicit selector (validated), else the build.harness selection key, else DefaultHarnessName; nil-project tolerant
func ValidateHarnessSelector(name string) error                        // bare or qualified; reserved image-tag alias check is bare-only
func KnownHarnessNames(cfg config.Config) []string                     // floor ∪ loose ∪ installed-bundle harnesses via resolver.List; IsKnownHarness(cfg, name) bool
func LoadHarness(cfg config.Config, name string) (*Bundle, error)      // resolves via bundle.NewResolver(cfg).Resolve, then LoadBundle(comp.FS)
```

A bundle dir = `harness.yaml` (manifest: version spec, stacks, volumes, seeds, staging, egress, optional `managed_prompt` — the build-time copy target for clawker's managed agent context; absent = the harness doesn't take one) + `Dockerfile.harness.tmpl` (block-slot fragment) + optional `assets/`. The parsed manifest (`config.Manifest` and its nested schema types) lives in `internal/config`; `LoadBundle` (`bundle.go`) reads + validates it, and `Compose` (`compose.go`) renders the fragment against the master template.

**Resolution:** harness selection resolves through the ONE algorithm in
`internal/bundle` — a bare name resolves user loose > project loose > embedded
floor; a qualified `namespace.bundle.component` address resolves from the
installed/in-place bundle set. There is no `harnesses:` path registry and no
walkup. `LoadHarness` keeps its `(cfg, name)` signature and internally calls
`bundle.NewResolver(cfg).Resolve(bundle.ComponentHarness, name)`, then
`LoadBundle(name, comp.FS)` — so the `Bundle.Name` is the exact selection
spelling (bare or dotted), which downstream becomes the image tag, the harness
label, and the per-harness overlay key. A loose harness named like a floor one
shadows it (surfaced in build output). Custom harness = drop a bundle dir into a
loose convention dir (`.clawker/harnesses/<name>/` or the user config-dir
equivalent), or install a bundle. Unresolvable name = hard "not found" error.

An empty selector resolves the default: `ResolveHarnessName(cfg, "")` returns
the `build.harness` selection key when set (any layer, highest wins wholesale
like `build.stacks`; validated, error names the key), else the built-in
`DefaultHarnessName`. An explicit selector always beats the key. The reserved
image-tag-alias check (`default`/`latest`/`base`) applies to bare names only; a
dotted qualified address can never collide with a bare alias.

### Stacks (`stack.go`)

Language stacks are file-backed definitions: `stack.yaml` + `Dockerfile.stack-root.tmpl` and/or `Dockerfile.stack-user.tmpl` (loaded via `LoadStackDefinition` into a `StackDefinition` — `stack_load.go`; the `stack.yaml` shape is `config.StackManifest`). Shipped: `go` (root), `node` (root LTS + user nvm), `python` (root uv + uv-managed CPython), `rust` (user rustup).

- **Resolution** (`resolveStack`): a declared stack address resolves through the ONE algorithm in `internal/bundle` — a bare name resolves user loose > project loose > embedded floor; a qualified `namespace.bundle.component` address resolves from installed bundles. A closer bare tier wins **wholesale** — never merged. There is no `stacks:` path registry and no bundle-embedded sibling lane: a bundled harness references its shipped sibling stack by its qualified self-address (`acme.tools.node`) like any other bundle stack. Unresolvable address = hard "not found" error.
- **Both strata always render (no cross-stratum dedup):** project `build.stacks: [go, node]` renders in the base image; a harness manifest's `stacks:` dependency list ALWAYS renders in the harness image with its resolved definition — even when the project also declared the same name in the base. Both render; fragment self-guards / apt idempotence / PATH shadowing own any interaction (design §2). `StackRootSteps` render before root_after_stacks (root), `StackUserSteps` before user_after_stacks (user).
- **Per-harness build overlay (`build.harnesses.<name>.{stacks,packages,inject}`):** the same primitive trio as the base build fields, scoped to ONE harness's image and consumed by `GenerateHarness` keyed by the harness's exact selection spelling. Overlay stacks render AFTER the bundle's installer stacks (installer → overlay, one resolution — a name repeated across the two sources renders once, at its installer position). Overlay packages render as an early-root apt RUN in the harness-image template (`HarnessPackages`), never deduped against the base package list (apt idempotence). Overlay `inject.user_commands`/`before_entrypoint` render only in that harness's image, appended after the global project inject at the same anchors. An overlay keyed to a harness that resolves nowhere is a hard `GenerateHarness` error naming the known harnesses — dead overlay config never silently drops.
- **Provenance:** a single `Resolve` reports only the winning tier, not the shadowed farther tiers (computing those requires scanning the installed-bundle set, which must never block a floor-only build — that full shadow listing is a `bundle list` concern). So the build records one line per **non-floor** stack resolution — a loose or bundled override — naming its source (`stack node ← project (…)`, `stack acme.tools.node ← bundle acme.tools`); the harness always names its source. The docker `Builder` collects `ProjectGenerator.Provenance()` and `clawker build` prints it to stderr.
- Fragments are **self-guarded** — they skip when the image already provides the tool (e.g. the node fragment keeps an existing node ≥ its floor major).
- Node specifics (node stack fragment): `ARG NODE_VERSION` (default `24`) names the LTS *line*, not a patch — the latest patch resolves per-build from `nodejs.org/dist/index.json`, floating onto security patches on rebuild (justified pin-policy exception; rationale in `docs/threat-model.mdx`). Tarball is GPG-verified via `SHASUMS256.txt.asc`. `ENV NODE_USE_SYSTEM_CA=1` makes node trust the OS CA bundle (and therefore the firewall MITM CA once merged).

### Egress Composition (`egress.go`)

```go
func EgressRules(cfg config.Config, name string) ([]config.EgressRule, error)
```

Composes the effective firewall rule set: the selected harness bundle's `egress:` floor first, then the project's `security.firewall` rules/add_domains. Firewall sync paths must call this — `cfg.ProjectEgressRules()` alone is missing the floor the harness needs to function. Empty name = the configured default harness (`ResolveHarnessName`).

### Asset Placement in the Harness Image (cache-locality + inject-lifetime invariants)

The rendered harness image splits content across three scopes, dictated by USER scope, build-time read dependencies, and inject-point lifetime contracts:

**1. Early root scope (root_after_stacks, before `USER ${USERNAME}`):** bundle-fragment root steps. The claude fragment writes `/etc/claude-code/managed-settings.json` here — the highest-precedence Claude Code env override, whose PATH (`.local/bin` + inherited `${PATH}`) is what lets any build-time `claude` invocation in `user_commands` / `before_entrypoint` inject points find the `claude` binary and node. It must exist before any potential build-time session, so it can't sit in the late block. (Claude Code globs `$NVM_DIR/versions/node/*` itself; clawker never adds a `$NVM_DIR/current/bin` PATH entry — pre-creating `current` collides with Claude Code's `current/<ver>` bookkeeping.)

**2. User scope (user_after_stacks + user_after_shell_switch + generic seed staging):** the harness install (the claude fragment puts it in user_after_shell_switch) and the manifest `seeds:` staged to `/home/${USERNAME}/.clawker/seed/` plus a generated `seed-manifest` (apply tokens consumed by CP's generic first-boot seed-apply step). Seeds stay in the user-scope section because `user_commands` / `before_entrypoint` inject points and user `Instructions.Copy` may reference the staged contents at injection time.

**3. Late root scope (trailing `USER root` → `ENTRYPOINT`), shared template:**
1. root_before_entrypoint (bundle late-root steps), then the managed-prompt COPY — the master template copies clawker's embedded `AgentPromptContent` (`assets/clawker-agent-prompt.md`, harness-agnostic) to the manifest-declared `managed_prompt.dest` with resolved `--chown`/`--chmod` (root:root 0644 defaults); rendered only when the manifest declares the block
2. `{{if .HasFirewallCA}}` block: CA cert COPY + `update-ca-certificates` + `SSL_CERT_FILE` / `CURL_CA_BUNDLE` ENVs (runtime traffic only; `docker build` itself goes via host network, not through the in-container firewall)
3. Host-proxy + socket-forwarder binaries (`host-open`, `git-credential-clawker`, `callback-forwarder`, `clawker-socket-server`) + single batched `chmod +x` (one layer, not four)
4. `COPY clawkerd` (every CLI release rolls this — last so its layer's invalidation tail is just `ENTRYPOINT`), then `ENTRYPOINT ["/usr/local/bin/clawkerd"]` + the cmd block (CMD)

**Why this works for cache:** a clawker bump that only touches late-block assets (the common case — agent prompt edit, host-proxy script edit, clawkerd binary bump) invalidates ONLY the late block; the harness install, seeds, inject points, and the entire base image stay cached. A seed change invalidates from the seed COPYs downward, still cheap.

**Test invariants** (`TestBuildContext_LateClawkerBlock`, rendered against the default claude bundle):
- managed-settings.json appears BEFORE the first `USER ${USERNAME}` switch (early root scope), with the `.local/bin:${PATH}` PATH and no `.nvm/current/bin` entry
- seeds appear BEFORE the trailing `USER root` switch (user scope)
- agent prompt + host-proxy/socket binaries + clawkerd appear AFTER the trailing `USER root` (late root scope)
- clawkerd's COPY precedes `ENTRYPOINT`

`TestBuildContext_CollapsedChmod` separately pins the single-chmod batching for the late root block's four `/usr/local/bin/*` binaries.

### Dockerfile Instruction Types

```go
type DockerfileInstructions struct { Copy []CopyInstruction; Args []ArgInstruction; UserRun, RootRun []RunInstruction }
type DockerfileInject struct { AfterFrom, AfterPackages, AfterUserSetup, AfterUserSwitch, UserCommands, BeforeEntrypoint []string }  // yaml after_claude_install (deprecated alias) merges into UserCommands
type CopyInstruction struct { Src, Dest, Chown, Chmod string }
type ArgInstruction struct { Name, Default string }
type RunInstruction struct { Cmd, Alpine, Debian string }  // OS-variant aware RUN
```

### OTEL Endpoint Composition

Bundler does not compose OTEL URLs itself. `DockerfileContext.OtelEndpoint` is populated by callers from `cfg.OtelCollectorURL()` (see `internal/config/consts.go`) and wired into the container as `OTEL_EXPORTER_OTLP_ENDPOINT`. The OTel SDK appends `/v1/metrics`, `/v1/logs`, and `/v1/traces` per signal, matching the collector's OTLP HTTP receiver. Single base covers every signal — no per-signal endpoint vars. Defaults to the otel-collector so Prometheus retains metric metadata for OpenSearch Dashboards (Prometheus' `/api/v1/metadata` excludes OTLP-ingested series). Direct OTLP push to Prometheus' native receiver remains supported as an alternate endpoint (saves a hop, trades metadata). Never hand-concatenate host + port + path in bundler code — add the accessor to config and read it.

### Constants and Embedded Assets

```go
const DefaultHarnessVersion, DefaultUsername, DefaultShell = "latest", "claude", "/bin/zsh"
const SubstrateImage = "debian:bookworm-slim@sha256:..."  // single pinned base for every generated base image
```

UID/GID come from `cfg.ContainerUID()` / `cfg.ContainerGID()` (no bundler-local constants).

Embedded: `DockerfileBaseTemplate`, `DockerfileHarnessImageTemplate`, `HostOpenScript`, `CallbackForwarderSource`, `GitCredentialScript`, `SocketForwarderSource`. The pre-compiled clawkerd binary (`clawkerdembed.Binary`, from `clawkerd/embed`) flows through `COPY clawkerd` as the last layer in the late root block — a clawkerd version bump invalidates only that layer via BuildKit/legacy content-keyed cache.

## Version Management (`versions.go`)

```go
const ClaudeCodePackage = "@anthropic-ai/claude-code"
type ResolveOptions struct { Debug bool; Output io.Writer; Package string }  // empty Package = ClaudeCodePackage
func NewVersionsManagerWithFetcher(fetcher registry.Fetcher, cfg *VariantConfig) *VersionsManager
func (m *VersionsManager) ResolveVersions(ctx, patterns, opts) (*registry.VersionsFile, error)
```

Patterns: `"latest"`, `"stable"`, `"next"` via dist-tags. `"2.1"` partial-matches highest `2.1.x`. `"2.1.2"` exact.

## Variant Configuration (`config.go`)

```go
type VariantConfig struct { DebianDefault, AlpineDefault string; Variants map[string][]string; Arches []string }
func DefaultVariantConfig() *VariantConfig   // trixie/alpine3.23, amd64+arm64v8
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
var ErrVersionNotFound, ErrInvalidVersion, ErrNoVersions error  // Re-exported from registry/
type NetworkError = registry.NetworkError   // { URL, Message, Err } -- Unwrap() supported
type RegistryError = registry.RegistryError // { Package, StatusCode, Message } -- IsNotFound() bool
type ParseError = registry.ParseError       // { URL, Snippet, Err } -- Unwrap() supported; HTTP 200 decode failure
```

## Dependencies

Imports: `internal/bundle` (component resolution + floor FS), `internal/config` (manifest/schema types), `internal/consts`, `internal/bundler/registry`, `github.com/Masterminds/semver/v3`, `internal/hostproxy/internals` (embed-only), `clawkerd/embed` (embed-only — `clawkerdembed.Binary`). **Does NOT import `internal/docker`.** Import DAG: `consts ← config ← bundle ← bundler ← docker`.

## Tests

Unit tests: `dockerfile_test.go`, `build_test.go`, `basehash_test.go`, `versions_test.go`, `bundle_test.go`, `stack_load_test.go`, `harness_test.go`, `stack_test.go`, `overlay_test.go`, `egress_test.go`. Golden: `golden_test.go` renders base + harness Dockerfiles against `testdata/golden/` (regen: `GOLDEN_UPDATE=1 go test ./internal/bundler/ -run TestGenerate_Golden`). Subpackage: `registry/npm_test.go`, `registry/github_test.go`. Docker integration: `test/whail/`.

Test helper: `testConfig(t, projectYAML) config.Config` wraps `configmocks.NewFromString(cleanedProject, settingsYAML)` with default monitoring settings — preferred test double for bundler tests. All test configs use YAML fixtures rather than mock/fake constructors.
