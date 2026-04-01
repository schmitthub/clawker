# QA Audit — Round 1 (2026-03-30) — REVIEWED 2026-04-01

Branch: `audit/qa-2026-03-30`
Type: QA | Scope: all | Agents: 7 (concurrency, error handling, input validation, resource leaks, API contracts, test coverage, regression hunter)

**Original totals: 5 CRITICAL, 9 HIGH, 12 MEDIUM, 4 LOW, 3 doc bugs, 6 test gaps**

**Post-review totals: 17 VALID, 4 PARTIAL, 12 INVALID, 3 doc bugs (all valid), 6 test gaps**

Review method: 3 specialized subagents (silent-failure-hunter x2, code-reviewer x3) + Serena code verification, synthesized by lead reviewer.

---

## CRITICAL

### C1: `configFunc` missing `sync.Once` — data race on config init
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/cmd/factory/default.go:274-284`

The nil-check cache (`if cachedConfig != nil || configError != nil`) has no synchronization. A background goroutine in `internal/clawker/cmd.go:56-64` can call `f.Logger()` -> `f.Config()` concurrently with the main goroutine's command execution. Every other Factory lazy noun uses `sync.Once` — this is clearly an oversight.

Downgraded from CRITICAL because: (a) race requires update-check to fail fast (DNS failure), (b) `config.NewConfig()` is deterministic so duplicate calls produce same result, (c) low practical probability.

**Proposed fix:** Replace nil-check with `sync.Once`, matching all other Factory lazy nouns.

---

### C2: Factory `hostProxyFunc`/`socketBridgeFunc` panic on init error
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/cmd/factory/default.go:225-248,251-270`

Both use `panic()` inside `sync.Once` on config/logger errors. No `recover()` exists in callers. However, these are unrecoverable init failures (if config fails, the CLI is inoperable). The root cause is a signature design problem: fields typed as `func() T` have no error channel.

**Proposed fix:** Change Factory field types to `func() (T, error)` and propagate errors through callers. Short-term: add `recover()` in the command layer to convert panics to user-friendly error messages.

---

### C3: `ContainerRestart` error dropped — post-start runs on failed restart
**Verdict: PARTIAL | Revised severity: HIGH**
Location: `internal/cmd/container/restart/restart.go:189-203`

The restart error IS returned if PostStart succeeds (`return err` at end). But: (1) PostStart runs unnecessarily on a failed restart, and (2) if BOTH fail, the PostStart error shadows the restart error — the root cause is lost.

**Proposed fix:** Check `err` from `ContainerRestart` before calling `BootstrapServicesPostStart`. If both fail, use `errors.Join(err, errBootstrapPost)`.

---

### C4: `ProjectRegistry.Fields()` will panic — missing `KindFunc` for `map[string]WorktreeEntry`
**Verdict: INVALID**

`NormalizeFields` classifies `[]ProjectEntry` as `KindStructSlice` — a leaf node that is NOT recursed into. The `map[string]WorktreeEntry` inside `ProjectEntry` is never reached. `TestProjectRegistryFields_AllFieldsHaveDescriptions` (schema_test.go:26-31) passes without panic.

---

### C5: Firewall bootstrap in PostStart, not PreStart — containers start unprotected
**Verdict: INVALID**

`Enable()` uses `docker exec` to run iptables inside the container — requires a running container. PreStart placement is architecturally impossible. The entrypoint script (`internal/bundler/assets/entrypoint.sh:96-100`) contains a firewall readiness gate blocking CMD until `/var/run/clawker/firewall-ready` is touched by `Enable()`. No unprotected window exists.

---

## HIGH

### H1: Path traversal via `EgressRule.Dst` in cert file writes
**Verdict: VALID | Revised severity: LOW (defense-in-depth only)**
Location: `internal/firewall/certs.go:202-203`

Domain names used directly in `filepath.Join(certDir, normalized+"-cert.pem")`. `normalizeDomain()` only strips dots — no validation for path separators.

**Severity rationale:** Requires host shell access to run `clawker firewall add`. If attacker has host access, they already own everything. Containers cannot run clawker, rebuild images, modify firewall config, or write to host filesystem. Even poisoning the config file requires a rebuild or binary invocation that containers don't have. No realistic attack path exists.

**Proposed fix:** Add domain validation in `normalizeRule` rejecting non-RFC-1123 characters. Fixes H1 and H2 simultaneously. Low priority.

---

### H2: CoreDNS config injection via unsanitized domain
**Verdict: VALID | Revised severity: LOW (defense-in-depth only)**
Location: `internal/firewall/coredns.go:68`

Domain names interpolated into CoreDNS Corefile via `fmt.Fprintf(&b, "%s {\n", domain)`. Same attack surface analysis as H1 — requires host access, containers have no path to exploit.

**Proposed fix:** Same domain validation as H1.

---

### H3: Signal goroutine leak in `StartAlternateScreenBuffer`
**Verdict: VALID | Severity: HIGH**
Location: `internal/iostreams/iostreams.go:167-175`

Goroutine blocks on `<-ch` forever if alternate screen is stopped normally. Signal channel never unregistered via `signal.Stop(ch)`. Repeated start/stop cycles accumulate leaked goroutines. The goroutine also calls `os.Exit(1)` which skips deferred cleanup (documented project gotcha).

**Proposed fix:** Store signal channel on `IOStreams`. In `StopAlternateScreenBuffer`, call `signal.Stop(ch)` and close channel. Use a done channel pattern for clean goroutine exit.

---

### H4: TOCTOU in `EnsureDaemon` — duplicate firewall daemons
**Verdict: PARTIAL | Revised severity: LOW**
Location: `internal/firewall/daemon.go:327-338`

Real TOCTOU gap but mitigated by: PID file overwrite is atomic, Docker container name uniqueness prevents duplicates, practical window is small. Risk is resource waste, not data corruption.

---

### H5: Docker client never closed in firewall daemon
**Verdict: VALID | Revised severity: LOW**
Location: `internal/firewall/daemon.go:70-84`

Docker client never `Close()`d. Low practical impact — daemon is single-process lifecycle where OS handles cleanup on exit.

**Proposed fix:** Add `defer d.docker.Close()` in `Run()`.

---

### H6: `netip.Addr.String()` zero value guard never fires
**Verdict: VALID | Revised severity: MEDIUM**
Location: `internal/firewall/manager.go:524-535`

Zero-value `netip.Addr.String()` returns `"invalid IP"`, so `if clientIP == ""` never catches it. Best-effort telemetry only.

**Proposed fix:** Check `ep.IPAddress.IsValid()` instead of `clientIP == ""`.

---

### H7: `io.ReadAll` error discarded in firewall Enable/Disable
**Verdict: PARTIAL | Revised severity: MEDIUM**
Location: `internal/firewall/manager.go:394,467`

`output, _ := io.ReadAll(hijack.Reader)` — output is supplementary diagnostic text in error messages. Primary decision point (exit code) is checked correctly. Degraded error messages, not silent failure.

**Proposed fix:** Log a warning on `io.ReadAll` failure.

---

### H8: `InitContainerConfig` nil logger panic
**Verdict: INVALID**

All production call sites obtain logger from Factory with error handling. No realistic nil logger scenario exists.

---

### H9: Broken `mockContainerLister` — tests pass for wrong reasons
**Verdict: VALID | Severity: HIGH**
Location: `internal/hostproxy/daemon_test.go:23-34`

Mock creates items slice then discards it (`_ = items`), always returns empty `ContainerListResult{}`. `TestWatchContainers_RespectsContextCancellation` sets `containerCount: 5` but mock returns 0 — watcher exits on "zero containers" before context cancellation fires. Test passes vacuously.

**Proposed fix:** Return `ContainerListResult{Items: items}` instead of discarding items.

---

## MEDIUM

### M1: Logger double-close in bootstrap phases
**Verdict: INVALID (stated claim); note: subtle log loss after first close**

`logger.Close()` is idempotent (mutex + closed flag, verified by `TestClose_Idempotent`). However, after PreStart's close, PostStart writes to closed writer — those logs silently lost. LOW impact.

---

### M2: Unescaped JSON in Loki push
**Verdict: VALID | Severity: MEDIUM**
Location: `internal/firewall/manager.go:545-554`, `firewall.sh:117-122`

`fmt.Sprintf` with `%s` to build JSON. Agent/project names with quotes break JSON. Best-effort telemetry.

**Proposed fix:** Use `encoding/json` in Go, `jq` in shell.

---

### M3: `CLAWKER_LOKI_PORT` host vs container port confusion
**Verdict: INVALID**

Loki uses `"{{.LokiPort}}:{{.LokiPort}}"` — same port on both sides.

---

### M4: `NeedsSocketBridge` nil guard suppresses SSH bridge
**Verdict: VALID | Revised severity: HIGH**
Location: `internal/cmd/container/shared/container_start.go:29-34`

When `cfg.Security.GitCredentials == nil`, returns `false`. But `GitSSHEnabled()` and `GPGEnabled()` handle nil receivers and default to `true`. Fresh projects silently skip socket bridge.

**Proposed fix:** Remove the `cfg.Security.GitCredentials == nil` guard — the methods already handle nil.

---

### M5: `ip, _ := netip.ParseAddr` silently uses zero IP
**Verdict: INVALID**

Input guaranteed valid by upstream `discoverNetwork()`.

---

### M6: `waitForPIDFile` blocks 5s without context, holding mutex
**Verdict: VALID | Severity: MEDIUM**
Location: `internal/socketbridge/manager.go:316-325`

5-second polling loop holding `m.mu.Lock()`. All other mutex-guarded methods blocked.

**Proposed fix:** Accept `context.Context`, or drop mutex before blocking wait.

---

### M7: Storage `Refresh()` silently skips unreadable layers
**Verdict: VALID | Severity: MEDIUM**
Location: `internal/storage/store.go:665-671`

Silently swallows `loadRaw` errors. Constructor treats same case as hard error. Inconsistency.

**Proposed fix:** Log warning or accumulate/return errors.

---

### M8: Container name total length unbounded — Docker 128 limit
**Verdict: PARTIAL | Severity: LOW**

Per-component 128 validation exists. Composed name not re-validated. Practically mitigated.

---

### M9: Port fields accept out-of-range values
**Verdict: VALID | Severity: LOW**

No schema-level port validation. Runtime validation exists for some paths.

---

### M10: `*bool` nil dereference in bundler monitoring telemetry
**Verdict: PARTIAL | Severity: MEDIUM**
Location: `internal/bundler/dockerfile.go:309-312`

Four `*bool` dereferenced without nil checks. Production `NewConfig()` populates defaults, but `NewFromString` does not. Latent panic for non-default config construction.

**Proposed fix:** Add nil checks with default fallbacks.

---

### M11: `ensureImage` silently ignores `io.Copy` error
**Verdict: VALID | Revised severity: LOW**
Location: `internal/firewall/manager.go:1050-1065`

Incomplete pull reported as success. Subsequent `ContainerCreate` fails with confusing error.

---

### M12: `BridgesSubdir`/`PidsSubdir` return identical paths
**Verdict: VALID | Revised severity: LOW (tech debt)**

Intentional — `BridgesSubdir` is deprecated alias. TODO exists in code.

---

## LOW

### L1-L4: ALL INVALID
- **L1**: One-shot timer (not in loop) — not a leak
- **L2**: Events ARE logged when dropped (`log.Warn()`)
- **L3**: Correct accessor `SettingsStore().Read()` is used
- **L4**: Intentional fire-and-forget with `context.Background()` + own timeout

---

## Doc Bugs (all VALID)

- **D1**: `hostproxy/CLAUDE.md` — `NewManager` and `NewDaemon` missing `log` param
- **D2**: `project/CLAUDE.md` — `NewProjectManager` missing `log` param + error return
- **D3**: `config/CLAUDE.md` — `LabelMonitoringStack()` documented but doesn't exist

---

## Test Gaps (zero coverage)

1. `BootstrapServicesPostStart` firewall-enabled path (production default)
2. `BootstrapServicesPostStart` socket bridge path
3. `ProjectRules()` firewall rule construction
4. `WaitForHealthy` timeout/polling
5. `ResolveAgentEnv` precedence logic
6. Firewall daemon stale PID file handling

---

## Review Summary

| Category | Valid | Partial | Invalid | Total |
|----------|-------|---------|---------|-------|
| CRITICAL | 2 (C1,C2) | 1 (C3) | 2 (C4,C5) | 5 |
| HIGH | 3 (H3,H6,H9) | 2 (H4,H7) | 1 (H8) | 9 |
| MEDIUM | 5 (M2,M4,M6,M7,M9) | 2 (M8,M10) | 3 (M1,M3,M5) | 12 |
| LOW | 0 | 0 | 4 (L1-L4) | 4 |
| Doc Bugs | 3 (D1,D2,D3) | 0 | 0 | 3 |

H1,H2 valid but downgraded to LOW (host access required, containers can't exploit). H5 valid but LOW (OS cleanup). M11,M12 valid but LOW.

### Priority fixes (by impact):
1. **M4**: Remove `GitCredentials == nil` guard in `NeedsSocketBridge` (breaks default SSH/GPG)
2. **H9**: Fix `mockContainerLister` to return items (tests pass vacuously)
3. **H3**: Fix signal goroutine leak in `StartAlternateScreenBuffer`
4. **C1**: Add `sync.Once` to `configFunc` (data race)
5. **C3**: Check restart error before PostStart; `errors.Join` for dual failure
6. **H6**: Use `ep.IPAddress.IsValid()` instead of string comparison
7. **M2**: Use `encoding/json` for Loki telemetry payloads
8. **M6**: Accept context in `waitForPIDFile`, release mutex during wait
9. **M7**: Log warning in `Refresh()` when layer is unreadable
10. **D1+D2+D3**: Update CLAUDE.md docs
11. **H1+H2**: Add domain validation in rule pipeline (low priority, defense-in-depth)