# Integration/E2E Test Image Building Optimization Needed

**Created:** 2025-01-22
**Status:** PROBLEM STATEMENT - Needs Future Planning
**Priority:** High (affects developer experience and CI time)

## Problem Summary

Integration and E2E tests are extremely slow because each test builds its own Docker image from scratch. A single test suite can take 10-15+ minutes, with most time spent on redundant image builds.

## Observed Metrics

From exec integration test run (6 tests):
- TestExecIntegration_BasicCommands: 17s (first build, no cache)
- TestExecIntegration_WithAgent: 138s (different project name = partial cache)
- TestExecIntegration_EnvFlag: 199s (different project name)
- TestExecIntegration_WorkdirFlag: 204s (different project name)
- TestExecIntegration_ErrorCases: 104s (different project name)
- TestExecIntegration_ScriptExecution: 252s (different project name, Claude install failed)
- **Total: ~664-750 seconds (11-12 minutes) for 6 tests**

The actual test logic executes in < 1 second per test. 99%+ of time is image building.

## Root Causes

### 1. Each Test Builds Its Own Image
```go
// Current pattern in every integration test:
imageTag := testutil.BuildTestImage(t, h, testutil.BuildTestImageOptions{
    SuppressOutput: true,
})
```
Each test calls `BuildTestImage` which:
- Creates a unique tag with timestamp: `clawker-e2e-{project}:{timestamp}`
- Runs the full Dockerfile generation and build
- Installs Claude Code from npm (slow, can fail)

### 2. Different Project Names Break Layer Caching
Each test uses a different project name:
- `exec-test`
- `exec-agent-test`
- `exec-env-test`
- `exec-workdir-test`
- `exec-error-test`
- `exec-script-test`

This creates different image tags, reducing Docker layer cache effectiveness.

### 3. Claude Code Installation is Slow and Flaky
The Dockerfile runs:
```dockerfile
RUN curl -fsSL "https://claude.ai/install.sh" | bash -s ${CLAUDE_CODE_VERSION}
```
This:
- Downloads from external servers (network latency)
- Can fail transiently (rate limiting, server issues)
- Takes 30-60+ seconds even when cached layers exist

### 4. No Image Sharing Between Tests
Tests don't share images. Even sequential tests in the same file rebuild.

## Impact

1. **Developer Experience**: Running integration tests locally is painful (10+ minute waits)
2. **CI Time**: CI pipelines are slow and expensive
3. **Flaky Tests**: External Claude Code install can fail, causing test failures unrelated to code changes
4. **Resource Waste**: Building identical images repeatedly wastes compute and bandwidth

## Potential Solutions (To Be Planned)

### Option A: Shared Test Fixture Image
Build one image at test suite start, reuse across all tests:
```go
func TestMain(m *testing.M) {
    imageTag := buildSharedTestImage()
    os.Setenv("TEST_IMAGE", imageTag)
    code := m.Run()
    cleanupSharedTestImage(imageTag)
    os.Exit(code)
}
```

### Option B: Pre-built Test Image in CI
- Build a test image once in CI setup phase
- Push to registry or cache
- All tests pull/use the pre-built image

### Option C: Test Suite Orchestration
- First test builds, stores image tag
- Subsequent tests reuse via shared state
- Requires test ordering or coordination

### Option D: Lazy Image Building with Caching
- Check if a suitable image already exists before building
- Use content-addressable tags (hash of Dockerfile)
- Skip build if image with matching hash exists

### Option E: Separate "Requires Claude Code" Tests
- Tests that need actual Claude Code functionality use full images
- Tests that only need container infrastructure use minimal images
- Tag tests appropriately

## Files Involved

- `internal/testutil/docker.go` - `BuildTestImage()` function
- `internal/testutil/harness.go` - Test harness setup
- `pkg/cmd/container/*/..._integration_test.go` - All integration tests
- `internal/build/build.go` - Image building logic

## Constraints to Consider

1. Tests must remain isolated (no shared state pollution)
2. Parallel test execution should work
3. Local and CI environments may differ
4. Some tests may need specific image configurations
5. Cleanup must still work (no orphaned images)

## Questions to Answer

1. Can all integration tests share one image, or do some need variants?
2. How to handle tests that modify the image (should be rare)?
3. How to invalidate cached images when Dockerfile changes?
4. What's the right granularity - per-package, per-file, or global?

## Next Steps

1. Audit all integration tests to categorize image requirements
2. Design shared image infrastructure
3. Implement TestMain-based image sharing for a single package as POC
4. Measure improvement
5. Roll out to all test packages

---

**Note:** The `BuildSimpleTestImage` function was added to `internal/testutil/docker.go` during debugging but should probably be removed or repurposed once proper optimization is implemented.
