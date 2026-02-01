# Suggested Commands

```bash
# Build
go build -o bin/clawker ./cmd/clawker

# Unit tests (no Docker)
make test

# Internal integration tests (Docker required)
go test ./test/internals/... -v -timeout 10m

# CLI workflow tests (Docker required)
go test ./test/cli/... -v -timeout 15m

# Agent E2E tests (Docker required)
go test ./test/agents/... -v -timeout 15m

# Specific CLI test category
go test -run ^TestContainer$ ./test/cli/... -v

# All test suites
make test-all

# Lint
golangci-lint run

# Check CLAUDE.md freshness
bash scripts/check-claude-freshness.sh
```
