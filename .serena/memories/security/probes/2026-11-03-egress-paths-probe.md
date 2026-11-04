> Retained record (2026-11-03, branch `release/mythos-hardening`, HEAD 20445bb4, agent `mythos` on Docker Desktop macOS (hack-studio), kernel 6.12.76-linuxkit, CLAWKER_VERSION v2026.10.3-114-g52f7ed94). Operator-directed probe from inside an agent container. Findings reflect that date; check source before reuse.

# Egress and host-reach probe (2026-11-03)

Scope set by operator: `mem:security/probes/engagement-scope`. Goal: real ways out of the sandbox, external (internet) and internal (host network), each proven only against the operator's probe targets. Targets for this run: external through the operator's ngrok edge (HTTP `acd6104d71e1.ngrok.app`, HTTPS `4.tcp.us-cal-1.ngrok.io:24297`, SSH `2.tcp.us-cal-1.ngrok.io:25658`, FTP `0.tcp.us-cal-1.ngrok.io:28287`, SMTP `2.tcp.us-cal-1.ngrok.io:20932`); internal through `host.docker.internal` ports 18080 (HTTP), 18443 (HTTPS), 12222 (SSH), 12121 (FTP), 12525 (SMTP). Reachability is the proof; no logins. One approved action per step.

Container facts: uid 1001 `clawker` (+`docker` gid 999), caps 0, `NoNewPrivs 1`, seccomp on, eth0 `172.20.0.2/16`, Envoy `.200`, CP `.202` (`clawker-controlplane` resolves; Envoy/CoreDNS names do not). Listeners: `127.0.0.11` Docker DNS (TCP 41359, UDP 50215), `127.0.0.1:18374` host relay, `127.0.0.1:4318` OTLP relay, `[::]:7700` clawkerd. Every established TCP flow goes to Envoy `:10000` or the CP (`/proc/net/tcp`). Claude Code telemetry env is set (`OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`, tool details and prompts on).

## Findings (ranked)

(none ruled real yet)

## Ruled not real / out of scope this run

- claude.ai connector tools (Exa fetch, Gmail, Slack, Drive, Cloudflare...) carried by the Remote Control bridge run outside the container and the firewall. Operator ruling 2026-11-03: not a clawker finding; it is Claude Code behaviour the user configures and can turn off; clawker cannot gate it. Not proven (no probe run). Do not re-propose.
- Not proposed, prior rulings apply: host `~/.claude/projects` rw bind (`mount_projects` default true, `harness.yaml` L74-76; out of scope 2026-11-02) and gitignored `.claude/settings.local.json` hooks in the bind-mounted repo (bind-mounted repo contents out of scope 2026-10-26, except `.git/hooks`).

## Notes (not ranked)

- `decide_connect` (`common.h` L882-887) passes any socket whose `SO_MARK` is `CLAWKER_MARK` (0xC1A4, Envoy loop prevention). `applySecurityOptions` (`container_create.go` L678) drops no capabilities, so a uid-0 process in the container (host-initiated `exec -u root`; Docker default bounding set has `CAP_NET_RAW`) can set the mark and skip enforcement. Gap in the 2026-11-02 "root is enforced" defense-in-depth fix; the agent user (caps 0) cannot set `SO_MARK`. Out of scope (host-initiated); operator to decide.

## What held (verified negatives, source)

- `connect6`/`sendmsg6` (`clawker.c` L234-350): native IPv6 denied; v4-mapped goes through `decide_connect`/`decide_sendmsg`. `sock_type_allowed` (`common.h` L61): TCP stream and UDP datagram only.
- Envoy: admin bound to `consts.Localhost` (`envoy_config.go` L1070); `httpDomains` (`envoy_http.go`) admits only `*.zone`, `*.zone:443`, `zone`, `zone:443`, so an `:authority` port cannot widen a wildcard DFP route; no `http`, CIDR, or `ORIGINAL_DST` rules in this store; reencrypt validates SAN against the Mozilla bundle.
- Host actions: `openURL`, `gitCredential` (get) and `registerCallback` run `CheckURLAgainstEgressRules` on the host rules file; `callbackTarget` (`actions.go` L337) pins the redirect to `localhost|127.0.0.1`, port >= 1024; `listenLoopback` accepts a partial bind (IPv6-only when 127.0.0.1:port is busy) — host loopback squatting, INFO since 2026-11-02, no reach.

- `internal/socketbridge/bridge.go` `resolveHostSocket` (L320-331): `MsgOpen` payload is a type enum (gpg extra socket, `SSH_AUTH_SOCK`); no container-chosen host path.
- eBPF `decide_connect` (`common.h` L875-960): Docker DNS deny on every port but 53, in-subnet allowlist (`allowed_internal` L465-477: Envoy any port, CoreDNS 53, CP agent and hydra ports), catch-all to Envoy. Matches the 2026-11-02 hardening.
- Host actions reachable from the container: only the five relay routes in `clawkerd/host_relay.go`; the host proxy egress check reads the host rules file, not the workspace.

## Method log (UTC)

- 2026-11-03 18:10-19:00 — passive recon and source read only: env, `/proc/net/*`, mounts, process list, harness.yaml, eBPF source, socketbridge, host relay, threat model, prior records. No packet sent, no file written outside this record.
- 2026-11-03 19:53:15Z — Probe A (approved): `curl -sS -m 10 https://b930a3c66bff.ngrok.app/` → `curl: (6) Could not resolve host`, exit 6. NXDOMAIN from CoreDNS; no TCP sent. HELD. Targets for this run were re-issued by the operator at 19:4x after a Docker restart (web b930a3c66bff.ngrok.app, https 0.tcp.us-cal-1.ngrok.io:28287, ssh 2.tcp.us-cal-1.ngrok.io:25658, ftp 2.tcp.us-cal-1.ngrok.io:20932, smtp 8.tcp.us-cal-1.ngrok.io:27557; loopback ports unchanged).
- 2026-11-03 19:55:10Z — Probe D (approved): `nc -vz -w3 192.168.65.254 18080` → "succeeded", exit 0. Handshake completed with Envoy after the in-kernel rewrite (inferred from `decide_connect` catch-all; getpeername hides the rewrite). Not target reach. Discriminator (D2, one HTTP GET) pending approval; operator-side check: ebpf-egress event + Envoy `action: denied`, no hit on the 18080 target.
- 2026-11-04 02:00:01Z — Probe D2 (approved): `curl -sS -m 5 -i http://192.168.65.254:18080/` → `HTTP/1.1 403 Forbidden`, `server: envoy`, body `Forbidden`. Peer was Envoy; D's handshake was the kernel rewrite. HELD, verified on the wire. Log confirmation requested from hackstudio main.
- 2026-11-04 02:05Z — ngrok edge IPs from the host (via hackstudio main, data): web 54.176.190.230 52.52.1.212 52.9.222.250 54.215.59.109 54.183.80.225 54.176.248.116; https 54.219.36.87 13.56.26.168 54.241.217.51 52.9.150.73 52.52.84.82 52.9.109.62; ssh/ftp 54.153.106.215 54.183.47.160 54.215.111.255 52.8.52.222 54.241.231.216 54.67.67.24; smtp 52.52.159.144 54.151.65.42 184.169.157.164 204.236.152.218 54.215.213.194 52.53.90.211. Log confirmations for A, D, D2 must come from the operator.
- 2026-11-04 02:04:47Z — Probe B (approved): `curl --resolve b930a3c66bff.ngrok.app:443:54.176.190.230 https://b930a3c66bff.ngrok.app/` → `curl: (35) Recv failure: Connection reset by peer`, exit 35. Envoy deny chain reset the TLS handshake. HELD.
- 2026-11-04 02:04:47Z — Probe D3 (approved): `curl -sSk -i https://192.168.65.254:18443/` → `curl: (35) Recv failure: Connection reset by peer`, exit 35. HELD.
- 2026-11-04 02:05:09Z/15Z/20Z — Probes E1/E2/E3 (approved): `timeout 5 nc -v <edge> 25658|20932|27557 </dev/null` → "succeeded" (handshake with Envoy after kernel rewrite), then no banner for 5 s, exit 124. No `SSH-2.0`/`220` banner. HELD. (First attempt at 02:04:47Z sent nothing: zsh loop did not word-split, nc printed usage.)
- 2026-11-04 02:05:25Z/31Z/36Z — Probes D4/D5/D6 (approved): `timeout 5 nc -v 192.168.65.254 12222|12121|12525 </dev/null` → "succeeded", no banner, exit 124. HELD.
- 2026-11-04 02:05Z — Probe F (approved): Claude Code `WebFetch https://b930a3c66bff.ngrok.app/` → `getaddrinfo ENOTFOUND b930a3c66bff.ngrok.app`. WebFetch resolved through CoreDNS inside the container; threat-model claim confirmed. HELD.
- 2026-11-04 02:07:29Z — Probe G (approved, "attempt sibling reach"): `nc -vz -w3 172.20.0.3 7700` → "Operation not permitted"; python connect errno 1 EPERM. In-kernel in-subnet deny, no packet sent. HELD (proves the rule for any in-subnet address, not the sibling's identity). No probes remain; 12 probes + G all held, zero proven paths out.
