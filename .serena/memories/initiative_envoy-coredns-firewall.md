# Envoy + CoreDNS Firewall Initiative

**Branch:** `feat/sidecar`
**Parent memory:** `brainstorm_envoy-coredns-firewall`

---

## Progress Tracker

**Implementation plan:** `docs/superpowers/plans/2026-03-17-envoy-coredns-firewall.md` (13 tasks, reviewed)

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Config schema refactoring | `complete` | Opus 4.6 |
| Task 2: FirewallManager interface + types + mock | `pending` | — |
| Task 3: Config generation (Envoy + CoreDNS) | `pending` | — |
| Task 4: CA + certificate management | `pending` | — |
| Task 5: Rule state management (storage.Store) | `pending` | — |
| Task 6: DockerFirewallManager implementation | `complete` | Opus 4.6 |
| Task 7: Factory wiring + container creation integration | `complete` | Opus 4.6 |
| Task 8: init-firewall.sh rewrite + bundler changes | `pending` | — |
| Task 9: CLI command group (`clawker firewall`) | `pending` | — |
| Task 10: Dante bypass implementation | `pending` | — |
| Task 11: Hostproxy lifecycle integration | `pending` | — |
| Task 12: Integration tests | `pending` | — |
| Task 13: Documentation + cleanup | `pending` | — |

**Parallelizable:** Tasks 2,3,4 after Task 1; Tasks 9,10,11 after Tasks 6+7; Tasks 12,13 after all.

## Key Learnings

### Task 1: Config Schema Refactoring
- `FirewallConfig.Enable` changed from `bool` to `*bool`. YAML `enable: true` in defaultProjectYAML deserializes correctly to `*bool` pointing to true.
- `FirewallEnabled()` now returns true when nil (default-enabled semantics).
- `NormalizeRules()` is the single conversion point from user config to internal `[]EgressRule` format.
- `requiredFirewallDomains` is now derived from `requiredFirewallRules` for backward compat.
- New Config interface methods: `EgressRulesFileName()`, `FirewallDataSubdir()`, `RequiredFirewallRules()`.
- Deleted `internal/bundler/firewall_test.go` (unit test with direct struct construction).
- `boolPtr` helper exists in `internal/cmd/container/shared/containerfs_test.go` — reused in same package.
- All 3777 tests pass.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run a single `code-reviewer` subagent to review this task's changes, then fix any findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

Replacing clawker's brittle IP-based firewall (DNS resolution + IP range API fetches at container startup via init-firewall.sh) with a shared Envoy + CoreDNS architecture on `clawker-net`. Key decisions from brainstorm:

- **Shared global firewall:** Envoy and CoreDNS run as separate containers on `clawker-net` with static IPs. Union egress policy across all projects. N+2 scaling.
- **iptables DNAT in agent containers** redirects traffic to Envoy. Root gets RETURN rule for escape hatch. NET_ADMIN on agent container, only root can exercise.
- **CoreDNS** resolves whitelisted domains only (NXDOMAIN catch-all). Agent containers use `--dns` pointing at CoreDNS.
- **Envoy** does TLS SNI passthrough for allowed domains, connection reset for denied. MITM inspection implicit when path rules configured.
- **Two-tier config:** `add_domains[]` (sugar: tls/allow) + `rules[]` (full: dst/proto/port/action/paths). Both normalize to `[]EgressRule` internally. `ip_range_sources[]` deprecated.
- **Hot-reload:** No container restarts for firewall changes. Merge user+project config → atomic write state → generate envoy.yaml + Corefile → hot-reload.
- **Escape hatch:** `clawker firewall bypass 30s --agent dev` — starts Dante (pre-installed) in target agent container via docker exec, root RETURN rule, auto-timeout.
- **FirewallManager interface:** v1=Docker API, future=gRPC impl swap for clawkerd control plane.
- **CA keypair:** Generated once, persisted in XDG data dir. Per-domain MITM certs signed by CA when rules have `paths`. CA injected into agent containers at creation time.
- **dante-server + proxychains4:** Must become native base packages in every clawker image.

### Key Files

**Config layer:**
- `internal/config/schema.go` — FirewallConfig, IPRangeSource types (to be refactored)
- `internal/config/config.go` — Config interface, RequiredFirewallDomains()
- `internal/config/defaults.go` — requiredFirewallDomains list

**Container creation:**
- `internal/cmd/container/shared/container.go` — CreateContainer(), buildRuntimeEnv()
- `internal/docker/env.go` — RuntimeEnv(), RuntimeEnvOpts

**Bundler/assets:**
- `internal/bundler/dockerfile.go` — basePackagesDebian/Alpine, Dockerfile generation
- `internal/bundler/assets/init-firewall.sh` — current iptables script (368 lines, to be replaced)
- `internal/bundler/assets/entrypoint.sh` — container entry point, firewall init block

**Factory/DI:**
- `internal/cmdutil/factory.go` — Factory struct (add Firewall noun)
- `internal/cmd/factory/default.go` — Factory constructor (wire Firewall)

**Reference patterns:**
- `internal/hostproxy/manager.go` — EnsureRunning() daemon pattern
- `internal/storage/` — atomic write, multi-file YAML store
- `pkg/whail/network.go` — Docker network CRUD, EnsureNetwork()

**New files (to create):**
- `internal/firewall/` — FirewallManager interface, Docker impl, config generation
- `internal/cmd/firewall/` — `clawker firewall` command group

### Design Patterns

- **Factory noun:** `f.Firewall()` returns a `FirewallManager` (lazy closure). Commands capture on Options struct.
- **Interface + impl:** `FirewallManager` interface in `internal/firewall/`, Docker implementation separate. Future gRPC impl swaps in.
- **hostproxy pattern:** `EnsureRunning()` idempotent startup, health checks, `IsRunning()`, `Stop()`.
- **Storage atomic write:** Write state to temp file → rename. Existing `storage` package pattern.
- **Config merge:** Existing user+project merge (project wins) already in config system. Feed merged result to firewall state engine.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package
- Read brainstorm memory `brainstorm_envoy-coredns-firewall` for full architectural decisions

---

## Task 1: Config Schema + FirewallManager Interface

**Creates/modifies:** `internal/config/schema.go`, `internal/config/defaults.go`, `internal/config/config.go`, `internal/firewall/firewall.go`, `internal/firewall/types.go`, `internal/firewall/firewalltest/`
**Depends on:** nothing

### Implementation Phase

1. **Refactor FirewallConfig in schema.go:**
   - Keep `Enable bool` and `AddDomains []string`
   - Add `Rules []EgressRule` field
   - Deprecate `IPRangeSources []IPRangeSource` (keep for backwards compat, log warning if used)
   - Define `EgressRule` type: `Dst`, `Proto` (tls/ssh/tcp, default tls), `Port` (optional), `Action` (allow/deny, default allow), `Paths` (optional, implies MITM inspection)
   - Add `NormalizeRules()` method that expands `AddDomains` into `[]EgressRule` and merges with `Rules`

2. **Update defaults.go:**
   - Convert `requiredFirewallDomains` to `[]EgressRule` format (all tls/allow)

3. **Add Config interface accessors:**
   - `FirewallStateDir() string` — XDG state dir for firewall runtime state
   - `FirewallDataDir() string` — XDG data dir for CA certs, generated configs
   - `FirewallNetworkName() string` — Docker network name constant
   - `FirewallEnvoyContainerName() string`, `FirewallCoreDNSContainerName() string`

4. **Create `internal/firewall/` package:**
   - `types.go`: `EgressRule` (if not in config), `FirewallState`, `MergedConfig`
   - `firewall.go`: `FirewallManager` interface:
     ```
     EnsureRunning(ctx) error
     Stop(ctx) error
     IsRunning(ctx) bool
     Add(ctx, []EgressRule) error
     Remove(ctx, []EgressRule) error
     Reload(ctx) error
     Bypass(ctx, containerID string, timeout time.Duration) error
     Status(ctx) (*FirewallStatus, error)
     List(ctx) ([]EgressRule, error)
     ```
   - `firewalltest/`: `MockFirewallManager` with function fields (standard pattern)

5. **Write tests** for `NormalizeRules()`, `EgressRule` validation, mock manager

### Acceptance Criteria

```bash
go build ./internal/config/...
go build ./internal/firewall/...
go test ./internal/config/... -v
go test ./internal/firewall/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 1 is complete. Begin Task 2: Firewall state engine + config generation."

---

## Task 2: Firewall State Engine + Config Generation

**Creates/modifies:** `internal/firewall/state.go`, `internal/firewall/envoy.go`, `internal/firewall/coredns.go`, `internal/firewall/state_test.go`, `internal/firewall/envoy_test.go`, `internal/firewall/coredns_test.go`
**Depends on:** Task 1

### Implementation Phase

1. **State engine (`state.go`):**
   - `FirewallStateStore` using `storage` package atomic write pattern
   - `MergeConfigs(userCfg, projectCfg, sessionRules) → MergedState`
   - `WriteMergedState(state) error` — atomic write to XDG state dir
   - `ReadMergedState() (MergedState, error)` — read current state
   - State file is YAML with full `[]EgressRule` list + metadata (last updated, source provenance)

2. **Envoy config generator (`envoy.go`):**
   - `GenerateEnvoyConfig(rules []EgressRule) ([]byte, error)`
   - Renders `envoy.yaml` from merged rules:
     - TLS Inspector listener on :10000
     - Per-domain SNI filter chains (passthrough for allow, reset for deny)
     - MITM filter chains for rules with `Paths` (per-domain cert references)
     - TCP/SSH listeners on :10001+ for non-TLS rules
   - Template-based or programmatic YAML generation
   - Reference: openclaw-deploy `/templates/envoy.ts` at `~/Code/openclaw-deploy/`

3. **CoreDNS config generator (`coredns.go`):**
   - `GenerateCorefile(rules []EgressRule, upstreamDNS []string) ([]byte, error)`
   - Renders Corefile:
     - Per-domain forward zone → upstream DNS (e.g. 1.1.1.2)
     - Catch-all `.` zone → NXDOMAIN via template plugin
   - Reference: openclaw-deploy `/templates/coredns.ts`

4. **Pipeline function:**
   - `RegenerateConfigs(state MergedState) error` — generates both configs, writes to FirewallDataDir

5. **Tests:** Golden file tests for generated envoy.yaml and Corefile from sample rule sets

### Acceptance Criteria

```bash
go test ./internal/firewall/... -v
GOLDEN_UPDATE=1 go test ./internal/firewall/... -run TestGenerate -v  # if golden files used
make test
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 3. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 2 is complete. Begin Task 3: Firewall container lifecycle."

---

## Task 3: Firewall Container Lifecycle (Envoy + CoreDNS)

**Creates/modifies:** `internal/firewall/manager.go`, `internal/firewall/manager_test.go`, `internal/cmdutil/factory.go`, `internal/cmd/factory/default.go`, `pkg/whail/network.go` (if needed)
**Depends on:** Task 2

### Implementation Phase

1. **Docker implementation of FirewallManager (`manager.go`):**
   - `DockerFirewallManager` struct — holds `whail.Engine`, `config.Config`, `*logger.Logger`
   - `EnsureRunning(ctx)`:
     - Create `clawker-net` Docker network if not exists (let Docker pick subnet)
     - Inspect network to get gateway IP, compute static IPs for Envoy/CoreDNS
     - Start `clawker-envoy` container (official image, mount generated envoy.yaml + certs)
     - Start `clawker-coredns` container (official image, mount generated Corefile)
     - Health check both containers
   - `Stop(ctx)` — stop and remove both containers + network
   - `IsRunning(ctx)` — check both containers are running and healthy
   - `Reload(ctx)` — regenerate configs from current state, signal hot-reload
   - `Add/Remove` — update session state, call Reload
   - `Status(ctx)` — return container health, active rule count, network info

2. **Wire into Factory:**
   - Add `Firewall func() (*firewall.DockerFirewallManager, error)` to `cmdutil.Factory`
   - Wire lazy closure in `factory/default.go`

3. **Network setup:**
   - If `whail.Engine` doesn't support static IP assignment on network connect, add it
   - Containers need `--dns` pointing at CoreDNS container IP

4. **Tests:** Mock-based tests for lifecycle orchestration. Integration test skeleton (Docker-required, in `test/` dir).

### Acceptance Criteria

```bash
go build ./internal/firewall/...
go build ./internal/cmdutil/...
go build ./internal/cmd/factory/...
go test ./internal/firewall/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 4. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 3 is complete. Begin Task 4: Agent container iptables integration."

---

## Task 4: Agent Container iptables Integration

**Creates/modifies:** `internal/bundler/assets/init-firewall.sh` (rewrite), `internal/bundler/assets/entrypoint.sh`, `internal/docker/env.go`, `internal/cmd/container/shared/container.go`
**Depends on:** Task 3

### Implementation Phase

1. **Rewrite init-firewall.sh:**
   - Strip all IP range fetching, domain DNS resolution, ipset logic
   - New script: receive Envoy IP + CoreDNS IP as env vars
   - Set up iptables DNAT: agent user TCP → Envoy IP:10000
   - Set up DNS redirect: agent user UDP/TCP 53 → CoreDNS IP:53
   - Root RETURN rules (uid 0 bypasses DNAT)
   - Keep Docker DNS (127.0.0.11) and loopback exceptions
   - Verification: test blocked domain, test allowed domain

2. **Update entrypoint.sh:**
   - Replace firewall init block to pass Envoy/CoreDNS IPs instead of domain lists
   - Remove JSON file writing for ip-range-sources/domains

3. **Update RuntimeEnvOpts + RuntimeEnv():**
   - Remove `FirewallDomains`, `FirewallIPRangeSources` fields
   - Add `FirewallEnvoyIP`, `FirewallCoreDNSIP` fields
   - Set `--dns` on container creation to point at CoreDNS

4. **Update buildRuntimeEnv() in container.go:**
   - Call `f.Firewall().EnsureRunning()` before building env
   - Get Envoy/CoreDNS IPs from FirewallManager
   - Trigger config regeneration (merge current project's rules into state)

5. **Update CreateContainer() to connect to clawker-net:**
   - Agent container must be on `clawker-net` to reach Envoy/CoreDNS
   - Set `--dns` flag to CoreDNS container IP

6. **Tests:** Unit tests for new init-firewall.sh logic. Integration test for iptables rules.

### Acceptance Criteria

```bash
go build ./internal/docker/...
go build ./internal/cmd/container/...
go test ./internal/docker/... -v
go test ./internal/cmd/container/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 5. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 4 is complete. Begin Task 5: `clawker firewall` command group."

---

## Task 5: `clawker firewall` Command Group

**Creates/modifies:** `internal/cmd/firewall/` (new), `internal/cmd/root/` (register commands)
**Depends on:** Task 3, Task 4

### Implementation Phase

1. **Create `internal/cmd/firewall/` package:**
   - `firewall.go` — parent command `clawker firewall`
   - `status.go` — `clawker firewall status` (show health, active rules, network info)
   - `list.go` — `clawker firewall list [--agent dev]` (list active rules)
   - `add.go` — `clawker firewall add <domain> [--persist] [--proto tls] [--port 443]`
   - `remove.go` — `clawker firewall remove <domain>`
   - `reload.go` — `clawker firewall reload` (force config regen from all configs)
   - `bypass.go` — `clawker firewall bypass <duration> --agent <name> / --container-name <name>`

2. **Register in root command** (follow existing pattern for container/volume/network/image)

3. **Follow existing command patterns:**
   - NewCmd(f, runF) pattern
   - Options struct with Factory function refs
   - FormatFlags for `--json`/`--format` on list/status
   - FilterFlags on list if useful

4. **Tests:** Unit tests for each command with mock FirewallManager

### Acceptance Criteria

```bash
go build ./cmd/clawker/...
go test ./internal/cmd/firewall/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 5 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 6. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 5 is complete. Begin Task 6: MITM cert management + path inspection."

---

## Task 6: MITM Cert Management + Path Inspection

**Creates/modifies:** `internal/firewall/certs.go`, `internal/firewall/envoy.go` (extend), `internal/cmd/firewall/rotate_ca.go`
**Depends on:** Task 2, Task 3

### Implementation Phase

1. **CA management (`certs.go`):**
   - `EnsureCA(dataDir string) (caCert, caKey, error)` — generate self-signed CA if not exists, load if exists
   - `GenerateDomainCert(caCert, caKey, domain string) (cert, key, error)` — sign per-domain cert
   - `RegenerateDomainCerts(rules []EgressRule) error` — generate certs for all rules with `Paths`
   - Store CA in `FirewallDataDir()/ca-cert.pem`, `ca-key.pem`
   - Store domain certs in `FirewallDataDir()/certs/<domain>-cert.pem`, `<domain>-key.pem`

2. **Extend Envoy config generation:**
   - For rules with `Paths`: generate MITM filter chain (TLS termination with domain cert, HTTP route matching, path allow/deny)
   - Mount certs dir into Envoy container

3. **CA injection into agent containers:**
   - Extend `containerfs` or `CreateContainer` flow to copy CA cert into agent container
   - Add to `/usr/local/share/ca-certificates/clawker-firewall-ca.crt` + `update-ca-certificates`

4. **`clawker firewall rotate-ca` command:**
   - Regenerate CA keypair + all domain certs
   - Warn user: running containers need restart to pick up new CA
   - Hot-reload Envoy with new certs

5. **Tests:** CA generation, domain cert signing, Envoy MITM config generation (golden files)

### Acceptance Criteria

```bash
go test ./internal/firewall/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 6 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 7. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 6 is complete. Begin Task 7: Escape hatch (Dante bypass)."

---

## Task 7: Escape Hatch (Dante Bypass)

**Creates/modifies:** `internal/firewall/bypass.go`, `internal/firewall/bypass_test.go`, `internal/cmd/firewall/bypass.go` (extend)
**Depends on:** Task 5

### Implementation Phase

1. **Bypass implementation (`bypass.go`):**
   - `Bypass(ctx, containerID, timeout)`:
     - `docker exec` into target agent container
     - Write Dante config to `/run/firewall-bypass-danted.conf` (loopback only, port 9100)
     - Write proxychains config to `/run/firewall-bypass-proxychains.conf`
     - Add iptables RETURN rule for root (uid 0)
     - Start `danted` as root (backgrounded)
     - Schedule timeout kill (background process)
   - `StopBypass(ctx, containerID)` — kill danted, remove RETURN rule, clean up configs
   - `ListBypasses(ctx)` — list containers with active bypasses

2. **Reference:** openclaw-deploy `/templates/bypass.ts` at `~/Code/openclaw-deploy/`

3. **Extend bypass.go command** (from Task 5) to wire to real implementation

4. **Tests:** Mock-based tests for docker exec calls, timeout behavior

### Acceptance Criteria

```bash
go test ./internal/firewall/... -v
go test ./internal/cmd/firewall/... -v
make test
```

### Wrap Up

1. Update Progress Tracker: Task 7 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 8. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the Envoy+CoreDNS Firewall initiative. Read the Serena memory `initiative_envoy-coredns-firewall` — Task 7 is complete. Begin Task 8: Bundler + init-firewall.sh migration."

---

## Task 8: Bundler + init-firewall.sh Migration

**Creates/modifies:** `internal/bundler/dockerfile.go`, `internal/bundler/assets/init-firewall.sh`, docs, CLAUDE.md files
**Depends on:** Task 4, Task 7

### Implementation Phase

1. **Add dante-server + proxychains4 to base packages:**
   - Add to `basePackagesDebian` in `dockerfile.go`
   - Add Alpine equivalents to `basePackagesAlpine` (dante, proxychains-ng)

2. **Deprecation handling for ip_range_sources:**
   - If `IPRangeSources` is non-empty in config, log a deprecation warning during config load
   - Still parse it (backwards compat), but ignore at runtime

3. **Update documentation:**
   - `CLAUDE.md` — update Key Concepts table (add FirewallManager, remove IP range references), update config schema example, add firewall command group to CLI Commands
   - `internal/firewall/CLAUDE.md` — new package docs
   - `.claude/docs/CLI-VERBS.md` — add `firewall` command group
   - `README.md` — update firewall section

4. **Update `.claude/rules/`** if firewall-related rules exist

5. **Clean up old code:**
   - Remove IP range source fetching logic from init-firewall.sh (already rewritten in Task 4)
   - Remove `FirewallIPRangeSources` from RuntimeEnvOpts if not already done
   - Mark `IPRangeSource` type as deprecated

6. **Integration test:** End-to-end test in `test/` that starts firewall containers, creates an agent container, verifies allowed domain passes and blocked domain fails

### Acceptance Criteria

```bash
go build ./cmd/clawker/...
make test
go test ./test/commands/... -v -timeout 10m  # if integration tests added
bash scripts/check-claude-freshness.sh
```

### Wrap Up

1. Update Progress Tracker: Task 8 -> `complete`
2. Append key learnings
3. Run a single `code-reviewer` subagent to review only this task's changes. Fix any findings before proceeding.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Initiative complete. Inform the user and present final status.

> **Initiative complete.** All 8 tasks done. The Envoy+CoreDNS firewall replaces the IP-based system. Run `clawker firewall status` to verify.
