# CoreDNS dns-to-bpf Plugin Initiative

**Branch:** `fix/project-egress-priority`
**Parent memory:** `ebpf_firewall_docker_desktop_uat`
**PRD Reference:** `.claude/docs/EBPF-DESIGN.md` § CoreDNS Plugin: dns-to-bpf

---

## Progress Tracker

| Task | Status | Agent |
|------|--------|-------|
| Task 1: Build the CoreDNS dns-to-bpf plugin Go package | `complete` | main |
| Task 2: Build custom CoreDNS image with embedded plugin | `complete` | main |
| Task 3: Integrate custom CoreDNS into firewall manager | `pending` | — |
| Task 4: End-to-end UAT — SSH routing via dns_cache | `pending` | — |

## Key Learnings

(Agents append here as they complete tasks)

- Task 2: Custom CoreDNS builds work by importing `coremain` + blank-importing the plugin package for its `init()` side effect, then prepending the plugin name to `dnsserver.Directives`. The `plugin.cfg`/`go generate` approach only works inside the CoreDNS repo. The embedded binary approach was unified into a shared `embeddedImageSpec` struct + `ensureEmbeddedImage` method, eliminating duplication with the eBPF pattern. `go.uber.org/automaxprocs` is a transitive dep from `coremain` — needs `go mod tidy` after adding the import. The `containerd/errdefs.IsNotFound` check is important for `ImageInspect` — without it, any Docker API error (daemon down, context cancelled) triggers an unnecessary rebuild attempt.
- Task 1: CoreDNS v1.14.2 uses Go 1.25, compatible with our module. `coredns/caddy` is the plugin registration framework (not the web server Caddy). The `nonwriter.New(w)` pattern captures responses cleanly. `test.NextHandler` takes `(rcode, error)` not a msg — need a custom `cannedHandler` for tests with canned DNS responses. The `dnsserver.GetConfig(c).Zone` gives us the Corefile zone name in setup, which is critical for wildcard domain hashing. CoreDNS pulls in a large dependency tree (k8s client-go, etc.) but it's isolated to binaries that import the plugin.

---

## Context Window Management

**After completing each task, you MUST stop working immediately.** Do not begin the next task. Instead:
1. Run acceptance criteria for the completed task
2. Update the Progress Tracker in this memory
3. Append any key learnings to the Key Learnings section
4. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings
5. Commit all changes from this task with a descriptive commit message
6. Present the handoff prompt from the task's Wrap Up section to the user
7. Wait for the user to start a new conversation with the handoff prompt

This ensures each task gets a fresh context window. Each task is designed to be self-contained — the handoff prompt provides all context the next agent needs.

---

## Context for All Agents

### Background

The eBPF firewall routes per-container egress traffic through Envoy (TCP/TLS) and CoreDNS (DNS). The BPF `connect4` program uses a `dns_cache` BPF map to resolve destination IPs back to domain names for per-domain TCP routing (e.g., SSH to github.com:22 vs gitlab.com:22 → different Envoy TCP listeners).

**The problem:** The `dns_cache` map is currently empty at runtime. A stopgap seeding approach in `ebpf-manager enable` resolves domains at attach time, but DNS round-robin means the seeded IP often differs from the IP the container actually connects to. SSH routing fails intermittently.

**The fix:** A CoreDNS plugin that intercepts every DNS response and writes the resolved IP → domain_hash mapping to the pinned `dns_cache` BPF map in real time. This is the Cilium architectural pattern — userspace DNS parsing → BPF maps.

### Key Files

| File | Purpose |
|------|---------|
| `internal/ebpf/bpf/common.h` | BPF map definitions — `dns_cache` map: key=`__u32` (IP, network byte order), value=`struct dns_entry {domain_hash, expire_ts}` |
| `internal/ebpf/types.go` | Go mirror types — `IPToUint32` (uses `binary.NativeEndian`), `DomainHash` (FNV-1a) |
| `internal/ebpf/manager.go` | `UpdateDNSCache(ip, domainHash, ttl)` — writes to the pinned map |
| `internal/firewall/coredns.go` | `GenerateCorefile()` — generates CoreDNS Corefile from egress rules |
| `internal/firewall/manager.go` | `corednsContainerConfig()` — CoreDNS container spec (stock image, Corefile mount, health port) |
| `internal/firewall/manager.go` | `ensureEbpfImage()` / `ebpfBuildContext()` — pattern for building Docker images from embedded binaries |
| `internal/firewall/ebpf_embed.go` | `go:embed` pattern for embedded binary |
| `Dockerfile.ebpf` | Multi-stage build pattern for firewall sidecar containers |
| `Makefile` | `ebpf-binary` target — cross-compile pattern for embedded Linux binaries |

### Design Patterns

- **Embedded binary + on-demand Docker image build**: The ebpf-manager binary is cross-compiled for Linux, embedded in the clawker binary via `go:embed`, and built into a Docker image on first use via `ebpfBuildContext()` (tar with inline Dockerfile + binary → `ImageBuild` API). The CoreDNS plugin should follow this exact pattern.
- **BPF map access from Go**: `cilium/ebpf` library. Open pinned map at `/sys/fs/bpf/clawker/dns_cache`, write entries with `map.Update(key, value, ebpf.UpdateAny)`.
- **Network byte order**: IPs in BPF maps use `binary.NativeEndian.Uint32(ip.To4())` — matches `ctx->user_ip4` in BPF programs. This was the root cause of the previous blocker (was using `BigEndian`).
- **Domain hashing**: `hash/fnv` FNV-1a 32-bit hash of lowercase domain name. Must match between firewall manager (`DomainHash()` in `manager.go`) and CoreDNS plugin. Import from `internal/ebpf/types.go` is not possible (CoreDNS plugin is a standalone binary), so the CoreDNS plugin imports `internal/ebpf` directly (same Go module) for `IPToUint32` and `DomainHash` — no duplication needed.
- **CoreDNS plugin interface**: `plugin.Handler` with `ServeDNS(ctx, w, r)`. Use `nonwriter.New(w)` to intercept responses after the next plugin resolves. Extract A records from the response, write to dns_cache.
- **Custom CoreDNS build**: CoreDNS compiles plugins via `plugin.cfg`. Create a custom `main.go` that imports stock CoreDNS + our plugin. Build as a static binary.

### Rules

- Read `CLAUDE.md`, relevant `.claude/rules/` files, and package `CLAUDE.md` before starting
- Use Serena tools for code exploration — read symbol bodies only when needed
- All new code must compile and tests must pass
- Follow existing test patterns in the package
- The CoreDNS plugin is in the same Go module — it CAN import `internal/ebpf` for `IPToUint32`, `DomainHash`, and BPF map types. No duplication.
- The `dns_cache` BPF map is pinned at `/sys/fs/bpf/clawker/dns_cache`. The CoreDNS container needs a bind mount of `/sys/fs/bpf` and `CAP_BPF` capability.
- Wildcard domains (`.example.com`): when CoreDNS resolves `sub.example.com` and it was forwarded by the `.example.com` zone, the dns_cache entry should use the WILDCARD domain's hash, not the subdomain's hash.

---

## Task 1: Build the CoreDNS dns-to-bpf plugin Go package

**Creates/modifies:** `internal/dnsbpf/` (NEW directory)
**Depends on:** nothing

### Implementation Phase

1. Create `internal/dnsbpf/` as a Go package within the clawker module
2. Implement the CoreDNS plugin interface:
   - `setup.go` — `init()` registers the plugin via `caddy.RegisterPlugin`. Parse Corefile config block: `dnsbpf { pin_path /sys/fs/bpf/clawker }`. Open the pinned `dns_cache` map.
   - `handler.go` — `ServeDNS` method. Use `nonwriter.New(w)` to call `plugin.NextOrFailure`, then inspect `nw.Msg.Answer` for `dns.A` records. For each A record: compute `IPToUint32(ip)` and `DomainHash(qname)`, call `UpdateDNSCache`. Then `w.WriteMsg(nw.Msg)`.
   - `bpfmap.go` — BPF map writer. Uses `cilium/ebpf` to open the pinned map and write entries. Struct layout must match `common.h`'s `dns_entry`. Handles map-not-found gracefully (logs warning, doesn't crash CoreDNS).
   - No hash/IP duplication needed — import `internal/ebpf` directly for `IPToUint32` and `DomainHash`.
3. Implement wildcard domain support: the Corefile zone name tells us which domain forwarded the query. If the zone is `.example.com` (wildcard), use `DomainHash(".example.com")` not `DomainHash("sub.example.com")`.
4. Unit tests:
   - `hash_test.go` — verify hash values match `internal/ebpf/types.go` output for known domains
   - `handler_test.go` — test with mock dns.ResponseWriter and verify dns_cache writes
   - `setup_test.go` — test Corefile parsing

### Acceptance Criteria

```bash
go test ./internal/dnsbpf/... -v
go vet ./internal/dnsbpf/...
```

### Wrap Up

1. Update Progress Tracker: Task 1 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 2. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the CoreDNS dns-to-bpf plugin initiative. Read the Serena memory `initiative_coredns-dnsbpf-plugin` — Task 1 is complete. Begin Task 2: Build custom CoreDNS image with embedded plugin."

---

## Task 2: Build custom CoreDNS image with embedded plugin

**Creates/modifies:** `cmd/coredns-clawker/main.go` (NEW), `Dockerfile.coredns` (NEW), `internal/firewall/coredns_embed.go` (NEW), `Makefile`
**Depends on:** Task 1

### Implementation Phase

1. Create `cmd/coredns-clawker/main.go` — custom CoreDNS entrypoint that imports stock CoreDNS + our `dnsbpf` plugin:
   ```go
   package main
   import (
       _ "internal/dnsbpf" // our plugin
       "github.com/coredns/coredns/core/dnsserver"
       "github.com/coredns/coredns/coremain"
   )
   // Register our plugin in the directive list
   func init() {
       dnsserver.Directives = append([]string{"dnsbpf"}, dnsserver.Directives...)
   }
   func main() { coremain.Run() }
   ```
2. Create `Dockerfile.coredns` — multi-stage build following `Dockerfile.ebpf` pattern:
   - Builder: `golang:1.25-bookworm`, copy go.mod/go.sum + relevant packages, build static binary
   - Runtime: `alpine:3.21` (not distroless — need debugging tools)
   - COPY the binary, set ENTRYPOINT to the custom CoreDNS
3. Add `internal/firewall/coredns_embed.go` — `go:embed assets/coredns-clawker` (same pattern as `ebpf_embed.go`)
4. Add Makefile targets:
   - `coredns-binary`: cross-compile `cmd/coredns-clawker` for linux/$(GOARCH)
   - Wire as dependency of `clawker-build` (like `ebpf-binary`)
5. Add `coredns_embed.go` binary to `.gitignore`
6. Add `ensureCorednsImage()` and `coredns_build_context()` to firewall manager (following `ensureEbpfImage` pattern). Use a local image tag `clawker-coredns:latest` instead of the stock `coredns/coredns:1.14.2`.

### Acceptance Criteria

```bash
# Binary compiles for Linux
GOOS=linux CGO_ENABLED=0 go build -o /tmp/coredns-clawker ./cmd/coredns-clawker
# Full clawker builds (embeds the binary)
make clawker
# Image builds via Docker
docker build -f Dockerfile.coredns -t clawker-coredns:test .
# CoreDNS starts and shows our plugin in the plugin list
docker run --rm clawker-coredns:test -plugins | grep dnsbpf
```

### Wrap Up

1. Update Progress Tracker: Task 2 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 3. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the CoreDNS dns-to-bpf plugin initiative. Read the Serena memory `initiative_coredns-dnsbpf-plugin` — Task 2 is complete. Begin Task 3: Integrate custom CoreDNS into firewall manager."

---

## Task 3: Integrate custom CoreDNS into firewall manager

**Creates/modifies:** `internal/firewall/manager.go`, `internal/firewall/coredns.go`
**Depends on:** Task 2

### Implementation Phase

1. Update `corednsContainerConfig()`:
   - Replace stock `corednsImage` with `clawkerCorednsImageTag` (local build, like ebpf)
   - Add `/sys/fs/bpf` bind mount (read-write, so plugin can write to dns_cache)
   - Add `CAP_BPF` capability (required for BPF map access)
   - Keep existing Corefile and health port mounts
2. Update `EnsureRunning()`:
   - Add `ensureCorednsImage(ctx)` step (before `ensureContainer(corednsContainer)`)
   - The ebpf init must happen BEFORE CoreDNS starts (so the pinned dns_cache map exists when the plugin opens it)
   - Reorder: ensure network → ensure configs → ensure ebpf image → ensure ebpf container → ebpf init → ensure coredns image → ensure coredns container → ensure envoy container → wait healthy
3. Update `GenerateCorefile()`:
   - Add `dnsbpf` directive to each per-domain forward zone (after `log`, before `forward`)
   - Add `dnsbpf` to internal host zones too (so host.docker.internal resolution populates dns_cache)
   - The catch-all zone does NOT get `dnsbpf` (NXDOMAIN responses have no A records)
4. Update `Stop()` ordering: stop agent-facing containers first (so dns_cache writes stop), then ebpf last
5. Remove the dns_cache seed code from `internal/ebpf/cmd/main.go` `runEnable()` (the plugin replaces it)
6. Remove the `Domain` field from `ebpfRoute` / `Route` structs (no longer needed for seeding)

### Acceptance Criteria

```bash
make test  # unit tests pass
make clawker  # full build succeeds
# Manual: start a container, check dns_cache is populated after DNS queries
```

### Wrap Up

1. Update Progress Tracker: Task 3 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Do not proceed to Task 4. Inform the user you are done and present this handoff prompt:

> **Next agent prompt:** "Continue the CoreDNS dns-to-bpf plugin initiative. Read the Serena memory `initiative_coredns-dnsbpf-plugin` — Task 3 is complete. Begin Task 4: End-to-end UAT — SSH routing via dns_cache."

---

## Task 4: End-to-end UAT — SSH routing via dns_cache

**Creates/modifies:** test results, memory updates, documentation
**Depends on:** Task 3

### Implementation Phase

1. `make restart` — full clean rebuild
2. Start a clawker container: `./bin/clawker run -it --rm --agent ebpf @ --dangerously-skip-permissions`
3. Test as `claude` user inside the container:
   - `nslookup github.com` → verify CoreDNS resolves it
   - Check dns_cache: `docker exec clawker-ebpf bpftool map dump pinned /sys/fs/bpf/clawker/dns_cache` → verify github.com's IP is in the map with correct domain_hash
   - `ssh -T git@github.com` → should authenticate (not connection reset)
   - `ssh -T git@gitlab.com` → should authenticate
   - `curl -s https://api.anthropic.com/` → should work (TLS through Envoy)
   - `curl -s http://host.docker.internal:18374/health` → host proxy should work
   - Test bypass: `clawker firewall bypass 30s --agent ebpf` → unrestricted for 30s
   - Test DNS-round-robin resilience: run `ssh -T git@github.com` multiple times → should always work even when IP changes
4. Verify wildcard domains work if any are configured
5. Update Serena memory `ebpf_firewall_docker_desktop_uat` with final status
6. Update `internal/firewall/CLAUDE.md` with CoreDNS plugin documentation

### Acceptance Criteria

```bash
# All manual tests pass
# dns_cache populated dynamically (not just seeded)
# SSH works reliably across DNS round-robin
# No firewall timeout or daemon race issues
```

### Wrap Up

1. Update Progress Tracker: Task 4 -> `complete`
2. Append key learnings
3. Run `code-reviewer`, `silent-failure-hunter`, `test-hunter`, `code-simplifier`, `comment-analyzer`,`type-design-analyzer` subagents to review this task's changes, then fix any and all findings.
4. Commit all changes from this task with a descriptive commit message.
5. **STOP.** Inform the user: "CoreDNS dns-to-bpf plugin initiative is complete. All 4 tasks done. SSH routing works reliably via dynamic dns_cache population."
