# Dockerfile.tmpl Cache Optimization — UAT (Marker-Free)

## Goal

Validate the cache optimization shipped on `feat/image-optimization` (commit `bb51901c`). Verify by INSPECTION + REAL CHANGE VECTORS, not source-file markers or no-op cheats.

Three things to prove:

1. **Build placement** — rendered Dockerfile has the three-tier asset layout (early root → user-scope → late root) in the expected line order.
2. **Cache configuration** — BuildKit cache mounts (apt, npm, etc.) and ARG directives render correctly and bind at the right scope.
3. **Build speeds** — invalidation tails match design when the right inputs change. Validated through real build-arg overrides, not synthetic edits.

## UAT environment

- Inside clawker dev container (`$CLAWKER_AGENT=dev`). Docker accessible via host socket.
- CLI binary at `bin/clawker` (rebuild: `go build -o bin/clawker ./cmd/clawker`).
- Test project: `/tmp/cache-test/.clawker.yaml` (created with `clawker init --yes --preset Bare`).
- Docker disk budget: ~38/58 GB used at start. Cache-test image is ~1.6 GB and replaces on rebuild.

## Verification plan (no markers, no cheats)

### Step A — Render-time placement audit

Render the Dockerfile produced from the test project's config and inspect structure:

```bash
cd /tmp/cache-test
/Users/andrew/Code/clawker/bin/clawker image build --help  # confirm --build-arg flag present
# Render via dry-render helper if exposed; otherwise read what BuildKit logs:
/Users/andrew/Code/clawker/bin/clawker build --progress plain --no-cache 2>&1 \
  | tee /tmp/baseline.log
```

From the log, capture step ordering. Assertions to confirm by eye AND by `grep`:

- Step that runs `cat > /etc/claude-code/managed-settings.json` (managed-settings heredoc) appears BEFORE the first `USER ${USERNAME}` switch line.
- Step `RUN curl ... claude.ai/install.sh` (Claude Code install) appears AFTER the first `USER ${USERNAME}` switch.
- Steps that COPY `statusline.sh` / `claude-settings.json` / `claude-config.json` appear immediately AFTER Claude Code install, while still under `USER ${USERNAME}` (user-scope seeds).
- Steps for `clawker-agent-prompt.md`, `clawker-ca.crt`, `update-ca-certificates`, `host-open.sh`, `git-credential-clawker.sh`, `callback-forwarder`, `clawker-socket-server`, `chmod +x`, and `clawkerd` appear AFTER the trailing `USER root` switch (late root block).
- `COPY clawkerd` is the last instruction before `ENTRYPOINT`.

Unit test that pins this in CI: `TestBuildContext_LateClawkerBlock` in `internal/bundler/dockerfile_test.go`.

### Step B — Cache directives present

In the same log, grep for cache-mount + ARG directives:

```bash
grep -E '(--mount=type=cache|^ARG |ARG CLAUDE_CODE_VERSION|ARG NODE_VERSION)' /tmp/baseline.log
```

Expectations:

- `--mount=type=cache,target=/var/lib/apt` on the apt-get RUN(s).
- `--mount=type=cache,target=/home/${USERNAME}/.npm` on the Claude Code install RUN (when BuildKit enabled).
- `ARG CLAUDE_CODE_VERSION=<concrete-version>` — concrete value (not literal "latest") because the build command resolved npm.
- `ARG NODE_VERSION=<pinned>` present.
- NO `ENV CLAUDE_CODE_VERSION=` anywhere.

Unit test that pins these: `TestBuildContext_ClaudeCodeVersionIsARG` + the existing Node-install assertions in `internal/bundler/dockerfile_test.go`.

### Step C — Speed baselines

Record two numbers:

1. **Cold full build**: `clawker build --no-cache --progress plain` from clean BuildKit cache. Captures the worst-case time when nothing is reusable.
2. **Warm content-hash short-circuit**: Re-run `clawker build` immediately after a successful build, with no source changes. Should complete in sub-second (clawker's `EnsureImage` checks `ImageExists(hashTag)` and short-circuits to a re-tag).

```bash
docker builder prune -af  # nuke BuildKit cache for clean cold baseline
cd /tmp/cache-test
time /Users/andrew/Code/clawker/bin/clawker build --progress plain 2>&1 | tail -3   # cold
time /Users/andrew/Code/clawker/bin/clawker build --progress plain 2>&1 | tail -3   # warm short-circuit
```

### Step D — ARG cache-bust mechanic (the headline claim)

Validates "ARG default change busts only on first usage" without touching any source file. Uses the existing `--build-arg` flag on `clawker build`:

```bash
# Pick two real published Claude Code versions from npm (e.g. via npm view @anthropic-ai/claude-code dist-tags)
VER_A=<a-real-version>
VER_B=<a-different-real-version>

# Build with version A (warms layer cache for that ARG value)
time /Users/andrew/Code/clawker/bin/clawker build --build-arg CLAUDE_CODE_VERSION=$VER_A --progress plain 2>&1 | tee /tmp/uat-arg-a.log

# Build with version B (different ARG default)
time /Users/andrew/Code/clawker/bin/clawker build --build-arg CLAUDE_CODE_VERSION=$VER_B --progress plain 2>&1 | tee /tmp/uat-arg-b.log
```

Expected on the second build:

- All apt/locale/useradd/git-delta/managed-settings/zsh-in-docker steps `(cached)`.
- `RUN curl ... claude.ai/install.sh ... $CLAUDE_CODE_VERSION` step rebuilds (cache miss — first usage of changed ARG).
- All steps below the install (user-scope seeds, late root block) rebuild as a consequence of the parent layer changing.

This is the canonical demonstration of the ARG cache mechanic. No file marker required.

### Step E — Builder-stage parallelism (optional)

Confirm the two builder stages (`callback-forwarder-builder`, `socket-server-builder`) run in parallel via BuildKit DAG. In the `--progress plain` log, their `RUN go build` lines should interleave timestamps with the final-stage early steps rather than appearing strictly serially.

## TODOs (sequence)

- [ ] **Step A — Placement audit.** Cold build, grep log, eyeball the three-tier layout.
- [ ] **Step B — Cache-directive audit.** Grep for `--mount=type=cache`, `ARG CLAUDE_CODE_VERSION=<concrete>`, no `ENV CLAUDE_CODE_VERSION=`.
- [ ] **Step C — Speed baselines.** Cold time + warm short-circuit time. Record both.
- [ ] **Step D — ARG cache-bust mechanic.** Two `--build-arg CLAUDE_CODE_VERSION=` values; capture which steps invalidate.
- [ ] **Step E (optional) — Builder-stage parallelism.** Eyeball timestamps for DAG concurrency.

## Caveats

- **Content-hash short-circuit:** `EnsureImage` skips the full build if a matching content-hash image exists. To force a real layer-level test, either pass `--no-cache` (heavy — runs everything) or change a real build input (the `--build-arg` path in Step D).
- **Don't touch source files for the UAT.** No markers, no no-op comments. The unit tests already pin the structural invariants in CI (`TestBuildContext_LateClawkerBlock`, `TestBuildContext_ClaudeCodeVersionIsARG`, `TestBuildContext_CollapsedChmod`, `TestBuildContext_FallsBackOnEmptyClaudeCodeVersion`). The UAT here is for human verification of behaviour the unit tests already guarantee about structure.
- **Disk:** watch `docker system df`. `docker builder prune -af` between cold-builds frees ~10+ GB. The `clawker-cache-test:latest` image replaces on rebuild; doesn't accumulate.

## Cleanup after UAT

1. `docker rmi clawker-cache-test:latest` (~1.6 GB).
2. `rm -rf /tmp/cache-test`.
3. `docker builder prune -af` (optional, recover BuildKit cache space).
4. No source-file reverts needed — UAT touches no source.

## Plan file

`/home/claude/.claude/plans/internal-bundler-assets-dockerfile-tmpl-imperative-teacup.md` — design rationale for the three-tier layout.

## IMPORTANT

- **Confirm with the user before each step.** UAT is observational; user may want to inspect results between steps.
- **Delete this memory before the work merges to main.** Use `mcp__serena__delete_memory` with name `dockerfile_cache_optimization_uat`. Confirm with user first.
