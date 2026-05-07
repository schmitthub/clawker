# Bug: ~/.claude/projects bind mount UID mismatch on Linux

PR #269 (`feat/claude-dir-mount`) added a live bind mount of host
`~/.claude/projects/` → container `/home/claude/.claude/projects` so
auto-memory + session jsonls survive container restarts.

Live bind mount = no UID translation. Kernel exposes raw host inode
UID into the container. Container `claude` user is hardcoded to
UID/GID 1001. When host UID != 1001 (every Linux user not lucky enough
to be UID 1001 — i.e. nearly all of them), Claude Code inside the
container gets `EACCES` writing session jsonls. Symptom: history +
auto-memory silently fail to persist across runs. macOS unaffected
(Docker Desktop virtiofs translates ownership).

Today's PR ships a stderr warning (`internal/workspace/setup.go:177`)
+ known-issues entry. That's a band-aid, not a fix.

## Where the hardcode lives

- `internal/consts/consts.go:242-243` — `ContainerUID = 1001`,
  `ContainerGID = 1001`. Authoritative source for every other
  package (no per-package shadows allowed, see `dependency-placement.md`).
- `internal/bundler/assets/Dockerfile.tmpl:172-176` — `useradd
  --uid {{.UID}}` / Alpine `adduser -u {{.UID}}`. Template params
  `.UID`/`.GID` come from `DockerfileContext` populated via
  `cfg.ContainerUID()` / `cfg.ContainerGID()` (which currently
  delegate to the `consts` constants).
- `internal/bundler/assets/entrypoint.sh` — runs as root, drops to
  `${CLAWKER_USER}` (= `claude`) via `gosu`. No UID logic; trusts
  whatever UID the image baked in.
- `internal/containerfs/containerfs.go:209-225` —
  `PreparePostInitTar` stamps tar headers with `cfg.ContainerUID()`
  /`cfg.ContainerGID()` for `.clawker/post-init.sh`.
- `internal/docker` `CopyToVolume` — two-phase ownership fix uses
  the same constants for tar headers + post-copy chown. Comfortable
  for staged copies into a Docker volume; breaks for live bind
  mounts because the host inode is the source of truth.

## Fix path (recommended)

Thread **host UID/GID** through the build pipeline so the image's
`claude` user matches the host user that built it. Live bind mounts
then "just work" — kernel exposes UID 1000 (or whatever), container
user is also 1000, write succeeds.

Rough plan:

1. **`internal/consts/consts.go`** — keep `ContainerUID`/`ContainerGID`
   as the *default* (1001), still used by the staged-copy paths
   (`CopyToVolume`, `PreparePostInitTar`) where the kernel never sees
   a host inode. Add an explicit doc note that these defaults apply
   to images built without a `--build-arg HOST_UID/HOST_GID` override.

2. **`internal/bundler/dockerfile.go`** — `DockerfileContext.UID`
   /`GID` populated from new bundler input (e.g. `BuildOptions.HostUID`
   /`HostGID`). Plumb through `ProjectGenerator` /
   `DockerfileManager`. When unset, fall back to `consts.ContainerUID`
   /`consts.ContainerGID` so existing tests + non-host-built images
   keep working.

3. **`internal/bundler/hash.go`** — `ContentHash` already SHA-256s the
   rendered Dockerfile, which embeds the UID. Different host UID =
   different rendered Dockerfile = different image hash. Multi-user
   hosts naturally get separate images (correct behavior, modest
   cache cost).

4. **`internal/bundler/assets/Dockerfile.tmpl`** — no template change
   needed; `{{.UID}}` / `{{.GID}}` already wired. The only edit might
   be the comment block above `useradd` to document the build-arg
   reasoning.

5. **`internal/bundler/assets/entrypoint.sh`** — no change. `_USER`
   resolves from `$CLAWKER_USER`, which is set in the Dockerfile from
   `${USERNAME}`. UID is whatever the image baked in.

6. **`internal/cmd/container/shared/container_create.go`** — when
   building the default image on demand (`BuildDefaultImage`), pull
   `os.Getuid()`/`os.Getgid()` and feed into bundler options. Pre-built
   images (`build.image:` in `clawker.yaml`) keep current behavior;
   the warning still fires for them when the host UID doesn't match.

7. **`internal/workspace/setup.go`** — once images carry host UID,
   the Linux UID-mismatch warning should compare against the actual
   in-container UID (read from image config, or pass `BuildArgs.HostUID`
   alongside the image label). Today the warning compares to a
   hardcoded `consts.ContainerUID` which would be wrong post-fix.

## Open questions

- **Static `1001` consumers that are NOT host-UID-sensitive**:
  `CopyToVolume` chown, `PreparePostInitTar`, statusline scripts.
  These continue to use 1001 because they target Docker volumes
  (UID-translated by Docker, not bind-mounted). Need to be careful
  not to spray host UID through paths that don't want it.
- **Existing image label `dev.clawker.uid`** doesn't exist yet — add
  it so post-build code can recover the baked-in UID without parsing
  Dockerfile or running `id` inside the image.
- **Pre-built / published images** (e.g. a future `clawker-default:latest`
  on Docker Hub) — these can't bake host UID per consumer. Keep them
  at 1001 and rely on idmapped mounts (Linux 5.12+, Docker 25+) as
  the eventual no-build path. Document this as a separate follow-up.
- **Windows / WSL2** — WSL2 user UIDs are typically also 1000; same
  fix applies. Native Windows file system semantics through Docker
  Desktop are a different beast (case sensitivity, perms emulation);
  out of scope.

## Why not just chown the host dir to 1001

Breaks host-side Claude Code: the user running it on the host as their
own UID then can't write to their own `~/.claude/projects/`. Net loss.

## Why not idmapped mounts

Right answer long-term but requires Linux ≥5.12 + Docker ≥25 + opting
in per mount. A floor-level capability the codebase can't assume yet.
Build-time UID matching is the universal fix; idmapped mounts can
layer in later as an opt-out (`security.idmap_bind_mounts: true`).

## Files most likely to touch when implementing

- `internal/bundler/dockerfile.go`
- `internal/bundler/hash.go` (verify hash includes UID — it does
  transitively through the rendered Dockerfile)
- `internal/cmd/container/shared/image.go` (BuildDefaultImage call site)
- `internal/cmd/container/shared/container_create.go` (warning logic
  + image labels)
- `internal/workspace/setup.go` (UID-mismatch warning needs the
  in-container UID, not the constant)
- `internal/bundler/dockerfile_test.go` (test variants)

## Related

- PR #269 commit 39f349b5 introduced the bind mount + warning.
- `feedback_no_splitting_work.md` — the trueBinPath helper in that
  PR was load-bearing for macOS pre-commit, not scope creep.
- `claude-plugin/clawker-support/skills/clawker-support/reference/known-issues.md`
  has the user-facing entry; remove/update once the fix lands.
