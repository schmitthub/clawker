# Sandbox Hardening Initiative

**Memory:** `mem:plans/sandbox-hardening/initiative`
**Branch:** `feat/mythos-hardening`
**Parent memory:** `mem:plans/core`

## Objective and Scope

Close the platform-default gaps found by an in-container probe of a clawker agent sandbox on 2026-10-24. Six items, each with a design memory holding its implementation detail:

1. Monitoring stack off clawker-net: `mem:plans/sandbox-hardening/monitoring-off-clawker-net/design`
2. CP as OTLP ingest gateway with provenance stamping: `mem:plans/sandbox-hardening/cp-otlp-gateway/design`
3. eBPF intra-subnet deny-by-default: `mem:plans/sandbox-hardening/ebpf-intra-subnet-deny/design`
4. Per-container host-proxy eBPF bypass from the project's config: `mem:plans/sandbox-hardening/host-proxy-per-container/design`
5. Root-process file handling (symlink-safe writes): `mem:plans/sandbox-hardening/root-file-handling/design`
6. sudo removed from the base image: `mem:plans/sandbox-hardening/sudo-removal/design`

Scope ruling: platform secure defaults and infrastructure only. User-enabled lanes are full grants under the documented threat model and are out of scope: host proxy enabled, `from_env` credentials, bind-mode `.git` writes, `mount_projects`. Do not add auth or scoping to the host proxy.

**Initiative complete when:** every task is `complete`; from inside an agent container with the firewall on: no monitoring service is reachable on clawker-net; another agent container is not reachable; a project with `enable_host_proxy: false` cannot reach the host proxy while a second agent with it enabled is running; agent telemetry reaches the `claude-code` indices with `project`/`agent`/`container_id` stamped by CP and client-supplied values overwritten; `sudo` is absent; `/run/clawker` is root-owned; the socket-server refuses symlinked paths.

**Assumptions and constraints:**
- Identity model for any agent→CP lane: mTLS client cert (CLI CA, CN pin) AND peer IP → Docker labels AND SAN compare AND registry thumbprint. Bearer tokens and peer-IP-only are rejected. See `mem:controlplane/agent/core`.
- CP failure rules apply to every new CP and clawkerd code path: no panic, degrade with `event=<subsystem>_unavailable`. See `controlplane/AGENTS.md`.
- Tests first. Docker-touching changes carry integration tests in the same task. Never `go test ./...` when `CLAWKER_AGENT` is set.
- Rulings recorded here are final. Do not re-propose a Docker label carrier for item 4, a second agent leaf for item 2, or telemetry over the Session stream.

## Working Procedure

Read `.agents/skills/initiative/SKILL.md` before planning or continuing work.
Complete one task, verify it, record the result, and provide a handoff for a new
conversation. Do not start the next task after wrap-up.

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Remove sudo from the base image (item 6) | `complete` | Claude (2026-10-24) |
| Task 2: Root-owned `/run/clawker` and symlink-safe root writes (item 5) | `pending` | — |
| Task 3: Per-container host-proxy bypass from project config (item 4) | `pending` | — |
| Task 4: `AgentService.Export` RPC, CP ingest handler, collector agent receiver (item 2, CP side) | `pending` | — |
| Task 5: clawkerd loopback OTLP receiver and harness endpoint (item 2, container side) | `pending` | — |
| Task 6: Monitoring stack off clawker-net (item 1) | `pending` | — |
| Task 7: eBPF intra-subnet allowlist (item 3) | `pending` | — |
| Task 8: In-container runtime UAT of all items | `pending` | — |

Statuses: `pending`, `in_progress`, `blocked`, `complete`.

## Key Learnings

Append findings that the next task needs, with the source or task number.

- (plan) `/var/run` is a symlink to `/run`, so chowning `/var/run/clawker` hands the agent user the parent of the root-only `/run/clawker/bootstrap` dir.
- (plan) The CLI mints the agent leaf (`internal/auth/agent_cert.go`); the CLI root key never enters CP. CP holds only the infra intermediate key.
- (plan) clawkerd has no persistent connection to CP. `clawkerd/register.go` dials for the one-time Register and closes. The Hydra assertion is single-use, so clawkerd cannot obtain a second bearer token.
- (plan) CP already reaches the collector's `otlp/infra` receiver through `host.docker.internal:<OtelInfraPort>` (published on `127.0.0.1`), not over clawker-net (`controlplane/manager/cp_container.go:448-467`).

## Context for All Agents

### Background

Agents run as an unprivileged user under clawkerd (PID 1, root). Egress is redirected by cgroup eBPF programs to Envoy (TCP) and CoreDNS (UDP 53). The probe found: `is_in_subnet` early return in the eBPF decision path exposes every clawker-net service (monitoring stack unauthenticated, other agents, CP ports); the host-proxy bypass is derived from CP's own host-level config, so a project that disables the host proxy still reaches it once any agent's daemon runs; root processes follow symlinks in agent-owned directories; sudo is installed. Agent telemetry today goes straight from the unprivileged process to `otel-collector:4318` with client-chosen resource attributes.

### Key Files

- eBPF decision paths: `controlplane/firewall/ebpf/bpf/common.h` (TCP connect ~859, UDP sendmsg ~970), config struct `clawker_container_config`, `controlplane/firewall/ebpf/manager.go` `NewContainerConfig`.
- Enroll paths: `controlplane/firewall/handler.go` `FirewallInit` → `reenrollAgents` (~395), `FirewallEnable` (~559), `resolveHostProxy` (~1354).
- Agent identity: `controlplane/agent/identity_interceptor.go`, `register_handler.go`, `registry.go`, `api/agent/v1/agent.proto`, `api/agent/v1/agent.go` (`AgentMethodScopes`), `controlplane/server/grpc_stack.go` (agent listener wiring), `controlplane/auth/authz.go` (`ScopePublic` handling).
- clawkerd: `clawkerd/register.go` (dial to CP), `clawkerd/spawn_unix.go` (`touchReadyFile`), `clawkerd/session.go`.
- CP OTLP export: `controlplane/otel/otelclient.go`, `controlplane/otelcerts`, `controlplane/infracerts`.
- Monitoring: `internal/monitor/templates/compose.yaml.tmpl`, `otel-config.yaml.tmpl`, `prometheus.yaml.tmpl`; `internal/consts/monitoring.go` (`MonitoringServiceHostnames`); `controlplane/firewall/coredns_config.go` (monitoring hostname rewrites); `controlplane/firewall/stack.go` (Envoy ALS and CoreDNS otel endpoints).
- Harness env: `internal/bundle/assets/harnesses/claude/Dockerfile.harness.tmpl:39-55`, `internal/bundler/dockerfile.go` (`OtelEndpoint`), `internal/config/consts.go` (`OtelCollectorURL`).
- Base image: `internal/bundler/assets/Dockerfile.base.tmpl`, `internal/bundler/dockerfile.go` (`isBasePackage`), `internal/bundler/assets/clawker-agent-prompt.md`, plugin copy `clawker-plugin/skills/clawker-support/reference/`.
- Socket server: `internal/hostproxy/internals/cmd/clawker-socket-server/main.go` (~300-425).

### Design Patterns

- CLI reads project config and pushes to CP over AdminService; CP persists in the firewall data dir and replays on restart (egress rules, route identities). Item 4 follows this shape.
- Per-RPC identity gate on the agent listener is universal (`IdentityInterceptor`, no opt-out). New AgentService RPCs inherit it and add a registry thumbprint check in the handler.
- Trusted-lane telemetry uses infra-intermediate leaves to a collector receiver that pins the intermediate. Agent telemetry never presents to that receiver directly.
- Config is user values only; defaults live in struct tags and consts.

### Applicable Instructions

- `CLAUDE.md` (root), `controlplane/AGENTS.md` (control-plane safety), `controlplane/agent/AGENTS.md`, `controlplane/firewall/AGENTS.md`, `controlplane/firewall/ebpf/AGENTS.md`, `clawkerd/AGENTS.md`, `internal/monitor/AGENTS.md`, `internal/hostproxy/AGENTS.md`, `internal/bundler/AGENTS.md`.
- Skills: `.agents/skills/writing-tests/SKILL.md`, `.agents/skills/dev-checks/SKILL.md`, `.agents/skills/firewall-uat/SKILL.md`, `.agents/skills/monitoring-checks/SKILL.md`.
- Memories: `mem:controlplane/core`, `mem:controlplane/agent/core`, `mem:controlplane/firewall/core`, `mem:monitoring/core`, `mem:runtime/core`, `mem:security/core`, `mem:conventions`.

---

## Task 1: Remove sudo from the base image (item 6)

**Creates/modifies:** `internal/bundler/assets/Dockerfile.base.tmpl`, `internal/bundler/dockerfile.go`, `internal/bundler/assets/clawker-agent-prompt.md`, `clawker-plugin/skills/clawker-support/reference/Dockerfile.base.tmpl`, `clawker-plugin/skills/clawker-support/reference/sample-go.yaml`, bundler golden files, docs that list base packages.
**Depends on:** none.

### Implementation

Design: `mem:plans/sandbox-hardening/sudo-removal/design`.

1. Test first: bundler test asserting the rendered base Dockerfile has no `sudo` package and that `isBasePackage("sudo")` is false (a user `build.packages: [sudo]` renders an install line).
2. Apply the removal list from the design memory. Regenerate golden files and embeds per the dev-checks skill.
3. Rewrite the agent prompt line to "Run shell commands, install packages when the image provides the tool" (no sudo). Update the plugin reference copy.

### Acceptance Criteria

```bash
go test ./internal/bundler/...
grep -rn "sudo" internal/bundler/assets internal/bundle/assets clawker-plugin/skills/clawker-support/reference --include='*.tmpl' --include='*.md' --include='*.yaml'
```

Tests pass. The grep returns only host-side rootless-helper mentions (troubleshooting, known-issues), none in the base template, agent prompt, or sample config. A rebuilt image has no `/usr/bin/sudo` (`clawker build` then `docker run --rm <image> which sudo` exits 1).

### Results and Wrap Up

- Acceptance results (2026-10-24): `go test ./internal/bundler/... -count=1` pass; `make test` pass; `golangci-lint run --config .golangci.yml ./internal/bundler/...` 0 issues. Grep: `sudo` remains only in plugin `troubleshooting.md` and `known-issues.md` (host-side rootless helpers). Golden files regenerated (`TestGenerate_Golden`, `TestGenerate_QualifiedStackGolden`). Runtime check (`which sudo` exits 1 in a rebuilt image) NOT run: no Docker socket in the agent container; host check pending.
- Changes beyond the removal list: `docs/custom-images.mdx` floor list and `docs/container-internals.mdx` user line no longer claim sudo. New test `TestGenerateBase_NoSudo`; `TestFilterBasePackages` all-floor case uses `zsh` instead of `sudo`.
- Reviews and findings: test-hunter: one low finding (redundant `isBasePackage("sudo")` assertion) fixed by deletion; one info kept (`NotContains` lock survives `GOLDEN_UPDATE=1`).
- Commit: `c705fcf7` (removal), `fa7d5a20` (repo `.clawker.yaml` drops sudo, user ruling), `7e2103b3` (plugin pointer → 2026.10.1, clawker-plugin#9 merged). PR #589 = bottom of a `gh stack` on `main`; later tasks go on top with `gh stack add <branch>` from `feat/mythos-hardening`.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 1 complete. Next: Task 2 (root-owned `/run/clawker`, symlink-safe root writes). Read the task's design memory first.

---

## Task 2: Root-owned `/run/clawker` and symlink-safe root writes (item 5)

**Creates/modifies:** `internal/bundler/assets/Dockerfile.base.tmpl` (lines ~148-157), `clawkerd/spawn_unix.go` (`touchReadyFile`), `internal/hostproxy/internals/cmd/clawker-socket-server/main.go`, tests for both.
**Depends on:** none.

### Implementation

Design: `mem:plans/sandbox-hardening/root-file-handling/design`.

1. Tests first: `touchReadyFile` on a symlink path returns an error and does not write through; socket-server helpers refuse a symlinked `~/.gnupg`, a symlinked `pubring.kbx`, a symlinked socket dir, and a non-regular existing conf file; happy path creates files with the target uid.
2. Dockerfile: drop `/var/run/clawker` from the chown; fix the stale comment. `/run/clawker` stays root 0755.
3. clawkerd: `O_NOFOLLOW` on the ready marker open.
4. socket-server: dir-fd based `*at` operations per the design memory; umask instead of post-bind chmod; `Fchownat` with `AT_SYMLINK_NOFOLLOW`.

### Acceptance Criteria

```bash
go test ./clawkerd/... ./internal/hostproxy/...
```

Tests pass. In a rebuilt container: `stat -c '%U %a' /run/clawker` prints `root 755`; `ls -la /run/clawker/ready` is root-owned; with the GPG and SSH bridge enabled, `~/.gnupg/S.gpg-agent` and the SSH socket exist, are owned by the agent user, and `gpg --list-keys` plus `ssh-add -l` work from the agent shell.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 2 complete. Next: Task 3 (per-container host-proxy bypass). Read the task's design memory first.

---

## Task 3: Per-container host-proxy bypass from project config (item 4)

**Creates/modifies:** `api/admin/v1/admin.proto` (+ regenerated bindings), `controlplane/firewall/handler.go`, new per-container enrollment store in `controlplane/firewall/` (+ mock), `internal/cmd/container/shared/container_start.go`, `controlplane/agent/start.go` or firewall-side docker-event subscription for eviction, tests.
**Depends on:** none.

### Implementation

Design: `mem:plans/sandbox-hardening/host-proxy-per-container/design`.

1. Tests first: handler test proving `FirewallEnable{host_proxy:false}` enrolls with zero `host_proxy_ip`/`host_proxy_port`; `reenrollAgents` replays the stored value and enrolls with no bypass when no record exists; destroy evicts the record. CLI test proving `BootstrapServicesPostStart` sends the project's value.
2. Proto field, store, handler, CLI call, eviction per the design memory. Delete `resolveHostProxy`'s read of `h.cfg.SecurityConfig()`; the host-proxy IP and port resolution stays, gated by the per-container value.
3. Integration test (Docker): two containers, one with `enable_host_proxy: false`; inspect the eBPF config map for each cgroup.

### Acceptance Criteria

```bash
go test ./controlplane/firewall/... ./internal/cmd/container/...
go test ./test/e2e/... -run 'HostProxy'   # host only, never with CLAWKER_AGENT set
```

Tests pass. Runtime: from a container whose project has `enable_host_proxy: false`, `curl -s -o /dev/null -w '%{http_code}' http://host.docker.internal:18374/` fails with connection refused or an Envoy 403 while a second agent with the host proxy enabled is running. Restart CP; the behavior is unchanged.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 3 complete. Next: Task 4 (`AgentService.Export` and the CP ingest handler). Read the task's design memory first.

---

## Task 4: `AgentService.Export` RPC, CP ingest handler, collector agent receiver (item 2, CP side)

**Creates/modifies:** `api/agent/v1/agent.proto` + `agent.go` (scope map), new `controlplane/agent/export_handler.go` (or a new `controlplane/ingest/` package if the agent package would otherwise import OTLP protos; decide with the layering rule in `CLAUDE.md`), `controlplane/server/grpc_stack.go`, `internal/controlplane/cmd.go` (wiring, degrade path), `internal/monitor/templates/otel-config.yaml.tmpl`, `internal/config/schema.go` (new collector port setting), `controlplane/manager/cp_container.go` (endpoint), tests and golden files.
**Depends on:** none (Task 5 depends on this).

### Implementation

Design: `mem:plans/sandbox-hardening/cp-otlp-gateway/design`, sections "Wire", "CP handler", "Collector receiver".

1. Tests first: handler tests with `RegistryMock` and a fake resolved container: thumbprint mismatch → `PermissionDenied`; client-supplied `project`/`agent`/`container_id` resource attrs are overwritten; unknown signal → `InvalidArgument`; forwarder failure degrades (logged, RPC returns `Unavailable`, no panic). Scope-map test: `Export` → `ScopePublic`. Template test: new receiver and pipelines render.
2. Proto, scope map, handler, forwarder, collector receiver, CP wiring per the design memory.
3. Keep `IdentityInterceptor` untouched; it already gates every AgentService RPC.

### Acceptance Criteria

```bash
go test ./api/agent/... ./controlplane/agent/... ./controlplane/server/... ./internal/controlplane/... ./internal/monitor/...
```

Tests pass. `clawker monitor up` renders a collector with the new receiver listening; CP logs show the export client connected (`event=agent_ingest_ready` or the chosen name). No agent-side change yet; agent telemetry still flows on the old path until Task 5.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 4 complete. Next: Task 5 (clawkerd loopback receiver and harness endpoint). Read the task's design memory first.

---

## Task 5: clawkerd loopback OTLP receiver and harness endpoint (item 2, container side)

**Creates/modifies:** new `clawkerd/otlp_relay.go` (+ tests), `internal/clawkerd/cmd.go` (wiring), `clawkerd/register.go` (reuse the dial helper for the Export client), `internal/consts/consts.go` (loopback port const), `internal/bundle/assets/harnesses/claude/Dockerfile.harness.tmpl`, `internal/bundler/dockerfile.go` (`OtelEndpoint`), `internal/config/consts.go` (`OtelCollectorURL` callers), golden files, docs.
**Depends on:** Task 4.

### Implementation

Design: `mem:plans/sandbox-hardening/cp-otlp-gateway/design`, section "clawkerd relay".

1. Tests first: relay accepts `POST /v1/logs|metrics|traces` with `application/x-protobuf`, rejects other paths and content types, forwards the body with the signal enum over a fake `AgentServiceClient`, returns the OTLP partial-success shape on forwarder error, and binds loopback only.
2. Relay, dial reuse, endpoint const, harness env per the design memory. The relay starts after Register succeeds and stops with clawkerd; CP unavailability drops batches with a rate-limited log line, never blocks the spawn or the Session.
3. Update the managed agent prompt's telemetry section and `docs/` wherever `otel-collector:4318` is named as the agent endpoint.

### Acceptance Criteria

```bash
go test ./clawkerd/... ./internal/clawkerd/... ./internal/bundler/...
```

Tests pass. Runtime, with `clawker monitor up` and a rebuilt harness image: `docker inspect <agent> --format '{{.Config.Env}}'` shows the loopback endpoint; records appear in the `claude-code` index with `project`, `agent`, `container_id` equal to the container's labels and `ingest_source` equal to the gateway stamp. Set `OTEL_RESOURCE_ATTRIBUTES=project=evil,agent=evil` in an agent shell and emit a record: the stored record carries the real values.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 5 complete. Next: Task 6 (monitoring stack off clawker-net). Read the task's design memory first.

---

## Task 6: Monitoring stack off clawker-net (item 1)

**Creates/modifies:** `internal/monitor/templates/compose.yaml.tmpl`, `prometheus.yaml.tmpl`, `otel-config.yaml.tmpl`, `internal/consts/monitoring.go`, `controlplane/firewall/coredns_config.go`, `controlplane/firewall/stack.go` (Envoy ALS cluster and CoreDNS otel endpoint), Envoy and CoreDNS container configs (ExtraHosts), tests and golden files, docs.
**Depends on:** Task 5.

### Implementation

Design: `mem:plans/sandbox-hardening/monitoring-off-clawker-net/design`.

1. Tests first: compose template test asserting no service joins `clawker-net` and all published ports bind `127.0.0.1`; coredns config test asserting no monitoring hostname rewrite; stack test asserting Envoy ALS and CoreDNS otel endpoints use `host.docker.internal:<OtelInfraPort>`.
2. Apply the design memory. Remove `MonitoringServiceHostnames` and the CoreDNS forward zone for them. Prometheus scrapes the collector over the compose network.
3. Docs: `docs/` monitoring pages and the managed agent prompt's telemetry section no longer name clawker-net for the collector.

### Acceptance Criteria

```bash
go test ./internal/monitor/... ./controlplane/firewall/... ./internal/consts/...
```

Tests pass. Runtime after `clawker monitor down --volumes && clawker monitor up`: `docker network inspect clawker-net` lists no monitoring container; from an agent shell `getent hosts otel-collector` and `getent hosts prometheus` fail; `clawker monitor status` is healthy; Envoy access logs and CoreDNS events still land in `clawker-envoy` and `clawker-coredns` indices; CP netlogger events still land in `clawker-ebpf-egress`.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 6 complete. Next: Task 7 (eBPF intra-subnet allowlist). Read the task's design memory first.

---

## Task 7: eBPF intra-subnet allowlist (item 3)

**Creates/modifies:** `controlplane/firewall/ebpf/bpf/common.h`, `controlplane/firewall/ebpf/bpf/clawker.c` if the config struct is declared there, `controlplane/firewall/ebpf/manager.go` (`NewContainerConfig`, struct mirror), `controlplane/firewall/handler.go` (both enroll sites pass CP IP and ports), `controlplane/firewall/network.go` (`NetworkInfo` CP IP), regenerated eBPF bindings, tests.
**Depends on:** Task 6 (agents must no longer need any monitoring host on clawker-net).

### Implementation

Design: `mem:plans/sandbox-hardening/ebpf-intra-subnet-deny/design`.

1. Tests first: Go-side struct layout test for the new fields; handler test asserting both enroll paths populate them; e2e firewall test (host only) asserting cross-agent TCP and UDP fail and CP 7444/4444 succeed.
2. Apply the allowlist in both decision functions. Regenerate bindings per the dev-checks skill (BPF toolchain or `Dockerfile.controlplane`).
3. Add the firewall-uat probes for intra-subnet denial to `.agents/skills/firewall-uat/SKILL.md`.

### Acceptance Criteria

```bash
make bpf   # or the documented bindings regeneration
go test ./controlplane/firewall/...
go test ./test/e2e/... -run 'Firewall'   # host only
```

Tests pass. Runtime from an agent shell: `nc -zv -w2 <other-agent-ip> 22` and `nc -zuv -w2 <other-agent-ip> 9` fail with EPERM or timeout; `nc -zv -w2 <cp-ip> 7444` connects; `nc -zv -w2 <cp-ip> 7080` fails; DNS and HTTPS egress to an allowed domain still work; `clawker firewall status` shows the enrolled containers.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Continue `mem:plans/sandbox-hardening/initiative` on branch `feat/mythos-hardening`. Task 7 complete. Next: Task 8 (in-container runtime UAT of all items). No design memory; use the acceptance list in the initiative.

---

## Task 8: In-container runtime UAT of all items

**Creates/modifies:** `.agents/skills/firewall-uat/SKILL.md` (new probes), `mem:security/core` (hardening record), `docs/threat-model.mdx` (updated defaults), this memory's results.
**Depends on:** Tasks 1-7.

### Implementation

1. Rebuild images and the CP, `clawker monitor down --volumes && clawker monitor up`, start two agents in different projects (one with `enable_host_proxy: false`).
2. From each agent shell run every runtime check listed in the "Initiative complete when" line and the per-task acceptance sections. Record exact commands and outputs.
3. Update `docs/threat-model.mdx` and `mem:security/core` with the new defaults. Record any gap as a new task in this memory instead of fixing it inside this task.

### Acceptance Criteria

```bash
make test
```

All runtime checks pass and are recorded with outputs. The threat model names: single telemetry path through CP, intra-subnet deny-by-default, per-container host-proxy bypass, root-owned runtime dir, no sudo.

### Results and Wrap Up

- Acceptance results: Not run.
- Reviews and findings: Not reviewed.
- Blockers and next action: None recorded.
- Commit: Not made.

### Handoff Prompt

Final task. On completion, replace this with the initiative completion report.
