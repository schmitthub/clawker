---
description: Code style guidelines for the clawker codebase
---

# Code Style

## Logging
- Use `zerolog` only (never `fmt.Print` for debug output)
- File logging to `~/.local/clawker/logs/clawker.log` with rotation (50MB, 7 days, 3 backups)
- Interactive mode: `logger.SetInteractiveMode(true)` suppresses console logs, file logs continue
- Project/agent context: `logger.SetContext(project, agent)` adds structured fields
- `logger.Debug()` never suppressed; `Info/Warn/Error` suppressed on console in interactive mode
- Never use `logger.Fatal()` in Cobra hooks — return errors instead

## Whail Client Enforcement
- No package imports `github.com/moby/moby/client` directly except `pkg/whail`
- No package imports `pkg/whail` directly except `internal/docker`
- `pkg/whail` decorates moby client, exposing the same interface — all moby methods available through whail
- In tests: use `whail.ImageListResult`, `whail.ImageSummary` etc. from `pkg/whail/types.go`, never import moby types

## Output Conventions
- User output: `cmdutil.PrintError()`, `cmdutil.PrintNextSteps()` → stderr
- Data output: stdout only (for scripting, e.g., `ls` table output)
- Errors: `cmdutil.HandleError(err)` for Docker errors

## Cobra Commands
- Always use `PersistentPreRunE` (never `PersistentPreRun`)
- Always include `Example` field with indented examples
- Subpackages under `internal/cmd/<noun>/` are for subcommands only
- Exception: `opts/` package exists for import cycle avoidance (parent imports subcommands, subcommands need shared types)

## CLI Guidelines Reference
- Follow conventions from https://clig.dev/ for CLI design patterns
