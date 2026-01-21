// Package lifecycle provides integration tests for container lifecycle operations.
//
// These tests verify the end-to-end container lifecycle including run, start, stop,
// exec, and related operations. Unlike unit tests that mock Docker interactions,
// these tests run against a real Docker daemon to catch regressions in container
// behavior.
//
// The tests are organized into two categories:
//
// 1. Internal package tests - Call internal Go functions directly (run.Run(), start.Start(), etc.)
// 2. CLI binary tests - Execute the actual clawker binary
//
// # Running Tests
//
// These tests require Docker to be running and are skipped by default.
// To run them:
//
//	CLAWKER_INTEGRATION_TESTS=1 go test ./internal/lifecycle/... -v -timeout 15m
//
// To run only CLI binary tests:
//
//	CLAWKER_INTEGRATION_TESTS=1 go test ./internal/lifecycle/... -v -run TestClawkerBinary -timeout 15m
//
// # Test Environment
//
// The TestEnv struct provides:
//   - Temporary working directory with test clawker.yaml
//   - Pre-built test image
//   - Docker client with clawker labels
//   - Automatic cleanup of test resources
package lifecycle
