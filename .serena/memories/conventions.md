# Project conventions

- Apply the root and package instructions. Use ASD-STE100 for chat, docs, and comments.
- `AGENTS.md` contains instructions. Each `CLAUDE.md` is a relative symbolic link to its sibling `AGENTS.md`.
- Commands receive dependencies through Factory closure fields. Resolve them in the run function. Fields return objects: use the Factory noun pattern.
- Keep constructors and domain logic out of command glue. Package interfaces and test helpers must serve callers.
- Put meaningful shared strings in `internal/consts`; package strings in `consts.go`. Use `config.Config` accessors for config-derived values and paths.
- Return or wrap errors. Do not discard them with `_`. Unactionable cleanup errors need a reason in a comment.
- Do not add `//nolint:` without explicit user approval. Keep required struct fields explicit and `exhaustruct` checks active. Prior line-specific approvals do not authorize new suppressions.
- Pass `context.Context` as the first parameter; never store it in a struct. Deferred cleanup uses `context.Background()`.
- Use `IOStreams` for user output. Data, status, success, and next steps go to stdout; warnings go to stderr. Return errors for central rendering.
- For `--format`, keep machine data on stdout and status/progress on stderr. Live displays use the TUI layer.
- Use the injected logger for file logs. Do not use logging as terminal output. Commands resolve it with `log, err := opts.Logger()` in the run function and check the error. Library packages take `*logger.Logger` in constructors; no globals. Tests use `logger.Nop()`. `log.With("project", name, "agent", agent)` adds structured fields. Never `logger.Fatal()` in Cobra hooks; return errors. File logs go to `cfg.LogsSubdir()/clawker.log` with rotation.
- Discard an error only when it is not actionable (deferred cleanup) and a comment says why. Use `errors.Is` for one benign sentinel and surface the rest.
- Comments and docs never spell a const's value; write "the clawker network", not the literal name.
- Tests use generated moq mocks and package fakes. Do not hand-edit generated code.

## Focused rules

- For command construction, presentation, and Docker boundaries: `mem:cli/core`.
- For config access and project-root ownership: `mem:config/core`.
- For CP error handling and shutdown restrictions: `mem:controlplane/core`.
- For YAML schema ownership and write rules: `mem:storage/core`.
- For regression tests and suitable test helpers: `mem:testing/core`.
