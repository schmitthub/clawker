# Project commands

Run from the project root unless a working directory is given.

## Development

| Purpose | Command |
| --- | --- |
| Build the CLI with existing embeds | `go build -o bin/clawker ./cmd/clawker` |
| Build all required binary embeds | `make clawker` |
| Unit suite without Docker integration tests | `make test` |
| Focused unit example | `go test ./internal/storage/... -count=1` |
| Daemon and agent unit checks | `make test-clawkerd` |
| Race checks, uncached results, and coverage | `make test-ci` |
| Lint with the repository config | `golangci-lint run --config .golangci.yml ./...` |
| Install Git hooks | `bash scripts/install-hooks.sh` |
| Run all hooks | `make pre-commit` |
| Generate CLI docs and config schemas | `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas` |
| Check generated docs | `make docs-check` |
| Check instruction freshness | `bash scripts/check-agents-freshness.sh --no-color` |
| Check Serena references, if its CLI is installed | `serena memories check` |

- Format changed Go files with `gofmt -w` followed by their paths.
- Regenerate mocks with `go generate ./...` from the package that owns the `//go:generate` directive. Do not edit generated mocks.
- Before a commit, check the six embed paths listed in `AGENTS.md` against the Makefile variables. Run `make clawker` for missing embeds, not as a routine check.
- `make pre-commit` can change module and license files. Review its diff.

## Host checks

These commands need the host Docker daemon or host kernel capabilities:

| Purpose | Command |
| --- | --- |
| CLI integration tests | `make test-e2e` |
| Docker/BuildKit integration tests | `make test-whail` |
| Unit plus both integration suites | `make test-all` |
| Privileged BPF tests on Linux | `make test-bpf` |

- If `CLAWKER_AGENT` is set, do not run `go test ./...`. The E2E suite can remove the host CP. Use `make test` or explicit unit package paths.
- Also avoid broad coverage/short targets that pass `./...` to the test runner: `test-coverage`, `clawker-test-coverage`, and `clawker-test-short`.
- Whail golden regeneration: `GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v`. Review fixture changes.
- For test helper selection and proof limits: `mem:testing/core`.
