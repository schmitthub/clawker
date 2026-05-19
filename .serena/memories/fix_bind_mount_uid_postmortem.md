# Post-mortem: bind-mount UID mismatch fix

Branch: `fix/uid`. Live document. Appended-to after each phase.

## Plan reference

`/home/claude/.claude/plans/look-at-the-uid-wild-pinwheel.md`

## Design (locked at plan approval)

- `consts.ContainerUID` / `consts.ContainerGID`: refactor from `const`
  to `var`, init from `os.Getuid()` / `os.Getgid()` with fallback to
  `1001`. They become the CLI-side accessors. All existing callers
  (4 direct + 4 via deprecated `cfg.ContainerUID/GID()`) get host UID
  transparently — zero call-site changes for CLI.
- `consts.HostUID` / `consts.HostGID`: new env-fed package vars in
  `internal/consts/controlplane.go`. Read `CLAWKER_HOST_UID` /
  `CLAWKER_HOST_GID` via a private `resolveHostUID` helper; fallback
  to `ContainerUID` / `ContainerGID`. CP-only.
- CLI sets the env vars on CP container in `BuildCPContainerConfig`.
- One CP caller (`controlplane/agent/init.go::userStage`) swaps to
  `consts.HostUID` / `consts.HostGID`.
- Workspace UID-mismatch warning at `setup.go:205-216`: dead post-fix,
  delete.

## Phase 1 — `consts.go` refactor

**What I did**

- Split the const block at `internal/consts/consts.go:256-266`. Kept
  `ContainerUser` + `ContainerHomeDir` as `const`. Added private
  `fallbackContainerUID = 1001` / `fallbackContainerGID = 1001` consts
  for the last-resort default.
- Moved `ContainerUID` / `ContainerGID` to `var` declarations
  initialized by IIFEs that read `os.Getuid()` / `os.Getgid()` and
  fall back to `fallbackContainer{UID,GID}` when the syscall returns
  `-1` (Windows).
- Doc comments name the call-site contract: CLI uses these; CP must
  use `HostUID` / `HostGID` because `os.Getuid()` inside the CP
  container is not the host UID.

**Decisions due to unforeseen issues**

- Introduced a separate `fallbackContainerUID` private const instead
  of inlining `1001`. Makes the fallback explicit and reusable —
  Phase 2's `HostUID/HostGID` env-fed vars will reference the same
  symbol (well, reference `ContainerUID`/`ContainerGID` directly per
  plan, but keeping the private const documents intent and lets future
  callers reference the magic number without re-grep'ing).
- `os` package was already imported in `consts.go` — no new imports.

**Bugs found**

- None mid-flight in this phase.

**Gotchas**

- Pre-existing diagnostics in `embed.go`/`embed_cp.go`/`embed_coredns.go`
  about missing `assets/clawkerd`, `assets/clawker-cp`, `assets/coredns-clawker`
  — these are build-time artifacts (`make cp-binary`, `make ebpf`,
  etc. populate them). Unrelated to UID refactor. Surfacing here so a
  future me doesn't chase them as if caused by this work.
- Diagnostic `manager.go` undefined `clawkerContainerConfig` etc. — same
  category: bpf2go-generated bindings missing until `make ebpf` runs.

## Phase 2 — `controlplane.go` additions

**What I did**

- Added `EnvHostUID` / `EnvHostGID` to the existing env-var const block,
  with a comment noting they degrade rather than fail-fast (unlike the
  four `HostDir` env vars validated by `HostDirs.Validate`).
- Added `HostUID` / `HostGID` package vars, fed by a new private
  `resolveHostUID(envName, fallback) int` helper. Helper handles env-empty,
  env-non-numeric, env-negative, env-set. Falls back to `ContainerUID`/
  `ContainerGID` (which themselves fall back to 1001).
- Doc comment names the call-site contract and the auto-memory
  degraded-mode consequence.
- Imported `strconv`.

**Decisions due to unforeseen issues**

- Named the helper `resolveHostUID` (lowercase, package-private). Lets
  Phase 4's `agent/init_test.go` call the helper directly with a known
  env name to assert wiring without the package-init-once trap.
- Helper rejects negative values via `v < 0`. `strconv.Atoi` already
  parses leading `-`, so a stray `-1` would have leaked through; the
  explicit check matches `os.Getuid()`'s own ≥0 contract used in
  Phase 1 IIFEs.
- Wrote `host_user_test.go` (not `consts_test.go`) so both Phase 1 and
  Phase 2 tests are colocated in a clearly-named file. There was no
  existing consts test file to extend, so naming is from scratch.

**Bugs found**

- None.

**Gotchas**

- Go package var init order: `HostUID = resolveHostUID(EnvHostUID,
  ContainerUID)` evaluates after `ContainerUID` because Go's spec
  guarantees dependency-ordered initialization. Verified by passing
  test. If `ContainerUID` were later inlined into a `const` (it can't,
  but a future refactor might try), `HostUID` would silently break —
  worth documenting if anyone proposes that.
- `t.Setenv` is the safe path. Tried `os.Unsetenv` for the unset case
  intentionally because `t.Setenv` only restores the prior value at
  end of test — fine for the unset → unset case since the probe env
  starts unset.

## Phase 3 — `cpboot/cp_container.go` env wiring

**What I did**

- Appended two entries to the `Env` slice in `BuildCPContainerConfig`
  at `internal/controlplane/cpboot/cp_container.go:316-317`:
  `CLAWKER_HOST_UID=<consts.ContainerUID>` and the GID twin.
  `strconv` was already imported.
- Added comment block explaining the contract: in the CLI process
  `consts.ContainerUID/GID` resolve via `os.Getuid()/Getgid()`, so the
  env values plumbed into the CP container are the host invoker's
  UID/GID at boot time.
- Added `TestCPContainer_HostUIDGIDEnv_Emitted` to
  `container_config_test.go` asserting both env entries land in
  `cpConfig.Env`. Added `strconv` import to the test file.

**Decisions due to unforeseen issues**

- None on the code change itself.

**Bugs found**

- None.

**Gotchas**

- The cpboot package depends on three embedded binaries via `go:embed`:
  `assets/clawker-cp`, `assets/ebpf-manager`, and (transitively via
  the firewall embed) `assets/coredns-clawker`. Plus `clawkerd`
  asset embedded in `internal/clawkerd/`. None of these are tracked
  in git — they're built fresh per environment. The Makefile target
  `make cp-binary` does NOT cascade-build them; each
  has its own target: `make cp-binary`, `make ebpf-binary`,
  `make coredns-binary`, `make clawkerd-binary`.
- The Makefile uses `go build -trimpath` which causes
  `error obtaining VCS status: exit status 128` inside a worktree.
  Workaround: `make GOFLAGS="-trimpath -buildvcs=false" <target>`.
  This is worth a future doc/Makefile update — the build is brittle
  for first-time worktree contributors who don't know the incantation.
  (Plan deviation: noted as follow-up, not addressed here — out of
  scope for the UID fix.)

## Phase 4 — `agent/init.go` caller swap

**What I did**

- Swapped `uint32(consts.ContainerUID)` → `uint32(consts.HostUID)` and
  the GID twin at `internal/controlplane/agent/init.go:702-703` inside
  `userStage`.
- Expanded the function's doc comment to explain WHY: CP runs in its
  own container; `os.Getuid()` inside CP is the CP image's UID, not the
  host's. `consts.HostUID` reads `CLAWKER_HOST_UID` set by the CLI when
  launching the CP container, which matches the UID the CLI baked into
  the agent image's claude user.
- Existing tests pass without modification.

**Decisions due to unforeseen issues**

- **Skipped a dedicated `userStage` unit test.** I planned one but
  hit a tautology problem: in unit-test context, `consts.HostUID ==
  consts.ContainerUID` (both fall back to `os.Getuid()` when
  `CLAWKER_HOST_UID` is unset). Asserting `userStage(...).Uid ==
  uint32(consts.HostUID)` would pass regardless of which alias the
  code reads, so it cannot catch the very regression it would
  ostensibly guard against. Phase 2's `resolveHostUID` tests cover
  the env-pipeline; Phase 7 E2E covers end-to-end behavior; Phase 8
  manual verification confirms the host-UID-to-baked-UID alignment.
  This is a [[feedback_no_tautological_tests]] avoidance — recording
  here so a code-reviewer who's wondering "why no unit test?" sees
  the reasoning.

**Bugs found**

- None.

**Gotchas**

- `consts.HostUID` is `int` while `PipeStage.Uid` is `uint32`. The
  cast `uint32(consts.HostUID)` is fine for any normal UID but would
  silently wrap if `consts.HostUID` somehow becomes negative. The
  Phase 2 resolver explicitly rejects negative env values; the Phase 1
  IIFE rejects negative `os.Getuid()`; both fall back to 1001
  (positive). So the cast is safe. Worth not removing the negative
  guard in either accessor.

## Phase 5 — `workspace/setup.go` dead-warning deletion

**What I did**

- Removed the `if runtime.GOOS == "linux" && os.Getuid() !=
  consts.ContainerUID` block (warning log + Warnings append) at
  `internal/workspace/setup.go:205-216`.
- Dropped now-unused `runtime` and `consts` imports.
- Updated the surrounding doc comment to say UID mismatch is no
  longer surfaced (container UID is host-derived by construction).
- Kept `var mountWarnings []string` + `Warnings` field on
  `SetupMountsResult` as a generic channel for future non-fatal mount
  diagnostics. Removing it would be API churn beyond plan scope.

**Decisions due to unforeseen issues**

- Left `Warnings` field on `SetupMountsResult` intact. The struct
  field is now always populated with `nil`. Alternative was removing
  the field entirely, but that requires updating every caller's
  unpack and felt like scope creep on a focused UID fix. Recording so
  a future cleanup PR knows the right path.

**Bugs found**

- None.

**Gotchas**

- `os` import is RETAINED — it's used at line 87 (`os.Getwd`) and
  259/261 (`os.Stat`/`os.IsNotExist`) for unrelated paths. Don't
  blindly remove `os` along with `runtime` and `consts`. The compiler
  catches it but a sloppy edit would have wasted a cycle.
- `mountWarnings` slice variable retained even though no callsite
  appends to it post-deletion — it's used at the result-struct
  construction. Could be inlined as `nil`, but leaving the named slot
  makes future warning-emitters obvious.

## Phase 6 — Doc updates

**What I did**

- `internal/workspace/CLAUDE.md`: rewrote the "Linux UID mismatch"
  bullet under "Failure handling". Dropped the now-false claim that
  container UID cannot be overridden at runtime; replaced with the
  new contract (UID baked from host invoker at build time, CP drops
  via `consts.HostUID` from env).
- `internal/config/CLAUDE.md`: updated the `ContainerUID() (1001)`
  line to reflect host-derived semantics and call out the CP-side
  alternative.
- `internal/workspace/setup.go`: in-file comment block above
  `mountWarnings` rewritten to match.

**Decisions due to unforeseen issues**

- None.

**Bugs found**

- None.

**Gotchas**

- The workspace CLAUDE.md previously referenced specific line numbers
  in `Dockerfile.tmpl` (L172-176). I removed that reference instead
  of repointing it — line numbers in docs drift silently. Pattern:
  describe the contract, not the file coordinate
  ([[feedback_no_pr_refs_in_docs]] applies analogously to line numbers).

## Phase 7 — Test sweep

**What I did**

- `make GOFLAGS="-trimpath -buildvcs=false" test` →
  `DONE 4725 tests, 8 skipped in 32.823s`. All non-Windows / non-root
  / non-integration tests pass.
- `go vet ./internal/consts/... ./internal/controlplane/cpboot/...
  ./internal/controlplane/agent/... ./internal/workspace/...
  ./internal/bundler/... ./internal/containerfs/... ./internal/docker/...`
  — silent, no findings.
- `go build -buildvcs=false ./cmd/clawker` — OK.

**Decisions due to unforeseen issues**

- **E2E suite NOT run.** This session executes inside a clawker
  container (`CLAWKER_AGENT=uid`). The project's
  `.claude/rules/testing.md` and root `CLAUDE.md` are explicit:
  running `go test ./test/e2e/...` from inside the container would
  tear down the host's control plane. Phase 7's E2E portion and
  Phase 8's manual `clawker run` verification both require the user
  to execute on the host. Documented as a hand-off below.
- Did not add a new `TestClaudeProjectsMount_HostUIDWritesBack_E2E`
  test file. The change should land first, then a follow-up E2E PR
  from a host-side environment that can safely exercise the full
  stack. Adding the file now risks landing an untested integration
  test that nobody verified runs end-to-end.

**Bugs found**

- None.

**Gotchas**

- `make test` does NOT include `make GOFLAGS="-trimpath
  -buildvcs=false"`; without that override the build fails with
  `error obtaining VCS status` inside a worktree. The Makefile sets
  `GOFLAGS := -trimpath` on line 17, which overrides whatever's in
  env, so `GOFLAGS=... make test` is silently ignored. Must use
  `make GOFLAGS="..."` syntax (command-line variable, not env).
  Same gotcha already recorded in Phase 3.

**Hand-off to user (host-side)**

1. **Build the new clawker:**
   ```
   make GOFLAGS="-trimpath -buildvcs=false" clawker
   ```
2. **Wipe bundler cache so the new UID gets baked into a fresh image:**
   ```
   rm -rf ~/.local/share/clawker/build/dockerfiles/*
   ```
3. **Bring up a test agent:**
   ```
   CLAWKER_AGENT=uidtest clawker run
   ```
4. **Inspect the running stack:**
   - `docker exec clawker.uidtest id claude` — expect `uid=$(id -u)`
     and `gid=$(id -g)` (not 1001 on a typical host).
   - `docker inspect clawker-controlplane --format '{{json .Config.Env}}' | tr ',' '\n' | grep CLAWKER_HOST_`
     — expect both `CLAWKER_HOST_UID=$(id -u)` and `CLAWKER_HOST_GID=$(id -g)`.
   - Drive Claude Code briefly to force a session-jsonl write.
   - `ls -la ~/.claude/projects/` — new jsonls owned by your host UID.
   - Stop the agent (`clawker container stop clawker.uidtest`), start
     again, and confirm auto-memory persists.
5. **Targeted E2E (optional but recommended):**
   ```
   go test ./test/e2e/... -v -timeout 10m -run TestClaudeProjectsMount
   ```
6. **CP fallback check (optional):**
   - Take down the CP normally, then manually `docker run` a
     `clawker-controlplane` image WITHOUT `CLAWKER_HOST_UID` /
     `CLAWKER_HOST_GID` env vars. The daemon should boot; userStage
     drops to 1001 (degraded auto-memory but no crash).

## Phase 8 — Manual verification

**Status: DEFERRED to user (host-side)**

Cannot execute from inside a clawker container. See Phase 7 hand-off
section for the exact command sequence the user should run on the
host to verify the fix end-to-end.

## Completion gate

**What I did**

Launched 4 reviewer subagents in parallel against the diff:
`code-reviewer`, `silent-failure-hunter`, `type-design-analyzer`,
`test-hunter`.

### Findings + actions

**code-reviewer**

1. (Important) Host-UID bakes into image hash; cache no longer shareable
   across users on same host. Worth a doc note.
   → **Addressed** in `internal/bundler/CLAUDE.md` (added paragraph
   explaining host-UID effect on content hash; called out it's
   intentional, not a cache bug).
2. (Important) Should UID 0 be allowed? Sudo'd CLI propagates root.
   → **Addressed** — see silent-failure-hunter #1 below.
3. (Nit) `resolveHostUID` name applies to GID too. → Skipped; renaming
   churn outweighs nit.
4. (Nit) Init-order fragility. → **Addressed** with one-line doc
   comment above `HostUID`.

**silent-failure-hunter**

1. (CRITICAL) Silent fallback to UID 0 when env missing. CP container
   runs as root for BPF caps; `os.Getuid()` inside CP = 0; without
   the env, `resolveHostUID(env, ContainerUID)` falls back to
   `ContainerUID` which inside CP is 0; `userStage` then drops to
   uid 0 silently, defeating drop-priv contract.
   → **Addressed** by two changes:
   - Decoupled `HostUID/HostGID` fallback from `ContainerUID`. They
     now fall back to `fallbackContainerUID/GID` (1001) directly. CP
     process never inherits a 0 from `ContainerUID` via this path.
   - Hardened `resolveHostUID` to reject `v == 0` (in addition to
     negative). Sudo'd CLI propagation of UID 0 also gets caught.
   - Hardened `ContainerUID/GID` IIFEs to reject `os.Getuid() == 0`
     (in addition to -1).
2. (HIGH) `strconv.Atoi` error swallowed silently.
   → **Addressed** — `resolveHostUID` now emits a structured stderr
   line `event=host_uid_invalid env=X value=Y error=Z action=fallback`
   on parse error or non-positive value. Stays silent on env-unset
   (expected CLI-process state). Lands in `docker logs <cp>` for
   the CP-container case.
3. (HIGH) Deleted workspace warning erases diagnostic for foreign
   images. Recommends runtime image-inspect probe.
   → **Deferred to follow-up.** Implementing a runtime probe (read
   agent image's claude user UID via Docker image inspect at
   container-create time, compare to `consts.ContainerUID`) is a
   meaningful feature, not in this fix's scope. Recorded here.
4. (HIGH) Stale image cache window post-upgrade.
   → **Mitigated** by the bundler-CLAUDE.md note: content hash
   changes when UID changes. Old cached images with `ContainerUID =
   1001` will cache-miss after upgrade and rebuild. The "image
   resolved by tag without consulting hash" case is the foreign-
   image path covered by deferred finding #3.
5. (MEDIUM) Package-var IIFE can't log.
   → **Partially addressed** — `resolveHostUID` now logs to stderr
   on the invalid-env paths, which is the load-bearing case. The
   structured-logger-via-Resolve(log) refactor is the architectural
   answer; scope creep for this PR. Deferred.
6. (MEDIUM) Test asserts wiring not behavior.
   → Behavior is now exercised by `TestResolveHostUID` covering all
   guard branches.

**type-design-analyzer**

Ratings: Encapsulation 2/5, Invariant 2/5, Usefulness 4/5,
Enforcement 2/5. Recommends typed getter functions returning
`uint32` to kill mutability + cast asymmetry.

→ **Deferred.** Real concerns but a typed-getter refactor changes
every call site (`consts.ContainerUID` → `consts.ContainerUID()`)
and diverges from the existing `HostConfigDir`-as-var precedent.
Doc comments + the new IIFE/resolver guards keep the runtime
invariants intact. Recording here so a follow-up cleanup PR knows
the suggested shape.

**test-hunter**

1. DELETE `TestContainerUID_TracksHostGetuid` (tautology).
   → **Done.** Removed the test entirely.
2. REWRITE `TestResolveHostUID` — drop duplicate `env_empty` case.
   → **Done.** Dropped `env_empty`. Added new `env_set_zero` case
   to cover the UID-0-rejection branch (silent-failure-hunter #1).
3. KEEP `TestCPContainer_HostUIDGIDEnv_Emitted`.
   → Untouched.

### Final retest after review fixes

`make GOFLAGS="-trimpath -buildvcs=false" test` →
`DONE 4723 tests, 8 skipped in 22.685s`. Drop of -2 vs first run
accounts for the deleted tautological test + the merged subcase.

## Implementation summary

**Diff shape** (8 files modified + 1 new test file + 1 new memory):

| File | Lines added | Purpose |
|---|---|---|
| `internal/consts/consts.go` | +36 / -6 | `ContainerUID/GID` const→var, reject UID 0 |
| `internal/consts/controlplane.go` | +60 / -1 | `EnvHostUID/GID`, `HostUID/GID`, `resolveHostUID` |
| `internal/consts/host_user_test.go` | NEW | 5-case table for `resolveHostUID` |
| `internal/controlplane/cpboot/cp_container.go` | +8 | Set `CLAWKER_HOST_UID/GID` on CP container Env |
| `internal/controlplane/cpboot/container_config_test.go` | +21 | `TestCPContainer_HostUIDGIDEnv_Emitted` |
| `internal/controlplane/agent/init.go` | +12 / -3 | `userStage` reads `HostUID/HostGID` |
| `internal/workspace/setup.go` | -22 / +6 | Delete dead warning + drop runtime/consts imports |
| `internal/workspace/CLAUDE.md` | doc rewrite | UID mismatch bullet → new contract |
| `internal/config/CLAUDE.md` | doc fix | `ContainerUID()/GID()` no longer 1001 |
| `internal/bundler/CLAUDE.md` | +1 paragraph | Host-UID effect on content hash |

**Deviations from plan**

- Plan said fall back to `ContainerUID` in `resolveHostUID`. Review
  caught that this would silently drop CP `userStage` to root.
  Switched fallback to `fallbackContainerUID` (1001) directly.
- Plan didn't reject UID 0 in the CLI-side IIFE. Review (code-reviewer
  + silent-failure-hunter) flagged the sudo case. Added `> 0` guard.
- Plan didn't include stderr logging on invalid env. Review flagged
  the silent-failure cost. Added one-line stderr emit on parse error
  / non-positive value.
- Plan said add a `TestClaudeProjectsMount_HostUIDWritesBack_E2E`.
  Skipped — running E2E from inside a clawker container would tear
  down the host CP. Deferred to host-side hand-off.

**Surprises**

- Embed assets (`clawkerd`, `clawker-cp`, `coredns-clawker`,
  `ebpf-manager`) aren't tracked in git and must be built per
  environment. `make test` doesn't cascade-build them; each has its
  own target. The `-trimpath` build flag also requires
  `-buildvcs=false` inside a worktree.
- `consts.ContainerUID` was a `const` that EVERY caller already
  read (directly or via `cfg.ContainerUID()` deprecated delegate).
  Refactoring it from const to var was almost zero call-site churn
  — only the CP-side userStage needed a manual swap, and that was
  for a different accessor (`HostUID`).
- The reviewer subagents found multiple real issues that would have
  shipped silent failures into production. The completion gate
  pattern paid for itself this round.

**Follow-up items**

1. **Image-inspect runtime probe** for foreign / mismatched images.
   Read `dev.clawker.uid` label (or `getent passwd claude` via exec)
   at container create time; warn-level log + `Warnings` entry on
   mismatch against `consts.ContainerUID`. Covers silent-failure
   findings #3 + #4 cleanly.
2. **Typed UID getter functions.** Convert `HostUID/HostGID` (and
   maybe `ContainerUID/GID`) to `func() uint32`. Kills exported
   mutability + the `uint32(...)` cast asymmetry at every call site.
   Suggested shape: type alias + unexported var + exported getter.
3. **Resolve(log) refactor.** Move resolution from package-var init
   to explicit `consts.ResolveHostIdentity(log)` called from
   `main()` in both CLI and CP entry points, so fallback events
   land in the project's structured logger surface (`ControlPlaneLogFile`)
   rather than raw stderr.
4. **Makefile UX.** Document the `-buildvcs=false` requirement in
   `make test` for worktree contributors. Could also rewrite
   the Makefile's `GOFLAGS := -trimpath` to `GOFLAGS ?= -trimpath
   -buildvcs=false` so the env-var-supplied override works.
