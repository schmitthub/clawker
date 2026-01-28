# Acceptance Testing Infrastructure - Quick Reference

## Environment Variables (Required)

```bash
# All three MUST be set and non-empty
export GH_ACCEPTANCE_HOST="github.com"              # GitHub instance host
export GH_ACCEPTANCE_ORG="gh-acceptance-testing"    # Test organization (NOT 'github' or 'cli')
export GH_ACCEPTANCE_TOKEN="ghp_..."                # Legacy PAT with full scopes
```

## Quick Commands

### Run All Tests
```bash
go test -tags=acceptance ./acceptance
```

### Run Specific Category
```bash
go test -tags=acceptance -run ^TestPullRequests$ ./acceptance
go test -tags=acceptance -run ^Test(PullRequests|Issues)$ ./acceptance
```

### Run Single Test
```bash
GH_ACCEPTANCE_SCRIPT=pr-view.txtar go test -tags=acceptance -run ^TestPullRequests$ ./acceptance
```

### Verbose Output
```bash
go test -tags=acceptance -v ./acceptance
```

### With Code Coverage
```bash
go test -tags=acceptance -coverprofile=coverage.out -coverpkg=./... ./acceptance
go tool cover -html=coverage.out
```

### Debug Mode (Keep Work Dir, Skip Cleanup)
```bash
GH_ACCEPTANCE_PRESERVE_WORK_DIR=true \
GH_ACCEPTANCE_SKIP_DEFER=true \
GH_ACCEPTANCE_SCRIPT=test-name.txtar \
go test -tags=acceptance -run ^TestCategory$ ./acceptance -v
```

## Key Environment Variables Injected in Tests

| Variable | Value | Purpose |
|----------|-------|---------|
| `GH_HOST` | Same as `GH_ACCEPTANCE_HOST` | GitHub API host |
| `ORG` | Same as `GH_ACCEPTANCE_ORG` | Test organization |
| `GH_TOKEN` | Same as `GH_ACCEPTANCE_TOKEN` | Authentication |
| `SCRIPT_NAME` | Test name (hyphens→underscores) | Unique test identifier |
| `RANDOM_STRING` | 10-char random string | Resource suffix for isolation |
| `HOME` | Test work directory | Git operations |
| `GH_CONFIG_DIR` | Test work directory | CLI config |

## Test Categories

| Category | Command | Count |
|----------|---------|-------|
| API | `go test -tags=acceptance -run ^TestAPI$ ./acceptance` | ~5 |
| Auth | `go test -tags=acceptance -run ^TestAuth$ ./acceptance` | ~10 |
| Extensions | `go test -tags=acceptance -run ^TestExtensions$ ./acceptance` | ~8 |
| GPG Keys | `go test -tags=acceptance -run ^TestGPGKeys$ ./acceptance` | ~5 |
| Issues | `go test -tags=acceptance -run ^TestIssues$ ./acceptance` | ~20 |
| Labels | `go test -tags=acceptance -run ^TestLabels$ ./acceptance` | ~10 |
| Organizations | `go test -tags=acceptance -run ^TestOrg$ ./acceptance` | ~8 |
| Projects | `go test -tags=acceptance -run ^TestProject$ ./acceptance` | ~10 |
| Pull Requests | `go test -tags=acceptance -run ^TestPullRequests$ ./acceptance` | ~40+ |
| Releases | `go test -tags=acceptance -run ^TestReleases$ ./acceptance` | ~10 |
| Repositories | `go test -tags=acceptance -run ^TestRepo$ ./acceptance` | ~15 |
| Rulesets | `go test -tags=acceptance -run ^TestRulesets$ ./acceptance` | ~8 |
| Search | `go test -tags=acceptance -run ^TestSearches$ ./acceptance` | ~5 |
| Secrets | `go test -tags=acceptance -run ^TestSecrets$ ./acceptance` | ~15 |
| SSH Keys | `go test -tags=acceptance -run ^TestSSHKeys$ ./acceptance` | ~5 |
| Variables | `go test -tags=acceptance -run ^TestVariables$ ./acceptance` | ~10 |
| Workflows | `go test -tags=acceptance -run ^TestWorkflows$ ./acceptance` | ~15 |

## Custom Test Commands

### defer COMMAND [ARGS...]
```txtar
# Register cleanup (runs after test)
defer gh repo delete --yes $ORG/$SCRIPT_NAME-$RANDOM_STRING
```

### env2upper VAR=value
```txtar
# Set VAR to uppercase value (for Actions secret names)
env2upper ORG_SECRET_NAME=$RANDOM_STRING
```

### replace FILE VAR=value
```txtar
# Replace $VAR in file (preserves permissions, safe for workflows)
replace .github/workflows/workflow.yml SECRET_NAME=$SECRET_NAME
```

### stdout2env VAR_NAME
```txtar
# Capture previous command's stdout to VAR_NAME
exec gh pr create --title 'Feature'
stdout2env PR_URL
```

### sleep SECONDS
```txtar
# Sleep for N seconds (for workflow propagation)
sleep 2
```

## Token Requirements

**Recommended**: Legacy Personal Access Token (PAT)

**Required Scopes** (varies by test):
```
repo                    # Repository access
workflow                # GitHub Actions
admin:repo_hook         # Webhooks
admin:public_key        # SSH keys
admin:gpg_key          # GPG keys
delete_repo            # Critical for cleanup!
write:org_action       # Organization Actions
admin:org_secret       # Organization secrets
```

**NOT Supported**: Fine-Grained PATs (lack necessary scopes like `delete_repo`)

## VS Code Setup

Add to `.vscode/settings.json`:
```json
{
  "gopls": {
    "buildFlags": ["-tags=acceptance"]
  }
}
```

Install extensions:
- `txtar` (brody715.txtar)
- `vscode-testscript` (twpayne.vscode-testscript)

## Debugging

**See full test output**:
```bash
go test -tags=acceptance -v ./acceptance
```

**Keep work directory for inspection**:
```bash
GH_ACCEPTANCE_PRESERVE_WORK_DIR=true go test -tags=acceptance ./acceptance
# Check path shown in output: WORK=/path/to/dir
```

**Skip cleanup for inspection**:
```bash
GH_ACCEPTANCE_SKIP_DEFER=true go test -tags=acceptance ./acceptance
```

**Run specific script**:
```bash
GH_ACCEPTANCE_SCRIPT=pr-merge.txtar go test -tags=acceptance -run ^TestPullRequests$ ./acceptance
```

## Test Format (.txtar)

```txtar
# Comment describing test
env VAR=value

# Execute command
exec gh pr create --title 'Title'

# Verify output
stdout 'Title'

# Register cleanup (runs after test)
defer gh repo delete --yes $ORG/$SCRIPT_NAME-$RANDOM_STRING

# Include file content
-- filename --
file content
```

## CI/CD Example (GitHub Actions)

```yaml
- name: Acceptance Tests
  env:
    GH_ACCEPTANCE_HOST: github.com
    GH_ACCEPTANCE_ORG: gh-acceptance-testing
    GH_ACCEPTANCE_TOKEN: ${{ secrets.GH_ACCEPTANCE_TOKEN }}
  run: go test -tags=acceptance ./acceptance

- name: With Coverage
  env:
    GH_ACCEPTANCE_HOST: github.com
    GH_ACCEPTANCE_ORG: gh-acceptance-testing
    GH_ACCEPTANCE_TOKEN: ${{ secrets.GH_ACCEPTANCE_TOKEN }}
  run: |
    go test -tags=acceptance \
      -coverprofile=coverage.out \
      -coverpkg=./... \
      ./acceptance
```

## Directory Structure

```
acceptance/
├── acceptance_test.go       # Test runner (422 lines)
├── README.md               # Full documentation
└── testdata/
    ├── api/               # API tests (~5)
    ├── auth/              # Auth tests (~10)
    ├── extension/         # Extension tests (~8)
    ├── gpg-key/           # GPG key tests (~5)
    ├── issue/             # Issue tests (~20)
    ├── label/             # Label tests (~10)
    ├── org/               # Org tests (~8)
    ├── project/           # Project tests (~10)
    ├── pr/                # PR tests (~40+)
    ├── release/           # Release tests (~10)
    ├── repo/              # Repo tests (~15)
    ├── ruleset/           # Ruleset tests (~8)
    ├── search/            # Search tests (~5)
    ├── secret/            # Secret tests (~15)
    ├── ssh-key/           # SSH key tests (~5)
    ├── variable/          # Variable tests (~10)
    └── workflow/          # Workflow tests (~15)
```

## Typical Test Execution

```
go test -tags=acceptance ./acceptance
ok      github.com/cli/cli/v2/acceptance    234.567s
```

**Expected Duration**:
- Full suite: 10-30 minutes
- Single test: 0.5-15 seconds

## Common Issues

### Missing Required Variables
```
Error: environment variable(s) GH_ACCEPTANCE_HOST, GH_ACCEPTANCE_ORG, GH_ACCEPTANCE_TOKEN must be set and non-empty
```
→ Set all three required environment variables

### Token Scope Insufficient
```
Error: insufficient permissions for repository operations
```
→ Use Legacy PAT with all required scopes, not Fine-Grained PAT

### Organization Restriction
```
Error: GH_ACCEPTANCE_ORG cannot be 'github' or 'cli'
```
→ Use a different organization name (e.g., `gh-acceptance-testing`)

### Test Timeout
```
context deadline exceeded
```
→ Check GitHub API rate limits, GitHub instance availability

### Orphaned Resources
```
# Resources not cleaned up
```
→ Check token has `delete_repo` scope, review `defer` commands

## Performance Notes

- ~175+ tests across 18 categories
- No built-in rate limiting (monitor API quota)
- Each test creates isolated temporary directory
- Deferred cleanup required for all created resources

## For Full Documentation

See `/INFRASTRUCTURE-ACCEPTANCE-TESTING.md` for comprehensive details.
