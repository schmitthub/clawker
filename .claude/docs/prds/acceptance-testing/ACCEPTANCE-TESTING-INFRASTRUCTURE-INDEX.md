# Acceptance Testing Infrastructure Documentation - Index

Complete infrastructure analysis and documentation for the GitHub CLI acceptance testing system.

---

## Documentation Files

### 1. INFRASTRUCTURE-ACCEPTANCE-TESTING.md (830 lines, 27 KB)
**Comprehensive Technical Reference**

The primary infrastructure documentation covering all aspects of the acceptance testing system.

**Sections**:
- Overview of execution model
- Build system integration (Makefile targets, build commands)
- Required environment variables (3 mandatory, 3 optional, 7 auto-injected)
- GitHub API requirements (token scopes, instance compatibility)
- Test organization and structure (18 categories, 175+ tests)
- Custom commands (defer, env2upper, replace, stdout2env, sleep)
- testscript configuration details
- CI/CD integration considerations
- Development environment setup
- Code coverage collection
- External dependencies
- Security considerations
- Monitoring and observability
- Operational procedures
- Known limitations and issues
- Adaptation recommendations for different environments

**Best For**: Infrastructure engineers, DevOps teams, senior developers

**Use When**: You need comprehensive details about any aspect of the acceptance testing infrastructure

---

### 2. ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md (292 lines, 8.3 KB)
**Quick Lookup & Command Reference**

Fast reference guide for common tasks and commands.

**Sections**:
- Environment variables (required, optional, injected)
- Quick commands (run all, specific categories, single tests)
- Test categories and run commands (17 categories)
- Custom test commands (syntax and usage)
- Token requirements
- VS Code setup
- Debugging techniques
- Test format (.txtar structure)
- CI/CD examples
- Directory structure
- Common issues and troubleshooting
- Performance characteristics

**Best For**: Developers, QA engineers, CI/CD maintainers

**Use When**: You need to quickly look up a command, run a specific test, or debug an issue

---

### 3. ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md (528 lines, 21 KB)
**Architectural Analysis & System Design**

Deep technical analysis of the system architecture and design decisions.

**Sections**:
- System overview (with architecture diagram)
- Environment configuration chain (variable flow)
- Test execution lifecycle (5 phases)
- Dependency graph
- Custom command implementation details
- Test data organization
- Build constraint impact
- Test script execution model
- Token scope dependency chain
- Performance characteristics
- Error handling and failure modes
- Key architectural decisions (6 major decisions)
- Integration points
- Network architecture
- Monitoring integration
- Infrastructure checklist

**Best For**: Architects, system designers, senior infrastructure engineers

**Use When**: You need to understand the system architecture, make design decisions, or troubleshoot complex issues

---

### 4. ACCEPTANCE-TESTING-INFRASTRUCTURE-SUMMARY.md
**Status & Summary Document** (in `.serena/` directory)

Executive summary of analysis results.

**Sections**:
- Analysis summary
- Deliverables overview
- Key findings
- Test category mapping
- Source file analysis
- Implementation recommendations
- Documentation file references
- Quick start checklist
- Analysis methodology
- Key metrics

**Best For**: Project leads, stakeholders reviewing the documentation

**Use When**: You want an overview of what was documented and key findings

---

## Quick Navigation

### By Role

**Infrastructure Engineer**
1. Start: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md (overview)
2. Reference: INFRASTRUCTURE-ACCEPTANCE-TESTING.md (details)
3. Debug: ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md (troubleshooting)

**Developer Writing Tests**
1. Start: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md (quick reference)
2. Reference: INFRASTRUCTURE-ACCEPTANCE-TESTING.md (test organization, custom commands)
3. Example: Existing test scripts in `acceptance/testdata/`

**CI/CD Engineer**
1. Start: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md (CI/CD examples)
2. Reference: INFRASTRUCTURE-ACCEPTANCE-TESTING.md (CI/CD integration section)
3. Details: ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md (performance, monitoring)

**System Architect**
1. Start: ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md (architecture overview)
2. Reference: INFRASTRUCTURE-ACCEPTANCE-TESTING.md (comprehensive details)
3. Decision support: Key architectural decisions section

**Project Lead/Stakeholder**
1. Start: ACCEPTANCE-TESTING-INFRASTRUCTURE-SUMMARY.md
2. Reference: Key infrastructure metrics, test categories
3. Details: As needed from other documents

---

### By Task

**Setting Up Acceptance Tests Locally**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Environment Variables (Required)" + "Run All Tests"

**Running Specific Test Category**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Test Categories" table

**Debugging Failed Test**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Debugging"
- Also: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Debugging Tools and Flags"

**Creating New Acceptance Test**
- See: acceptance/README.md (existing documentation)
- Reference: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Test Organization & Structure"
- Examples: Files in acceptance/testdata/

**Setting Up GitHub Actions Integration**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "CI/CD Example (GitHub Actions)"
- Also: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "CI/CD Integration"

**Understanding Token Scopes**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Token Requirements"
- Also: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "GitHub API Requirements"

**Collecting Code Coverage**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Run commands" (coverage example)
- Also: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Code Coverage"

**Setting Up VS Code**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "VS Code Setup"

**Understanding Custom Commands**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Custom Test Commands"
- Also: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Custom Commands (testscript Extensions)"
- Deep dive: ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md > "Custom Command Implementation Details"

**Troubleshooting Failures**
- See: ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md
- Section: "Common Issues"
- Also: INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Limitations & Known Issues"
- Analysis: ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md > "Error Handling & Failure Modes"

**Adapting for Different Environment**
- See: INFRASTRUCTURE-ACCEPTANCE-TESTING.md
- Section: "Recommended Adaptations for Other Environments"

---

## Key Infrastructure Facts

### Technology Stack
- **Framework**: github.com/cli/go-internal/testscript
- **Language**: Go 1.25.5+
- **Test Format**: .txtar (text archive) files
- **Test Execution**: Direct ghcmd.Main() invocation (not binary)
- **Build Constraint**: //go:build acceptance

### Test Volume
- **Total Tests**: 175+ acceptance tests
- **Categories**: 18 feature areas
- **Framework Focus**: Shell-like test script execution with custom commands

### Required Infrastructure
- Go 1.25.5 or later (compiler)
- GitHub instance (github.com or GitHub Enterprise Server)
- Legacy Personal Access Token (with 8+ required scopes)
- Test organization (dedicated, not 'github' or 'cli')
- Network access (HTTPS to GitHub API)
- Git binary (system dependency)

### Environment Variables

**Required** (3):
- GH_ACCEPTANCE_HOST - GitHub instance host
- GH_ACCEPTANCE_ORG - Test organization
- GH_ACCEPTANCE_TOKEN - Legacy PAT token

**Optional** (3):
- GH_ACCEPTANCE_SCRIPT - Run single test
- GH_ACCEPTANCE_PRESERVE_WORK_DIR - Keep temp directory
- GH_ACCEPTANCE_SKIP_DEFER - Skip cleanup

### Custom Commands (5)
1. defer - Register cleanup
2. env2upper - Uppercase values
3. replace - Replace file placeholders
4. stdout2env - Capture output
5. sleep - Delay execution

### Performance Metrics
- Single test: 0.5-15 seconds
- Full suite: 10-30 minutes
- API calls: 500-1000+ per full run
- Memory: 50-100MB
- Disk: 500MB-1GB

---

## File Structure Reference

```
Root Repository
├── INFRASTRUCTURE-ACCEPTANCE-TESTING.md (830 lines)
│   └── Comprehensive technical reference
├── ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md (292 lines)
│   └── Quick lookup and commands
├── ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md (528 lines)
│   └── Architectural deep dive
└── ACCEPTANCE-TESTING-INFRASTRUCTURE-INDEX.md (this file)
    └── Navigation and quick facts

acceptance/
├── acceptance_test.go (422 lines)
│   └── Test runner implementation
├── README.md (196 lines)
│   └── User guide (existing)
└── testdata/ (18 directories, 175+ .txtar files)
    ├── api/ (~5 tests)
    ├── auth/ (~10 tests)
    ├── extension/ (~8 tests)
    ├── gpg-key/ (~5 tests)
    ├── issue/ (~20 tests)
    ├── label/ (~10 tests)
    ├── org/ (~8 tests)
    ├── project/ (~10 tests)
    ├── pr/ (~40+ tests)
    ├── release/ (~10 tests)
    ├── repo/ (~15 tests)
    ├── ruleset/ (~8 tests)
    ├── search/ (~5 tests)
    ├── secret/ (~15 tests)
    ├── ssh-key/ (~5 tests)
    ├── variable/ (~10 tests)
    └── workflow/ (~15 tests)

.serena/
├── ACCEPTANCE-TESTING-INFRASTRUCTURE-SUMMARY.md
│   └── Analysis summary and status
└── ACCEPTANCE-TESTING-OVERVIEW.md (existing)
    └── Overview from phase 1

Makefile (line 47-49)
└── acceptance target definition

go.mod
└── testscript dependency
```

---

## Quick Commands

```bash
# Set up environment
export GH_ACCEPTANCE_HOST="github.com"
export GH_ACCEPTANCE_ORG="gh-acceptance-testing"
export GH_ACCEPTANCE_TOKEN="ghp_..."

# Run all tests
go test -tags=acceptance ./acceptance

# Run specific category
go test -tags=acceptance -run ^TestPullRequests$ ./acceptance

# Run single test
GH_ACCEPTANCE_SCRIPT=pr-view.txtar go test -tags=acceptance -run ^TestPullRequests$ ./acceptance

# Collect coverage
go test -tags=acceptance -coverprofile=coverage.out -coverpkg=./... ./acceptance

# Verbose output
go test -tags=acceptance -v ./acceptance

# Debug with work directory
GH_ACCEPTANCE_PRESERVE_WORK_DIR=true GH_ACCEPTANCE_SKIP_DEFER=true go test -tags=acceptance -v ./acceptance
```

---

## Common Lookup Table

| Need | Where to Look |
|------|---------------|
| How to run tests? | Quick Reference: "Quick Commands" |
| What's GH_ACCEPTANCE_TOKEN? | Quick Reference: "Environment Variables" |
| How defer works? | Quick Reference: "Custom Test Commands" > defer |
| What are test categories? | Quick Reference: "Test Categories" table |
| CI/CD setup? | Quick Reference: "CI/CD Example (GitHub Actions)" |
| Debugging techniques? | Quick Reference: "Debugging" |
| Token scopes needed? | Quick Reference: "Token Requirements" |
| System architecture? | Analysis: "System Overview" |
| Failure modes? | Analysis: "Error Handling & Failure Modes" |
| Design decisions? | Analysis: "Key Architectural Decisions" |
| Full details? | Comprehensive: All sections |
| Troubleshooting? | Quick Reference: "Common Issues" |

---

## Documentation Statistics

| Document | Lines | Size | Audience | Purpose |
|----------|-------|------|----------|---------|
| INFRASTRUCTURE-ACCEPTANCE-TESTING.md | 830 | 27 KB | Infrastructure teams | Comprehensive reference |
| ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md | 292 | 8.3 KB | Developers, QA | Quick lookup |
| ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md | 528 | 21 KB | Architects, designers | System architecture |
| ACCEPTANCE-TESTING-INFRASTRUCTURE-SUMMARY.md | ~400 | ~15 KB | Stakeholders | Analysis summary |
| **Total** | **2050+** | **~71 KB** | **All roles** | **Complete coverage** |

---

## How to Use This Documentation

### Step 1: Understand Your Role
- Developer: Use Quick Reference daily
- Infrastructure: Use Comprehensive as primary, Analysis for decisions
- Architect: Use Analysis for design, Comprehensive for details
- Stakeholder: Use Summary for overview

### Step 2: Find What You Need
- Use "Quick Navigation" section above
- Use "Common Lookup Table"
- Use "By Task" section for specific needs

### Step 3: Get the Details
- Quick Reference for most common needs
- Comprehensive for detailed specifications
- Analysis for architecture and design decisions

### Step 4: Refer Back
- Bookmark the Index for future reference
- Use specific document sections as needed
- Consult original test files (testdata/) for examples

---

## Document Maintenance

These documentation files reference:
- `acceptance/acceptance_test.go` (422 lines) - Test runner implementation
- `acceptance/README.md` (196 lines) - User guide
- `acceptance/testdata/` (175+ test scripts) - Test examples
- `Makefile` (line 47-49) - Build integration
- `go.mod` - Dependency declarations

**Last Updated**: January 27, 2026
**Coverage**: Complete infrastructure documentation
**Status**: Ready for distribution

---

## Support & Questions

For questions about specific sections:
1. **Build System**: See INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Build System Integration"
2. **Environment Setup**: See ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md > "Environment Variables"
3. **Test Writing**: See acceptance/README.md (existing guide) or INFRASTRUCTURE-ACCEPTANCE-TESTING.md > "Test Organization"
4. **Architecture**: See ACCEPTANCE-INFRASTRUCTURE-ANALYSIS.md
5. **Debugging**: See ACCEPTANCE-INFRASTRUCTURE-QUICK-REFERENCE.md > "Debugging"

---

**Documentation Complete & Indexed**
**Created**: January 27, 2026
**Repository**: github-cli
**Feature**: Acceptance Testing Infrastructure
