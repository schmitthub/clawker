# Docker CLI Testing Dependencies Analysis

This directory contains a comprehensive analysis of all testing-related dependencies in the Docker CLI repository, with specific focus on testing implementation across all tiers (unit, integration, e2e).

## Files in This Analysis

### 1. `dependency-catalog.md` (Main Document - 778 lines)
**Comprehensive analysis** covering:
- Executive summary of testing architecture
- Dependency summary with categorization
- Detailed breakdown of all testing dependencies
- Internal testing infrastructure (FakeCli, builders, environment utilities)
- Build & CI/CD testing infrastructure (Docker multi-stage builds, docker-bake)
- Testing patterns and best practices
- Platform & environment testing strategies
- Complete dependency graph and relationships
- Testing tools and build environment
- Adaptation recommendations for different scenarios
- Risk assessment
- Full dependency table
- Integration with external systems

**Use this document for:** Deep understanding of testing architecture, adaptation planning, dependency risk analysis.

### 2. `QUICK_REFERENCE.md` (Quick Lookup - ~300 lines)
**Quick reference guide** containing:
- Core testing dependencies table (3 total)
- Testing tiers overview (unit, integration, e2e)
- Build and test infrastructure quick reference
- Key test patterns with code examples
- Internal testing infrastructure overview
- Makefile commands
- Configuration files reference
- Environment variables
- Quick start guide for adding new tests

**Use this document for:** Day-to-day reference, new developer onboarding, quick pattern lookup.

## Key Findings

### Core Testing Dependencies

Only **3 direct testing dependencies**:

1. **gotest.tools/v3** (v3.5.2)
   - Primary testing framework
   - Provides assertions, file system utilities, command execution, environment management, polling, and skipping

2. **gotestsum** (v1.13.0)
   - Test runner with enhanced formatting
   - Better test output organization for parallel tests
   - Coverage aggregation

3. **google/go-cmp** (v0.7.0)
   - Deep comparison for complex objects
   - Used alongside gotest.tools for flexible equality testing

### Testing Architecture

- **Minimal external dependencies** - Deliberately avoids testify/mock, GoMock, etc.
- **Internal test doubles** - FakeCli and fakeClient implementations in test files
- **Multi-tier testing** - Unit, integration, and e2e tests
- **Docker-based orchestration** - Reproducible containerized test execution via docker-bake
- **Platform awareness** - Conditional test execution for architecture-specific features

### Three-Tier Testing Pyramid

```
         E2E Tests
      (Full daemon)
    /              \
   Integration      
   (Multiple        
   components)      
  /                \
Unit Tests
(Isolated with fakes)
```

## Critical Observations

### Strengths

1. **Minimal dependency footprint** - Only 1 dedicated testing library
2. **Explicit test doubles** - All mocks defined in test files (no code generation)
3. **Comprehensive infrastructure** - Internal utilities eliminate need for external tools
4. **Good tooling integration** - gotestsum provides excellent CI/CD support
5. **Reproducible testing** - Docker containerization ensures consistency

### Intentional Design Decisions

- **NOT using**: testify/mock, GoMock, mockery (prioritizes explicitness)
- **NOT using**: Ginkgo/Gomega (prefers standard Go testing patterns)
- **NOT using**: Code generation (manual test doubles are simpler)
- **Using**: Internal FakeCli and builder patterns instead

## Dependency Replacement Scenarios

### Easy Replacements (Low effort)
- `gotestsum` → standard `go test` (lose enhanced formatting)
- Output formatting → custom test wrapper

### Moderate Replacements (Medium effort)
- `google/go-cmp` → testify/assert (similar functionality, different API)
- Assertion style → testify/require (2-3 weeks for 200+ files)

### Difficult Replacements (High effort)
- `gotest.tools/v3` → custom assertion package (need to rewrite helpers for all modules)
- Testing patterns → requires architectural changes to all 200+ test files

### Not Recommended for Replacement
- Internal test infrastructure (FakeCli, builders) - highly optimized for this project

## Key Files Referenced

| File | Purpose |
|------|---------|
| `go.mod` | Module definition with testing dependency pins |
| `Dockerfile` | Multi-stage build with test infrastructure |
| `docker-bake.hcl` | Test target definitions (test, test-coverage, e2e) |
| `Makefile` | Test execution commands (make test-unit, etc.) |
| `.golangci.yml` | Linting configuration with test-specific rules |
| `internal/test/cli.go` | FakeCli test double implementation |
| `internal/test/builders/` | Domain-specific test fixture builders |
| `internal/test/environment/` | E2E environment setup utilities |

## Testing Commands Quick Reference

```bash
# Unit tests
make test-unit
GOTESTSUM_FORMAT=standard-verbose make test-unit

# Coverage
make test-coverage
# Output: build/coverage/coverage.txt

# E2E tests
docker buildx bake e2e-image
docker run --rm -it -v /var/run/docker.sock:/var/run/docker.sock docker-cli

# Linting
make lint
golangci-lint run ./cli/...

# Specific test
gotestsum -- ./cli/command/container/... -run TestRunLabel -v
```

## How to Use This Analysis

### For Architects/Decision Makers
1. Read the Executive Summary in `dependency-catalog.md`
2. Review "Adaptation Recommendations" section
3. Check "Risk Assessment" for maintenance concerns

### For Developers
1. Start with `QUICK_REFERENCE.md`
2. Review "Key Test Patterns" section
3. Check "Quick Start: Adding a New Test"
4. Reference the appropriate builder/utility from "Internal Testing Infrastructure"

### For DevOps/CI-CD Teams
1. Review "Build & CI/CD Testing Infrastructure" in `dependency-catalog.md`
2. Check "Testing Commands Quick Reference" in this README
3. Consult docker-bake.hcl test targets (test, test-coverage, e2e-image)

### For Dependency Analysis
1. Consult "Full Dependency List" table in `dependency-catalog.md`
2. Review "Dependency Graph & Relationships" section
3. Check "Testing Tools & Build Environment" for transitive dependencies

## Integration Points

### CI/CD Integration
- Test results compatible with Codecov, Coveralls
- JUnit XML output support via gotestsum
- Coverage artifacts: `build/coverage/coverage.txt`

### Local Development
- Can run tests locally without Docker (if Go installed)
- Dockerfile ensures reproducible test environment
- Environment variables control test behavior

### GitHub/VCS Integration
- Per-commit linting via golangci-lint
- Per-commit test execution
- Coverage tracking and badges

## Version Information

- **Go Version**: 1.25.6 (minimum 1.24.0)
- **gotest.tools/v3**: v3.5.2
- **gotestsum**: v1.13.0
- **google/go-cmp**: v0.7.0
- **Docker buildx**: v0.29.1 (in e2e tests)
- **Docker compose**: v2.40.0 (in e2e tests)

## Related Documentation

See also in the parent directory:
- `CLAUDE.md` - Project instructions for development
- `go.mod` - Go module definition with all dependencies
- `Dockerfile` - Multi-stage Docker build with test infrastructure
- `docker-bake.hcl` - Build orchestration configuration
- `.golangci.yml` - Linting rules and configuration

## Analysis Metadata

- **Generated**: 2026-01-30
- **Repository**: github.com/docker/cli
- **Analysis Scope**: Testing implementation across all tiers
- **Focus Areas**: Dependencies, patterns, infrastructure, CI/CD integration
- **Platforms Analyzed**: darwin (analysis), linux/darwin/windows (test targets)

---

**Note**: This analysis focuses specifically on testing dependencies. For a comprehensive dependency analysis of the entire Docker CLI project, see the main project documentation and go.mod file.
