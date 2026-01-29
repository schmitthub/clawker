# Acceptance Test Known Failures

Last run: 2026-01-29 on branch `a/loader-refactor` (macOS Darwin 25.2.0, Docker Desktop, Apple Silicon)

## Summary

67 pass, 21 fail. Failures fall into 5 categories. None are related to the interactive attach flow changes.

---

## Category 1: Container Naming Mismatch (10 tests — timeout at 30s)

**Tests:** `cp`, `exec-basic`, `inspect`, `kill`, `list-format`, `logs-basic`, `pause-unpause`, `run-detach`, `start-stop`, `stats`, `top`

**Symptom:** `wait_container_running` / `wait_container_exit` times out with "context deadline exceeded" or "No such container". The container name used in the wait helper doesn't match the actual container name created.

**Root cause:** The tests use `clawker.$PROJECT.<agent>` as the container name pattern. After the loader refactor, the container naming may produce different results when project resolution is empty or uses a different slug. The `wait_container_running` helper does a Docker inspect on the expected name but the actual container has a different name.

**Example error:**
```
wait_container_running: inspect failed: Get ".../containers/clawker.acceptance-script_logs_basic-5da0c59cd0.logs-5da0c59cd0/json": context deadline exceeded
```

The `logs-basic` test also shows a variant: the container runs and exits before `wait_container_exit` can find it (container already removed or named differently).

**Fix direction:** Investigate how the acceptance test framework resolves project names after the loader refactor. The container names may need 2-segment format (`clawker.<agent>`) when project is empty, or the txtar scripts need updated `$PROJECT` substitution.

---

## Category 2: macOS/Docker Desktop Unsupported Features (5 tests)

**Tests:** `opts-oom`, `opts-memory-swappiness`, `opts-blkio-validation`, `opts-storage-opt`, `opts-cpu-scheduling`

**Symptom:** Features that rely on Linux cgroup v1/v2 kernel support not available on Docker Desktop for macOS.

### opts-oom
- `--oom-kill-disable` triggers Docker warning: "Your kernel does not support OomKillDisable. OomKillDisable discarded."
- Inspect returns `false` but test expects `true`

### opts-memory-swappiness
- Inspect returns empty/0 instead of expected `50`
- macOS Docker Desktop doesn't support memory swappiness tuning

### opts-blkio-validation
- `opts-blkio-validation.txtar:12: have 360 matches for '.', want 1`
- Validation error output differs from expected on macOS

### opts-storage-opt
- `unexpected command failure` — `--storage-opt` not supported by Docker Desktop's storage driver

### opts-cpu-scheduling
- `can't evaluate field CpuShares in type *container.HostConfig`
- This is a **Go template field name bug**: the Docker API uses `CpuShares` (capital S) but the Go struct field may be `CPUShares`. The inspect `--format` template uses wrong casing.

**Fix direction:** Skip these tests on macOS or add platform guards. Fix the `CpuShares` template field name.

---

## Category 3: Container Reference Resolution (3 tests)

**Tests:** `opts-link`, `opts-volumes-from`, `opts-cidfile`

**Symptom:** Commands that reference other containers by name fail with "No such container" or similar.

### opts-link
- `--link clawker.$PROJECT.lnktgt-$RANDOM_STRING:myalias` fails because Docker can't find the target container
- Same naming issue as Category 1 — the container name constructed in the txtar doesn't match the actual name

### opts-volumes-from
- `--volumes-from` references a container that doesn't exist (same naming issue)

### opts-cidfile
- `unexpected command failure` on `--cidfile` — may be related to working directory or path issues in the txtar sandbox

**Fix direction:** Same as Category 1 — fix container name resolution in txtar scripts.

---

## Category 4: Inspect Format Bug (1 test)

**Test:** `opts-entrypoint-clear`

**Symptom:** `no match for '""' found in stdout` — inspecting a cleared entrypoint doesn't produce the expected empty string output.

**Fix direction:** Check what `clawker container inspect --format '{{.Config.Entrypoint}}'` returns for a container with `--entrypoint ""`. May need to handle nil vs empty slice formatting.

---

## Category 5: Build Image Naming (1 test)

**Test:** `root/build`

**Symptom:** `no match for 'clawker-acceptance-script_build-...' found in stdout`. The `clawker build` command produces `clawker:latest` instead of `clawker-<project>:latest`.

**Root cause:** After the loader refactor, when no project is resolved (empty project in test sandbox), the image name defaults to `clawker:latest` without the project prefix. The test expects `clawker-$PROJECT:latest`.

**Fix direction:** Either fix the build command to use the project name from the config file, or update the test expectation.

---

## Category 6: Project Init (2 tests)

**Tests:** `project/init-basic`, `project/init-force`

**Symptom:** `init-force` — after `clawker project init newproj-$RANDOM_STRING --force --yes`, the generated `clawker.yaml` doesn't contain the project name. The YAML has `version: "1"` but no `project:` field.

**Root cause:** After the loader refactor, `project` is no longer persisted in `clawker.yaml` — it comes from the registry (`Config.Project` is `yaml:"-"`). The test still expects `project:` in the YAML file.

**Fix direction:** Update the txtar tests to check the registry file instead of `clawker.yaml` for the project name. Or check `clawker config check` output.

---

## Priority

1. **Category 1 (naming)** — Most impactful, affects 10+ tests. Likely a single root cause in how acceptance test projects resolve names.
2. **Category 6 (project init)** — Quick fix, just update test assertions for registry-based project storage.
3. **Category 5 (build naming)** — Single test, related to empty project handling.
4. **Category 3 (container refs)** — Subset of Category 1.
5. **Category 4 (entrypoint)** — Minor inspect formatting issue.
6. **Category 2 (macOS)** — Platform-specific, lowest priority. Add skip guards or conditional assertions.
