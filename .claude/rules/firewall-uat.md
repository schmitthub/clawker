---
description: Runtime behavioral UAT of the firewall egress stack (Envoy+CoreDNS+eBPF) from inside the agent container
---

# Firewall Runtime UAT

Golden files + `envoy --mode validate` prove the generated config is **valid**.
They do NOT prove **behavior**. Behavior is verified live, and you are the
vehicle: when `$CLAWKER_AGENT` is set, this Claude Code session runs **inside a
clawker agent container whose egress is routed through the live firewall
stack** (eBPF redirect → Envoy → CoreDNS). Exercising egress from this shell IS
the behavioral test.

## Roles

- **You (in-container):** exercise egress with the probe tools below. You
  CANNOT run host `clawker` (see `feedback_no_host_clawker_in_container`).
- **User (host operator):** mutates rules host-side —
  `clawker firewall add <host> [--proto https|http|ssh|tcp|wss|...] [--port N] [--path /p --action allow|deny]`.
  Ask them to add/remove rules; then you re-probe. To live-apply a
  `clawker.yaml` egress edit (`security.firewall.add_domains` /
  `security.firewall.rules`) without a restart, have them run
  `clawker firewall refresh` (global, no `--agent`; add/update only — deletes
  still go through `clawker firewall remove`).

`clawker firewall add` flags: `--proto` (default https), `--port` (default
proto-specific), `--path` + `--action` (path-scoped rule, required together).

## Probe tools available in-container

`curl`, `ssh` (+ forwarded host agent), `nghttp` + `h2load` (HTTP/2 + h2 WS;
`h2load` also does h3 — useful for the QUIC/alt-svc sibling), `websocat`,
`wscat`, `python3`, `openssl`, `gh`, `git`. **`nc` is absent.** The SSH agent is
the host's, live-mirrored through the socketbridge (fresh `net.Dial` per
connection — no cache; `ssh-add -l` reflects host agent state at that instant).

No tool here drives an **h2/h3 Extended CONNECT WebSocket** (websocat = h1.1
only; `nghttp -u` = h2c upgrade, not WS). So WS-over-h1.1 is runtime-testable
(`websocat wss://…/ws/echo` → C2 echoes the frame back through the MITM), but
the h2 (`allow_connect`) / h3 (`allow_extended_connect`) WS paths are only
config-confirmable (`grep -c allow_connect`), not live-drivable in-container.

For real network/curl, the Bash tool's sandbox strips it — use
`dangerouslyDisableSandbox: true`.

## Behavioral discriminators (HARD-won — memorize)

| observation | meaning |
|---|---|
| `NXDOMAIN` / "could not resolve host" | host not in allowlist — blocked at CoreDNS (layer 2) |
| HTTP **403, body `Forbidden\n`** | clawker host/path **deny** (`firewallBlockedBody`) — Envoy path/vhost gate |
| HTTP **403, EMPTY body**, `server: envoy` | Envoy **upgrade refused** (WS upgrade on a non-`wss` route — no `upgrade_configs`) — NOT a clawker deny |
| `server: envoy` + `x-envoy-upstream-service-time` + upstream's own code (e.g. 404) | request **reached the real upstream** = allowed |
| `ssh -Tv` → `Authenticated to <host> ([<EnvoyIP>]:<port>)` | shows the **Envoy hop** + the dedicated-listener port (`TCPPortBase+idx`, one per opaque host) |
| access log `response_code:0` + `response_flags:DC` + `response_code_details:downstream_remote_disconnect` | **client** disconnected before the response — NOT a block (`action:allowed`). Normal `uv`/`pip`/`npm` h2 stream-cancel churn. |

**clawker-net is NOT an egress-test surface.** Intra-net traffic (a sibling
container's clawker-net IP — e.g. the C2's) is INTENTIONALLY open, not redirected
to Envoy; hitting it bypasses the firewall by design and false-positives a
leak / confused-deputy. An egress-attack test needs a **PUBLIC** dst (through
Envoy), e.g. the C2's ngrok edge — never its clawker-net IP. Confused-deputy's
structural defense is config-verifiable instead: FQDN flows are all
`LOGICAL_DNS`/DFP, ZERO `ORIGINAL_DST`/`use_original_dst` (those appear only for
IP/CIDR rules, the range-validated carve-out).

**SSH routing is special — banner is the only proof.** Any real SSH server
completes a valid handshake, so a misroute ("everything funneled to host X")
still returns a valid `ssh -T` response. The TRUE upstream is discriminated by
the **banner / `remote software version`** (GitHub: `…version 6279353` +
"does not provide shell access"; GitLab: `GitLab-SSHD` + "Welcome to GitLab").
For opaque-host fan-out (n≥2), also confirm each host lands on a **distinct**
`[<EnvoyIP>]:<port>` — same port = collision/misroute.

`Permission denied (publickey)` on an opaque-ssh probe is a **routing PASS**
(real daemon negotiated auth then rejected the key) — auth ≠ routing.

## Spot-checking the live generated config

Dev config (when `.clawkerlocal/` XDG overrides are active):
`.clawkerlocal/.local/share/clawker/firewall/envoy.yaml` — **~31k lines, grep/
sed only, never full-read**. Corefile / rules / certs are siblings. Use it to
ground a runtime result in the artifact (e.g. `grep -c upgrade_configs` to
confirm 0 when no `wss` rule exists). See `project_clawkerlocal_xdg_overrides`.

## Adversarial C2 harness

`test/adversarial/` — Go C2 + SQLite, exposed via ngrok at `ajschmitt.ngrok.app`
(stable host). For exfil-block tests: try to reach a non-allowlisted host/path/
proto and confirm the block. Allowlisted subset is operator-controlled per run
(e.g. https `/ws/` + `/allowed/` only). See `test/adversarial/CLAUDE.md`.

## What golden/validate cannot catch (only live UAT can)

CP↔generator **contract** gaps — e.g. a dropped health listener (config still
valid, bringup hangs). See `project_envoy_health_listener_regression`. Run live
UAT before declaring the generator branch done.
