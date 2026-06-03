# RCA: Host-proxy `/open/url` fail-closes globally → OAuth/`/login` dead (egress port schema drift)

**Status:** Bug #1 ROOT CAUSE CONFIRMED with live + repro evidence. 2026-06-03. Branch `fix/clawkerd-networking-bugs`.
Bug #2 (clawkerd not reaping → TTY lockup on exit) is a SEPARATE issue, partly observed here (see bottom), not yet RCA'd.

## Symptom (as reported)
- `clawker.truluv.sec`: claude code OAuth login totally broken. Host-proxy browser event won't open on host. Manual paste of auth URL → subsequent token request also fails.
- Reproduced in `clawker.clawker.dev` too: `/login` fails identically. **NOT container-specific.**

## Reframe (evidence corrected the symptom)
It is NOT "claude code can't reach network." truluv.sec's `claude-code/2.1.161` is making SUCCESSFUL calls through Envoy in real time — Envoy access log for client `172.18.0.3` shows `api.anthropic.com/api/event_logging/v2/batch`→200, `github.com/.../git-upload-pack`→200, `api.github.com/graphql`→200, `platform.claude.com/`→200, `developer.apple.com`→200. General egress WORKS. Claude code keeps working because it is already authed off the shared config volume. **What is broken is OAuth *login* (the browser-open step).**

## ROOT CAUSE — egress rule `port` schema drift between authoritative config and host-proxy mirror copy
PR **#330** (commit `31bf3f04`, "transport-first Envoy egress config generator") changed `config.EgressRule.Port` from `int` → `string` (to support dynamic port *ranges* like `"9000-9100"`). That commit touched `internal/config/schema.go` + `internal/controlplane/firewall/rules_store.go` but **did NOT update `internal/hostproxy/egress_check.go`**, which holds an *intentional local mirror copy* of the rule structs (mirror exists on purpose to avoid importing `internal/config` and violating the package DAG — see `internal/hostproxy/CLAUDE.md` "Leaf package" note). The mirror's last touch was #314, long before the int→string change.

HEAD state (the drift):
- Authoritative `internal/config/schema.go:157`: `Port string \`yaml:"port,omitempty"\``
- Mirror `internal/hostproxy/egress_check.go:19`: `Port int \`yaml:"port,omitempty"\``

### Failure chain (each link proven)
1. `firewall.NormalizeRule` (`rules_store.go:192-199`) sets `r.Port = "443"/"80"/"22"` (string) for any empty default port BEFORE persisting. So every stored rule carries a non-empty Port string.
2. The firewall rules store writes `egress-rules.yaml` by `yaml.Marshal`-ing `config.EgressRule`. yaml.v3 QUOTES numeric-looking strings → emits `port: "443"` (verified by marshal repro: `Port="443"`→`port: "443"`, `Port="9000-9100"`→`port: 9000-9100`, `Port=""`→omitted).
3. Host-proxy `readEgressRules` does `yaml.Unmarshal(data, &egressRulesFile{})` where `Port` is `int`. Unmarshaling `!!str "443"` (or a range) into `int` ERRORS. yaml.v3 fails the WHOLE document on the first bad element. (Verified in-module repro: `port: "443"`→`yaml: unmarshal errors`; `port: 443` bare-int→OK; `port: "9000-9100"`→error.)
4. `CheckURLAgainstEgressRules` (`egress_check.go`) treats any read/parse failure as **fail-closed**: `return fmt.Errorf("cannot read egress rules: %w", err)`.
5. `handleOpenURL` (`server.go:411-419`) → HTTP **403** `{"success":false,..,"error":"blocked by egress policy"}` for EVERY url.
6. Effect: host-proxy `/open/url` blocks 100% of URLs (apple, github, api.anthropic.com, AND all OAuth URLs all return 403) → the host browser NEVER opens → OAuth/`/login` dead. GLOBAL, because the host-proxy daemon is shared across all containers/projects.

### Live proof of the Envoy-vs-hostproxy split (same URLs, opposite verdicts)
| URL | via Envoy (real egress, claude) | via host-proxy `/open/url` |
|---|---|---|
| claude.com/cai/oauth/authorize | 307 ALLOWED | 403 blocked |
| platform.claude.com/v1/oauth/token | 405 (reached upstream) | 403 blocked |
| platform.claude.com/oauth/code/callback | 200 ALLOWED | 403 blocked |
| developer.apple.com / github.com / api.anthropic.com | 200 ALLOWED | **403 blocked (all)** |

Host proxy daemon is UP and HEALTHY (`/health`→200 `{"status":"ok","service":"clawker-host-proxy"}`). NOT a dead-PID problem. `/callback/register`→200 (that path doesn't parse rules). Only the egress-rule-parsing path (`/open/url`) is poisoned.

## Why unit tests didn't catch it
`internal/hostproxy/testdata/egress-rules.yaml` is STALE: every rule uses bare-int `port: 443` and legacy `proto: tls`. Bare-int unmarshals fine into `Port int`, so `egress_check_test.go` passes. The testdata was never regenerated against the new writer (which quotes ports). Golden drift hid the schema drift.

## FIX APPLIED (surgical, 2026-06-03, branch fix/clawkerd-networking-bugs) — UNTESTED LIVE, NEEDS REVIEW UAT
Quick fix shipped per user: type change + range-aware matching, NO live re-OAuth verification yet, NO testdata regen yet. All edits in `internal/hostproxy/egress_check.go` + its test. `go build`/`go vet`/`go test ./internal/hostproxy/` all PASS.

What changed:
1. `egressRule.Port` `int` → **`string`** (the mandatory fix — makes the file parse at all). Added comment explaining why it MUST be string (firewall writes quoted `port: "443"` + range specs that poison int unmarshal → fail-closed).
2. `normalizeEgressRule`: int defaults → string defaults (`r.Port == ""` → `"443"/"80"/"22"`). tcp/other protos keep `Port=""` (no default), same as before (was port 0).
3. `matchRules` line ~133: `r.Port != port` → `!portSpecMatches(r.Port, port)`.
4. New helper `portSpecMatches(spec string, p int) bool`: `strings.Cut(spec, "-")` → range membership (lo<=p<=hi); else `strconv.Atoi` single-port equality; unparseable spec matches nothing. `strconv` was already imported. Range only attaches to opaque tcp/ssh/udp, which `/open/url` (http/https only) never hits — but membership is correct regardless.
5. Test fixes ONLY (no new coverage added): `egress_check_test.go` struct literals `Port: 443` → `Port: "443"`; `TestNormalizeEgressRule` `wantPort int`→`string`, cases updated (incl. "tcp keeps empty port" `0`→`""`), format verbs `%d`→`%q`.

### NOT DONE — pickup list for review/UAT
- **Live verification skipped (user deferred).** Must re-confirm after a CP/host-proxy rebuild: `/open/url` now returns success (or proper per-rule 403) for OAuth URLs, and `clawker` `/login` completes end-to-end. The host-proxy daemon is a long-lived subprocess — verify the REBUILT binary is actually running (stale daemon will keep failing). The drift fix only takes effect once the new `clawker` binary's host-proxy daemon restarts.
- **testdata NOT regenerated.** `internal/hostproxy/testdata/egress-rules.yaml` still uses old bare-int `port: 443` + legacy `proto: tls`. Tests pass because bare-int also unmarshals into a string field fine — BUT the testdata still doesn't exercise the LIVE writer shape (quoted ports, ranges, `proto: https`). Regenerate it to the current writer format (quoted `port: "443"`, a range rule, an opaque tcp rule) so the suite would actually catch a future regression. This is the gap that hid the bug originally.
- **No config↔mirror drift guard added.** The root architectural fragility remains: the intentional mirror-copy in egress_check.go silently fails closed when config.EgressRule evolves. Recommended follow-up: a `_test.go` in internal/hostproxy that marshals a real `config.EgressRule` (config import is fine in _test) and asserts `readEgressRules` parses it — fails the build the next time the schema drifts.
- **Token-exchange symptom still unconfirmed** (see secondary-symptom section below) — revisit during UAT once browser-open works; expect it resolves as a downstream effect.

## (original FIX plan — superseded by FIX APPLIED above, kept for reference)
1. `internal/hostproxy/egress_check.go`: change mirror `egressRule.Port int` → `string`. Then update the port-handling that assumes int:
   - `matchRules` line ~128: `r.Port != port` (int compare) → must parse the dynamic spec ("443" or "lo-hi" range) and test membership. Mirror `firewall.ParsePortSpec` semantics (single port or inclusive range; range meaningful only for opaque tcp/ssh/udp; ignored for http/https/ws/wss which scope by host/SNI).
   - `normalizeEgressRule` line ~225 (`if r.Port == 0 { r.Port = 443/80/22 }`) → string defaults `""`→`"443"/"80"/"22"`.
   - The URL-derived `port` is an int from `strconv.Atoi(parsed.Port())` / `defaultPort` — keep int on the request side, compare int-port ∈ parsed-spec.
2. Regenerate `internal/hostproxy/testdata/egress-rules.yaml` to the CURRENT writer format (quoted `port: "443"`, `proto: https` not `tls`, include a port-range rule + an opaque tcp rule) so tests actually exercise the live shape. Add a test that round-trips a `firewall`-written file (or marshals `config.EgressRule`) through `readEgressRules` to lock the contract.
3. ARCHITECTURE: the intentional mirror-copy pattern silently fails closed on schema drift. Add a guard so config↔mirror divergence is caught at build/test time — e.g. a test in `internal/hostproxy` that marshals a `config.EgressRule` (import allowed in _test only) and asserts the mirror parses it, OR move the read-only parse into a tiny leaf package both sides share. Mirror copies of an evolving schema are a recurring trap (cf. feedback_trace_dont_skim_or_assume_design: contract gaps live at layer boundaries golden/unit tests bypass).

## Secondary symptom — "token request also fails" (NOT yet independently confirmed)
User reports the manual-paste token exchange also fails. I have NO evidence Envoy blocks it: `platform.claude.com/v1/oauth/token` POST → 405 at Envoy (reached upstream; real request would be 200). Token exchange is claude code's own direct HTTPS POST → normal Envoy egress → allowed. Most likely a DOWNSTREAM consequence of the broken browser-open / OAuth callback flow rather than an independent egress block. Note: a `callback-forwarder` zombie (`<defunct>`, PID 543, reparented to clawkerd PID 1) was observed in truluv.sec — host-open spawns callback-forwarder then `open_url` 403s; the forwarder is left to die unreaped. Treat token-failure as unconfirmed until the OAuth state machine is traced end-to-end. Do not assert it is egress.

## Bug #2 preview (separate, to investigate next per user plan)
clawkerd (PID 1) is NOT reaping zombies in BOTH containers. My own container (`clawker.clawker.dev`, PID1 clawkerd) has many `<defunct>`: statusline.sh, jq, go, gopls, python3.11. truluv.sec has the callback-forwarder zombie. User suspects this is why TTY isn't returned on container exit and that bug #1 hits the edge case 100%. Universal, not truluv-specific. RCA pending — user will exit truluv.sec to trigger and have me watch its clawkerd logs.

## Diagnostic method notes (for reuse)
- This session runs INSIDE `clawker.clawker.bug` (CLAWKER_AGENT=bug) which is identical to `clawker.clawker.dev`. Docker socket IS available (user re-enabled `security.docker_socket`).
- To probe the bugged agent's real network env: `docker exec -u claude clawker.truluv.sec sh -lc '...'`. MUST use `-u claude` — root hits CoreDNS (NXDOMAIN on host.docker.internal) while claude(1001) uses the docker embedded resolver (192.168.65.254). That root/claude resolver split is NORMAL, present in both containers, not the bug.
- claude code live sockets: decode `/proc/<pid>/net/tcp` — truluv.sec PID57 had 35 ESTABLISHED to `172.18.0.200:10000` (=0x2710, Envoy) = normal keep-alive pool, not stuck.
- Envoy access log: `docker logs clawker-envoy 2>&1 | grep <client-ip>`. truluv.sec=172.18.0.3, this container=172.18.0.2, Envoy listener=172.18.0.200.
- Live `egress-rules.yaml` is HOST-side (host proxy reads it via host path); not readable from inside the agent. The host-proxy log line `"blocked by egress rules"` (with the exact unmarshal err) lands in the host-proxy daemon log — the one artifact confirming WHICH parse error, readable only host-side.
