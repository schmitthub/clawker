# Shared Base Image Split (option 2) — IMPLEMENTED 2026-07-06

Per-project shared base image `clawker-<proj>:base` (harness-agnostic layers) + thin harness images `FROM` it. Motivation: user root_run/user_run toolchains ≈ 9.5GB of a 10GB image duplicated per harness; base-layer sharing by image reference survives `--no-cache` on harness builds.

## Design (user-approved)

- **Base** (`Dockerfile.base.tmpl`, no block slots): FROM build.image → packages → docker CLI → locale → root_run → useradd → shared dirs (NOT HarnessVolumeDirs) → history → WORKDIR → git-delta → USER + static ENVs → zshenv → zsh-in-docker → user_run → user Copy → HEALTHCHECK. Injects: after_from/after_packages/after_user_setup/after_user_switch.
- **Harness** (`Dockerfile.harness-image.tmpl`, composed via harness.Compose): Go builder stages → FROM base → SHELL sh reset + ARG USERNAME + USER root → block_1, block_2, volume-dirs mkdir → USER user → block_3 → ARG ZSH_ENV + SHELL zsh → block_4 → seeds+manifest → after_harness_install → before_entrypoint (user scope, as master) → USER root → block_5 → firewall CA → host-proxy binaries → clawkerd LAST → ENTRYPOINT → block_6.
- Clawker late-root assets harness-side: clawkerd bump must not change base image ID (would cache-bust all harness steps).
- **block_1 contract change**: runs AFTER useradd now (first root step of harness image). ARGs don't survive FROM → USERNAME/ZSH_ENV re-declared; SHELL carries via image config → sh reset + zsh restore reproduce per-block shell semantics (blocks 1-3 sh, 4-6 zsh).
- Freshness: `BaseContentHash` (internal/bundler/basehash.go) = sha256(rendered base Dockerfile + copy-src file contents); compared to `consts.LabelBaseContentHash` label on :base; rebuild on miss/drift or --no-cache. NOT whole-context hash.
- Base ensure lives in `Builder.Build` (internal/docker/builder.go) — two BuildImage calls; base labels = ImageLabels + hash + LabelPurpose=PurposeBaseImage (no harness/user labels); harness gets hash label too; --pull base-only; OnComplete harness-only; `phaseProgress` namespaces base step IDs (`base:`), leaves `[internal]` names intact for TUI filter.
- Base build context = PROJECT context dir (fixes user instructions.copy which was silently broken — staged tar never had project files). Legacy tar injects Dockerfile as `Dockerfile.clawker-base` (reserved, never clobbers user Dockerfile). Harness context = bundle assets + CA + clawker embeds only.
- `base` reserved tag: consts.ImageTagBase + ValidateHarnessKey switch (covers -t base, @:base, hostile registry key via ResolveHarnessName choke point).
- whail fix: `pkg/whail/buildkit/solve.go` — absolute Dockerfile path now sets `attrs["filename"]=Base()` (was leaking abs path into frontend; base BuildKit build is first consumer).
- Master `Dockerfile.tmpl` FROZEN (legacy DockerfileManager/BuildDefaultImage path) — 3-template drift risk, header cross-refs; NO claude-plugin copies (plugin = standalone support skill, its reference Dockerfile.tmpl unchanged → drift hook green).
- Generator API: Generate()/GenerateBuildContext() DELETED; GenerateBase/GenerateHarness (+BaseImageRef field, ErrNoBaseImageRef), GenerateHarnessBuildContext/WriteHarnessBuildContextToDir renames, GenerateBaseBuildContext new. BuilderOptions.Dockerfile field removed (no writers).

## Test surface

- Goldens split per scenario: `<name>.base.Dockerfile` + `<name>.harness.Dockerfile` (old single-file goldens deleted; union hand-verified — only FROM/mkdir/chown splits differ).
- New: TestGenerateBase_ExcludesHarnessSurface, TestGenerateHarness_FromBaseBoundary (SHELL/ARG guards), TestGenerateHarness_RequiresBaseImageRef, TestGenerateBaseBuildContext, basehash_test.go (determinism/change/ignore/missing-marker), builder_test.go two-phase suite (BuildsBaseThenHarness, SkipsBaseWhenHashMatches, StaleHashRebuildsBase, NoCacheRebuildsBase, BaseFailureAborts, HarnessBuildNeverPulls, CustomDockerfileSkipsBase, TestPhaseProgress) with per-call capture ImageBuildFn + inspectNotFoundError fake.
- Re-homed to harness render: LateClawkerBlock, CollapsedChmod, ClawkerdIsPID1, HarnessVersionIsARG (upstream marker now nvm install), telemetry/seeds tests.
- e2e TestPresetBuilds_E2E: asserts :base + :default exist, second build keeps base image ID stable. HOST-ONLY.
- Gotcha: index-based template tests break if template COMMENTS contain literal markers (e.g. "USER ${USERNAME}" in a header comment) — keep template comments marker-free.

## Status

All phases done; `make test` (5473) green, golangci clean on touched pkgs, gen-docs regenerated (build help mentions shared base). NOT yet: host e2e run, live UAT (base skip + codex rebuild sharing), commit.
