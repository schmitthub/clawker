# Initiative: E2E Adversarial Harness for AgentService.Connect

> **Status:** Tracked. Not scheduled. Sized as a focused branch
> (~150-300 LOC harness plumbing + 7 test bodies retargeted to streaming
> Connect + 2 fixture helpers).
> **Surfaced during:** Branch 4 follow-up review, 2026-04-26.

## Problem

`test/e2e/clawkerd_failures_test.go` ships seven adversarial cases —
each asserts the CP rejects one specific defended-channel break with
`codes.PermissionDenied` (or `codes.Unauthenticated` for the scope
case). Each case is **authored but skipped** with an explicit reason
about what helper the harness is missing.

Concrete consequence: every CP-side cross-check (PKCE compare, slot
single-use, slot TTL, cert thumbprint, peer IP, container label,
per-listener scope vocabulary) is unit-tested in `internal/controlplane/`
but has no E2E gate. A future regression that, say, accidentally
removes the peer-IP cross-check would not be caught by `make test-all`
— the unit test for the regressed file passes (because the regression
removed the test alongside the code), and the E2E that would catch it
silently skips.

The happy-path E2E (`clawkerd_register_test.go`'s
`TestClawkerdRegister_HappyPath`) does exercise the full
announce → Connect → idle → stop → evict lifecycle through the CLI,
but only on the success path. The seven adversarial cases are the
defense-in-depth layer.

## Why this is an initiative, not a task

Each test needs a different harness extension. Bundling them avoids
landing four near-duplicate helpers across separate branches:

| Test | Helper extension required |
|------|--------------------------|
| `ReplayConsumesSlot`, `WrongVerifier`, `ExpiredSlot` | mTLS dial to `cp.AgentPort` + open server-streaming `Connect` + `Recv` to surface the rejection |
| `CertSwap` | Same + cert-override (mint a different cert for the CLI's announce vs. clawkerd's dial) |
| `CrossContainerTheft` | Two-container fixture: one `RunInContainer` to populate bootstrap, then an mTLS dial that presents container A's cert from container B's IP |
| `LabelTamper` | Docker label patch via API between `AnnounceAgent` and `Connect` |
| `AgentScopeAgainstAdminListener` | Hydra token-fetch helper to obtain an agent-scoped token, then a dial against `cp.AdminPort` (not the agent listener) |

The first three share one helper and ship together; the rest each add
~50 LOC. Doing them as separate one-off PRs would have everyone
re-reading the same threat model and re-deriving the same dial
plumbing every time.

## Components

### 1. `harness.AgentDial` — the core mTLS dial helper

**Location:** `test/e2e/harness/agent_dial.go` (new file).

**Surface:**

```go
// AgentDialOptions controls the cert + token presented to the CP's
// agent listener. Defaults reproduce what clawkerd would present
// (CLI-CA-signed leaf cert with CN=agent_name, agent-scoped Hydra
// token); fields override individually so adversarial tests can break
// one defended attribute at a time without rebuilding the whole stack.
type AgentDialOptions struct {
    AgentName  string             // CN of the leaf cert + ConnectRequest.AgentName
    Cert       *tls.Certificate   // override cert; nil → mint via auth.MintAgentCert
    BearerToken string            // override token; empty → fetch via Hydra
    PeerAddr   string             // optional: override the source IP (requires SO_REUSEADDR fixture)
}

// Dial opens an mTLS connection to cp.AgentPort and returns an
// AgentServiceClient. Caller is responsible for closing the returned
// conn. Used for adversarial tests that bypass the CLI-side bootstrap
// flow.
func (h *Harness) AgentDial(t *testing.T, opts AgentDialOptions) (
    agentv1.AgentServiceClient, *grpc.ClientConn,
)

// Connect drives Connect + first Recv in one call. Returns the err
// from the first Recv (where transport-level rejections surface for
// server-streaming RPCs).
func (h *Harness) AgentConnect(t *testing.T, opts AgentDialOptions, req *agentv1.ConnectRequest) (
    *agentv1.Command, error,
)
```

**Implementation notes:**
- Trust roots: load CLI CA via `consts.AuthCACertPath()` (already in test scope via `testenv`).
- Mint cert: reuse `auth.MintAgentCert(caCertPath, caKeyPath, agentName)` — already exercised by `internal/cmd/container/shared/agent_bootstrap.go`.
- Hydra token: factor out the assertion + `/oauth2/token` POST from `cmd/clawkerd/main.go` into a shared helper in `internal/auth/agent_token.go` (today it's inlined in clawkerd; the e2e harness needs the same logic). Keep clawkerd as the only production caller; the harness imports the helper for tests only.
- `PeerAddr` override: requires the e2e harness to bind a loopback alias (or run in a netns) so the dial originates from a non-clawker-net address. Most tests don't need this — `CrossContainerTheft` is the only one.
- Bearer attached via `grpc.WithPerRPCCredentials` (matches clawkerd's production wiring; T7's lesson).

### 2. `harness.HydraTokenFetch` — agent-scoped token helper

**Location:** `test/e2e/harness/hydra_token.go` (new file).

```go
// HydraToken fetches an `agent:self:register`-scoped access token from
// the running CP's Hydra. Used by AgentScopeAgainstAdminListener to
// prove the agent token cannot satisfy admin-scoped RPCs.
func (h *Harness) HydraToken(t *testing.T, agentName string) string
```

Internally calls the same shared helper from §1 — fetch the assertion,
POST to `/oauth2/token`, return `access_token`. The CP's Hydra public
port resolves via `h.Run("controlplane", "status", "--json")` → port
field, or via the testenv-resolved settings.

### 3. `harness.SecondContainerWithBootstrap` — cross-container theft fixture

**Location:** `test/e2e/harness/two_container.go` (new file).

```go
// SecondContainerWithBootstrap creates container CY that mounts
// container CX's bootstrap material via a private bind mount.
// Returns CY's clawker-net IP so the test can assert the peer-IP
// check rejects: same cert + same verifier, but the dial originates
// from CY's IP.
func (h *Harness) SecondContainerWithBootstrap(t *testing.T, hostBootstrapDir, agentName string) (
    containerIP string, cleanup func(),
)
```

Or: skip the second-container approach entirely and use `PeerAddr`
override (§1) bound to a different loopback alias. The dual-container
fixture is more faithful to the actual threat (cert+verifier
exfiltration to a second container) but heavier; the loopback alias
covers the same CP-side check at lower cost. Pick based on whether
you want to exercise the dockerevents-driven inspection path
end-to-end.

### 4. `harness.LabelPatch` — docker label tamper helper

**Location:** `test/e2e/harness/label_patch.go` (new file).

```go
// LabelPatch updates a single label on a running container via the
// Docker API. Used by LabelTamper to set dev.clawker.agent to a
// different name between AnnounceAgent and Connect. Docker doesn't
// support live label updates on a running container — this helper
// stops, edits, restarts, which is fine for the adversarial test
// (the slot stays alive across the brief downtime as long as
// AgentSlotTTL hasn't elapsed).
func (h *Harness) LabelPatch(t *testing.T, containerID, labelKey, labelValue string)
```

Alternative if the stop/restart is too disruptive: create the
container with the wrong label from the start (a one-shot CLI flag
override or a direct ContainerCreate via the harness) and skip the
patch. Simpler but less faithful to the threat model.

### 5. Retarget the seven authored tests to streaming Connect

The skip strings explicitly say "Register" today (the unary RPC name);
tests must be rewritten to call `Connect` (server-streaming) and
inspect `stream.Recv()` for the rejection. The authored helpers
`announce`, `pkceFromVerifier`, `thumbprintHex` already exist in
`clawkerd_failures_test.go` — those stay; the test bodies fill in
where the `t.Skip(...)` lines are.

### 6. Update the happy-path test's stale narrative

`clawkerd_register_test.go:14-18` has a comment explaining "Until
[the wires] land in run/start (currently a known gap — see the Branch
4 plan memory)". That gap closed in cp-initiative-clawkerd-cli-integration
T8. The comment should be updated or removed alongside this work.

## Test strategy

### Each adversarial test

The shape is consistent across all seven:

1. CLI-side: announce a slot via `h.Run("controlplane", ...)` or a
   direct `AdminClient.AnnounceAgent` call (the latter for tests that
   need to break the announce payload itself).
2. Harness-side: `h.AgentConnect(t, opts, req)` — opts breaks one
   defended attribute (cert, verifier, IP, label, scope).
3. Assert: `requireDenied(t, err, codes.PermissionDenied)` (already
   authored). For the scope test, expect `codes.Unauthenticated`.
4. Cross-cutting assertion: `h.Run("controlplane", "agents", "--json")`
   shows the agent is NOT registered (no entry).

### Slot-state side effects

`WrongVerifier` must additionally assert the slot survived (a benign
retry can succeed). Fetch slot state via... actually, the agentslots
registry doesn't expose a query RPC — current options:
- Re-attempt Connect with the correct verifier within `AgentSlotTTL`
  and assert success. Indirect but exercises real behavior.
- Add a debug-only `AdminService.ListSlots` RPC. New surface area;
  defer unless the indirect approach proves flaky.

### Test isolation

Adversarial tests should NOT share a CP container — each spins up its
own (via the existing harness pattern) so a slot reservation in one
test doesn't bleed into another. The cleanup chain in
`harness.NewIsolatedFS` already tears the CP down; each test gets a
fresh one. Cost: ~5-10s of CP boot per test. Acceptable for an E2E
suite that's already slow.

## Open design questions

1. **Loopback alias vs. second-container fixture for IP override.**
   Loopback alias is cheap (just bind to `127.0.0.2`) but requires
   the harness to ensure clawker-net's container-IP-allocation logic
   accepts non-clawker-net dial sources. Second-container fixture is
   faithful but ~2x the CP container boot cost for the one test. Lean
   loopback alias unless it produces false negatives.
2. **Hydra token-fetch helper location.** Option A: extract into
   `internal/auth/agent_token.go` (clawkerd + harness both import).
   Option B: inline in harness only, accept the duplication.
   Option A is the right factoring (the duplication is otherwise a
   maintenance hazard if the assertion shape ever changes); ~30 LOC
   move from `cmd/clawkerd/main.go` → `internal/auth/`.
3. **`AdminService.ListSlots` debug RPC.** Tempting for the slot-state
   assertions but adds CP-side surface that has to be maintained
   forever. Defer until the indirect-Connect-retry approach proves
   inadequate.
4. **Should `AgentDial` take `*Harness` or stand alone?** Bound method
   matches existing `h.Run` / `h.RunInContainer` patterns; standalone
   would let the e2e test files call it without a Harness. Bound method
   wins for consistency.

## Tests for the harness itself

The harness helpers ARE tested — by the seven adversarial cases that
will exercise them. No need for a separate harness-test layer; if the
adversarial suite passes, the harness works.

## Cross-references

- **`cp-initiative-clawkerd-cli-integration`** (DONE) — landed the
  Connect server-streaming refactor that this initiative's tests must
  target. The `IdentityInterceptor` and CN cross-check are the new
  attack surface that the adversarial suite gates.
- **`adversarial-test-harness`** memory (the live red-team C2, NOT
  this) — separate harness at `test/adversarial/`. Not related; the
  name collision is unfortunate.
- The `t.Skip(...)` strings in `test/e2e/clawkerd_failures_test.go`
  are the authoritative per-test breakdown of what each helper
  extension needs to expose.

## When to schedule this

Lower priority than `cp-initiative-cp-restart-resilience` (which
gates production-readiness). This initiative gates *defense-in-depth
verification of the agent registration story*, which is valuable but
not a release blocker — the unit-test layer in
`internal/controlplane/agent/` covers each cross-check individually.
Reasonable candidate for a Branch 5 or 6 backlog slot, or a between-
branches polish session.

## Suggested commit shape

1. Extract Hydra token-fetch helper to `internal/auth/agent_token.go`;
   clawkerd switches to it. (~30 LOC move + clawkerd test still green.)
2. Land `harness.AgentDial` + `AgentConnect` + retarget the three
   PKCE/slot adversarial tests. (Largest commit; ~150 LOC harness +
   3 test bodies.)
3. Land `harness.LabelPatch` + retarget `LabelTamper`. (~50 LOC.)
4. Land `harness.HydraToken` + retarget `AgentScopeAgainstAdminListener`.
   (~30 LOC.)
5. Land `harness.SecondContainerWithBootstrap` (or loopback alias) +
   retarget `CertSwap` + `CrossContainerTheft`. (~70 LOC.)
6. Update `clawkerd_register_test.go` stale narrative comment.

Each commit independently passes `make test-all` (the previous
commit's tests still skip, the current commit's tests now run).
