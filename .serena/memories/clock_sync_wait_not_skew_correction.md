# Clock sync = WAIT before mint, NOT iat skew correction (host clock is truth)

Branch `fix/cp-bug` (2026-06). REVERSES the approach of commits ce2ee625 ("gate
container create on host↔CP clock sync") and 7af67c5a ("reuse gate's clock-skew
measurement for agent assertion"). If you read those commits, they are NO LONGER
the design — do not reintroduce iat skew correction.

## The corrected model (the logic flaw fixed)

- **Host clock is the source of truth.** Docker forces the CP/LinuxKit-VM clock
  to track the host, not the other way around. So a host-minted JWT `iat` is
  already in the domain Hydra/fosite validates against (zero leeway).
- The ONLY failure window is the transient post-sleep lag where the just-woken
  VM clock trails the host. Fix = **WAIT for reconvergence before minting/
  exchanging**, never shift `iat` by a measured offset.

## What this means concretely

- **Agent assertion** (`auth.BuildAgentAssertion(audience, signingKey)` — no skew
  param): minted in host clock at container CREATE. **Create does NOT boot CP**
  (wrong lifecycle point). The every-start pre-start `BootstrapServicesPreStart →
  cpboot.EnsureRunning → cpReady → waitForCPClockSync` waits for CP↔host
  convergence BEFORE Docker start, so clawkerd exchanges the baked assertion only
  after the clocks align. Agent cert (MintAgentCert) also minted at create
  (host-clock x509, 24h) — fine, not clock-sync-gated.
- **CLI admin assertion** (`adminclient/dial.go`): `tokenSource.token()` calls
  `waitForSync` (loops `measureClockSkew` until |skew|≤`clockSyncTolerance`=2s)
  before the first mint, latches `synced`. On non-convergence it **FAILS FAST**
  — returns a wrapped "waiting for CP clock sync" error (NO degrade-and-mint, NO
  best-effort mint; that would only earn the "token used before issued" 500). The
  wait is bounded by the eager-dial budget `initialTokenDeadline`=15s
  (`clockSyncWaitTimeout = initialTokenDeadline`).

## Deleted (gone for good)
`shared.EnsureControlPlaneForCreate`, `CreateContainerOptions.ClockSkew`,
`InstallAgentBootstrapOptions.Skew`, skew params on
BuildAgentAssertion/GenerateAgentBootstrap, dial.go's `skewKnown`/`maxPlausible
ClockSkew`/`notableClockSkew`/`clock_skew_*` + `clock_sync_wait_degraded` logs,
the `log` param on `adminclient.Dial` (+ tokenSource.log; it only ever fed the
removed degrade log). `cpboot.EnsureRunning`/`cpReady`/`waitForCPClockSync` +
the `cpboot.Manager` interface collapsed `(time.Duration, error)` → `error`
(the returned offset was vestigial; moq mock regenerated).

## KEPT (these MEASURE skew to detect convergence — not correction)
`adminclient.ProbeClockSkew`, `measureClockSkew`, `clockSkew()`, `AbsDuration`,
cpboot's `waitForCPClockSync` loop + `cpClockSkewTolerance`(2s)/`cpClockSync
Timeout`(30s). Also the fixed 15s `assertionClockSkewLeeway` backdate (defensive
sub-second pad, applied unconditionally in `BuildSignedAssertion`).

## Discriminator for future edits
"Wait for the clock to converge, then mint host-clock" = correct. "Measure the
offset and add it to iat" = the reverted bug. `AssertionClaims.Now` is now a
test-only seam (production always uses `time.Now()`).
