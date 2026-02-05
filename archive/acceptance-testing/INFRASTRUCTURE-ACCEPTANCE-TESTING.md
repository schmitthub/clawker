# Acceptance Testing Infrastructure Requirements

## Overview

The GitHub CLI acceptance testing infrastructure is a blackbox testing system that validates the CLI tool against real GitHub instances. Tests are built on the `github.com/cli/go-internal/testscript` framework, which allows executing shell-like test scripts with custom commands and environment management.

**Execution Model**: Tests call `ghcmd.Main()` directly rather than building a binary, enabling code coverage collection while treating the CLI as a blackbox.

**Build Constraint**: `//go:build acceptance` ensures tests are excluded from standard `go test ./...` runs.

---

## Build System Integration

### Build Targets

**Makefile Target:**
```makefile
.PHONY: acceptance
acceptance:
	go test -tags acceptance ./acceptance
```

**Build Requirements:**
- Go 1.25.5 or later (per `go.mod`)
- Build constraint tag: `-tags acceptance`
- No special compilation flags required (standard Go test execution)

### Build Execution Command

```bash
# Full acceptance test suite
GH_ACCEPTANCE_HOST=<host> \
GH_ACCEPTANCE_ORG=<org> \
GH_ACCEPTANCE_TOKEN=<token> \
go test -tags acceptance ./acceptance

# Specific test category
go test -tags=acceptance -run ^TestPullRequests$ ./acceptance

# Specific test script
GH_ACCEPTANCE_SCRIPT=pr-view.txtar \
go test -tags=acceptance -run ^TestPullRequests$ ./acceptance

# With code coverage
go test -tags=acceptance \
  -coverprofile=coverage.out \
  -coverpkg=./... \
  ./acceptance
```

**Coverage Collection:**
- Use `-coverprofile=coverage.out` to collect coverage data
- Use `-coverpkg=./...` to measure coverage across all packages
- Coverage is accurate because tests call `ghcmd.Main()` directly, not a binary

---

## Required Environment Variables

### Mandatory Variables

All three of these must be set and non-empty before running acceptance tests.

| Variable | Required | Format | Purpose | Example |
|----------|----------|--------|---------|---------|
| `GH_ACCEPTANCE_HOST` | Yes | Hostname | GitHub instance host | `github.com` or `github.enterprise.example.com` |
| `GH_ACCEPTANCE_ORG` | Yes | Organization name | Test organization (NOT 'github' or 'cli') | `gh-acceptance-testing` |
| `GH_ACCEPTANCE_TOKEN` | Yes | Token string | Authentication token with required scopes | Legacy PAT token |

**Validation Rules:**
- All three variables must be non-empty strings
- `GH_ACCEPTANCE_ORG` cannot be `"github"` or `"cli"` (validated in code)
- Token must have sufficient GitHub scopes for all test operations
- Recommended: Use Legacy Personal Access Token (PAT), not Fine-Grained PATs

### Optional Control Variables

| Variable | Purpose | Values | Default |
|----------|---------|--------|---------|
| `GH_ACCEPTANCE_SCRIPT` | Run single test script instead of all | Filename with `.txtar` extension | Empty (run all) |
| `GH_ACCEPTANCE_PRESERVE_WORK_DIR` | Keep temporary test directory after execution | `"true"` or `"false"` | `"false"` |
| `GH_ACCEPTANCE_SKIP_DEFER` | Skip cleanup commands (for debugging) | `"true"` or `"false"` | `"false"` |

### Variables Automatically Injected into Test Scripts

These environment variables are set automatically by the test setup and available to `.txtar` test scripts.

| Variable | Source | Format | Purpose |
|----------|--------|--------|---------|
| `GH_HOST` | `GH_ACCEPTANCE_HOST` | Hostname | GitHub API host |
| `ORG` | `GH_ACCEPTANCE_ORG` | Organization name | Test organization for creating resources |
| `GH_TOKEN` | `GH_ACCEPTANCE_TOKEN` | Token string | GitHub authentication |
| `SCRIPT_NAME` | Derived from filename | Underscored name | Unique test identifier (hyphens → underscores) |
| `RANDOM_STRING` | Generated | 10-char random string | Unique resource suffix for global isolation |
| `HOME` | Test work directory | Path | Required for git operations |
| `GH_CONFIG_DIR` | Test work directory | Path | Required for gh CLI config |

---

## GitHub API Requirements

### Token Scopes

Token scope requirements vary by test suite. The token must have sufficient scopes for all operations performed in deferred cleanup commands.

**Typical Required Scopes** (varies by test):
- `repo` - Repository access (read/write/delete)
- `workflow` - GitHub Actions workflows
- `admin:repo_hook` - Repository webhooks
- `admin:public_key` - SSH key management
- `admin:gpg_key` - GPG key management
- `delete_repo` - Repository deletion (critical for cleanup)
- `write:org_action` - Organization Actions
- `admin:org_secret` - Organization secret management

**Why Legacy PAT is Recommended:**
- Fine-Grained PATs lack `delete_repo` scope
- Fine-Grained PATs have insufficient privileges for some operations
- Legacy PATs provide necessary comprehensive permissions

**Token Validation:**
- No early validation in test runner (scope failures occur during test execution)
- Tests should fail early if token lacks required scopes
- Cleanup (`defer`) may fail if scopes insufficient (TODO: implement scope validation)

### GitHub Instance Requirements

**Supported:**
- `github.com` (public GitHub)
- GitHub Enterprise Server instances (GHES)

**Organization Requirements:**
- Must be an organization (not a personal account)
- Must NOT be `github` or `cli` organizations
- Must have permissions to create/delete resources
- Recommended: Dedicated acceptance testing organization (e.g., `gh-acceptance-testing`)

---

## Test Organization & Structure

### Test Categories (18 total)

Tests are organized by feature area with corresponding directories under `/acceptance/testdata/`:

| Category | Directory | Test Function | Test Count | Features Tested |
|----------|-----------|----------------|------------|-----------------|
| API | `api/` | `TestAPI` | ~5 | GraphQL/REST API interactions |
| Authentication | `auth/` | `TestAuth` | ~10 | Login, logout, token, setup-git |
| GPG Keys | `gpg-key/` | `TestGPGKeys` | ~5 | GPG key management |
| Extensions | `extension/` | `TestExtensions` | ~8 | CLI extension management |
| Issues | `issue/` | `TestIssues` | ~20 | Issue create/view/edit/close |
| Labels | `label/` | `TestLabels` | ~10 | Label management |
| Organizations | `org/` | `TestOrg` | ~8 | Organization operations |
| Projects | `project/` | `TestProject` | ~10 | GitHub projects |
| Pull Requests | `pr/` | `TestPullRequests` | ~40+ | PR create/view/merge/comment |
| Releases | `release/` | `TestReleases` | ~10 | Release management |
| Repositories | `repo/` | `TestRepo` | ~15 | Repository operations |
| Rulesets | `ruleset/` | `TestRulesets` | ~8 | Repository rulesets |
| Search | `search/` | `TestSearches` | ~5 | Search functionality |
| Secrets | `secret/` | `TestSecrets` | ~15 | Repository/org secret management |
| SSH Keys | `ssh-key/` | `TestSSHKeys` | ~5 | SSH key management |
| Variables | `variable/` | `TestVariables` | ~10 | GitHub Actions variables |
| Workflows | `workflow/` | `TestWorkflows` | ~15 | GitHub Actions workflows |
| **Total** | | | **~175+** | |

### Test Script Format (.txtar)

Tests are defined in `.txtar` (text archive format) files:

**Typical Test Structure:**
```txtar
# Comment describing the test
env VAR=value

# Execute command
exec gh pr create --title 'Title'

# Assert output matches regex
stdout 'Title'

# Register cleanup command (runs after test)
defer gh repo delete --yes $ORG/$SCRIPT_NAME-$RANDOM_STRING

# Include file content inline
-- filename --
file content here
```

**Test Script Elements:**
- Comments: Lines starting with `#`
- Environment: `env VAR=value` statements
- Commands: `exec COMMAND [ARGS...]` (required by `RequireExplicitExec=true`)
- Assertions: `stdout REGEX`, `stderr REGEX` (case-sensitive regex matching)
- File operations: `mkdir`, `mv`, `cd`, `rm` (testscript builtins)
- Files: `-- filename --` followed by file content (inline test data)

### Test Execution Flow

1. **Setup Phase** (`sharedSetup`):
   - Extract script name from test working directory
   - Set `SCRIPT_NAME` (convert hyphens to underscores)
   - Set `HOME` and `GH_CONFIG_DIR` to test work directory
   - Set `GH_HOST`, `ORG`, `GH_TOKEN` from environment
   - Generate 10-character `RANDOM_STRING`

2. **Test Execution**:
   - Execute commands with custom `sharedCmds` available
   - Environment variables available to all commands
   - Assertions validate command output

3. **Cleanup Phase**:
   - Execute deferred commands in reverse order (even on failure)
   - Skip defer if `GH_ACCEPTANCE_SKIP_DEFER=true`
   - All cleanup commands must succeed for test to pass

---

## Custom Commands (testscript Extensions)

Custom commands are defined in `sharedCmds()` function and available in test scripts.

### defer COMMAND [ARGS...]

Register a command to execute after test completes (cleanup pattern).

**Behavior:**
- Executes even if test fails (critical for resource cleanup)
- Can be disabled with `GH_ACCEPTANCE_SKIP_DEFER=true`
- Must succeed for test to pass
- Executes in reverse order (LIFO)

**Example:**
```txtar
# Create repository
exec gh repo create $ORG/$SCRIPT_NAME-$RANDOM_STRING

# Register cleanup
defer gh repo delete --yes $ORG/$SCRIPT_NAME-$RANDOM_STRING
```

**Infrastructure Impact:**
- Token must have `delete_repo` scope
- Failures in defer indicate insufficient scopes
- Orphaned resources indicate cleanup failure

### env2upper VAR=value

Convert value to uppercase and set environment variable.

**Behavior:**
- Takes `name=value` format
- Sets `name` env var to uppercase of `value`
- Required because GitHub Actions secret names are automatically uppercased

**Example:**
```txtar
env2upper ORG_SECRET_NAME=$RANDOM_STRING
```

### replace FILE VAR=value [VAR=value...]

Replace `$VAR` placeholders in file with provided values.

**Behavior:**
- Reads file, replaces exact `$KEY` patterns
- Preserves original file permissions
- Does NOT perform environment variable expansion (safe for workflows with `${{ }}` syntax)
- Used for customizing workflow files

**Example:**
```txtar
env2upper SECRET_NAME=$SCRIPT_NAME_$RANDOM_STRING
replace .github/workflows/workflow.yml SECRET_NAME=$SECRET_NAME
```

### stdout2env VAR_NAME

Capture stdout from previous command into environment variable.

**Behavior:**
- Reads output from last executed command
- Strips trailing newline
- Sets environment variable with name provided

**Example:**
```txtar
exec gh pr create --title 'Feature'
stdout2env PR_URL
```

### sleep SECONDS

Sleep for specified number of seconds (integer only).

**Behavior:**
- Blocks test execution for specified duration
- Used for workflow registration delays
- GitHub Actions workflows need propagation time after creation

**Example:**
```txtar
exec gh workflow enable workflow.yml
sleep 2  # Wait for workflow to be registered
exec gh workflow run workflow.yml
```

---

## testscript Configuration

### testscript.Params Structure

```go
testscript.Params{
    Dir:                 path.Join("testdata", command),  // Test data directory
    Files:               files,  // Specific files if GH_ACCEPTANCE_SCRIPT set
    Setup:               sharedSetup(tsEnv),  // Initialize environment
    Cmds:                sharedCmds(tsEnv),   // Custom commands
    RequireExplicitExec: true,  // All commands must use 'exec'
    RequireUniqueNames:  true,  // Test script names must be unique
    TestWork:            tsEnv.preserveWorkDir,  // Keep temp dir if true
}
```

### Configuration Details

| Parameter | Value | Purpose |
|-----------|-------|---------|
| `Dir` | `testdata/<category>` | Directory containing `.txtar` test files |
| `Files` | Varies | Specific files to run (if `GH_ACCEPTANCE_SCRIPT` set) |
| `Setup` | `sharedSetup()` | Environment initialization function |
| `Cmds` | `sharedCmds()` | Custom command handlers map |
| `RequireExplicitExec` | `true` | Forces use of `exec` keyword for all commands |
| `RequireUniqueNames` | `true` | Prevents duplicate test script names |
| `TestWork` | Boolean | Keep temporary work directory (for debugging) |

---

## CI/CD Integration

### GitHub Actions Considerations

**Test Isolation:**
- Each test runs in its own temporary directory
- Environment variables isolated per test
- Network calls made to real GitHub instance

**Rate Limiting:**
- No built-in rate limit handling
- ~175+ tests may exceed GitHub API rate limits if run quickly
- Recommendation: Add jitter/backoff or run in smaller batches

**Token Exposure Risk:**
- Verbose mode (`-v` flag) dumps environment variables including token
- Partial redaction implemented (PR #9804) but not comprehensive
- Recommendation: Use secrets management in CI/CD

**Expected Duration:**
- Full test suite: 10-30 minutes (varies by API latency)
- Individual tests: 0.5-15 seconds
- Total time influenced by GitHub API response times

### Running in CI/CD

**Basic Setup:**
```yaml
- name: Run acceptance tests
  env:
    GH_ACCEPTANCE_HOST: github.com
    GH_ACCEPTANCE_ORG: gh-acceptance-testing
    GH_ACCEPTANCE_TOKEN: ${{ secrets.GH_ACCEPTANCE_TOKEN }}
  run: go test -tags=acceptance ./acceptance
```

**With Coverage:**
```yaml
- name: Run acceptance tests with coverage
  env:
    GH_ACCEPTANCE_HOST: github.com
    GH_ACCEPTANCE_ORG: gh-acceptance-testing
    GH_ACCEPTANCE_TOKEN: ${{ secrets.GH_ACCEPTANCE_TOKEN }}
  run: |
    go test -tags=acceptance \
      -coverprofile=coverage.out \
      -coverpkg=./... \
      ./acceptance
    go tool cover -html=coverage.out -o coverage.html
```

**Secrets Management:**
- Use repository secrets (GitHub Actions `secrets.` context)
- Legacy PAT stored in `GH_ACCEPTANCE_TOKEN` secret
- Consider token rotation policies

---

## Development Environment Setup

### Prerequisites

| Requirement | Version | Purpose |
|-------------|---------|---------|
| Go | 1.25.5+ | Build and run tests |
| Git | Latest | Repository operations in tests |
| GitHub CLI (gh) | Latest | Installing/testing CLI |

### IDE/Editor Configuration

**VS Code** - Add build flags to `settings.json`:
```json
{
  "gopls": {
    "buildFlags": ["-tags=acceptance"]
  }
}
```

**Syntax Highlighting:**
- Install `txtar` extension (brody715.txtar)
- Or install `vscode-testscript` extension (twpayne.vscode-testscript)

### Local Development Workflow

1. **Create Acceptance Testing Organization:**
   ```bash
   # Use dedicated org (e.g., personal test org)
   export GH_ACCEPTANCE_ORG="my-username-acceptance"
   ```

2. **Generate PAT Token:**
   ```bash
   # Via GitHub Web UI with all required scopes
   # OR via gh CLI
   gh auth login --hostname github.com --scopes repo,workflow,admin:repo_hook,admin:public_key,admin:gpg_key,write:org_action,admin:org_secret
   ```

3. **Set Environment Variables:**
   ```bash
   export GH_ACCEPTANCE_HOST="github.com"
   export GH_ACCEPTANCE_ORG="my-username-acceptance"
   export GH_ACCEPTANCE_TOKEN="ghp_..."
   ```

4. **Run Specific Test Category:**
   ```bash
   go test -tags=acceptance -run ^TestPullRequests$ ./acceptance -v
   ```

5. **Debug Test Failure:**
   ```bash
   # Keep work directory and skip cleanup
   GH_ACCEPTANCE_PRESERVE_WORK_DIR=true \
   GH_ACCEPTANCE_SKIP_DEFER=true \
   GH_ACCEPTANCE_SCRIPT=pr-view.txtar \
   go test -tags=acceptance -run ^TestPullRequests$ ./acceptance -v

   # Inspect test working directory
   ls $WORK  # Path shown in test output
   ```

### Debugging Tools & Flags

| Flag/Variable | Purpose | Example |
|---------------|---------|---------|
| `-v` | Verbose output (shows commands and stdio) | `go test -tags=acceptance -v ./acceptance` |
| `GH_ACCEPTANCE_SCRIPT` | Run single test script | `GH_ACCEPTANCE_SCRIPT=pr-view.txtar` |
| `GH_ACCEPTANCE_PRESERVE_WORK_DIR=true` | Keep temp directory after test | For post-test inspection |
| `GH_ACCEPTANCE_SKIP_DEFER=true` | Skip cleanup commands | For debugging resource state |
| `-run` | Filter test functions | `-run ^TestPullRequests$` |

**Debug Output Includes:**
- `WORK=<path>` - Temporary working directory
- Comment timings - Duration per test section
- Full command output on failure
- Failure location with regex match details

**Caution:**
- Verbose mode may expose sensitive tokens in logs
- Always preserve/skip defer when debugging (avoids resource cleanup)
- Clean up manually if skip defer is used

---

## Code Coverage

### Collection

```bash
# Generate coverage profile
go test -tags=acceptance \
  -coverprofile=coverage.out \
  -coverpkg=./... \
  ./acceptance

# View coverage in browser
go tool cover -html=coverage.out -o coverage.html
open coverage.html

# View coverage in terminal
go tool cover -func=coverage.out
```

### Coverage Characteristics

- **Accurate Coverage**: Calls `ghcmd.Main()` directly (not binary), enabling real coverage metrics
- **Scope**: Can measure coverage across all packages with `-coverpkg=./...`
- **Tradeoff**: Acceptance tests + coverage adds execution time
- **Not Blackbox**: Despite treating CLI as blackbox, direct Main() call enables coverage collection

---

## External Dependencies

### testscript Framework

**Dependency**: `github.com/cli/go-internal v0.0.0-20241025142207-6c48bcd5ce24`

**Features Used:**
- `testscript.RunMain()` - Register command handlers for test execution
- `testscript.Run()` - Execute txtar test scripts
- `testscript.Params` - Test configuration
- `testscript.Env` - Test environment management
- `testscript.TestScript` - Custom command API

**Irreplaceability**: High - purpose-built for CLI testing, Go standard library integration

### Internal CLI Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/cli/cli/v2/internal/ghcmd` | Main CLI entry point (called by testscript) |
| `github.com/cli/cli/v2/internal/config` | YAML config management (used via `GH_CONFIG_DIR`) |
| `github.com/cli/cli/v2/pkg/cmdutil` | Command utilities and error types |

### System Dependencies

| Tool | Purpose | Version |
|------|---------|---------|
| Git | Repository operations in tests | Latest (tested with common versions) |
| GitHub API | Real API calls for all test operations | Public GitHub or GHES instance |

---

## Security Considerations

### Token Management

**Risks:**
1. Token exposure in verbose logging
2. Token scope insufficiency for cleanup operations
3. Token rotation not automated

**Mitigations:**
- Use dedicated PAT for acceptance testing (not personal use token)
- Implement token redaction in logs (partial implementation in PR #9804)
- Use GitHub Actions secrets for CI/CD (automatic redaction)
- Document required token scopes

### Authentication

**Methods:**
- Token-based: `GH_ACCEPTANCE_TOKEN` (legacy PAT recommended)
- Not supported: Fine-grained PATs, OAuth with limited scopes

**Requirements:**
- Token must have full permissions to test organization
- Token must allow `delete_repo` scope (critical for cleanup)
- Token must allow all feature-specific scopes (workflow, secrets, etc.)

### Resource Isolation

**Mechanisms:**
- `RANDOM_STRING` suffix for globally-visible resources
- Deferred cleanup for all created resources
- Isolated test working directory per script

**Risks:**
- Orphaned resources if cleanup fails (scope issues, network failures)
- Potential for resource name collisions (mitigated by random suffix)
- Resource leaks if defer commands fail

---

## Monitoring & Observability

### Test Output

**Standard Output Format:**
```
--- FAIL: TestPullRequests (duration)
    --- FAIL: TestPullRequests/pr-merge (duration)
        testscript.go:584: WORK=/path/to/work/dir
            # Comment (duration)
            # Another comment (duration)
            > command executed
            FAIL: testdata/pr/pr-merge.txtar:42: no match for pattern
```

**Key Information:**
- `WORK=` - Temporary directory (useful for preservation/debugging)
- Comment lines - Test sections with timing
- `>` lines - Commands executed
- Assertion failures - Regex match details

### Debugging Information

**Verbose Mode** (`-v` flag):
- Shows every command and its output
- Includes environment variable dump
- WARNING: Exposes tokens (redaction not comprehensive)

**Work Directory Contents:**
- Test files (from inline definitions)
- Command output (`stdout`, `stderr` files)
- Git/GitHub resources created during test
- Useful for post-test inspection

---

## Operational Procedures

### Running Full Test Suite

```bash
# Set required environment variables
export GH_ACCEPTANCE_HOST="github.com"
export GH_ACCEPTANCE_ORG="gh-acceptance-testing"
export GH_ACCEPTANCE_TOKEN="<legacy-pat>"

# Run all acceptance tests
go test -tags=acceptance ./acceptance

# With coverage
go test -tags=acceptance \
  -coverprofile=coverage.out \
  -coverpkg=./... \
  ./acceptance
```

### Running Specific Test Categories

```bash
# Pull requests only
go test -tags=acceptance -run ^TestPullRequests$ ./acceptance

# Multiple categories
go test -tags=acceptance -run ^Test(PullRequests|Issues)$ ./acceptance

# Verbose for debugging
go test -tags=acceptance -v -run ^TestPullRequests$ ./acceptance
```

### Debugging Failed Tests

```bash
# Preserve work directory and skip cleanup
export GH_ACCEPTANCE_PRESERVE_WORK_DIR=true
export GH_ACCEPTANCE_SKIP_DEFER=true

# Run specific test script
export GH_ACCEPTANCE_SCRIPT=pr-view.txtar
go test -tags=acceptance -run ^TestPullRequests$ ./acceptance -v

# Inspect work directory shown in WORK=... output
ls /path/to/work/dir
```

### Cleanup After Debug Session

When using `GH_ACCEPTANCE_SKIP_DEFER=true`, manually clean up resources:

```bash
# List resources created by failed test
gh repo list $GH_ACCEPTANCE_ORG | grep $SCRIPT_NAME-$RANDOM_STRING

# Delete manually if needed
gh repo delete $GH_ACCEPTANCE_ORG/$SCRIPT_NAME-$RANDOM_STRING --yes
```

---

## Limitations & Known Issues

### Scope Validation

**Issue**: Tests don't validate token scopes early, leading to mid-test failures during cleanup.

**Status**: Documented as TODO in acceptance/README.md

**Impact**: If token lacks `delete_repo` scope, cleanup (`defer` commands) fail, leaving orphaned resources.

**Workaround**: Ensure token has all required scopes before running tests.

### Rate Limiting

**Issue**: No built-in rate limiting or backoff in tests.

**Status**: Identified but not implemented.

**Impact**: Running full test suite may exceed GitHub API rate limits (~5000 requests/hour for authenticated requests).

**Workaround**: Run tests at off-peak times or against GHES with higher rate limits.

### Token Exposure in Logs

**Issue**: Verbose mode (`-v` flag) dumps environment variables including tokens.

**Status**: Partial redaction implemented (PR #9804), not comprehensive.

**Impact**: Sensitive tokens may appear in CI/CD logs.

**Workaround**: Use GitHub Actions secrets (automatic redaction), avoid verbose logs in production CI/CD.

### Fine-Grained PAT Support

**Issue**: Fine-Grained PATs lack necessary permissions (specifically `delete_repo`).

**Status**: Documented recommendation for Legacy PAT.

**Impact**: Tests fail with Fine-Grained PATs.

**Workaround**: Use Legacy Personal Access Token instead.

---

## Recommended Adaptations for Other Environments

### For New Teams/Organizations

1. **Create Dedicated Org**: Create organization specifically for acceptance testing (e.g., `org-acceptance-testing`)

2. **Generate Legacy PAT**:
   ```bash
   # Via GitHub.com web UI or gh CLI
   gh auth login --scopes repo,workflow,admin:repo_hook,admin:public_key,admin:gpg_key,write:org_action,admin:org_secret
   ```

3. **Configure Secrets** (for CI/CD):
   ```yaml
   # GitHub Actions
   GH_ACCEPTANCE_HOST: github.com
   GH_ACCEPTANCE_ORG: org-acceptance-testing
   GH_ACCEPTANCE_TOKEN: (stored as repository secret)
   ```

4. **Set Up Monitoring** (optional):
   - Track orphaned resources created by failed tests
   - Set up periodic cleanup of old resources
   - Alert on test suite failures

### For GitHub Enterprise Server (GHES)

1. **Network Access**: Ensure CI/CD runners can reach GHES instance

2. **Token**: Generate Legacy PAT from GHES instance with required scopes

3. **Host Configuration**:
   ```bash
   export GH_ACCEPTANCE_HOST="ghes.example.com"
   export GH_ACCEPTANCE_TOKEN="<ghes-pat>"
   ```

4. **Rate Limiting**: GHES typically has higher rate limits than public GitHub

### For Air-Gapped/Private Networks

1. **Test Organization**: Create local GHES organization for acceptance testing

2. **Token Management**: Generate and rotate tokens locally

3. **CI/CD Integration**: Ensure CI/CD runners have network access

4. **No External Dependencies**: Tests require GitHub API access (no external dependencies)

---

## File References

### Key Files

| File | Purpose | Lines |
|------|---------|-------|
| `/acceptance/acceptance_test.go` | Main test runner and infrastructure | 422 |
| `/acceptance/README.md` | User-facing documentation | 196 |
| `/acceptance/testdata/` | Test script files (.txtar) | ~175+ tests |
| `/Makefile` | Build target for acceptance tests | Line 47-49 |
| `/go.mod` | Dependency declarations | testscript dependency |

### Test Data Structure

```
acceptance/
├── acceptance_test.go              # Test runner
├── README.md                       # Documentation
└── testdata/
    ├── api/                        # API tests (5+ scripts)
    ├── auth/                       # Auth tests (10+ scripts)
    ├── extension/                  # Extension tests (8+ scripts)
    ├── gpg-key/                    # GPG key tests (5+ scripts)
    ├── issue/                      # Issue tests (20+ scripts)
    ├── label/                      # Label tests (10+ scripts)
    ├── org/                        # Org tests (8+ scripts)
    ├── project/                    # Project tests (10+ scripts)
    ├── pr/                         # PR tests (40+ scripts)
    ├── release/                    # Release tests (10+ scripts)
    ├── repo/                       # Repo tests (15+ scripts)
    ├── ruleset/                    # Ruleset tests (8+ scripts)
    ├── search/                     # Search tests (5+ scripts)
    ├── secret/                     # Secret tests (15+ scripts)
    ├── ssh-key/                    # SSH key tests (5+ scripts)
    ├── variable/                   # Variable tests (10+ scripts)
    └── workflow/                   # Workflow tests (15+ scripts)
```

---

## Summary

The GitHub CLI acceptance testing infrastructure provides a comprehensive blackbox testing framework for validating CLI functionality against real GitHub instances. Key infrastructure requirements include:

1. **Environment**: Three mandatory environment variables (host, org, token) + optional control variables
2. **Build System**: Standard Go testing with `-tags acceptance` build constraint
3. **GitHub Instance**: Real GitHub or GHES instance with access to test organization
4. **Token**: Legacy PAT with comprehensive scopes (especially `delete_repo` for cleanup)
5. **Framework**: testscript for shell-like test script execution with custom commands
6. **Test Organization**: 18 feature categories with ~175+ test scripts

The system supports code coverage collection, single-test debugging, and CI/CD integration while maintaining true blackbox testing principles.
