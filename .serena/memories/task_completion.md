# Completion checks

Select checks from the changed behavior. Record the commands, results, and any checks that remain unavailable.

## Code changes

1. Add a failing regression test before a bug fix or new behavior. Use the responsible package and observable result.
2. Format changed Go files with `gofmt -w` and their paths. Regenerate affected moq mocks from their owner package.
3. Run the affected unit packages with `go test`, explicit package paths, and `-count=1`.
4. Run `make test` and `golangci-lint run --config .golangci.yml ./...`.
5. For Docker behavior, complete the relevant host integration checks. For BPF behavior, include the relevant privileged host checks. Unit success does not prove live container behavior.
6. Before a commit, run required hooks with `make pre-commit`; check embed availability first. Review any files changed by hooks.

## Documentation and records

- Update the README and relevant package `AGENTS.md` when their content changes. Keep each `CLAUDE.md` link relative.
- Update design/architecture docs if the design changes. Update Mintlify pages for user-facing behavior.
- If the command tree or config schema changes, run `go run ./cmd/gen-docs --doc-path docs --markdown --website --schemas`, then `make docs-check`.
- After bug fixes or features, check `clawker-plugin/skills/clawker-support/reference/known-issues.md`. Changes there belong to the submodule.
- Keep current rules in module memories. Keep retained plan/research/history records in their own topics; follow `mem:memory_maintenance`.
- For memory-only edits, check memory targets, source paths, retained-record links, and `git diff --check`. No new Go test is needed for a text move.
- Run `serena memories check` when available. If the CLI is absent, check the local graph and report that limit.
- For all changes, run `git diff --check` and inspect the final diff.

If `CLAWKER_AGENT` is set, never use `go test ./...` or host integration targets in the container. Report host checks as pending until results exist. For command details: `mem:suggested_commands`.
