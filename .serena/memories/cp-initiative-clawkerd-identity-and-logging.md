# Initiative: clawkerd Identity Composite + File Logging

> **Branch:** `feat/clawkerd-init`
> **Status:** Committed on `feat/clawkerd-init` (commits 1b51a2fc + af14d949 + 43b76c97 already on origin). UAT outcome pending. Delete this memory once UAT confirms.

---

## Summary of work landed (uncommitted)

Two logical commits' worth of work, ready to split:

### Commit 1 — composite agent identity (thumbprint, agent_name, project) end-to-end + canonical cert CN
- `internal/consts/consts.go` — added `NamePrefix = "clawker"` next to `Domain`/`LabelDomain`.
- `internal/docker/names.go` — `const NamePrefix = consts.NamePrefix` alias (legacy callers unchanged; ~150 callsites still compile).
- `api/admin/v1/admin.proto` — added `AnnounceAgentRequest.project = 6`, `Agent.project = 6`. Comment semantic for `agent_name` flipped from canonical to short. `make proto` regenerated.
- `api/agent/v1/agent.proto` — added `ConnectRequest.project = 3`. Same semantic flip.
- `internal/auth/agent_cert.go` — `MintAgentCert(caCert, caKey, project, agent string)`. Added pure helper `auth.CanonicalAgentCN(project, agent string) string` (single source of truth: 3-segment for scoped project, 2-segment for empty). CN composed inside MintAgentCert via the helper.
- `internal/auth/agent_cert_test.go` — full test pass updated; added `TestMintAgentCert_EmptyProjectStillMints`.
- `internal/cmd/container/shared/agent_bootstrap.go` — `GenerateAgentBootstrap(caCert, caKey, project, agent, hydraURL, signingKey)`; `AnnounceAgent(ctx, admin, b, project, agent, containerID)` sets `req.AgentName=agent` and `req.Project=project`.
- `internal/cmd/container/shared/container_start.go` — `CommandOpts.Project` field added; `prepareAgentBootstrap` passes `cmdOpts.Project` to both helpers.
- `internal/cmd/container/run/run.go` — `RunOptions.Project string`; populated from `projectName` after `CreateContainer`; threaded into both `CommandOpts` start callsites.
- `internal/controlplane/agentslots/registry.go` — `Slot.Project string` (empty allowed); `slotKey{Thumbprint, AgentName, Project}`; `Reserve` uses 3-tuple; `Consume(thumbprint, agentName, project, verifier)`. Mock regenerated.
- `internal/controlplane/agentslots/registry_test.go` — `mkSlot(project, agent, verifier)` + `mkThumb(project, agent)` helpers; added `TestRegistry_Reserve_SameAgentDifferentProjects` (the headline collision-free test).
- `internal/controlplane/agentslots/subscribe_test.go` — panicOnceRegistry's Consume signature updated.
- `internal/controlplane/agentregistry/registry.go` — `Entry.Project`; `Lookup(thumbprint [32]byte, cn string)` verifies thumbprint AND `auth.CanonicalAgentCN(Project, AgentName) == cn`. Mismatch on either half collapses to `ErrUnknownAgent`. Mock regenerated.
- `internal/controlplane/agentregistry/registry_test.go` — completely rewritten using (project, agent) helpers. Added `TestRegistry_Lookup_CNMismatch` and `TestRegistry_Lookup_EmptyProject`.
- `internal/controlplane/agentregistry/subscribe_test.go` — `panicOnceRegistry.Lookup` signature updated; all callsites pass canonical CN.
- `internal/controlplane/agent/identity_interceptor.go` — extracts cert CN from peer cert and passes it through to `reg.Lookup(thumbprint, peer.CommonName)`. Identical fail-secure semantics + the wrapped `Context()` discipline preserved.
- `internal/controlplane/agent/identity_interceptor_test.go` — `LookupFunc` mock signatures updated; happy-path test now asserts CN forwarded to Lookup.
- `internal/controlplane/agent/handler.go` — Connect: assembles canonical from `(req.Project, req.AgentName)` via `auth.CanonicalAgentCN` and constant-time-compares against peer cert CN BEFORE slot consume; `slots.Consume` 4-arg call; label cross-check NOW verifies BOTH `consts.LabelAgent == slot.AgentName` AND `consts.LabelProject == slot.Project`; `registry.Add` populates `Entry.Project`. Added structured `project=` field on every relevant log line.
- `internal/controlplane/agent/handler_test.go` — fixture takes (project, agent); cert CN built via `auth.CanonicalAgentCN`; inspector now sets BOTH labels. Added `TestConnect_ProjectTamper` (wire-body project mismatch) and `TestConnect_ProjectLabelMismatch` (label-tamper attack on project half only).
- `internal/controlplane/server.go` — `adminServer.AnnounceAgent` stores `req.Project` on the slot at `Reserve`. `ListAgents` populates `Agent.Project` on each result.
- `internal/controlplane/server_test.go` — fixtures use short (project, agent) tuples; added `Project` round-trip assertion in `ListAgents` test and Reserve assertion in `AnnounceAgent_Reserves`.

### Commit 2 — clawkerd file logging via internal/logger (+ wire CLAWKER_PROJECT)
- `cmd/clawkerd/main.go`:
  - Logger init in `main()` BEFORE `run()` (only `os.Stderr` write is the logger-init-failure path).
  - `logger.New(Options{LogsDir: "/var/log", Filename: "clawkerd.log"})` — same lumberjack defaults as host (50MB / 7d / 3 backups, gzip).
  - `agent + project` bound on every log line via `log.With(...)`.
  - Reads `CLAWKER_PROJECT` (consts.EnvProject) — empty allowed (matches 2-segment naming).
  - `ConnectRequest{AgentName, Project, CodeVerifier}`.
  - Levels per spec: ERROR (every failure), INFO (state transitions: boot, token_exchange_attempt, token_acquired, connect_dial, welcome_received, stream_idle, stream_closed_*, shutdown), DEBUG (verifier_deleted, connection_closed, unknown_command_payload). NO WARN.

### Documentation refreshed
- `internal/controlplane/agentslots/CLAUDE.md` — composite key now `(thumbprint, agent_name, project)`; same-agent-name-different-projects isolation called out.
- `internal/controlplane/agent/CLAUDE.md` — Connect cross-checks updated (CN composed from `(project, agent)`; both labels checked); `internal/auth` added to Uses.
- `cmd/clawkerd/CLAUDE.md` — env list updated to `CLAWKER_PROJECT`; ConnectRequest shape; full Logging section (level taxonomy, structured fields, single allowed stderr write).
- `internal/cmd/container/shared/CLAUDE.md` — `GenerateAgentBootstrap` + `AnnounceAgent` signatures + `CommandOpts.Project`.
- `.claude/docs/KEY-CONCEPTS.md` — `agentslots.Registry`, `agentregistry.Registry`, `agent.Handler`, `auth.MintAgentCert` updated; added new `auth.CanonicalAgentCN` row; `cmd/clawkerd` row mentions structured logging.
- `CLAUDE.md` (project root) — added critical warning: NEVER run `go test ./...` inside a clawker container (e2e tears down CP/firewall and blocks the agent's network egress). Use targeted paths or `make test`.

### Hot-fixes already on origin (do not redo)
- `fd475fb1` Hydra audience fix (assertion `aud` = `https://127.0.0.1:4444/oauth2/token`).
- `dbbb7b2c` dropped redundant `CLAWKER_AGENT_NAME`; clawkerd reads `CLAWKER_AGENT`.
- `900d7301` doc cleanup of `security.firewall.enable` (master switch is `firewall.enable` in settings.yaml).

---

## Test status

All targeted package tests pass. Verified via:

```bash
go test -count=1 \
  ./internal/auth/... \
  ./internal/consts/... \
  ./internal/cmd/container/... \
  ./internal/controlplane/agent/... \
  ./internal/controlplane/agentregistry/... \
  ./internal/controlplane/agentslots/... \
  ./internal/controlplane/... \
  ./cmd/clawkerd/...
```

Mocks regenerated via `go generate ./...` in each affected package.

**WARNING re testing inside the clawker container:** never run `go test ./...` — it pulls in `test/e2e` which tears down the host CP/firewall, killing the agent's egress. This caveat is now in the project root `CLAUDE.md`. Use targeted paths or `make test`.

---

## UAT commands the user should run

```bash
# 1. Build + ship binaries
make restart                # rebuild CLI (depends on clawkerd-binary)
clawker build               # rebuild project image with new clawkerd

# 2. Smoke test — happy path
clawker run -it --rm --agent test @ --dangerously-skip-permissions

# In another terminal, while the agent is running:

# 3. Verify clawkerd is running and writing to its log file
clawker exec -u root --agent test pgrep -af clawkerd
clawker exec -u root --agent test ls -la /var/log/clawkerd.log
clawker exec -u root --agent test tail -20 /var/log/clawkerd.log

# 4. Verify cert CN is canonical
clawker exec -u root --agent test \
  openssl x509 -in /run/clawker/bootstrap/cert.pem -noout -subject

# Expected: subject=CN=clawker.<project>.test (canonical form)

# 5. Verify the assertion's audience claim
clawker exec -u root --agent test cat /run/clawker/bootstrap/assertion.jwt | \
  cut -d. -f2 | base64 -d 2>/dev/null | grep -o '"aud":"[^"]*"'

# Expected: "aud":"https://127.0.0.1:4444/oauth2/token"

# 6. Verify ConnectRequest carried project (CP-side log)
docker logs clawker-controlplane 2>&1 | \
  grep -E "agent connect: registered|project=" | head

# 7. Verify ListAgents shows project
clawker controlplane agents --json | jq

# Expected: each entry has agent_name, container_id, cert_thumbprint,
# project (NEW), registered_at_unix, last_seen_unix.

# 8. Verifier deleted (single-use indicator)
clawker exec -u root --agent test ls /run/clawker/bootstrap/
# Expected: cert.pem, key.pem, ca.pem, assertion.jwt — NO verifier file.

# 9. Two projects with same agent name don't collide
# (run a second project with same agent name, both should register cleanly)

# 10. Eviction on container exit
clawker container stop test
sleep 2
clawker controlplane agents
# Expected: test agent gone from registry.
```

---

## Deferred work — surface to user when this initiative ships

1. **clawkerd → container death linkage.** clawkerd should be the entrypoint such that its death = container death. Today the entrypoint launches it in background and doesn't track. User said "deal with that later" during the original UAT. Track as follow-up.
2. **CP endpoint disclosure to unprivileged user.** `CLAWKER_CP_AGENT_ADDR`, `CLAWKER_CP_HYDRA_URL`, etc. visible via `env` to the `claude` user. Bootstrap material is root-only but the addresses leak. Consider moving to bootstrap-dir file (root:0400) instead of env. Track as follow-up.
3. **E2E adversarial harness update.** Already tracked in `cp-initiative-e2e-adversarial-harness` memory. Tests need updating for the new composite identity + project field.

---

## Memory deletion

Once the user UATs and confirms, ask whether to delete this memory.
