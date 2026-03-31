# QA Audit — Round 4 (2026-03-30)

Branch: `audit/qa-2026-03-30` | Agents: 6 (whail/docker engine, iostreams/tui/term, firewall deep re-audit, loop runner, bundler/docs/signals, regression+pattern hunter)

**New unique findings: 9 CRITICAL, 19 HIGH, 26 MEDIUM, 10 LOW** (5 R1/R2/R3 duplicates excluded)

**Regression hunter extended M31 (HandleError double-print) to 4 more files and M32 (success-to-stderr) to 5 more commands.**

Full findings: `.serena/memories/correctless/audit-qa-round4-2026-03-30.md`

## NEW CRITICAL
- **C12**: Dashboard data race on `result`/`runErr` between goroutine and main goroutine (`dashboard.go:46`)
- **C13**: Rate limiter busy-loop spin when `waitDuration <= 0` (`runner.go:344`)
- **C14**: TTY cache cannot be overridden to `false` — `SetStdoutTTY(false)` ignored (`iostreams.go:209`)
- **C15**: `ContainerWait` goroutine leaks on every normal container exit (`whail/container.go:334`)
- **C16**: `EnsureVolume` double-passes labels — suppresses engine test labels (`docker/volume.go:34`)
- **C17**: `restartContainer` wraps nil error — prevents firewall recovery (`firewall/manager.go:1070`)
- **C18**: Docker name filter substring match targets wrong container (`firewall/manager.go:889,971,1018`)
- **C19**: Dockerfile ARG template injection via unescaped user config (`Dockerfile.tmpl:36`)
- **C20**: Package names injected into apt-get/apk RUN without shell validation (`Dockerfile.tmpl:82,120`)

## NEW HIGH
- **H33**: Circuit breaker updated on transient infrastructure errors (`runner.go:441`)
- **H34**: Rate limiter token consumed before container creation — phantom calls (`runner.go:327`)
- **H35**: Circuit breaker counters not persisted across sessions (`circuit.go` + `session.go`)
- **H36**: Ctrl+C during active iteration hangs process — hijacked connection not closed (`runner.go:592`)
- **H37**: Negative TasksCompleted from agent silently decrements totals (`session.go:298`)
- **H38**: StopSpinner deadlocks under slow stderr writer (`spinner.go:140`)
- **H39**: WrapLines uses raw rune count for ANSI words — incorrect boundaries (`text.go:113`)
- **H40**: Field browser label alignment uses byte count not visible width (`fieldbrowser.go:1106`)
- **H41**: BuildKit drainProgress no panic recovery — wg.Wait hangs forever (`buildkit/builder.go:46`)
- **H42**: SuppressOutput ignored in BuildKit path — progress always shown (`buildkit/builder.go:51`)
- **H43**: Legacy ImageBuild body not drained on error — connection pool exhaustion (`docker/client.go:223`)
- **H44**: matchPattern basename-first makes patterns match at any depth (`docker/volume.go:326`)
- **H45**: Firewall CA cert write non-atomic — partial cert/key on disk (`firewall/certs.go:75`)
- **H46**: RotateCA removes certs dir while Envoy running — race with ensureConfigs (`firewall/certs.go:161`)
- **H47**: ExecInspect called before Docker records exit code — spurious 0 (`firewall/manager.go:344`)
- **H48**: Bypass shell injection via unvalidated hostProxy URL (`firewall/manager.go:576`)
- **H49**: EscapeMDXProse double-escapes text inside backticks (`docs/markdown.go:20`)
- **H50**: Semver prerelease comparison lexicographic not numeric (`bundler/semver/semver.go:196`)
- **H51**: SetupSignalContext goroutine not stopped on cancel — second signal absorbed (`signals/signals.go:15`)

## NEW MEDIUM
M49-M74: Loop output byte-length skew, stale tasks file, safety/completion threshold ordering, WizardPage value-type state loss, KVEditor type assertion panic, browserPage discards Cmd, FlexRow centering bug, wizard 0x0 sizing, progress double-newline, FindContainerByName regex dots, nil Result channel fragile, duplicate cached build events, BuildKit dockerfile dir dead code, WaitForHealthy error type mismatch, RemoveRules unnecessary restart, CoreDNS host/container port semantic, emitAgentMapping JSON sprintf, EnsureCA half-state orphaned certs, readPIDFile no trim, GenerateDomainCert missing KeyUsage, man page empty SEE ALSO, build.Version empty string, install checksum grep anchoring, NPM versions struct{} discard, bypass context.Background no timeout, AddRules+daemon spawn race

## NEW LOW
L22-L31: Dashboard blocking channel write, CountVisibleWidth CJK/emoji, 0% cosmetic, alt-screen lock broken pipe, isContainerRunning restarting state, EnsureCA cross-process race, man page separator, EmbeddedScripts error swallow, install script stderr in response, stale PID reuse (all daemons)

## CUMULATIVE TOTALS (R1+R2+R3+R4)
| Severity | R1 | R2 | R3 | R4 | Total |
|----------|----|----|----|----|-------|
| CRITICAL | 5 | 3 | 3 | 9 | **20** |
| HIGH | 9 | 10 | 13 | 19 | **51** |
| MEDIUM | 12 | 18 | 18 | 26 | **74** |
| LOW | 4 | 8 | 9 | 10 | **31** |
| Doc bugs | 3 | 0 | 1 | 0 | **4** |
| Test gaps | 6 | 0 | 0 | 0 | **6** |
| **Total** | 39 | 39 | 44 | 64 | **186** |