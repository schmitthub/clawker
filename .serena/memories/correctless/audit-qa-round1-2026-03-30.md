# QA Audit — Round 1 (2026-03-30)

Branch: `audit/qa-2026-03-30`
Type: QA | Scope: all | Agents: 7 (concurrency, error handling, input validation, resource leaks, API contracts, test coverage, regression hunter)

**Totals: 5 CRITICAL, 9 HIGH, 12 MEDIUM, 4 LOW, 3 doc bugs, 6 test gaps**

Full findings file: `.serena/memories/correctless/audit-qa-round1-2026-03-30.md`

## CRITICAL

- **C1**: `configFunc` missing `sync.Once` — data race on config init (`cmd/factory/default.go:275`)
- **C2**: Factory `hostProxyFunc`/`socketBridgeFunc` panic on init error instead of returning error (`cmd/factory/default.go:235,261`)
- **C3**: `ContainerRestart` error dropped — post-start runs on failed restart (`cmd/container/restart/restart.go:189`)
- **C4**: `ProjectRegistry.Fields()` will panic — missing `KindFunc` for `map[string]WorktreeEntry` (`config/schema.go:422`)
- **C5**: Firewall bootstrap in PostStart, not PreStart — containers start unprotected (`cmd/container/shared/container_start.go`)

## HIGH

- **H1**: Path traversal via `EgressRule.Dst` in cert file writes (`firewall/certs.go:144`)
- **H2**: CoreDNS config injection via unsanitized domain (`firewall/coredns.go:58`)
- **H3**: Signal goroutine leak in `StartAlternateScreenBuffer` (`iostreams/iostreams.go:166`)
- **H4**: TOCTOU in `EnsureDaemon` — duplicate firewall daemons (`firewall/daemon.go:327`)
- **H5**: Docker client never closed in firewall daemon (`firewall/daemon.go:70`)
- **H6**: `netip.Addr.String()` zero value guard never fires — bad IP to Loki (`firewall/manager.go:478`)
- **H7**: `io.ReadAll` error discarded in firewall Enable/Disable (`firewall/manager.go:345,418`)
- **H8**: `InitContainerConfig` nil logger panic (`cmd/container/shared/containerfs.go:88`)
- **H9**: Broken `mockContainerLister` — tests pass for wrong reasons (`hostproxy/daemon_test.go:28`)

## MEDIUM

- **M1**: Logger double-close in bootstrap phases (`container_start.go:62,112`)
- **M2**: Unescaped JSON in Loki push — Go + shell (`firewall/manager.go:503`, `firewall.sh:117`)
- **M3**: `CLAWKER_LOKI_PORT` host vs container port confusion (`firewall.sh:124`, `docker/env.go:92`)
- **M4**: `NeedsSocketBridge` nil guard suppresses SSH bridge (`container_start.go:30`)
- **M5**: `ip, _ := netip.ParseAddr` silently uses zero IP (`firewall/manager.go:929`)
- **M6**: `waitForPIDFile` blocks 5s without context, holding mutex (`socketbridge/manager.go:246`)
- **M7**: Storage `Refresh()` silently skips unreadable layers (`storage/store.go:668`)
- **M8**: Container name total length unbounded — Docker 128 limit (`docker/names.go:121`)
- **M9**: Port fields accept out-of-range values (`config/schema.go:349`)
- **M10**: `*bool` nil dereference in bundler monitoring telemetry (`bundler/dockerfile.go:309`)
- **M11**: `ensureImage` silently ignores `io.Copy` error (`firewall/manager.go:1010`)
- **M12**: `BridgesSubdir`/`PidsSubdir` return identical paths (`config/consts.go:340`)

## LOW

- **L1**: `time.After` timer leak in daemon grace period
- **L2**: Dashboard `sendEvent` drops events silently when channel full
- **L3**: Deprecated `Settings()` accessor in new LokiPort code
- **L4**: `emitAgentMapping` drops caller context before goroutine spawn

## Doc Bugs

- **D1**: `hostproxy/CLAUDE.md` — `NewManager` missing `log` param
- **D2**: `project/CLAUDE.md` — `NewProjectManager` missing `log` param + error return
- **D3**: `config/CLAUDE.md` — `LabelMonitoringStack()` documented but doesn't exist

## Test Gaps (zero coverage)

1. `BootstrapServicesPostStart` firewall-enabled path (production default)
2. `BootstrapServicesPostStart` socket bridge path
3. `ProjectRules()` firewall rule construction
4. `WaitForHealthy` timeout/polling
5. `ResolveAgentEnv` precedence logic
6. Firewall daemon stale PID file handling