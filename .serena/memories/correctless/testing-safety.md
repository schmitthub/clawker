# Testing Safety — Container Environment

Never run `go test ./...` directly — always use Makefile targets (`make test`).

E2E tests (`make test-e2e`, `test/e2e/`) destroy firewall infrastructure between jobs. Since agents run inside clawker containers, running E2E tests will crash network access and kill the session.

Safe inside container:
- `make test` (unit tests only)
- `go test ./internal/foo/...` (specific package)

Never run from inside container:
- `make test-e2e`, `make test-whail`, `make test-all`
- `go test ./...` (picks up integration packages)
- Any Docker-dependent test suite
