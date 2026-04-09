# Fix: TCP/SSH Egress Priority with Content-Addressed Ports

## End Goal
Make per-container iptables TCP/SSH DNAT ordering prioritize the local project's rules, with stable Envoy listener port assignments that don't shift when rules are added/removed.

## Background
- Non-TLS TCP port mappings (SSH port 22) use iptables DNAT — first match wins
- When multiple projects define SSH rules for different Git providers (github.com, gitlab.com), only the first iptables rule's Envoy listener receives traffic
- Envoy is a shared global firewall — one instance serves all containers
- Current port assignment is positional: `TCPPortBase + idx` where idx is the rule's position in the store
- Adding/removing rules can shift port assignments, breaking running containers' stale iptables

## Branch State: `fix/project-egress-priority`
The branch has working code but needs the content-addressed ports fix before it's correct. Current state:

### Done
- [x] Package rename: `internal/docker/dockertest` → `internal/docker/mocks`
- [x] `internal/docker/mocks/mock_moby.go` — lightweight moby client mocking (`WithMockClient`, `WithBaseMockClient`) without whail dependency
- [x] `internal/firewall/mocks/stubs.go` — `NewTestManager(t, cfg)` creates real Manager with mock Docker client
- [x] `FormatPortMappings()` exported on Manager — reads store rules, computes TCPMappings, reorders by local project
- [x] `localLayerFirewallRules(cfg)` — extracts SSH/TCP-only rules from most-local config layer via `cfg.ProjectStore().Layers()` raw data (filters out TLS/HTTP to avoid path_rules data loss)
- [x] Reorder logic: computes TCPMappings from store order (matching Envoy config), then partitions by local destination, returns local-first ordering preserving Envoy port assignments
- [x] 9 tests in `priority_test.go` — all passing
- [x] All doc references updated from `dockertest` → `mocks` across ~15 markdown files
- [x] Stale comments fixed in `mock_moby.go` (removed `withDefaults`/`tlsconfig` references from upstream moby)
- [x] Tests use `t.Chdir()` (Go 1.24+) instead of custom helper
- [x] Tests require `testenv.WithProjectManager(nil)` + project registration for walk-up layer discovery
- [x] All 4396 tests pass, pre-commit hooks pass

### TODO
- [ ] **Content-addressed port assignment in `TCPMappings()`** — Replace `TCPPortBase + idx` with `TCPPortBase + FNV32(ruleKey) % portRange`. The `ruleKey` is `dst:proto:port` which is already guaranteed unique by `normalizeAndDedup`. Hash collisions between different strings are effectively impossible with < 50 rules over a 50K port range, but use linear probing as belt-and-suspenders. This makes port assignments stable regardless of rule mutations.
- [ ] **Re-enable iptables on all running containers after Envoy restart** — In `regenerateAndRestart()`, after restarting Envoy, list all running agent containers and call `Enable()` on each. The daemon's `watchContainers` loop currently only counts containers for shutdown — it does NOT re-enable iptables. This is the safety net for any case where port assignments change.
- [ ] **Update `EnvoyPorts.Validate()`** — May need adjustment since TCP ports are no longer sequential from TCPPortBase
- [ ] **Update envoy_test.go** — Tests that assert specific port numbers need updating for hash-based assignment
- [ ] **Update priority_test.go** — Port number assertions change from positional to hash-based
- [ ] Run full test suite, pre-commit hooks
- [ ] Amend or create new commit, force-push branch, update PR

## Key Architecture Decisions
1. **Envoy ports are global and stable** — `TCPMappings()` assigns ports deterministically from rule keys, not position. Same rule always gets same port.
2. **iptables ordering is per-container** — `FormatPortMappings()` reorders the mappings so local project's entries come first, but keeps their Envoy port assignments.
3. **Only SSH/TCP rules extracted from local layer** — TLS rules don't need iptables priority (they share the egress listener with SNI matching).
4. **Walk-up layer discovery needs registered project** — `resolveProjectRoot()` reads the project registry. Without registration, walk-up silently returns no layers.

## Key Files
- `internal/firewall/envoy.go` — `TCPMappings()`, `TCPMapping` struct, `GenerateEnvoyConfig()`
- `internal/firewall/manager.go` — `FormatPortMappings()`, `localLayerFirewallRules()`, `Enable()`, `Bypass()`, `regenerateAndRestart()`
- `internal/firewall/daemon.go` — `watchContainers()` (count only, no re-enable)
- `internal/firewall/rules.go` — `normalizeAndDedup()`, `ruleKey()`
- `internal/firewall/priority_test.go` — 9 tests
- `internal/firewall/mocks/stubs.go` — `NewTestManager(t, cfg)`
- `internal/docker/mocks/mock_moby.go` — moby client mock transport
- `internal/bundler/assets/firewall.sh` — iptables DNAT rules (port-only matching)

## Lessons Learned
- User wants minimal, direct implementations — don't over-abstract with option types or new interfaces when a simple inline change works
- Don't suggest deferring work — if the branch name says "fix X", fix X completely
- Use `internal/docker/mocks` for Docker test fakes, not `pkg/whail/whailtest` directly (import boundary rule)
- Config layer discovery via `WithWalkUp()` requires project registration in the registry — use `testenv.WithProjectManager(nil)` + `pm.Register()` in tests
- The user prefers `mocks` (plural) package naming convention, matching `internal/config/mocks`, `internal/firewall/mocks`

## Claude Code Plan File
`/home/claude/.claude/plans/joyful-wandering-dijkstra.md` (outdated — was for the initial simple approach before content-addressed ports decision)

---
**IMPERATIVE: Always check with the user before proceeding with the next TODO item. If all work is done, ask the user if they want to delete this memory.**
