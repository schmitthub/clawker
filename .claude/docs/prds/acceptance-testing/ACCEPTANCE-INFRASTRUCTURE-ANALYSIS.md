# Acceptance Testing Infrastructure - Analysis & Architecture

## System Overview

The acceptance testing infrastructure validates GitHub CLI functionality through real GitHub API interactions using a testscript-based testing framework.

```
┌─────────────────────────────────────────────────────────────────┐
│                    ACCEPTANCE TEST EXECUTION                    │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Test Runner                                                    │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  acceptance_test.go (422 lines)                          │  │
│  │  - TestMain() → testscript.RunMain()                     │  │
│  │  - 17 test functions (API, Auth, PR, Issues, etc.)       │  │
│  │  - testScriptParamsFor() → Params config                 │  │
│  │  - sharedSetup() → Environment setup                     │  │
│  │  - sharedCmds() → Custom commands                        │  │
│  └──────────────────────────────────────────────────────────┘  │
│           ↓                                                      │
│  Test Script Execution (.txtar files)                           │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  testdata/<category>/*.txtar (175+ test files)           │  │
│  │  - Inline test data and commands                         │  │
│  │  - Shell-like syntax with assertions                     │  │
│  │  - Custom commands: defer, replace, env2upper, etc.      │  │
│  └──────────────────────────────────────────────────────────┘  │
│           ↓                                                      │
│  CLI Execution                                                   │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  ghcmd.Main() (Direct invocation, not binary)            │  │
│  │  - Called by testscript for "gh" command                 │  │
│  │  - Enables code coverage collection                      │  │
│  └──────────────────────────────────────────────────────────┘  │
│           ↓                                                      │
│  GitHub API (Real Instance)                                     │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  github.com or GitHub Enterprise Server                  │  │
│  │  - Authenticated via GH_ACCEPTANCE_TOKEN                 │  │
│  │  - Creates/deletes real resources                        │  │
│  │  - Cleanup via deferred commands                         │  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Environment Configuration Chain

```
User Environment Variables
├── GH_ACCEPTANCE_HOST (required)
├── GH_ACCEPTANCE_ORG (required)
├── GH_ACCEPTANCE_TOKEN (required)
├── GH_ACCEPTANCE_SCRIPT (optional)
├── GH_ACCEPTANCE_PRESERVE_WORK_DIR (optional)
└── GH_ACCEPTANCE_SKIP_DEFER (optional)
        ↓
testScriptEnv (Config Object)
├── host
├── org
├── token
├── script
├── preserveWorkDir
└── skipDefer
        ↓
sharedSetup() injects into testscript.Env
├── SCRIPT_NAME (derived from test filename)
├── HOME (test work directory)
├── GH_CONFIG_DIR (test work directory)
├── GH_HOST (from host)
├── ORG (from org)
├── GH_TOKEN (from token)
└── RANDOM_STRING (generated: 10 chars)
        ↓
Test Script Execution
├── Variables available to "exec" commands
├── Variables available to custom commands
└── Variables visible to gh CLI invocations
```

## Test Execution Lifecycle

```
1. SETUP PHASE
   ├─ Parse environment variables (3 required, 3 optional)
   ├─ Validate: org != "github" and org != "cli"
   ├─ Initialize testScriptEnv struct
   ├─ Call testscript.Run() with Params
   └─ sharedSetup() sets up test environment

2. TEST INITIALIZATION (per script)
   ├─ Create isolated temp directory
   ├─ Set HOME and GH_CONFIG_DIR to temp dir
   ├─ Inject environment variables
   ├─ Register custom commands (defer, replace, etc.)
   └─ Load .txtar test script

3. TEST EXECUTION (per .txtar file)
   ├─ Execute "exec" commands
   ├─ Collect stdout/stderr for assertions
   ├─ Process custom commands
   ├─ Assert output with regex matching
   ├─ Create GitHub resources via gh CLI
   └─ Track deferred cleanup commands

4. CLEANUP PHASE (on completion, success or failure)
   ├─ Skip if GH_ACCEPTANCE_SKIP_DEFER=true
   ├─ Execute deferred commands in reverse order (LIFO)
   ├─ Delete created resources
   ├─ Delete temporary directory (unless GH_ACCEPTANCE_PRESERVE_WORK_DIR=true)
   └─ Report test result

5. POST-TEST
   ├─ Collect test metrics (timing per section)
   ├─ Report failures with context
   ├─ If coverage enabled: aggregate coverage data
   └─ Exit with appropriate status code
```

## Dependency Graph

```
acceptance_test.go
├── Standard Library
│   ├── fmt (formatting)
│   ├── os (environment, file I/O)
│   ├── path (path manipulation)
│   ├── strconv (string conversion)
│   ├── strings (string operations)
│   ├── testing (Go test interface)
│   ├── time (time/duration)
│   └── math/rand (random number generation)
├── github.com/cli/go-internal/testscript
│   ├── testscript.RunMain()
│   ├── testscript.Run()
│   ├── testscript.Params
│   ├── testscript.Env
│   └── testscript.TestScript
├── github.com/cli/cli/v2/internal/ghcmd
│   └── ghcmd.Main() [Direct invocation]
├── github.com/cli/cli/v2/internal/config
│   └── [Used via GH_CONFIG_DIR env var]
└── github.com/cli/cli/v2/pkg/cmdutil
    └── [Used internally by CLI commands]

Test Execution Dependencies
├── Git (system binary)
│   └── exec git [clone, checkout, commit, etc.]
└── GitHub API (network)
    ├── Authentication: GH_ACCEPTANCE_TOKEN
    ├── Host: GH_ACCEPTANCE_HOST
    └── Operations: repo create/delete, pr create, etc.
```

## Custom Command Implementation Details

```
sharedCmds() returns map[string]func()

defer COMMAND [ARGS...]
├─ Implementation: testscript.TestScript.Defer()
├─ Execution: After test completes (even on failure)
├─ Use Case: Resource cleanup (repos, issues, etc.)
├─ Order: LIFO (Last In, First Out)
├─ Behavior: Fails if command fails
├─ Skip: If GH_ACCEPTANCE_SKIP_DEFER=true
└─ Critical For: Token must have delete_repo scope

env2upper VAR=value
├─ Implementation: strings.ToUpper() + Setenv()
├─ Use Case: GitHub Actions secret names (auto-uppercase)
├─ Example: env2upper ORG_SECRET=$RANDOM_STRING
└─ Result: ORG_SECRET env var set to uppercase

replace FILE VAR=value [VAR=value...]
├─ Implementation: os.ReadFile() + strings.ReplaceAll() + WriteFile()
├─ Use Case: Customize workflow files safely
├─ Pattern: Exact "$KEY" matching (not env var expansion)
├─ Behavior: Preserves file permissions (os.Stat + os.WriteFile mode)
└─ Safe For: GitHub Actions workflows with ${{ }} syntax

stdout2env VAR_NAME
├─ Implementation: ts.ReadFile("stdout") + Setenv()
├─ Use Case: Capture gh command output
├─ Example: stdout2env PR_URL (after "gh pr create")
├─ Processing: Strips trailing newline
└─ Result: Previous command's stdout available as env var

sleep SECONDS
├─ Implementation: time.Sleep(Duration * time.Second)
├─ Use Case: Wait for workflow propagation
├─ Input: Integer (seconds)
└─ Purpose: GitHub Actions need time to register new workflows
```

## Test Data Organization

```
acceptance/
├── README.md (196 lines)
│   └─ User guide for running/writing tests
├── acceptance_test.go (422 lines)
│   ├─ TestMain() - Entry point
│   ├─ 17 test functions:
│   │  ├─ TestAPI()
│   │  ├─ TestAuth()
│   │  ├─ TestExtensions()
│   │  ├─ TestGPGKeys()
│   │  ├─ TestIssues()
│   │  ├─ TestLabels()
│   │  ├─ TestOrg()
│   │  ├─ TestProject()
│   │  ├─ TestPullRequests()
│   │  ├─ TestReleases()
│   │  ├─ TestRepo()
│   │  ├─ TestRulesets()
│   │  ├─ TestSearches()
│   │  ├─ TestSecrets()
│   │  ├─ TestSSHKeys()
│   │  ├─ TestVariables()
│   │  └─ TestWorkflows()
│   └─ Infrastructure:
│      ├─ testScriptParamsFor()
│      ├─ sharedSetup()
│      ├─ sharedCmds()
│      ├─ testScriptEnv.fromEnv()
│      └─ Helper functions
└── testdata/
    ├── api/ (~5 scripts)
    │   └─ api-*.txtar
    ├── auth/ (~10 scripts)
    │   └─ auth-*.txtar
    ├── extension/ (~8 scripts)
    │   └─ extension-*.txtar
    ├── gpg-key/ (~5 scripts)
    │   └─ gpg-key-*.txtar
    ├── issue/ (~20 scripts)
    │   └─ issue-*.txtar
    ├── label/ (~10 scripts)
    │   └─ label-*.txtar
    ├── org/ (~8 scripts)
    │   └─ org-*.txtar
    ├── project/ (~10 scripts)
    │   └─ project-*.txtar
    ├── pr/ (~40+ scripts)
    │   └─ pr-*.txtar
    ├── release/ (~10 scripts)
    │   └─ release-*.txtar
    ├── repo/ (~15 scripts)
    │   └─ repo-*.txtar
    ├── ruleset/ (~8 scripts)
    │   └─ ruleset-*.txtar
    ├── search/ (~5 scripts)
    │   └─ search-*.txtar
    ├── secret/ (~15 scripts)
    │   └─ secret-*.txtar
    ├── ssh-key/ (~5 scripts)
    │   └─ ssh-key-*.txtar
    ├── variable/ (~10 scripts)
    │   └─ variable-*.txtar
    └── workflow/ (~15 scripts)
        └─ workflow-*.txtar
```

## Build Constraint Impact

```
//go:build acceptance
├─ Effect: Excludes acceptance_test.go from standard builds
├─ Reason: Tests require external GitHub instance
├─ Invocation: go test -tags=acceptance ./acceptance
├─ Without tag: Tests skipped, normal go test ./... works
└─ Configuration: VS Code gopls needs buildFlags: ["-tags=acceptance"]
```

## Test Script Execution Model

```
.txtar File Structure:
┌─────────────────────────────────┐
│ # Comment lines                 │
│ env VAR=value                   │
│ exec gh command arg             │
│ stdout REGEX_PATTERN            │
│ defer cleanup-command           │
│ -- filename --                  │
│ file content                    │
└─────────────────────────────────┘
         ↓ parsed by
testscript.Run()
         ↓
Script Execution:
├─ Execute each "exec" line as shell command
├─ Assertions check regex match on stdout/stderr
├─ Deferred commands collected for later
├─ Files (-- name --) written to test directory
└─ Environment variables inherited and available
```

## Token Scope Dependency Chain

```
Required Token Scopes:
├─ repo ──→ Create/delete repositories
├─ workflow ──→ GitHub Actions workflows
├─ admin:repo_hook ──→ Webhooks
├─ admin:public_key ──→ SSH key management
├─ admin:gpg_key ──→ GPG key management
├─ delete_repo ──→ Critical for cleanup
├─ write:org_action ──→ Organization Actions
└─ admin:org_secret ──→ Organization secrets

Scope Validation:
├─ No early validation (happens during test)
├─ Cleanup fails if delete_repo scope missing
├─ Token validation: User responsibility
└─ Recommended: Legacy PAT (Fine-Grained PATs insufficient)
```

## Performance Characteristics

```
Test Execution Timeline:
├─ Setup per test suite: ~100ms
├─ Per test script: 0.5-15 seconds
│  ├─ Setup (auth setup-git, repo create): ~2-3s
│  ├─ Test execution: ~1-10s
│  └─ Cleanup (defer commands): ~0.5-3s
├─ Full suite (175+ tests): 10-30 minutes
└─ Bottleneck: GitHub API latency

Resource Consumption:
├─ Disk: ~500MB-1GB (temp directories, git repos)
├─ Memory: ~50-100MB (Go runtime)
├─ Network: ~500-1000+ API calls
└─ Rate Limit: GitHub allows ~5000 requests/hour (authenticated)

Coverage Collection Overhead:
├─ Direct Main() call: ~10-20% overhead vs no coverage
├─ Memory: Additional ~10-20MB
└─ Disk: Coverage profiles ~1-5MB
```

## Error Handling & Failure Modes

```
Failure Points & Recovery:
├─ Missing Environment Variables
│  └─ Fatal: Cannot proceed without GH_ACCEPTANCE_HOST, _ORG, _TOKEN
├─ Invalid Organization
│  └─ Fatal: Cannot use 'github' or 'cli' organization
├─ Authentication Failure
│  └─ Fails during first gh command, no cleanup runs
├─ Insufficient Token Scopes
│  └─ Test-specific failures, cleanup may fail (resource leak)
├─ GitHub API Unavailable
│  └─ Timeout, test fails, cleanup may fail (resource leak)
├─ Test Assertion Failure
│  └─ Test fails, cleanup runs (via defer)
├─ Cleanup Command Failure
│  └─ Test fails, orphaned resources remain
└─ Work Directory Cleanup Failure
   └─ Manual cleanup required (if GH_ACCEPTANCE_PRESERVE_WORK_DIR=true)
```

## Key Architectural Decisions

```
Decision 1: Direct Main() Call vs Binary Build
├─ Choice: Direct ghcmd.Main() invocation
├─ Benefit: Code coverage collection enabled
├─ Tradeoff: Slightly less "blackbox" but acceptable
└─ Impact: Coverage metrics available (important for CI/CD)

Decision 2: testscript Framework Selection
├─ Choice: github.com/cli/go-internal/testscript
├─ Benefit: Purpose-built for CLI testing
├─ Tradeoff: Text-based script format (not Go code)
└─ Impact: Accessible test authoring, no Go knowledge required

Decision 3: Real GitHub Instance vs Mocking
├─ Choice: Real GitHub instance API calls
├─ Benefit: True blackbox testing, validates API compatibility
├─ Tradeoff: Requires network, GitHub token, external dependency
└─ Impact: Tests are integration tests, not unit tests

Decision 4: Per-Test Resource Ownership
├─ Choice: Each test owns full resource lifecycle
├─ Benefit: Test isolation, independent execution
├─ Tradeoff: More resources created, more cleanup required
└─ Impact: Scales to ~175+ tests without refactoring

Decision 5: Custom Commands for Common Patterns
├─ Choice: defer, replace, env2upper, stdout2env custom commands
├─ Benefit: Encapsulate common patterns, reduce duplication
├─ Tradeoff: Must maintain custom command implementations
└─ Impact: ~5 core patterns cover most test needs

Decision 6: Build Constraint for Test Isolation
├─ Choice: //go:build acceptance
├─ Benefit: Tests excluded from standard go test ./...
├─ Tradeoff: Separate build tag required for coverage
└─ Impact: Keeps standard tests fast, acceptance tests separate
```

## Integration Points with Build System

```
Makefile Integration:
├─ Target: acceptance (line 47-49)
├─ Command: go test -tags=acceptance ./acceptance
├─ No special compilation needed
└─ Works with standard Go tooling

go.mod Dependency:
├─ github.com/cli/go-internal v0.0.0-20241025142207-6c48bcd5ce24
├─ Pinned to specific commit (development version)
├─ No special version management
└─ Standard Go dependency handling

Code Structure:
├─ acceptance/acceptance_test.go (built from single source)
├─ acceptance/testdata/ (test data files, not compiled)
├─ No generated code (all manual)
└─ No build-time code generation
```

## Network Architecture

```
Test Execution Environment:
┌─────────────────┐
│  Test Runner    │
│  (go test)      │
└────────┬────────┘
         │ GH_ACCEPTANCE_TOKEN
         │ GH_ACCEPTANCE_HOST
         │
         ↓
┌─────────────────────────────────┐
│    GitHub Instance              │
│  (github.com or GHES)           │
│                                 │
│  Test Organization              │
│  ├─ test-repos (created)        │
│  ├─ test-issues (created)       │
│  ├─ test-prs (created)          │
│  └─ (all cleaned up after test) │
└─────────────────────────────────┘

Network Requirements:
├─ HTTP/HTTPS access to GitHub API
├─ TLS/SSL certificate verification
├─ Low latency preferred (~100-500ms per request)
└─ No special firewall configuration (standard HTTPS)
```

## Monitoring & Observability Integration Points

```
Test Output Analysis:
├─ WORK=/path location for manual inspection
├─ Comment-based timing information per section
├─ Assertion failure details (expected vs actual)
├─ Full command output on failure
└─ Deferred cleanup command output

Code Coverage Integration:
├─ -coverprofile=coverage.out flag
├─ -coverpkg=./... for full codebase coverage
├─ go tool cover -html for visualization
└─ Coverage data accurate due to Main() invocation

CI/CD Integration:
├─ Exit code 0 for success, 1 for failure
├─ Stdout/stderr available for log aggregation
├─ Coverage profiles for historical tracking
└─ Test timing information in output
```

## Summary of Key Infrastructure Components

| Component | Type | Responsibility |
|-----------|------|-----------------|
| `acceptance_test.go` | Source | Test runner, setup, custom commands |
| `testscript` | Framework | Test execution engine |
| `.txtar` files | Data | Test scripts and inline data |
| `sharedSetup()` | Function | Environment variable injection |
| `sharedCmds()` | Function | Custom command handlers |
| `ghcmd.Main()` | Entry Point | CLI execution |
| GitHub API | External | Real resources for testing |
| `GH_ACCEPTANCE_*` | Configuration | Test execution parameters |
| Build constraint | Configuration | Test isolation from standard runs |
| Go 1.25.5+ | Runtime | Compilation and execution environment |

---

## Comprehensive Infrastructure Checklist

**Required Infrastructure Elements:**
- [ ] Go 1.25.5 or later (compiler)
- [ ] GitHub instance (github.com or GHES)
- [ ] Test organization (dedicated, not 'github' or 'cli')
- [ ] Legacy Personal Access Token (with all required scopes)
- [ ] Git binary (system dependency)
- [ ] Network access (HTTPS to GitHub API)
- [ ] Disk space (~500MB-1GB for test execution)
- [ ] Three environment variables (GH_ACCEPTANCE_HOST, _ORG, _TOKEN)

**Optional Infrastructure Elements:**
- [ ] Code coverage tools (go tool cover)
- [ ] VS Code with gopls extensions
- [ ] Dedicated acceptance testing CI/CD pipeline
- [ ] Resource monitoring/cleanup automation

**Infrastructure Challenges:**
- [ ] Token scope complexity (8+ required scopes)
- [ ] Resource cleanup reliability (network/scope failures)
- [ ] Test execution duration (10-30 minutes for full suite)
- [ ] GitHub API rate limiting (5000 requests/hour)
- [ ] Token exposure in verbose logs
- [ ] Fine-Grained PAT incompatibility

---

**For comprehensive details, see: `/INFRASTRUCTURE-ACCEPTANCE-TESTING.md`**
**For quick reference, see: `/ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md`**
