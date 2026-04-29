# PR Review Fixes — `feat/clawkerd-commands`

Master index for the 30 findings from the PR review of branch `feat/clawkerd-commands` (review session: 2026-04-28). Each fix is a focused work unit defined in a sibling file in this directory. Agents claim tasks one at a time, complete them, then return here for the next.

## How to use this directory

1. **Read this file in full first.** Architecture context + claim protocol below.
2. **Pick a task** from the status table that is `pending` and whose `blocked_by` are all `complete`.
3. **Claim it** by editing this file: change the task's status to `in_progress` and add your agent name in the `claimed_by` column.
4. **Open the task's file** (e.g. `01-agentregistry-foundation.md`) and execute the plan in it. Each task file is self-contained — it lists affected files, decision rationale, ordered steps, test requirements, verification commands, and known gotchas.
5. **Mark complete** by editing this file: status → `complete`. Append a one-line summary in the task file's `Resolution` section with the commit SHA.
6. **Do not silently expand scope.** If the task file's plan is wrong, document why in the task file's `Notes` section and stop — do not adjacently refactor.

## Claim protocol

- One agent owns one task at a time. Two-task ownership is allowed only when the second task is explicitly listed as `parallel-safe` in the table.
- A task in `in_progress` for >2h with no commit progress is considered stale and may be re-claimed.
- All work happens on branch `feat/clawkerd-commands` (or a child branch fanning back into it). Do not target `main` directly.
- After completing a task, run `make test` (unit suite). If tests fail in unrelated packages, that's a pre-existing condition — note in the task's `Resolution` and proceed.
- **Never run `go test ./...` or `test/e2e` from inside a clawker container** — see project CLAUDE.md "CRITICAL — IF YOU ARE RUNNING IN A CLAWKER CONTAINER" warning.

## Architecture context (must internalise before claiming)

Three load-bearing concepts shape every fix:

1. **CP ≠ firewall.** Control Plane is unconditional infrastructure (auth, AdminService, AgentService, agent slot/registry bookkeeping, mTLS, OAuth2). Firewall is one optional subsystem CP manages. Do not gate non-firewall behavior on `firewall.enable`. See project root `CLAUDE.md` `<critical_clarification>` block.

2. **Identity is `(thumbprint, container_id)` — both UNIQUE in sqlite.** `agent_name` and `project` are CROSS-CHECK fields stored alongside, NOT identity. The MustProjectSlug/MustAgentName panic vector at `agentregistry/sqlite.go:312` exists because the code reconstructs the canonical CN from agent_name+project at read time instead of either (a) storing it pre-computed or (b) treating it as untrusted data with err-returning constructors. Decision: store `canonical_cn` as a sqlite column (Task #1). See `internal/controlplane/agent/CLAUDE.md` for the Connect handler's identity binding chain.

3. **Registry write order matters for orphan-row prevention.** The CLI is the sole writer of agentregistry. Registry row currently signifies "container has been bootstrapped"; decision is to make it signify "container fully ready" by moving `reg.Add` to AFTER `InjectPostInitScript` (Task #2). See `internal/cmd/container/shared/agent_bootstrap.go` comment block at L220-234.

## Status table

| # | Task | Status | Claimed by | Blocked by | Parallel-safe |
|---|------|--------|-----------|-----------|:-------------:|
| 01 | [agentregistry foundation refactor](01-agentregistry-foundation.md) | complete | claude-opus-4.7 | — | no |
| 02 | [container_create: move reg.Add to last step](02-bootstrap-order.md) | complete | claude-opus-4.7 | 01 | no |
| 03 | [cmd/controlplane/agents: remove DBPath field](03-agents-cmd-dbpath.md) | complete | claude-opus-4.7 | 01 | no |
| 04 | [informer→Overseer: typed event bus refactor](04-informer-per-kind-split.md) | complete | claude-opus-4.7 | — | no |
| 05 | [agentdial refactor + tests](05-agentdial-refactor.md) | complete | claude-opus-4.7 | 01, 04 | no |
| 06 | [agentregistry/subscribe: ring buffer](06-subscribe-ringbuffer.md) | complete | claude-opus-4.7 | 04 | yes |
| 07 | [cmd/clawkerd/session: audit + race + atomic + tests](07-clawkerd-session-fixes.md) | complete | claude-opus-4.7 | — | yes |
| 08 | [cmd/clawkerd/listener: EKU + audit + tests](08-clawkerd-listener-fixes.md) | complete | claude-opus-4.7 | — | yes |
| 09 | [agentslots: sweep log + janitor race test](09-agentslots-sweep-tests.md) | complete | claude-opus-4.7 | 04 | yes |
| 10 | [server_test: nil-agents → empty ListAgents](10-server-test-nil-agents.md) | complete | claude-opus-4.7 | — | yes |
| 11 | [misc close-error swallows](11-misc-close-swallows.md) | complete | claude-opus-4.7 | 03 | yes |
| 12 | [docs: rewrite agentslots/CLAUDE.md](12-doc-agentslots-rewrite.md) | complete | claude-opus-4.7 | — | yes |

`parallel-safe = yes` means the task touches files that no in-progress task is mutating. Always re-check at claim time.

## Dependency graph

```
01 (agentregistry foundation) ─┬─→ 02 (bootstrap order)
                                ├─→ 03 (agents DBPath) ─→ 11 (close swallows)
                                └─→ 05 (agentdial)
04 (informer split) ────────────┬─→ 05 (agentdial)
                                 ├─→ 06 (subscribe ringbuffer)
                                 └─→ 09 (agentslots sweep)
07 (clawkerd session) ── independent
08 (clawkerd listener) ── independent
10 (server_test nil-agents) ── independent
12 (agentslots doc) ── independent (touches docs only; landing alongside 09 is fine)
```

`07`, `08`, `10`, `12` are independent of all others. `06` is small and only blocked by `04`.

## Findings → task mapping

For traceability back to the review session. Original finding codes (C# / S# / T# / Y#) map as follows:

| Finding | Task | Notes |
|---------|------|-------|
| C1 | RETRACTED | Preserved scaffolding by design — see review session |
| C2 (Lookup panic) | 01 | Subsumed by canonical_cn column |
| C3 (RCE audit log) | 07 | Full-argv at every Started/Done |
| C4 (stageErrs race) | 07 | Per-goroutine local + chan |
| C5 (EKU comment+assertion) | 08 | Cross-ref + ServerAuth EKU assertion |
| C6 (Factory noun for DBPath) | 03 | Remove field; use consts directly |
| C7 (bootstrap orphan row) | 02 | Move reg.Add to last step |
| S1 (dial err wrap misleading) | 05 | Split into dial_target + stream_err |
| S2 (Recv err shutdown swallow) | 07 | Info log on ctx-cancel teardown |
| S3 (FD leak ceiling) | 05 | Track close-error count + bail |
| S4 (Evict invariant violation) | 01 | Drop in-memory cache entirely |
| S5 (RowsAffected swallowed) | 01 | N/A after cache drop; quick-win otherwise |
| S6 (schema apply non-atomic) | 01 | BEGIN/COMMIT TX wrap |
| S7 (malformed row drift) | 01 | Eliminated by no-cache (no reload path) |
| S8 (reap aborts on docker err) | 01 | Bounded retry inside Reap |
| S9 (publish drops err on shutdown) | 05 | ctx-aware skip helper |
| S10 (panicTimes unbounded) | 05 + 06 | Ring buffer in both subscribers |
| S11 (sweep log lacks context) | 09 | Add agent + project fields |
| S12 (no Session entry audit) | 08 | Info log at runSession entry |
| S13 (stdinW close swallow) | 07 | Helper logs once per goroutine + Warn |
| S14 (routeSignal false positives) | 07 | Filter os.ErrProcessDone |
| S15 (Consume err level) | 05 | Promote non-ErrSlotInvalid to Error |
| S16 (closer.Close swallow) | 11 | Log via opts.Logger() |
| S17 (Reap evict err return) | 01 | Subsumed by interface change |
| S18 (log.Close swallow) | 11 | Print to stderr (logger is closing) |
| T1 (session.go zero tests) | 07 | + atomicBool→sync/atomic |
| T2 (agentdial zero tests) | 05 | Full coverage 4 areas |
| T3 (sqlite zero tests) | 01 | Full sqlite_test.go |
| T4 (listener zero tests) | 08 | In-process gRPC + bad-CN |
| T5 (concurrent test doesn't cover sqlite) | 01 | Subsumed by sqlite_test.go's `TestSQLiteRegistry_ConcurrentWriters` |
| T6 (janitor race test) | 09 | Single-shot mid-Consume hook |
| T7 (nil-agents test) | 10 | TestListAgents_NilRegistry_ReturnsEmpty |
| Y1+Y2 (identity stringly-typed) | 01 | Store canonical_cn as column |
| Y3 (8-tuple return) | 05 | Result struct + outcome enum |
| Y4 (command_id no invariant) | 07 | Validate non-empty at unmarshal |
| Y5 (Lifecycle comingled) | 04 | Per-kind event queues |
| Doc (agentslots CLAUDE.md) | 12 | Container_id-keyed model |

## Decisions retained from review session

These are settled — do not re-litigate without explicit approval from the user:

- **S4: Drop in-memory cache** in agentregistry/sqlite.go. Rationale: sqlite is local fs + WAL, per-RPC query is cheap, single source of truth = disk, no cache invariant to violate. Benchmark before merging if concerned about hot path.
- **C7: Move `reg.Add` to last step** of container creation (after InjectPostInitScript). Rationale: registry row should mean "fully ready" not "bootstrapped".
- **C6: Remove DBPath field**, do not introduce Factory noun. Rationale: `consts.ControlPlaneDBPath()` reads `CLAWKER_DATA_DIR` env at call time; testenv.New(t) sets that env via t.Setenv automatically.
- **Y1+Y2: Store `canonical_cn` as sqlite column.** Rationale: identity is thumbprint + container_id; agent_name + project are cross-check data. CN is what Lookup actually compares against — store it pre-computed instead of reconstructing via panic-prone Must* constructors.
- **Y5: Per-kind event queues**, not shared ResourceUpdate.Lifecycle field. User quote: "you never comingle events from different producers that is absurd and lazy."
- **Y3: Result struct + outcome enum** for establishWithRetry. Single switch at caller, no string sentinels.
- **C1: RETRACTED.** The dead-code Register handler is intentional preserved scaffolding for future PKCE-bound RPCs. Do not delete.

## Provenance

- Source: review session 2026-04-28 (caveman-mode, 30 findings, 4 specialist agents).
- Reviewer agents (one-shot): code-reviewer, silent-failure-hunter, pr-test-analyzer, type-design-analyzer.
- Decision-maker: human user (Andrew Schmitt). Every "Decision" line in a task file traces to a direct user choice in that session.

When updating: keep the status table the source of truth. Per-task files describe HOW; this file describes WHAT and WHEN.
