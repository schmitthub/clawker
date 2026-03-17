# Brainstorm: The Control Plane and clawkerd

> **Status:** Active
> **Created:** 2026-02-16
> **Last Updated:** 2026-02-16 12:00

## Problem / Topic
The POC (test/controlplane/) validates the two-gRPC-server pattern. The plan is to iteratively evolve this POC toward production — each iteration adds one real concern and validates it via the integration test. The master document is `clawkerd-container-control-plane` memory; this brainstorm is the working scratchpad for the current session.


## POC Results (from test/controlplane/)

### What was built
- **Proto schema** (`internal/clawkerd/protocol/v1/agent.proto`): AgentReportingService (Register), AgentCommandService (RunInit)
- **clawkerd binary** (`clawkerd/main.go`): Container-side agent — starts gRPC server, registers with CP, handles RunInit (executes bash commands, streams progress, writes ready file)
- **Control plane server** (`internal/controlplane/`): server.go + registry.go — accepts Register, resolves container IP via Docker inspect, connects back to clawkerd's gRPC server, calls RunInit, consumes progress stream
- **Test Dockerfile** (`test/controlplane/testdata/Dockerfile`): Two-stage build (Go builder → Alpine), installs su-exec + tini, root entrypoint with gosu/su-exec drop
- **Test entrypoint** (`test/controlplane/testdata/entrypoint.sh`): Starts clawkerd in background, drops to claude user via su-exec
- **Integration test** (`test/controlplane/controlplane_test.go`): Full end-to-end — builds image, starts CP in-process, runs container, verifies registration, init progress, privilege separation
- **Harness extensions**: WithNetwork(), WithPortBinding(), network join on start
- **Makefile**: `make test-controlplane`, `make proto` (buf generate), excluded from unit tests

### What was validated
1. Two-gRPC-server pattern works across Docker network (host → container via port mapping)
2. Address discovery works (clawkerd registers with listen port, CP resolves via Docker inspect + port binding)
3. Server-streaming RunInit progress flows correctly (STARTED → COMPLETED per step → READY)
4. Root entrypoint + su-exec privilege drop works (clawkerd runs as root UID 0, main process as claude UID 1001)
5. tini as PID 1 (via Dockerfile ENTRYPOINT, not HostConfig.Init in POC) manages both processes
6. Ready file signal mechanism works (/var/run/clawker/ready)
7. Init step command execution with stdout/stderr capture works

### What was NOT validated / deferred
- HostConfig.Init (POC uses explicit tini in Dockerfile ENTRYPOINT instead)
- Graceful degradation (clawkerd falling back to baked-in defaults when CP unreachable)
- Reconnection logic (gRPC stream drops)
- Docker Events integration
- Watermill message queue
- SchedulerService (CLI → CP resource management)
- Entrypoint waiting on clawkerd ready signal (POC entrypoint is fire-and-forget)

## Open Items / Questions
- How to handle the entrypoint wait? Current POC starts clawkerd & then immediately drops privileges. Should it wait for ready file before exec su-exec?
- Should we move to HostConfig.Init=true (Docker injects tini) or keep explicit tini in entrypoint? POC uses explicit.
- What's the plan for the `internal/controlplane/` vs `internal/clawkerd/` package split? Currently CP is in `controlplane/`, agent protocol in `clawkerd/protocol/`. Is this the right layout long-term?
- The test uses `host.docker.internal:host-gateway` for container→host communication. In production, the CP listens on clawker-net. How does address resolution change?
- Container ID mismatch: Docker hostname is 12-char truncated ID, but Docker API uses full ID. The test handles both — should clawkerd send full ID (read from /proc or cgroup)?

## Decisions Made
- Two-gRPC-server pattern: VALIDATED by POC. CP and clawkerd each run their own gRPC server.
- su-exec over gosu: POC chose su-exec (Alpine native, ~10KB). Works.
- Root entrypoint + privilege drop: VALIDATED. Clean separation.
- Ready file at /var/run/clawker/ready: Works as signal mechanism.
- buf for protobuf generation: Configured (buf.yaml + buf.gen.yaml), `make proto` target added.
- Test harness extended with WithNetwork() and WithPortBinding() for control plane tests.

## Conclusions / Insights
- The two-server gRPC pattern is clean and works well across Docker networking boundaries.
- Port binding (host port mapping) is needed on macOS/Docker Desktop where container IPs aren't routable from host. The CP's resolveAgentAddress() handles both port mapping and direct IP fallback.
- The POC entrypoint is minimal (3 lines) — the complexity lives in Go, not bash. This validates the "init logic in Go, not bash" principle.
- clawkerd's RunInit handles step failures gracefully (logs, sends FAILED event, continues to next step).

## Gotchas / Risks
- Container ID truncation: Docker sets hostname to 12-char prefix. Need consistent ID handling between clawkerd and CP.
- The CP currently does Docker inspect in the Register RPC handler — this is synchronous and could slow registration if Docker is slow.
- The `go s.runInitOnAgent()` goroutine in Register has no structured lifecycle management yet (no errgroup, no cancellation tracking).
- No auth on the clawkerd→CP gRPC connection beyond the shared secret in Register. The callback connection (CP→clawkerd) has no auth at all.

## Unknowns
- Production entrypoint behavior: should it block on clawkerd ready signal or proceed immediately?
- How will the existing hostproxy retirement timeline work? The memory says "replaced" but current codebase still has full hostproxy.
- What's the migration path from current `CreateContainer()` orchestration to CP-mediated creation?
- How does the init spec get populated in production? Currently hardcoded in test.

## Next Steps
- Decide which production concern to tackle next in the POC iteration
- Candidates: entrypoint wait, HostConfig.Init, init spec from clawker.yaml, Docker Events, graceful degradation
- Each iteration: add the concern to test/controlplane/, validate, update master memory
