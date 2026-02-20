# Brainstorm: Config Test Doubles

> **Status:** Completed
> **Created:** 2026-02-19
> **Last Updated:** 2026-02-19 00:00

## Problem / Topic
The config package's test helpers (`stubs.go`) return either concrete `configImpl` (no call tracking, no method overriding) or read-only wrappers. We have a `ConfigMock` (moq-generated) but no convenient constructors that return it pre-wired with real behavior. The gh CLI's `stub.go` solves this elegantly: `NewFromString` returns a `*ConfigMock` with every Func field delegating to a real config underneath, enabling partial mocking and call assertions.

## Open Items / Questions
- Should `NewMockConfig()` and `NewBlankConfig()` both exist or should one be removed?
- Should `NewFakeConfig(opts)` survive or be replaced by the mock-delegation pattern?
- What should the Write behavior be for mock-backed configs? Delegate to real configImpl or no-op?
- Should `NewIsolatedTestConfig` return `*ConfigMock` or keep returning `Config` interface?
- How do we handle the 55 callers of `NewMockConfig()` during migration?
- Should `NewConfigFromString` (non-test, returns `(Config, error)`) be kept separate?

## Decisions Made
- `NewFromString(yaml) *ConfigMock` — wires all 46 read Func fields to delegate to `ReadFromString`-created configImpl; Set/Write/Watch left nil (panic on call)
- `NewBlankConfig() *ConfigMock` — calls `NewFromString("")`, replaces old `NewMockConfig`
- `NewIsolatedTestConfig(t *testing.T)` and `StubWriteConfig(t *testing.T)` kept for FS-backed tests; `testTB` interface removed in favor of `*testing.T`
- Removed: `NewMockConfig`, `NewFakeConfig`, `FakeConfigOptions`, `NewConfigFromString`, `readOnlyConfig`, `ErrReadOnlyConfig`, `NewIsolatedFSConfigFromTestdata`, `copyDirRecursive`
- Env override tests migrated from `NewMockConfig` to `NewIsolatedTestConfig` (file-backed)

## Conclusions / Insights
- (none yet)

## Gotchas / Risks
- (none yet)

## Unknowns
- (none yet)

## Next Steps
- Migrate 55 `NewMockConfig()` callers across the codebase to `NewBlankConfig()` (user handling separately)
- All documentation updated to reflect new API