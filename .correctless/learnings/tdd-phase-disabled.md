# Learning: `/ctdd` phase is disabled on clawker

**Date recorded**: 2026-04-14
**Branch**: feat/firewall-cp-migration (B2)
**Triggered by**: Branch 1 (feat/control-plane, merged as PR #250 on 2026-04-13)

## What happened on Branch 1

Branch 1 ran the Correctless TDD phase with a subagent. The subagent produced shallow unit tests that asserted trivial properties — e.g., "mock returns what I configured it to return." No integration coverage, no real gRPC exercise, no verification that the eBPF manager actually touched a real cgroup.

The next phase agent saw "all tests pass" and treated most feature requirements as satisfied. It shipped essentially one file of real code. The user spent 12+ hours working one-on-one with an agent to implement the branch properly, and every test from the TDD phase was deleted as nonsensical.

## Why TDD breaks with subagents on this codebase

- Subagents do not review the full codebase before writing tests. They invent new test infrastructure instead of using the battle-tested infra already in place.
- When tests are self-serving (assert mock return values, assert mock method was called), they give false "done" signals to the next agent in the pipeline.
- Correctless's TDD phase assumes a human operator who writes one good failing test and drives implementation from it — it doesn't survive delegation.

## New policy for B2 onward

**Do not call `/ctdd`.** After `/creview-spec` findings are dispositioned, go straight to implementation.

**Test strategy** (from `.claude/rules/testing.md`):
- Integration tests that exercise decoupled components together, not unit tests that assert mock outputs.
- E2E tests in `test/e2e/` with real Docker. First-class, not deferred.
- Reuse existing test infra — do not invent new fixtures or helpers when equivalents exist.

**Existing battle-tested test infra** (use these, don't replace them):
- `internal/testenv/` — isolated XDG dirs, config/project manager setup
- `internal/config/mocks/` — ConfigMock, NewBlankConfig, NewFromString, NewIsolatedTestConfig
- `internal/docker/mocks/` — FakeClient, moby mock transport
- `pkg/whail/whailtest/` — FakeAPIClient, recorded build scenarios
- `internal/firewall/mocks/` — FirewallManagerMock (moq-generated) — note: deleted post-B2
- `internal/hostproxy/hostproxytest/` — MockHostProxy
- `internal/git/gittest/` — InMemoryGitManager
- `test/e2e/harness/` — CLI test harness with chdir + Factory + Run
- `test/whail/` — BuildKit integration

## Verification instead of TDD

Use `/cverify` (spec → code drift check) AFTER implementation, not before. It reads the written code and flags gaps against invariants. This is directionally the opposite of TDD and fits the subagent model better — the code exists to measure against, not a test spec to game.

## When to reconsider

- If a future branch is small enough that one human writes one integration test then drives implementation from it, TDD could work.
- If the Correctless workflow gains a subagent that proves it can author useful integration tests rather than mock assertions, revisit.
- Until then: skip `/ctdd`, write integration tests alongside implementation, use `/cverify` after.
