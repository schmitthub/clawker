# `clawker build` ↔ `docker build` Parity Initiative

## End Goal

Bring `clawker image build` (aliased as `clawker build`) to **flag-surface parity with `docker build`** for flags that have genuine clawker value. Drop anything packaging/distribution-flavored — clawker images run **locally on the same daemon they were built on**.

Holistic secondary goal: follow standard build-CLI conventions for **proper metadata, tags, digest capture, attestations**. For a security-positioned tool, attestations (`--provenance`, `--sbom`) are first-class even for local-only builds (audit trail + SBOM for CVE scanning).

## Curation Filter (Phase 2 outcome — locked)

Two filters, applied in order, with no invented rationalizations:

1. **Packaging / distribution / cross-target?** → drop
2. **Genuine local-dev or security-tool value?** → keep

Cross-references: [[feedback_parity_needs_value]], [[feedback_no_invented_security_framings]].

### SDK Pattern Alignment Table

| Aspect | Standard SDK pattern | Clawker now |
|---|---|---|
| Apply user tags | exporter `name=` attr | ✅ |
| Extract digest | parse `ExporterResponse[ExporterImageDigestKey]` (BuildKit) + `aux.ID` (legacy) | ✅ via `OnComplete` |
| Skip-rebuild | trust daemon-side cache (BuildKit Solve / classic `probeCache`) | ✅ implicit |
| Content-addressed local ref | `--iidfile` writes digest to file | ✅ |

### Peer-project references benchmarked

- **docker/cli** `cli/command/image/build.go` — flag parsing, ImageBuildOptions
- **moby/moby** `daemon/internal/builder-next/exporter/exporter.go imageExporterMobyWrapper` — confirms `name-canonical=true` silently stripped; `aux.ID` in stream is legacy digest source
- **moby/buildkit** `exporter/containerimage/export.go` — sets `ExporterResponse[ExporterImageDigestKey]` (`containerimage.digest`)
- **docker/buildx** `commands/build.go getImageID` — extract digest from SolveResponse + write `--iidfile`
- **containers/podman** `libimage/normalize.go NormalizeName` — defaults `:latest` if tag missing

## Branch State (current)

- Branch: `feat/image-optimization`
- HEAD: `aa06f2f6 chore(memory): in-flight serena memories — build-parity initiative + cache UAT`
- Working tree: clean
- Commits in this initiative (pushed):
  - `bb51901c` — ARG CLAUDE_CODE_VERSION + Dockerfile.tmpl three-tier reorder
  - `6e959fd7` — Phase 0: EnsureImage chain removal + BuildKit digest capture + `--iidfile`
  - `aa06f2f6` — in-flight serena memories

## Already on `clawker image build` (Phase 0)

`-f/--file`, `-t/--tag`, `--no-cache`, `--pull`, `--build-arg`, `--label`, `--target`, `-q/--quiet`, `--progress`, `--network`, `--iidfile`

## Authoritative `docker build --help` output (DO NOT re-synthesize via deepwiki)

```
docker build [OPTIONS] PATH | URL | -

      --add-host strings              Add a custom host-to-IP mapping (format: "host:ip")
      --allow stringArray             Allow extra privileged entitlement (network.host, security.insecure, device)
      --annotation stringArray        Add annotation to the image
      --attest stringArray            Attestation parameters (format: "type=sbom,generator=image")
      --build-arg stringArray         Set build-time variables
      --build-context stringArray     Additional build contexts (e.g., name=path)
      --builder string                Override the configured builder instance
      --cache-from stringArray        External cache sources
      --cache-to stringArray          Cache export destinations
      --call string                   Set method for evaluating build ("check", "outline", "targets")
      --cgroup-parent string          Set the parent cgroup for "RUN" instructions during build
      --check                         Shorthand for "--call=check"
  -D, --debug                         Enable debug logging
  -f, --file string                   Name of the Dockerfile (default: "PATH/Dockerfile")
      --iidfile string                Write the image ID to a file
      --label stringArray             Set metadata for an image
      --load                          Shorthand for "--output=type=docker"
      --metadata-file string          Write build result metadata to a file
      --network string                Set the networking mode for "RUN" instructions
      --no-cache                      Do not use cache when building the image
      --no-cache-filter stringArray   Do not cache specified stages
  -o, --output stringArray            Output destination
      --platform stringArray          Set target platform for build
      --policy stringArray            Policy configuration
      --progress string               Set type of progress output (auto/none/plain/quiet/rawjson/tty)
      --provenance string             Shorthand for "--attest=type=provenance"
      --pull                          Always attempt to pull all referenced images
      --push                          Shorthand for "--output=type=registry,unpack=false"
  -q, --quiet                         Suppress build output and print image ID on success
      --sbom string                   Shorthand for "--attest=type=sbom"
      --secret stringArray            Secret to expose to the build
      --shm-size bytes                Shared memory size for build containers
      --ssh stringArray               SSH agent socket or keys to expose to the build
  -t, --tag stringArray               Image identifier
      --target string                 Set the target build stage to build
      --ulimit ulimit                 Ulimit options
```

## Final Phase 2 Outcome — LOCKED Keep List (5 flags)

| Flag | Honest rationale |
|---|---|
| `--no-cache-filter` | Stage iteration workflow; force-rebuild a specific stage |
| `--secret` | Build-time creds (npm/GH tokens) without baking into layers |
| `--cache-from`/`--cache-to` (local types only) | Filesystem cache share across dev machines |
| `--attest`/`--provenance`/`--sbom` | SBOM consumed locally by trivy/grype — only legitimate security framing |
| `--call`/`--check` | Lint mode for generated Dockerfile |

## Locked Drop List (with reasons — never re-litigate)

| Flag | Drop reason |
|---|---|
| `--platform` | Cross-arch packaging for other systems. clawker images run locally on the daemon they were built on. |
| `--add-host` | eBPF firewall doesn't touch buildkitd; build runs on host daemon, no clawker interaction |
| `--allow` | `network.host`/`security.insecure` opt-in; default-deny is fine, no user demand |
| `--annotation` | OCI manifest metadata clawker doesn't read or ship |
| `--cgroup-parent` | Build cgroup ≠ agent cgroup; clawker controls only agent runtime cgroup |
| `--shm-size` | Build container ephemeral; no DoS angle |
| `--ulimit` | Same; short-lived build, fd/proc caps irrelevant |
| `--policy` | BuildKit policy runs on host daemon, no CP signal |
| `-o`/`--output` | "Forensic extraction" was invented framing — actually distribution-flavored |
| `--metadata-file` | CI integration; clawker users build locally, not in CI |
| `-D`/`--debug` | clawker likely has its own; alignment not worth the conflict cost |
| `--ssh` | No clawker-generated Dockerfile does private git clone during build |
| `--build-context` | Niche (only for user-supplied custom Dockerfile) |
| `--push` | Pure registry upload |
| `--load` | clawker default behavior |
| `--builder` | clawker owns BuildKit lifecycle |
| `--output type=registry/oci/image` | Registry variants |
| `--cache-from`/`--cache-to type=registry` | Registry-backed cache |

## Earlier agent errors (avoid repeats — see [[feedback_no_invented_security_framings]])

- Made up `--security-opt`, `--memory`, `--memory-swap`, `--pids-limit`, `--cpu-shares`, `--cpu-period`, `--cpu-quota`, `--cpuset-cpus`, `--cpuset-mems`, `--isolation`. NONE in `docker build`. Conflated with `docker run` surface.
- Reframed `--platform` as "cross-arch security regression testing" → invented justification, user rejected
- Reframed `--output type=local/tar` as "forensic extraction" → same invented framing pattern

## Critical Confusions to Avoid

1. `docker build` ≠ `docker buildx` ≠ `docker buildx build`. Three distinct flag surfaces.
2. NEVER trust deepwiki for "list all flags of X" — synthesizes legacy + buildx without delineation.
3. ALWAYS get authoritative `--help` output directly via `Bash` against an actual docker binary.
4. Attestations are NOT distribution-only for security tools. SBOM is consumed locally by trivy/grype.
5. `name-canonical=true` exporter attribute is silently stripped by moby wrapper. `--iidfile` is the alternative.

## TODOs

### Phase 3 — Implementation (next; per-flag with signoff)

For each of the 5 locked-keep flags:
- [ ] Add to `BuildOptions` in `internal/cmd/image/build/build.go`
- [ ] Register cobra flag with help text matching docker CLI's wording verbatim
- [ ] Plumb through `BuilderOptions` (`internal/docker/builder.go`) → `BuildImageOpts` (`internal/docker/client.go`) → `whail.ImageBuildKitOptions` (`pkg/whail/types.go`) → BuildKit Solve attrs (`pkg/whail/buildkit/solve.go`)
- [ ] Add test
- [ ] Verify behavior matches docker CLI (run `docker build` with same flag, compare result)

Implementation tier order (memory-suggested, user TBD on cadence):
1. `--no-cache-filter` (Tier A trivial — single attr)
2. `--secret` (Tier B — mount syntax parse)
3. `--cache-from`/`--cache-to` (Tier B — pass-through with local-type doc)
4. `--call`/`--check` (Tier C — Solve evaluation mode reroute)
5. `--attest`/`--provenance`/`--sbom` (Tier C — BuildKit attestation plumb + verify exporter)

### Phase 4 — Output-shape parity

- [ ] Fix `--quiet`: today suppresses all output; should print just image ID to stdout on success (matches `docker build -q`)
- [ ] Honor `BUILDKIT_PROGRESS` env (currently only honor `--progress` flag)

### Phase 5 — Resume paused UAT (separate concern, see [[dockerfile_cache_optimization_uat]])

- [ ] Test 1: modify `internal/bundler/assets/clawker-agent-prompt.md` (real edit, no fake marker accumulation), build, observe expected invalidation (steps 19–27), revert immediately
- [ ] Tests 2–5 same pattern for host-open.sh, claude-settings.json, clawkerd binary, socket-server main.go
- [ ] Confirm with user before each test

## IMPORTANT

- **Always check with the user before proceeding with the next TODO item.** Each phase / flag addition needs explicit user signoff.
- **DELETE THIS MEMORY when work complete.** Once Phase 3/4/5 done, ask user to delete via `mcp__serena__delete_memory` with name `docker_build_parity_initiative`. Captures in-flight investigation state, not durable project facts.
