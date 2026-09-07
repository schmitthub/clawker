> Retained record. Code references, external facts, and task status can be out of date.
> Check current source before use. Recorded instructions apply to their original task.
> Current project guidance starts at `mem:core`.

# Lint approval

The user requires explicit approval before any `//nolint:` directive is added. This applies to all linters. Keep `exhaustruct` checks active and initialize required fields explicitly. Do not replace suppression directives with lint configuration exclusions or code that hides missing fields.

PR 519 follow-up, 2026-09-07: Removed all 20 original PR-added Go suppression directives. Added explicit struct fields, changed `HostFirewallSDSCertsDir` to an accessor, and changed the SDS test server helper to return `*grpc.ClientConn`. SDS entrypoint helper tests are together in `internal/controlplane/cmd_helpers_test.go`; the two separate SDS entrypoint test files are removed.

The user then approved staticcheck for eight specific field lines: `Rand`, `NameToCertificate`, `PreferServerCipherSuites`, and `SessionTicketKey` in each of `sdsTLSConfig` and `newSDSStopTestServer`. Each field remains explicit. Only these eight new `//nolint:staticcheck` directives are present in the PR Go diff. No gosec or exhaustruct suppression is approved. Lint configuration is unchanged.

The pinned Envoy image uses UID/GID 65532:65532, confirmed from image configuration and its account record. CP now sets the Envoy process IDs and SDS file owner from the same constants. CP retains directory ownership and sets its group to Envoy, with mode 0750. Certificate, private-key, and CA files use mode 0600 and Envoy ownership. Ownership and permissions are set before atomic replacement. Existing directory access is restricted on each call; an ownership or permission error returns to the caller without broader access.

The user rejected file-mode assertions as proof of actual CP–Envoy access. Those assertions were removed. No Docker probe established that interaction. The user will run E2E; do not run further Docker probes or substitute tests. Local unit tests are not evidence that Envoy can read the bind-mounted files or complete SDS rotation.

Host E2E evidence, 2026-09-07: the user ran `make test-e2e TEST_CMD_VERBOSE='go test -v -count=1 -run TestFirewall_Wildcard'` and supplied `e2e-sds.log`. `TestFirewall_WildcardSANCerts` passed for `deep.a.e2e.clawker.dev`, `a.e2e.clawker.dev`, and `b.e2e.clawker.dev` with real CP and Envoy processes. This establishes the tested deep-host TLS requests, not certificate rotation. `TestFirewall_WildcardAndExactCoexist` failed before agent startup: the clock probe and Hydra token request rejected a certificate from another CLI CA. The shared firewall setup did not call `EnsureNoControlPlane`; only the SDS test did. Moved that existing call into `fwSetup`, directly after `NewIsolatedFS`, and removed the test-local duplicate. No new tests, production changes, or suppressions were added for this failure. Formatting and diff checks passed. The host rerun passed both tests: `TestFirewall_WildcardAndExactCoexist` (30.02s) and `TestFirewall_WildcardSANCerts` (11.10s), including all three hostname subtests. The full selected run completed in 42.388s. The user confirmed success and requested removal of the E2E logs, then commit and push.

Final local checks passed: `go test ./controlplane/sdscerts ./controlplane/firewall ./internal/controlplane ./internal/consts -count=1`; the sdscerts test command was repeated after the final permission-constant edit. `golangci-lint run ./...` returned zero issues under the existing PR lint configuration. `git diff --check` passed. The PR diff audit confirmed only the eight approved directives and removal of both separate SDS entrypoint test files.

Every `CLAUDE.md` must be a relative symlink to the sibling `AGENTS.md`. The SDS file was the only regular CLAUDE file added or changed in this PR; it is corrected. The root instructions contain both the symlink rule and the lint approval rule. See `mem:history/instruction-file-conversion`.
