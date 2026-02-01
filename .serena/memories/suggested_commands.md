# Suggested Commands

```bash
# Build
go build -o bin/clawker ./cmd/clawker

# Unit tests
go test ./...

# Integration tests (Docker required)
go test -tags=internals ./internal/cmd/... -v -timeout 10m

# E2E tests (Docker required)
go test -tags=e2e ./internal/cmd/... -v -timeout 15m

# Acceptance tests
go test -tags=acceptance ./acceptance -v -timeout 15m

# Specific acceptance category
go test -tags=acceptance -run ^TestContainer$ ./acceptance -v

# Generate mocks
make generate-mocks

# Lint
golangci-lint run

# Check CLAUDE.md freshness
bash scripts/check-claude-freshness.sh
```
