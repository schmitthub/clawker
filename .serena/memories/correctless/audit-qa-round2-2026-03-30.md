# QA Audit — Round 2 (2026-03-30)

Branch: `audit/qa-2026-03-30` | Agents: 6 (state mgmt, subprocess lifecycle, container orchestration, TUI, build/bundler, deep-dive)

**New unique findings: 3 CRITICAL, 10 HIGH, 18 MEDIUM, 8 LOW** (10 R1 duplicates excluded)

Full findings: `.serena/memories/correctless/audit-qa-round2-2026-03-30.md`

## NEW CRITICAL
- **C6**: Container exit code lost in loop runner — block-scoped var discards real exit code, closed channel returns 0 (`runner.go:632`)
- **C7**: Config volume orphaned when history volume creation fails — reused uninitialized on next run (`workspace/strategy.go:96`)
- **C8**: Firewall CA cert install broken on Alpine — Debian-only path, MITM CA untrusted (`Dockerfile.tmpl:248`)

## NEW HIGH
- **H10**: StoreUI `MarkForWrite` skipped for default-value fields — save is silent no-op (`storeui/edit.go:343`)
- **H11**: ContainerStart PostStart failure leaves container running — no rollback (`container_start.go:181`)
- **H12**: SocketBridge cleanup SIGTERM no wait — duplicate bridges, protocol corruption (`socketbridge/manager.go:256`)
- **H13**: Hostproxy startDaemon orphaned daemon blocks future EnsureRunning (`hostproxy/manager.go:144`)
- **H14**: Output decline counter not reset on zero-output loops — false circuit trips (`circuit.go:214`)
- **H15**: `limitedBuffer.Write` short count violates io.Writer contract (`runner.go:704`)
- **H16**: `EnsureImage` content hash excludes firewall CA cert — stale image after rotation (`docker/builder.go:93`)
- **H17**: `post-init-done` marker created as root, not container user (`entrypoint.sh:216`)
- **H18**: `GIT_CONFIG_COUNT=1` hardcoded — clobbers user git config env (`docker/env.go:130`)
- **H19**: `VersionsFile.MarshalJSON` loses sort order — non-deterministic output (`registry/types.go:68`)

## NEW MEDIUM
M13-M30: projectRegistry TOCTOU, dirty paths stranded, PID reuse false positive, StopDaemon no wait, bridge goroutine tracking, fd leak, snapshot volume orphan, partial config init, resolveVolumePath semantics, IsOutsideHome symlink bypass, sendWarning cancelled ctx, completion signal hides loopErr, SelectField Ctrl+C stuck, cursor heading bug, DashboardResult nil err, lastSaveError persists, symlinks skipped in build context, ResolveVersions raw stderr

## NEW LOW
L5-L12: StopAll ignores errors, ringBuffer dead code, KV duplicate key drop, resolveWorkDir silent fallback, dual parseEnvFile, version comment maintenance, EmbeddedScripts error swallow, splice idiom fragility