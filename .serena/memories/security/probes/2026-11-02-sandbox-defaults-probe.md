> Retained record (2026-11-02, branch `release/mythos-hardening`, HEAD e30706c6, agent `dev` on Docker Desktop macOS, kernel 7.0.14-linuxkit, CLAWKER_VERSION v2026.10.3-108-ge30706c6). Operator-directed probe from inside an agent container. Findings reflect that date; check source before reuse.

# Sandbox secure-defaults probe (2026-11-02)

Scope set by operator: find failures in the secure defaults that users are not aware of. Static analysis of the sandbox configuration plus in-container recon. No escape attempts: no packet left the container except the Docker-DNS regression checks below (both denied). User-decided risk is out of scope (GitHub token scopes, SSH/GPG forwarding on, bind-mounted repo including `.git`, host `~/.claude/projects` bind, docker group membership, host proxy on). Prior record: `mem:security/probes/2026-10-26-sandbox-escape-probe`.

## Findings (ranked)

### 1. HIGH — eBPF hooks see only TCP and UDP; SCTP and UDP-Lite sockets bypass them

- `clawker_sock_create` (`controlplane/firewall/ebpf/bpf/clawker.c` ~L393) denies only `SOCK_RAW` and `SOCK_DGRAM`+ICMP. Every other inet socket is allowed. `clawker_connect4` returns allow for any type other than STREAM/DGRAM and is never invoked for SCTP at all (the kernel calls the cgroup connect hook from `tcp_v4_pre_connect`/`udp_pre_connect`; `sctp_prot` and `udplite_prot` have no `pre_connect`). `sendmsg4` is wired to `udp_sendmsg` only, and `udp_sendmsg` skips the hook on a connected socket.
- Kernel in the Docker Desktop VM has `SCTP`, `SCTPv6`, `UDP-Lite`, `UDPLITEv6` (`/proc/net/protocols`). `socket(AF_INET, SOCK_STREAM, 132)`, `socket(AF_INET, SOCK_SEQPACKET, 132)`, `socket(AF_INET6, SOCK_STREAM, 132)` and `socket(AF_INET, SOCK_DGRAM, 136)` all succeed under enforcement.
- Hook-level proof (self-connect to own IP `172.18.0.2:1`, in-subnet and not allowlisted, no packet leaves the container): TCP `EPERM`, UDP `EPERM`, SCTP `ECONNREFUSED` (INIT sent, refused by own kernel), UDP-Lite `connect()` succeeds. Reproduced twice. A connected UDP-Lite socket then sends with zero hooks.
- Egress reach was not tested (operator ruling). Expected impact: on Linux Docker Engine `nf_conntrack` handles SCTP and UDP-Lite and MASQUERADE forwards them, so this is unfiltered bidirectional egress to any IP literal (no DNS needed). Docker Desktop NAT support for protocols 132/136 unknown. In-subnet L3 reach to siblings, CP, gateway is unfiltered either way (needs a listener to matter).
- Docs claim coverage: `docs/threat-model.mdx` L133 "UDP (non-DNS UDP dropped entirely)", L159 "blocks all outbound traffic".
- Fix direction: make `sock_create` an allowlist: allow `SOCK_STREAM` with `IPPROTO_TCP` and `SOCK_DGRAM` with `IPPROTO_UDP` only (the kernel resolves protocol 0 before the hook runs), deny `SOCK_SEQPACKET`. Add e2e `TestFirewall_SCTPBlocked` and `TestFirewall_UDPLiteBlocked` (create must fail with EPERM). Update the firewall-uat skill.

### 2. HIGH — uid 0 is exempt from every eBPF program; `clawker exec` defaults to root

- `enter_enforced` (`common.h` ~L745): `if (uid == 0) return ENTER_NOT_MANAGED` before any lookup. Root processes get unfiltered egress, no netlogger events, no in-subnet deny.
- Container `Config.User` is `opts.User` with default `""` (`container_create.go` L235, L405), so Docker uses the image `USER`, and `Dockerfile.harness-image.tmpl` ends with `USER root` (L126). `clawker exec`/`clawker container exec` has `--user` default `""` (`exec.go` L123) and no fallback to `consts.ContainerUser`. A plain `clawker exec --agent X -- bash` (and any `docker exec`) is therefore a root shell with the firewall off. `exec` also exposes `--privileged`. The exec docs show `-u root` as an example and never state the default. Not stated in `docs/security.mdx` or `docs/threat-model.mdx`.
- The CMD path is fine: clawkerd drops to the container user (`spawn_unix.go` ~L1010); boot steps run as `consts.HostUID()` except `dockerSocketStep` (fixed `sh -c` string as root, `boot_steps.go` L14).
- Fix direction: (a) exec defaults to the container user (`CLAWKER_USER`/`consts.ContainerUser`) and documents that `-u root` leaves the firewall; (b) remove the uid-0 exemption. clawkerd needs only CP `cp_agent_port`, `cp_hydra_port` and CoreDNS, which `allowed_internal` already admits; name resolution of `clawker-controlplane` can use the `cp_ip` literal already in `container_config`. If an exemption must stay, key it on PID 1 (`bpf_get_ns_current_pid_tgid`), not uid.

### 3. MEDIUM — git credential host action: no host scoping, and `store`/`erase` reach the host credential store

- `internal/hostproxy/actions.go` `gitCredential`/`validateGitCredential`: checks only action, non-empty protocol/host, and injection characters. `open/url` and `callback/register` run `checkEgress`; git credential does not. Any `host` value reaches the host's `git credential fill|approve|reject` (on macOS `osxkeychain` queries the keychain by server+protocol). `approve` overwrites and `reject` deletes the host entry; a sandboxed git getting one 401 erases the user's host token.
- Defaults: `enable_host_proxy: true`, `forward_https: true` (`schema.go` L309, L324). Grant check and CP identity are in place (`controlplane/agent/host_handler.go` `authorizeHost`), so this is in-grant reach, not an auth bypass. Previous probe item 2, still open on the scoping half.
- Fix direction: `get` only by default (`approve`/`reject` answered locally as no-ops); require `protocol://host` to match an allowed https rule when the firewall is on.

### 4. MEDIUM — the egress rule store is global across projects

- `EgressRule` (`api/admin/v1/admin.proto` L141) has no project or container scope. `container_config` (32 bytes) carries addresses only. `dns_cache` is keyed by IP, `route_map` by route identity; `dnsbpf` writes answers for every client. Every enrolled container on the host shares one allowlist: project A's `add_domains` are reachable from project B's agents.
- Documented only in passing under `clawker firewall prune` (`docs/firewall.mdx` L455); L196 says "Per-project rules".
- Fix direction: either state it plainly in the firewall docs and threat model, or carry a per-container rule-set id in `container_config` and key `route_map`/CoreDNS views on it.

### 5. MEDIUM — project config is agent-writable and trusted by the host CLI on the next run

- `.clawker.yaml` sits in the bind-mounted workspace (rw). The host CLI reads it on every `run`/`start`/`build` with no trust prompt or change detection (no config hash anywhere in `internal/cmd` or `internal/config`). Keys an agent can set that the host then honors: `security.docker_socket: true` (host root), `security.cap_add`, `security.enable_host_proxy`, `security.firewall.add_domains`/`rules` (widen egress), `agent.from_env` (forward host env), `agent.env_file` (absolute and `~` paths accepted, `agentenv.go` L165/L182; `dotenv.ParseWithLookup` falls back to the host env for bare `KEY` lines, so a planted `.env` with `AWS_SECRET_ACCESS_KEY` on a line forwards the host value), `build.root_run`/`user_run` (run during `docker build` on the host daemon as root with unrestricted network).
- Harness `mounts`/`volumes` cannot name host paths (`validateMountSpec` → staging inside declared volumes). `HostPassthroughEnv` is `TERM_PROGRAM` only.
- `docs/threat-model.mdx` L185 says the agent "cannot ... access other applications" and credential stores; with a config edit it can on the next start.
- Operator ruling needed: the 2026-10-26 ruling excluded the bind-mounted repo contents; this is the config file as an attack surface. Fix direction: hash the security-relevant subtree (`security`, `agent.env_file`, `agent.from_env`, `build`) at `project init`/first run, refuse to start on a changed hash until the user confirms; or read those keys only from a host-side file outside the workspace.

### 6. LOW — no resource limits by default

- `pids.max = max`, `memory.max = max`, `cpu.max = max` in the container cgroup. `--pids-limit` default 0 (Docker default, unlimited), no memory default. A fork bomb or allocation loop from inside takes down the Docker Desktop VM or starves a Linux host. The threat model claims host protection from inside.
- Fix direction: default `PidsLimit` (e.g. 4096) and a documented memory default; users keep the flags.

### 7. LOW — named volumes outlive `clawker rm` by default

- `clawker container remove` deletes volumes only with `--volumes` (`remove.go` L87). `~/.claude` (settings.json hooks, plugins, `.credentials.json`), `~/.clawker` (post-init marker, hook scripts) and `/commandhistory` are per project+agent volumes. A compromised agent persists hooks or plugins into the next "fresh" container of the same agent name. Docs call the container disposable without saying that `rm` keeps these.
- Fix direction: docs note plus a `clawker rm` hint, or default `--volumes` for `rm --force`.

### 8. INFO

- OAuth callback (`registerCallback`): port comes from the auth URL's `redirect_uri`, any port 1024-65535, auth URL is egress-checked, sessions are owner-scoped in the CP gateway. Loopback port squatting on the host remains possible for allowed auth hosts; previous item 1 is otherwise closed.
- Host `.gitconfig` is mounted ro at `/tmp/host-gitconfig` (names, signing key id, credential helper names). Information only.
- `commandhistory` volume mounted at `/commandhistory` world-readable; `.bash_history` empty.

## What held (verified)

- Docker embedded DNS direct ports (#598 fix): TCP `127.0.0.11:39753` and UDP `127.0.0.11:36857` → `permission denied` for `example.com`; CoreDNS :53 → NXDOMAIN.
- `sock_create` denies: ping socket `EPERM`, raw `EPERM`. `AF_VSOCK` stream/dgram `EPERM` (Docker default seccomp; `/proc/sys/net/vsock/ns_mode` is `global`, so this seccomp rule is load-bearing). `AF_PACKET`, `AF_XDP` `EPERM` (no CAP_NET_RAW). `IPPROTO_L2TP` `EPROTONOSUPPORT` (module absent). `unshare -U` / `-Ur` `EPERM`.
- Process: caps 0 (bounding = Docker default), `NoNewPrivs 1`, seccomp 2, `/sys` and `/sys/fs/cgroup` ro, workspace `nosuid,nodev`, `/run/clawker/bootstrap` root-only, `/proc/1/environ` unreadable, no `sudo`.
- Network: only lo has IPv6; eth0 v4 only. Listeners: `127.0.0.1:4318` OTLP relay, `127.0.0.1:18374` host relay, `[::]:7700` clawkerd mTLS, Docker DNS on 127.0.0.11. Envoy admin is loopback-only (`envoyAdmin`); CoreDNS admitted on 53 only; CP on 7444/4444 only.
- Host actions: behind CP `AgentService` with registered-peer check, `HostProxy`/`GitHTTPS` grants, and callback session ownership. Host proxy has no inbound TCP listener except the OAuth callback ports.
- GPG forwarding uses the gpg-agent `extra` (restricted) socket (`socketbridge/bridge.go` L471). SSH forwards the full agent (user choice).
- Lifecycle hooks: `pre-run.sh`/`post-init.sh` are re-delivered by the CLI each start via tar copy (moby untar removes an existing non-dir first), run as the container user.

## Status after hardening (2026-11-02, branch `fix/mythos-probe-2026-11-02` onto `release/mythos-hardening`)

1. SCTP/UDP-Lite: FIXED. `sock_create` is an allowlist (`sock_type_allowed`, TCP stream + UDP datagram only). Privileged `TestProgRun_SockTypeAllowed`; e2e `TestFirewall_SocketTypeAllowlist`.
2. uid-0 exemption: FIXED, removed from `enter_enforced` (in-container defense in depth; the agent has no path to uid 0 by design). The `exec` root-default part was REJECTED: `exec` is a host-side action by the privileged user, not an attack surface. e2e `TestFirewall_RootEnforced`.
3. git credential: FIXED. `get` runs `checkEgress` on `protocol://host/path`; `store`/`erase` answered locally (`event=git_credential_local_noop`); non-http(s) protocol refused. `TestActions_GitCredential_*`.
4. Global rule store: DOCUMENTED (threat model, firewall docs). Per-container rule-set id not implemented.
5. Agent-writable project config: REJECTED by the operator (2026-11-02). The project config is the user's own file; the trust boundary is container vs host, not project file vs user. A trust-fingerprint gate (`clawker project trust`) was built and removed in the same PR. Do not re-propose.
6. pids limit: FIXED, default `consts.DefaultPidsLimit` (4096), `--pids-limit -1` lifts. `TestContainerOptions_BuildConfigs_PidsLimitDefault`.
7. Volumes outlive `rm`: DOCUMENTED plus a stderr hint on `container remove` without `--volumes` (`TestRemoveRun_VolumesKeptHint`).

## Out of scope by operator ruling (not re-probed)

The durable scope rule is `mem:security/probes/engagement-scope`.

Project config files in the workspace (the user owns and reviews them; a host-side acceptance gate on their keys was rejected as bad hardening advice); bind-mounted repo and `.git`; host `~/.claude/projects` bind (contains session transcripts of every project on the host, 33 directories, rw); `clawker` in group `docker` (gid 999, no socket); SSH agent full-socket forwarding; host proxy on by default; granted token scopes.

## Method log

Recon: `id`, env, `/proc/self/status`, `mountinfo`, `/proc/net/{tcp,tcp6,udp,unix,protocols,if_inet6}`, cgroup limits, `ping_group_range`, `vsock/ns_mode`. Static: `clawker.c` programs 1-5, `common.h` `enter_enforced`/`container_config`/maps, `container_create.go` (`applySecurityOptions`, `Config.User`, limits), `exec.go`, `Dockerfile.harness-image.tmpl`, `boot_steps.go`, `agent/exec.go`, `host_handler.go`, `hostgateway/gateway.go`, `hostproxy/actions.go`, `agentenv.go`, `schema.go` defaults, `admin.proto`, `dnsbpf.go`, `bundler/bundle.go`, `remove.go`, `envoy_config.go` admin, docs claims. Runtime (no egress): python `socket()` creation matrix, self-connect errno classification for TCP/UDP/SCTP/UDP-Lite, `dig` against the Docker DNS direct ports, `unshare`. Script: scratchpad `sock.py` (not kept).
